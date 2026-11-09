package question

import (
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
)

func TestCommandsThatBringUpAMacOSDialogAreSaid(t *testing.T) {
	for _, c := range []struct {
		detail, want string
	}{
		{"crontab -l | grep -v x | crontab -", "administer"},
		{"osascript -e 'tell application \"Finder\" to quit'", "control another app"},
		{"/usr/sbin/screencapture -x shot.png", "record your screen"},
		{"cd x && crontab jobs.txt", "administer"},
		{"bash -c \"crontab /tmp/x\"", "administer"},
		{"sh -c 'osascript -e \"tell app\"'", "control another app"},
		{"sudo crontab -e", "administer"},
	} {
		got := MacOSNotice(&protocol.Question{Title: "Bash command", Detail: c.detail})
		if !strings.Contains(got, c.want) {
			t.Errorf("%q: got %q", c.detail, got)
		}
	}
}

func TestOrdinaryCommandsSayNothing(t *testing.T) {
	for _, detail := range []string{"npm test", "crontab -l", "cat crontabs.txt", "grep -r mycrontab .", "echo noosascript",
		"cat /etc/crontab", "rg osascript .", "git log --grep crontab", "osascript -e 'display notification \"done\"'"} {
		if got := MacOSNotice(&protocol.Question{Detail: detail}); got != "" {
			t.Errorf("%q: got %q", detail, got)
		}
	}
	if MacOSNotice(nil) != "" {
		t.Error("nil question")
	}
}

func TestAQuestionThatOnlyMentionsACommandSaysNothing(t *testing.T) {
	q := &protocol.Question{Title: "Should I use crontab?", Prompt: "Use crontab or launchd?"}
	if got := MacOSNotice(q); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestCodexsCommandIsInItsPrompt(t *testing.T) {
	q := &protocol.Question{Prompt: "$ crontab jobs.txt", Detail: "Would you like to run the following command?"}
	if got := MacOSNotice(q); !strings.Contains(got, "administer") {
		t.Fatalf("got %q", got)
	}
}
