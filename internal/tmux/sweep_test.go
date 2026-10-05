package tmux

import (
	"context"
	"slices"
	"testing"
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
