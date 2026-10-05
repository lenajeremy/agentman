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
	for _, key := range []string{"Tab", "BTab", "Enter", "Escape", "Up", "Down", "Left", "Right", "Space"} {
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
		if err := checkKeys(name, []string{"Enter"}); err == nil {
			t.Errorf("pane %q was accepted", name)
		}
		if err := SendKeys(context.Background(), name, "Enter"); err == nil {
			t.Errorf("SendKeys pressed a key in %q", name)
		}
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
