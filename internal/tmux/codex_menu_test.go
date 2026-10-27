package tmux

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// codexMenuScript stands in for Codex 0.162's folder trust prompt: a number
// picks and confirms its option, except the option already selected, which
// only Enter confirms. What was chosen, and every key after it, is written
// to the file named by $1.
const codexMenuScript = `out="$1"
stty raw -echo
printf '\033[2J\033[H'
printf '  Folder access\r\n  /Users/me/code/shop\r\n\r\n'
printf '  Trust this folder? Codex can read, edit, and run files here, subject to your\r\n'
printf '  permission settings. Your trust decision will be saved.\r\n\r\n'
printf '› 1. Trust and continue\r\n  2. Back to Agent Command Center\r\n\r\n  enter continue · esc back\r\n'
enter=$(printf '\r')
while :; do
  k=$(dd bs=1 count=1 2>/dev/null)
  case "$k" in
    1) continue ;;
    2) choice=2 ;;
    "$enter") choice=1 ;;
    *) continue ;;
  esac
  break
done
printf 'chose %s' "$choice" > "$out"
printf '\033[2J\033[H› Ask Codex to do anything\r\n'
while :; do
  k=$(dd bs=1 count=1 2>/dev/null)
  printf ' then %s' "$(printf '%s' "$k" | od -An -c | tr -d ' ')" >> "$out"
done
`

func launchCodexMenu(t *testing.T) (name, out string) {
	t.Helper()
	dir := t.TempDir()
	out = filepath.Join(dir, "chosen")
	script := filepath.Join(dir, "menu.sh")
	if err := os.WriteFile(script, []byte(codexMenuScript), 0o755); err != nil {
		t.Fatal(err)
	}
	name = "agentman-test-menu-" + filepath.Base(dir)
	if err := Launch(context.Background(), name, dir, []string{"sh", script, out}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = Kill(context.Background(), name) })
	waitFor(t, func() bool {
		pane, err := Capture(context.Background(), name)
		return err == nil && strings.Contains(pane, "enter continue")
	}, "the menu never drew")
	return name, out
}

func chosen(t *testing.T, out string) string {
	t.Helper()
	readSoon(t, out, "chose")
	// Long enough for a stray Enter to arrive and be written down.
	time.Sleep(400 * time.Millisecond)
	raw, _ := os.ReadFile(out)
	return string(raw)
}

// The option already selected: its number did nothing, and the trust prompt
// answered from the phone stayed on screen while the phone said delivered.
func TestCodexMenuConfirmsTheSelectedOptionWithEnter(t *testing.T) {
	requireTmux(t)
	name, out := launchCodexMenu(t)
	if err := AnswerCodexMenu(context.Background(), name, "1"); err != nil {
		t.Fatal(err)
	}
	if got := chosen(t, out); got != "chose 1" {
		t.Fatalf("got %q", got)
	}
}

// Another option's number confirms it, and nothing follows: an Enter now
// would land in whatever the menu gave way to.
func TestCodexMenuAnotherOptionTakesItsNumberAlone(t *testing.T) {
	requireTmux(t)
	name, out := launchCodexMenu(t)
	if err := AnswerCodexMenu(context.Background(), name, "2"); err != nil {
		t.Fatal(err)
	}
	if got := chosen(t, out); got != "chose 2" {
		t.Fatalf("got %q", got)
	}
}

func TestCodexMenuRefusesAnOptionItDoesNotShow(t *testing.T) {
	requireTmux(t)
	name, out := launchCodexMenu(t)
	if err := AnswerCodexMenu(context.Background(), name, "3"); err == nil {
		t.Fatal("answered an option the menu does not have")
	}
	time.Sleep(300 * time.Millisecond)
	if _, err := os.Stat(out); err == nil {
		t.Fatal("a key reached the menu")
	}
}
