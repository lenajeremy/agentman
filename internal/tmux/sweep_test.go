package tmux

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func launchIdle(t *testing.T, kind string) string {
	t.Helper()
	name := NewName(kind)
	if err := Launch(context.Background(), name, t.TempDir(), []string{"sh", "-c", "sleep 60"}); err != nil {
		t.Fatalf("launch: %v", err)
	}
	t.Cleanup(func() { _ = Kill(context.Background(), name) })
	return name
}

func listed(t *testing.T, ctx context.Context, name string) bool {
	t.Helper()
	sessions, err := List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return slices.ContainsFunc(sessions, func(session Session) bool { return session.Name == name })
}

// Every adapter lists the panes and snapshots the process table for itself,
// once a second: five lists and three ps runs a sweep for the same answer.
// Inside a sweep they are asked once. Outside one, every call is fresh,
// because launching, sending and answering need the panes as they are now.
func TestASweepListsPanesAndProcessesOnce(t *testing.T) {
	requireTmux(t)
	ctx := context.Background()
	first := launchIdle(t, "sweeptest")

	sweep, done := WithSweep(ctx)
	if !listed(t, sweep, first) {
		t.Fatal("the sweep's list is missing a running session")
	}
	second := launchIdle(t, "sweeptest")
	if listed(t, sweep, second) {
		t.Error("a sweep listed tmux a second time")
	}
	if !listed(t, ctx, second) {
		t.Error("a list outside the sweep was not fresh")
	}

	tree, err := SnapshotProcessTree(sweep)
	if err != nil {
		t.Fatal(err)
	}
	again, err := SnapshotProcessTree(sweep)
	if err != nil {
		t.Fatal(err)
	}
	if tree != again {
		t.Error("a sweep ran ps a second time")
	}
	if outside, err := SnapshotProcessTree(ctx); err != nil || outside == tree {
		t.Error("a process snapshot outside the sweep was not fresh")
	}

	// Anything that keeps the sweep's context past its end asks afresh.
	done()
	if !listed(t, sweep, second) {
		t.Error("an ended sweep still answered from memory")
	}
}

// A sweep's reads use the sweep's own context. An adapter that gives up early
// must not leave every other adapter with an empty list for the sweep.
func TestOneAdaptersDeadlineDoesNotEmptyTheSweep(t *testing.T) {
	requireTmux(t)
	name := launchIdle(t, "sweeptest")
	sweep, done := WithSweep(context.Background())
	defer done()

	impatient, cancel := context.WithCancel(sweep)
	cancel()
	_, _ = List(impatient)
	_, _ = SnapshotProcessTree(impatient)

	if !listed(t, sweep, name) {
		t.Error("a cancelled caller's failed list was kept for the whole sweep")
	}
	if tree, err := SnapshotProcessTree(sweep); err != nil || tree == nil {
		t.Errorf("a cancelled caller's failed snapshot was kept: %v", err)
	}
}

func capturedSoon(t *testing.T, ctx context.Context, name, want string) {
	t.Helper()
	waitFor(t, func() bool {
		pane, err := Capture(ctx, name)
		return err == nil && strings.Contains(pane, want)
	}, "the pane never showed "+want)
}

// Discovery captured each agent pane with its own tmux run: eight a second.
// A sweep captures them all in one, the first time any is asked for. A pane
// is read from that once; a second look in the same sweep is fresh, so a
// flow that captures, presses a key and captures again sees the change.
func TestASweepCapturesEveryPaneInOneGo(t *testing.T) {
	requireTmux(t)
	ctx := context.Background()
	first, _ := newSink(t)
	second, _ := newSink(t)
	if err := Send(ctx, second, "before"); err != nil {
		t.Fatal(err)
	}
	capturedSoon(t, ctx, second, "before")

	sweep, done := WithSweep(ctx)
	defer done()
	if _, err := Capture(sweep, first); err != nil {
		t.Fatal(err)
	}
	// Typed after the sweep read the panes.
	if err := Send(ctx, second, "after"); err != nil {
		t.Fatal(err)
	}
	capturedSoon(t, ctx, second, "after")

	pane, err := Capture(sweep, second)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pane, "before") || strings.Contains(pane, "after") {
		t.Errorf("the second pane was not read with the first:\n%s", pane)
	}
	again, err := Capture(sweep, second)
	if err != nil || !strings.Contains(again, "after") {
		t.Errorf("a second look in the sweep was not fresh: %q, %v", again, err)
	}
}

// A pane that closes between the sweep's list and its capture stops tmux's
// run partway. The panes it did not reach are captured one by one.
func TestASweepCaptureSurvivesAPaneThatClosed(t *testing.T) {
	requireTmux(t)
	ctx := context.Background()
	first, _ := newSink(t)
	second, _ := newSink(t)
	if err := Send(ctx, first, "still here"); err != nil {
		t.Fatal(err)
	}
	if err := Send(ctx, second, "still here"); err != nil {
		t.Fatal(err)
	}
	capturedSoon(t, ctx, first, "still here")
	capturedSoon(t, ctx, second, "still here")

	sweep, done := WithSweep(ctx)
	defer done()
	if _, err := List(sweep); err != nil {
		t.Fatal(err)
	}
	gone, kept := first, second
	if err := Kill(ctx, gone); err != nil {
		t.Fatal(err)
	}
	pane, err := Capture(sweep, kept)
	if err != nil || !strings.Contains(pane, "still here") {
		t.Errorf("an open pane was lost with a closed one: %q, %v", pane, err)
	}
	if _, err := Capture(sweep, gone); err == nil {
		t.Error("a closed pane was reported captured")
	}
}

// Revealing Codex's queued question presses a key. Within a sweep, the
// capture that decides it may be from before a phone answered the question,
// so the key must go on what the pane shows now.
func TestRevealDecidesOnThePaneAsItIsNow(t *testing.T) {
	requireTmux(t)
	ctx := context.Background()
	dir := t.TempDir()
	keys := filepath.Join(dir, "keys")
	cleared := filepath.Join(dir, "cleared")
	tray := `printf '\n• Queued follow-up inputs\n  ? 1 question\n  shift+← to answer\n'`
	script := tray + "; while [ ! -f " + cleared + " ]; do sleep 0.05; done; clear; printf 'answered\\n'; stty raw -echo; cat > " + keys
	name := NewName("codex")
	if err := Launch(ctx, name, dir, []string{"sh", "-c", script}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = Kill(context.Background(), name) })
	capturedSoon(t, ctx, name, "shift+← to answer")
	other, _ := newSink(t)

	sweep, done := WithSweep(ctx)
	defer done()
	if _, err := Capture(sweep, other); err != nil { // the sweep reads every pane
		t.Fatal(err)
	}
	if err := os.WriteFile(cleared, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	capturedSoon(t, ctx, name, "answered")

	if _, err := RevealCodexQuestion(sweep, name); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if typed, _ := os.ReadFile(keys); len(typed) > 0 {
		t.Errorf("a key was pressed on a question that was already gone: %q", typed)
	}
}
