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
	} {
		got := MacOSNotice(&protocol.Question{Title: "Bash command", Detail: c.detail})
		if !strings.Contains(got, c.want) {
			t.Errorf("%q: got %q", c.detail, got)
		}
	}
}

func TestOrdinaryCommandsSayNothing(t *testing.T) {
	for _, detail := range []string{"npm test", "crontab -l", "cat crontabs.txt", "grep -r mycrontab .", "echo noosascript"} {
		if got := MacOSNotice(&protocol.Question{Detail: detail}); got != "" {
			t.Errorf("%q: got %q", detail, got)
		}
	}
	if MacOSNotice(nil) != "" {
		t.Error("nil question")
	}
}
