package tmux

import (
	"context"
	"slices"
	"strings"
	"testing"
)

// None of these tests run tmux: every refusal happens before it would be
// started, and the arguments are checked as built.

func TestEveryNamedKeyIsAccepted(t *testing.T) {
	for _, key := range []string{"Tab", "BTab", "Enter", "Escape", "Up", "Down", "Left", "Right", "Space", "M-j"} {
		if err := checkKeys("agentman-cursor-1-a", []string{key}); err != nil {
			t.Errorf("%s was refused: %v", key, err)
		}
	}
}

// Anything else would either type text — tmux types an unknown name as its
// letters — or reach a key that does something the allowlist never offered.
func TestKeysOutsideTheAllowlistAreRefused(t *testing.T) {
	for _, key := range []string{
		"C-c", "C-u", "C-b", "M-x", "S-Left", "F1", "q", "y", "1", "", "tab", "ENTER",
		"Enter Enter", "-l", "--", "Escape\n", "Tab;", "BSpace", "DC", "PageUp",
		// alt+j is allowed by name, not as the first of a family.
		"M-J", "M-k", "M-Enter", "M-C-j", "C-M-j", "M-j ", " M-j", "m-j", "Escape M-j", "j",
	} {
		if err := checkKeys("agentman-cursor-1-a", []string{key}); err == nil {
			t.Errorf("%q was accepted", key)
		}
	}
}

// One bad key refuses the whole call, before any key is pressed.
func TestOneRefusedKeyRefusesTheCall(t *testing.T) {
	if err := checkKeys("agentman-cursor-1-a", []string{"Down", "Down", "C-c", "Enter"}); err == nil {
		t.Fatal("a call with a refused key in the middle was accepted")
	}
	err := SendKeys(context.Background(), "agentman-cursor-1-a", "Down", "C-c")
	if err == nil || !strings.Contains(err.Error(), "C-c") {
		t.Fatalf("SendKeys = %v, want the refusal naming the key", err)
	}
}

func TestKeysAreOnlyPressedInAgentmanPanes(t *testing.T) {
	for _, name := range []string{"", "work", "agentman-", "my-agentman-pane", "%3", "agentman"} {
		for _, key := range []string{"Enter", "M-j"} {
			if err := checkKeys(name, []string{key}); err == nil {
				t.Errorf("%s in pane %q was accepted", key, name)
			}
			if err := SendKeys(context.Background(), name, key); err == nil {
				t.Errorf("SendKeys pressed %s in %q", key, name)
			}
		}
	}
}

// Antigravity's subagent panel, then a move and a confirmation: the whole
// sequence is checked before alt+j is pressed, so a bad key after it leaves
// the panel closed.
func TestAltJIsCheckedWithTheRestOfTheCall(t *testing.T) {
	if err := checkKeys("agentman-antigravity-1-a", []string{"M-j", "Down", "Enter"}); err != nil {
		t.Fatalf("opening the subagent panel and choosing was refused: %v", err)
	}
	err := SendKeys(context.Background(), "agentman-antigravity-1-a", "M-j", "Down", "M-x")
	if err == nil || !strings.Contains(err.Error(), "M-x") {
		t.Fatalf("SendKeys = %v, want the refusal naming M-x", err)
	}
	if got := keyArgs("agentman-antigravity-1-a", "M-j"); !slices.Equal(got,
		[]string{"send-keys", "-t", "agentman-antigravity-1-a", "M-j"}) {
		t.Fatalf("args = %q", got)
	}
}

func TestACallMustNameAFewKeys(t *testing.T) {
	if err := checkKeys("agentman-kiro-1-a", nil); err == nil {
		t.Error("a call with no keys was accepted")
	}
	burst := make([]string, maxNamedKeys+1)
	for i := range burst {
		burst[i] = "Down"
	}
	if err := checkKeys("agentman-kiro-1-a", burst); err == nil {
		t.Errorf("a burst of %d keys was accepted", len(burst))
	}
	if err := checkKeys("agentman-kiro-1-a", burst[:maxNamedKeys]); err != nil {
		t.Errorf("%d keys were refused: %v", maxNamedKeys, err)
	}
}

// The key goes as a name, not with -l: with -l, tmux would type the letters
// "B", "T", "a", "b" into the prompt instead of pressing shift+Tab.
func TestAKeyIsSentByName(t *testing.T) {
	got := keyArgs("agentman-cursor-1-a", "BTab")
	want := []string{"send-keys", "-t", "agentman-cursor-1-a", "BTab"}
	if !slices.Equal(got, want) {
		t.Fatalf("args = %q, want %q", got, want)
	}
}
