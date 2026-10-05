package source

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// The events agy 1.2.17 logged for a session of research prompts, in order,
// with the prompts themselves left out.
func agyLog(t *testing.T, throughLine int) string {
	t.Helper()
	raw, err := os.ReadFile("testdata/antigravity-cli.log")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(raw), "\n")
	return strings.Join(lines[:throughLine], "\n")
}

const agyLoggedConversation = "ef116096-84c3-4723-bbc3-aed0f3cca68b"

func TestAntigravityLogSaysWhatTheTranscriptCannot(t *testing.T) {
	for _, tc := range []struct {
		name     string
		through  int
		lastStep int
		want     protocol.State
		known    bool
	}{
		// Esc cancelled the turn and nothing reached the transcript.
		{"cancelled with Esc", 6, 6, protocol.StateIdle, true},
		// A permission prompt is on screen: the call (step 8) is written,
		// its result (step 9) waits on the user.
		{"waiting on a permission", 10, 8, protocol.StateWaitingInput, true},
		{"permission answered", 11, 8, "", false},
		// ask_question logs no response; its answer reaching the transcript
		// is what settles it.
		{"waiting on a question", 43, 35, protocol.StateWaitingInput, true},
		{"question answered", 43, 36, "", false},
		// A new message after a cancel starts a turn again.
		{"a message after a cancel", 8, 7, "", false},
	} {
		got, known := antigravityLogState(agyLog(t, tc.through), agyLoggedConversation, tc.lastStep)
		if got != tc.want || known != tc.known {
			t.Errorf("%s: %q %v, want %q %v", tc.name, got, known, tc.want, tc.known)
		}
	}
}

// A confirmation is logged without its conversation. One surfaced after the
// process's latest message went elsewhere — the user started another
// conversation — is not this one's.
func TestAntigravityLogIgnoresAnotherConversationsPrompt(t *testing.T) {
	text := agyLog(t, 8) + "\n" +
		"I1005 10:30:43.000000     732 conversation_manager.go:779] Forwarding user message to conversation 0cf8ac1d-48f6-4aee-b760-80b7dd1eb885 (items=1, media=0)\n" +
		`I1005 10:30:45.517362    1952 tool_confirmation_manager.go:226] Surfacing tool confirmation: "WriteToFile" at step 9`
	if got, known := antigravityLogState(text, agyLoggedConversation, 7); known {
		t.Errorf("state %q", got)
	}
}

// An agy outside Agentman's panes, blocked on a permission: only its log says
// so.
func TestAntigravityUnmanagedSessionWaitingOnAPermission(t *testing.T) {
	home := t.TempDir()
	writeAntigravityConversation(t, home, agConversation, agLinePrompt, agLineCall)
	log := filepath.Join(home, ".gemini", "antigravity-cli", "log", "cli-20261005_102942.log")
	if err := os.MkdirAll(filepath.Dir(log), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "I1005 10:30:42.861050     732 conversation_manager.go:779] Forwarding user message to conversation " + agConversation + " (items=1, media=0)\n" +
		"I1005 10:30:45.517362    1952 tool_confirmation_manager.go:226] Surfacing tool confirmation: \"RunCommand\" at step 2\n"
	if err := os.WriteFile(log, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	s := newTestAntigravity(t, home, 900, map[int]antigravityProcess{
		900: {cwd: "/work/api", conversations: []string{agConversation}, log: log},
	})
	if got := discoverAntigravity(t, s)["antigravity:"+agConversation].State; got != protocol.StateWaitingInput {
		t.Errorf("state = %s", got)
	}

	// Answered: the result is on disk.
	file, err := os.OpenFile(s.transcriptPath(agConversation), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	file.WriteString(agLineResult + "\n")
	file.Close()
	if got := discoverAntigravity(t, s)["antigravity:"+agConversation].State; got != protocol.StateBusy {
		t.Errorf("after the answer: state = %s", got)
	}
}

func TestParseAntigravityOpenFilesFindsTheLog(t *testing.T) {
	output := "p64801\nfcwd\nn/Users/me/work\n" +
		"f3w\nn/Users/me/.gemini/antigravity-cli/log/cli-20261005_102942.log\n" +
		"f24\nn/Users/me/.gemini/antigravity-cli/brain/" + agConversation + "\n"
	got := parseAntigravityOpenFiles("/Users/me", []byte(output))[64801]
	if got.log != "/Users/me/.gemini/antigravity-cli/log/cli-20261005_102942.log" ||
		len(got.conversations) != 1 || got.cwd != "/Users/me/work" {
		t.Errorf("process = %+v", got)
	}
}
