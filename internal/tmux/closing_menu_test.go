package tmux

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fileMenuScript stands in for Kiro 2.37's prompt: an @ at the start of a
// word opens a file menu under the prompt that stays open to the end of
// the line; Escape closes it and keeps the words; Enter with it open picks
// the file instead of sending. Each Enter's outcome, and an Escape pressed
// with no menu open, are written to the file named by $1.
const fileMenuScript = `out="$1"
stty raw -echo
line=''; menu=0; prev=' '
draw() {
  printf '\033[2J\033[H› %s\r\n' "$line"
  [ "$menu" = 1 ] && printf '@notes.md\r\n esc to cancel · ↵ to select\r\n'
}
draw
enter=$(printf '\r'); esc=$(printf '\033'); kill=$(printf '\025')
while :; do
  k=$(dd bs=1 count=1 2>/dev/null)
  case "$k" in
    "$enter") if [ "$menu" = 1 ]; then printf 'picked\n' >> "$out"; else printf 'sent: %s\n' "$line" >> "$out"; fi; line=''; menu=0 ;;
    "$esc") if [ "$menu" = 1 ]; then menu=0; else printf 'escape with no menu\n' >> "$out"; fi ;;
    "$kill") line=''; menu=0 ;;
    '') line="$line "; prev=' ' ;;
    *) [ "$k" = '@' ] && [ "$prev" = ' ' ] && menu=1; line="$line$k"; prev="$k" ;;
  esac
  draw
done
`

func launchFileMenu(t *testing.T) (name, out string) {
	t.Helper()
	dir := t.TempDir()
	out = filepath.Join(dir, "outcomes")
	script := filepath.Join(dir, "prompt.sh")
	if err := os.WriteFile(script, []byte(fileMenuScript), 0o755); err != nil {
		t.Fatal(err)
	}
	name = "agentman-test-filemenu-" + filepath.Base(dir)
	if err := Launch(context.Background(), name, dir, []string{"sh", script, out}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = Kill(context.Background(), name) })
	waitFor(t, func() bool {
		pane, err := Capture(context.Background(), name)
		return err == nil && strings.Contains(pane, "›")
	}, "the prompt never drew")
	return name, out
}

func outcomes(t *testing.T, out string) string {
	t.Helper()
	time.Sleep(400 * time.Millisecond)
	raw, _ := os.ReadFile(out)
	return string(raw)
}

func fileMenuOpen(pane string) bool { return strings.Contains(pane, "esc to cancel · ↵ to select") }

// Typed and sent as Send does it, a message with an @word mid-line is lost
// to the menu; SendClosingMenu closes the menu first and sends it whole.
func TestAMenuTheMessageOpensIsClosedBeforeItIsSent(t *testing.T) {
	name, out := launchFileMenu(t)
	if err := Send(context.Background(), name, "Read @notes and say hi"); err != nil {
		t.Fatal(err)
	}
	if got := outcomes(t, out); got != "picked\n" {
		t.Fatalf("the stand-in doesn't lose the message as Kiro does: %q", got)
	}
	_ = os.Remove(out)
	if err := SendClosingMenu(context.Background(), name, "Read @notes and say hi", fileMenuOpen); err != nil {
		t.Fatal(err)
	}
	if got := outcomes(t, out); got != "sent: Read @notes and say hi\n" {
		t.Errorf("outcome = %q", got)
	}
}

// With no menu on screen, no Escape is pressed: in Kiro, one would stop
// the turn under way.
func TestNoMenuNoEscape(t *testing.T) {
	name, out := launchFileMenu(t)
	if err := SendClosingMenu(context.Background(), name, "just say hi", fileMenuOpen); err != nil {
		t.Fatal(err)
	}
	if got := outcomes(t, out); got != "sent: just say hi\n" {
		t.Errorf("outcome = %q", got)
	}
}
