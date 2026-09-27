package parser

import (
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Steps Antigravity CLI 1.2.12 wrote for one real turn. Thinking and the tail
// of long strings are cut; the rest is as written, carriage returns included.
const (
	agStepPrompt = `{"step_index": 0, "source": "USER_EXPLICIT", "type": "USER_INPUT", "status": "DONE", "created_at": "2026-09-27T17:02:00Z", "content": "<USER_REQUEST>\nRun the shell command: ls -la   then tell me how many files there are.\n</USER_REQUEST>\n<ADDITIONAL_METADATA>\nThe current local time is: 2026-09-27T18:02:00+01:00.\n</ADDITIONAL_METADATA>"}`
	agStepCall   = `{"step_index": 1, "source": "MODEL", "type": "PLANNER_RESPONSE", "status": "DONE", "created_at": "2026-09-27T17:02:00Z", "tool_calls": [{"name": "run_command", "args": {"CommandLine": "ls -la", "Cwd": "/work", "WaitMsBeforeAsync": 5000, "toolAction": "Listing files", "toolSummary": "Run ls -la"}}]}`
	agStepResult = `{"step_index": 2, "source": "MODEL", "type": "GENERIC", "status": "DONE", "created_at": "2026-09-27T17:02:03Z", "content": "Created At: 2026-09-27T18:02:03+01:00\nCompleted At: 2026-09-27T18:02:41+01:00\n\nThe command exited with code 0.\nOutput:\ntotal 8\r\n-rw-r--r--@ 1 mac  wheel    6 Sep 27 18:01 notes.txt\r\n\n"}`
	agStepReply  = `{"step_index": 3, "source": "MODEL", "type": "PLANNER_RESPONSE", "status": "DONE", "created_at": "2026-09-27T17:02:41Z", "content": "There is **1** regular file."}`
)

func parseAntigravity(lines ...string) []protocol.Message {
	p := NewAntigravityParser("antigravity:s")
	var out []protocol.Message
	for i, line := range lines {
		out = append(out, p.Parse(line, int64(i*1000))...)
	}
	return out
}

func TestAntigravityTurnBecomesPromptToolAndReply(t *testing.T) {
	got := parseAntigravity(agStepPrompt, agStepCall, agStepResult, agStepReply)
	if len(got) != 4 {
		t.Fatalf("got %d messages, want 4: %+v", len(got), got)
	}
	if got[0].Role != protocol.RoleUser ||
		got[0].Text != "Run the shell command: ls -la   then tell me how many files there are." {
		t.Errorf("prompt kept Antigravity's own wrapper: %q", got[0].Text)
	}
	running, settled := got[1], got[2]
	if running.Tool == nil || running.Tool.Name != "Shell" || running.Tool.Summary != "ls -la" ||
		running.Tool.Status != protocol.ToolRunning {
		t.Errorf("call = %+v", running)
	}
	if settled.ID != running.ID || settled.Tool.Status != protocol.ToolOK || settled.Ts != running.Ts {
		t.Errorf("result did not settle the call in place: %+v vs %+v", settled, running)
	}
	if strings.Contains(settled.Text, "Created At") || !strings.Contains(settled.Text, "notes.txt") {
		t.Errorf("result preview = %q", settled.Text)
	}
	if got[3].Role != protocol.RoleAssistant || got[3].Text != "There is **1** regular file." {
		t.Errorf("reply = %+v", got[3])
	}
}

// Paging reads backwards and meets each result before its call. The call must
// still come out as one settled row, not a running one and an orphan.
func TestAntigravityReadsBackwardsToTheSameRows(t *testing.T) {
	got := parseAntigravity(agStepReply, agStepResult, agStepCall, agStepPrompt)
	if len(got) != 3 {
		t.Fatalf("got %d messages, want 3: %+v", len(got), got)
	}
	call := got[1]
	if call.Tool == nil || call.Tool.Status != protocol.ToolOK || !strings.Contains(call.Text, "notes.txt") {
		t.Errorf("the call did not pick up its result: %+v", call)
	}
	forward := parseAntigravity(agStepPrompt, agStepCall, agStepResult, agStepReply)
	if call.ID != forward[2].ID {
		t.Errorf("backwards id %q, forwards %q: the two reads would not merge", call.ID, forward[2].ID)
	}
}

// The prompt and the tool call share a second. The app breaks ties by id, so
// ids must sort in step order: an unpadded "s10" would land before "s9".
func TestAntigravitySameSecondStepsKeepTheirOrder(t *testing.T) {
	got := parseAntigravity(agStepPrompt, agStepCall)
	if got[0].Ts != got[1].Ts {
		t.Fatalf("fixture no longer shares a second: %d vs %d", got[0].Ts, got[1].Ts)
	}
	if !(got[0].ID < got[1].ID) {
		t.Errorf("ids %q and %q sort out of step order", got[0].ID, got[1].ID)
	}
	if !(antigravityID(9) < antigravityID(10)) {
		t.Error("step 10 sorts before step 9")
	}
}

func TestAntigravityFailingCommandIsAnError(t *testing.T) {
	failed := strings.Replace(agStepResult, "exited with code 0", "exited with code 1", 1)
	got := parseAntigravity(agStepPrompt, agStepCall, failed)
	if got[2].Tool.Status != protocol.ToolError {
		t.Errorf("a non-zero exit was reported as %s", got[2].Tool.Status)
	}
}

// A turn interrupted before it said anything leaves a response holding only
// thinking. There is nothing to show for it.
func TestAntigravityThinkingOnlyResponseIsSilent(t *testing.T) {
	interrupted := `{"step_index": 5, "source": "MODEL", "type": "PLANNER_RESPONSE", "status": "DONE", "created_at": "2026-09-27T17:03:00Z", "thinking": "Considering the essay"}`
	if got := parseAntigravity(interrupted); len(got) != 0 {
		t.Errorf("got %+v", got)
	}
}
