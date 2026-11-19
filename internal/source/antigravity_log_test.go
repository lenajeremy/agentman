package source

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// agy refuses a turn for quota only in its log, as it did on this Mac on
// 2026-11-04; the transcript still ends at the prompt.
const agyQuotaLog = `I1104 10:36:58.529131    2508 conversation_manager.go:874] Forwarding user message to conversation 060fd15f-936b-4f82-ade4-f8e2fb2b4f78 (items=1, media=0)
E1104 11:11:09.518965    2509 errorreport.go:247] agent executor error: generating and executing: RESOURCE_EXHAUSTED (code 429): Individual quota reached. Please upgrade your subscription to increase your limits. Resets in 4h25m49s.
E1104 11:11:09.521864    2509 errorreport.go:247] generating and executing: RESOURCE_EXHAUSTED (code 429): Individual quota reached. Please upgrade your subscription to increase your limits. Resets in 4h25m49s.
I1104 11:11:09.521928     215 quota_manager.go:45] doRefreshQuota: starting reload (force=false)
`

func TestAntigravityLogSaysWhenOutOfQuota(t *testing.T) {
	conversation := "060fd15f-936b-4f82-ade4-f8e2fb2b4f78"
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.Local)
	limit := antigravityLogLimit(agyQuotaLog, conversation, now)
	at := time.Date(2026, 11, 4, 11, 11, 9, 0, time.Local)
	if limit == nil || limit.Text != "Individual quota reached" || limit.At != at.UnixMilli() ||
		limit.ResetsAt != at.Add(4*time.Hour+25*time.Minute+49*time.Second).UnixMilli() {
		t.Fatalf("limit %+v", limit)
	}
	// The refused turn is over, though the transcript ends at the prompt.
	if state, ok := antigravityLogState(agyQuotaLog, conversation, 0); !ok || state != protocol.StateIdle {
		t.Fatalf("state %q %v", state, ok)
	}
	// Another conversation's refusal is not this one's.
	if limit := antigravityLogLimit(agyQuotaLog, "aaaaaaaa-0000-0000-0000-000000000000", now); limit != nil {
		t.Fatalf("another conversation: %+v", limit)
	}
	// A new message is a new turn.
	again := agyQuotaLog + "I1104 12:22:58.140736  101367 conversation_manager.go:874] Forwarding user message to conversation " + conversation + " (items=1, media=0)\n"
	if limit := antigravityLogLimit(again, conversation, now); limit != nil {
		t.Fatalf("after a new message: %+v", limit)
	}
	// Last year's date, read in January.
	if limit := antigravityLogLimit(agyQuotaLog, conversation, time.Date(2027, 1, 2, 9, 0, 0, 0, time.Local)); limit == nil ||
		time.UnixMilli(limit.At).Year() != 2026 {
		t.Fatalf("across the year: %+v", limit)
	}
}
