package tmux

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// These tests drive a real tmux, because the whole point of this package is
// that the interaction with tmux behaves as expected. They are skipped when
// tmux is unavailable so the suite still passes on a machine without it.
func requireTmux(t *testing.T) {
	t.Helper()
	if !Available() {
		t.Skip("tmux is not installed")
	}
}

// newSink starts a session running `cat > file`, so whatever is typed into it
// lands somewhere assertable.
func newSink(t *testing.T) (name, outPath string) {
	t.Helper()
	dir := t.TempDir()
	outPath = filepath.Join(dir, "out.txt")
	name = "agentman-test-" + filepath.Base(dir)

	ctx := context.Background()
	// A shell is needed for the redirection; tmux execs it, so the pane
	// process ends up being cat itself.
	if err := Launch(ctx, name, dir, []string{"sh", "-c", "cat > " + outPath}); err != nil {
		t.Fatalf("launch: %v", err)
	}
	t.Cleanup(func() { _ = Kill(context.Background(), name) })

	waitFor(t, func() bool {
		_, err := os.Stat(outPath)
		return err == nil
	}, "session did not start")
	return name, outPath
}

func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal(msg)
}

func readSoon(t *testing.T, path, want string) string {
	t.Helper()
	var got string
	waitFor(t, func() bool {
		raw, err := os.ReadFile(path)
		if err != nil {
			return false
		}
		got = string(raw)
		return strings.Contains(got, want)
	}, "never received %q; got "+got)
	return got
}

func TestSendDeliversTextLiterally(t *testing.T) {
	requireTmux(t)
	name, out := newSink(t)

	// Shell metacharacters and the literal word "Enter" must all survive: a
	// message is user text, never something to interpret.
	const message = `run $HOME/x.sh && echo "done" ` + "`whoami`" + ` — press Enter`
	if err := Send(context.Background(), name, message); err != nil {
		t.Fatal(err)
	}

	got := readSoon(t, out, "run $HOME")
	if !strings.Contains(got, message) {
		t.Errorf("delivered text was altered:\n got %q\nwant %q", got, message)
	}
}

func TestSendClearsAnExistingDraft(t *testing.T) {
	requireTmux(t)
	name, out := newSink(t)
	ctx := context.Background()

	// Regression: a message used to be typed on top of whatever was already in
	// the prompt box, fusing a half-written draft and the new message into one
	// garbled prompt — observed in the wild as "this is falserun the tests".
	if _, err := run(ctx, "send-keys", "-t", name, "-l", "HALF WRITTEN DRAFT"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)

	if err := Send(ctx, name, "the real message"); err != nil {
		t.Fatal(err)
	}

	got := readSoon(t, out, "the real message")
	if strings.Contains(got, "HALF WRITTEN DRAFT") {
		t.Errorf("the draft was not cleared before typing; agent received %q", got)
	}
}

func TestSendHandlesMultilineText(t *testing.T) {
	requireTmux(t)
	name, out := newSink(t)

	// Raw newlines would submit at the first line break and scatter the rest
	// across follow-up turns, so multi-line text goes through bracketed paste.
	const message = "first line\nsecond line\nthird line"
	if err := Send(context.Background(), name, message); err != nil {
		t.Fatal(err)
	}

	got := readSoon(t, out, "third line")
	for _, line := range []string{"first line", "second line", "third line"} {
		if !strings.Contains(got, line) {
			t.Errorf("lost %q from a multi-line message; got %q", line, got)
		}
	}
}

func TestSendRejectsEmptyMessages(t *testing.T) {
	requireTmux(t)
	name, _ := newSink(t)
	if err := Send(context.Background(), name, "   \n  "); err == nil {
		t.Error("expected an empty message to be refused rather than submitted")
	}
}

func TestListOnlyReportsOurSessions(t *testing.T) {
	requireTmux(t)
	ctx := context.Background()

	// A session the user created for their own work must never be typed into.
	foreign := "user-own-session-" + filepath.Base(t.TempDir())
	if err := Launch(ctx, foreign, t.TempDir(), []string{"sh", "-c", "sleep 30"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = Kill(context.Background(), foreign) })

	ours, _ := newSink(t)

	sessions, err := List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var sawOurs bool
	for _, session := range sessions {
		if session.Name == foreign {
			t.Error("List returned a session agentman does not own")
		}
		if session.Name == ours {
			sawOurs = true
		}
	}
	if !sawOurs {
		t.Error("List did not return our own session")
	}
}

func TestOwnsPIDMatchesDescendants(t *testing.T) {
	requireTmux(t)
	name, _ := newSink(t)

	sessions, err := List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var pane int
	for _, session := range sessions {
		if session.Name == name {
			pane = session.PanePID
		}
	}
	if pane == 0 {
		t.Fatal("could not find the pane pid")
	}

	// Ancestry is how a discovered agent is matched to the tmux session that
	// can type into it, so a pane must claim itself and nothing unrelated.
	if !OwnsPID(pane, pane) {
		t.Error("a pane should own its own process")
	}
	if OwnsPID(pane, os.Getpid()) {
		t.Error("the test process is not inside the pane but was claimed by it")
	}
	if OwnsPID(pane, 1) {
		t.Error("init must never be claimed")
	}
}

func TestNewNameDoesNotCollideWithinOneSecond(t *testing.T) {
	seen := make(map[string]struct{}, 1000)
	for range 1000 {
		name := NewName("codex")
		if _, exists := seen[name]; exists {
			t.Fatalf("NewName returned duplicate %q", name)
		}
		seen[name] = struct{}{}
	}
}
