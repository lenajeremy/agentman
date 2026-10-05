package tmux

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// launchRefusable starts a pane that draws a menu, waits for one key, then
// after a moment draws a composer and records whatever is typed into it.
// That is how Codex's "No, and tell Codex what to do differently" behaves:
// the turn ends, and the composer comes back a little later.
func launchRefusable(t *testing.T, composerAfter string) (name, typed string) {
	t.Helper()
	dir := t.TempDir()
	typed = filepath.Join(dir, "typed")
	script := "printf 'Would you like to run the following command?\\n  3. No, and tell Codex what to do differently (esc)\\n'; " +
		"stty raw -echo; dd bs=1 count=1 of=" + filepath.Join(dir, "key") + " 2>/dev/null; stty -raw echo; " +
		"sleep " + composerAfter + "; clear; printf 'Conversation interrupted\\n› Ask Codex to do anything\\n'; cat > " + typed
	name = NewName("refusetest")
	if err := Launch(context.Background(), name, dir, []string{"sh", "-c", script}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = Kill(context.Background(), name) })
	waitFor(t, func() bool {
		pane, _ := Capture(context.Background(), name)
		return strings.Contains(pane, "tell Codex")
	}, "the menu never appeared")
	return name, typed
}

func composerShown(pane string) bool { return strings.Contains(pane, "› Ask Codex") }

func TestRefuseThenSendTypesOnceTheComposerIsBack(t *testing.T) {
	requireTmux(t)
	name, typed := launchRefusable(t, "0.6")
	if err := RefuseThenSend(context.Background(), name, "3", "use echo instead", composerShown); err != nil {
		t.Fatal(err)
	}
	got := readSoon(t, typed, "use echo instead")
	if !strings.HasPrefix(strings.TrimLeft(got, "\x15"), "use echo instead") {
		t.Errorf("typed %q", got)
	}
}

func TestRefuseThenSendStopsWhenTheComposerNeverReturns(t *testing.T) {
	requireTmux(t)
	name, typed := launchRefusable(t, "30")
	err := RefuseThenSend(context.Background(), name, "3", "use echo instead", composerShown)
	if err == nil {
		t.Fatal("the note was reported sent without a composer to send it to")
	}
	time.Sleep(200 * time.Millisecond)
	if raw, _ := os.ReadFile(typed); len(raw) > 0 {
		t.Errorf("typed %q into a pane that never showed its composer", raw)
	}
}
