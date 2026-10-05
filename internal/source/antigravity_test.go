package source

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/tmux"
)

const (
	agConversation = "16d0a7e5-165d-4de6-a2d4-bd791f805bdf"
	agLinePrompt   = `{"step_index": 0, "source": "USER_EXPLICIT", "type": "USER_INPUT", "status": "DONE", "created_at": "2026-09-27T17:02:00Z", "content": "<USER_REQUEST>\nlist the files\n</USER_REQUEST>"}`
	agLineCall     = `{"step_index": 1, "source": "MODEL", "type": "PLANNER_RESPONSE", "status": "DONE", "created_at": "2026-09-27T17:02:00Z", "tool_calls": [{"name": "run_command", "args": {"CommandLine": "ls -la"}}]}`
	agLineResult   = `{"step_index": 2, "source": "MODEL", "type": "GENERIC", "status": "DONE", "created_at": "2026-09-27T17:02:03Z", "content": "The command exited with code 0.\nOutput:\nnotes.txt\n"}`
	agLineReply    = `{"step_index": 3, "source": "MODEL", "type": "PLANNER_RESPONSE", "status": "DONE", "created_at": "2026-09-27T17:02:41Z", "content": "There is 1 file."}`
)

// writeAntigravityConversation lays out a conversation the way agy does.
func writeAntigravityConversation(t *testing.T, home, conversation string, lines ...string) string {
	t.Helper()
	root := filepath.Join(home, ".gemini", "antigravity-cli")
	logs := filepath.Join(root, "brain", conversation, ".system_generated", "logs")
	if err := os.MkdirAll(logs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "presence"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "presence", conversation+".lock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "settings.json"), []byte(`{"model":"Gemini 3.8 Flash (High)"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(logs, "transcript_full.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// newTestAntigravity builds a source whose process table has one agy at pid,
// holding whatever held names — the stand-in for lsof.
func newTestAntigravity(t *testing.T, home string, pid int, held map[int]antigravityProcess, panes ...tmux.Session) *AntigravitySource {
	t.Helper()
	s, err := NewAntigravitySource(home)
	if err != nil {
		t.Fatal(err)
	}
	s.listPanes = func(context.Context) ([]tmux.Session, error) { return panes, nil }
	s.snapshotProcesses = func(context.Context) (*tmux.ProcessTree, error) {
		if pid == 0 {
			return tmux.ProcessTreeFromTable(""), nil
		}
		return tmux.ProcessTreeFromTable(strings.Join([]string{
			strconv.Itoa(pid) + " 1 agy",
			"4242 1 /usr/bin/vim",
		}, "\n")), nil
	}
	s.openConversations = func(context.Context, string, []int) map[int]antigravityProcess { return held }
	s.capturePane = func(context.Context, string) (string, error) { return "", nil }
	return s
}

func discoverAntigravity(t *testing.T, s *AntigravitySource) map[string]protocol.Session {
	t.Helper()
	found, err := s.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]protocol.Session{}
	for _, session := range found {
		byID[session.ID] = session
	}
	return byID
}

func TestAntigravityFindsTheConversationARunningAgyHolds(t *testing.T) {
	home := t.TempDir()
	writeAntigravityConversation(t, home, agConversation, agLinePrompt, agLineCall, agLineResult, agLineReply)
	s := newTestAntigravity(t, home, 900, map[int]antigravityProcess{
		900: {cwd: "/work/api", conversations: []string{agConversation}},
	})
	session, ok := discoverAntigravity(t, s)["antigravity:"+agConversation]
	if !ok {
		t.Fatal("the running conversation was not found")
	}
	if session.Cwd != "/work/api" || session.Name != "list the files" || session.State != protocol.StateIdle {
		t.Errorf("session = %+v", session)
	}
	if session.Model != "Gemini 3.8 Flash (High)" || session.Inject != protocol.InjectNone {
		t.Errorf("model %q inject %s", session.Model, session.Inject)
	}
}

// agy never removes its presence lock, so a conversation on disk says nothing
// about whether it is running. With no agy holding it, it must not be listed.
func TestAntigravityIgnoresConversationsNoProcessHolds(t *testing.T) {
	home := t.TempDir()
	writeAntigravityConversation(t, home, agConversation, agLinePrompt, agLineReply)
	if sessions := discoverAntigravity(t, newTestAntigravity(t, home, 0, nil)); len(sessions) != 0 {
		t.Errorf("a stopped conversation was listed: %v", sessions)
	}
}

func TestAntigravityBindsToItsPaneAndReadsItsState(t *testing.T) {
	home := t.TempDir()
	writeAntigravityConversation(t, home, agConversation, agLinePrompt, agLineCall)
	pane := tmux.Session{Name: "agentman-antigravity-1-a", PanePID: 900, Cwd: "/work/api"}
	s := newTestAntigravity(t, home, 900, map[int]antigravityProcess{
		900: {cwd: "/work/api", conversations: []string{agConversation}},
	}, pane)
	s.capturePane = func(context.Context, string) (string, error) {
		return "● Bash(ls -la) (ctrl+o to expand)\n\nCommand\n" + strings.Repeat("─", 60) + "\n\n" +
			"Requesting permission for:\n   ls -la\n\nRun this command?\n> 1. Yes, run command\n  2. No, cancel\n\n" +
			"  ↑/↓ Navigate · tab Amend · ctrl+g edit/expand command\n" +
			"esc to cancel                                          Gemini 3.8 Flash · high\n", nil
	}
	sessions := discoverAntigravity(t, s)
	session, ok := sessions["antigravity:tmux-agentman-antigravity-1-a"]
	if !ok || len(sessions) != 1 {
		t.Fatalf("the session was not keyed on its pane exactly once: %v", sessions)
	}
	if session.State != protocol.StateWaitingInput || session.Question == nil ||
		session.Question.Detail != "ls -la" || session.Inject != protocol.InjectTmux {
		t.Errorf("session = %+v", session)
	}
	// The pane names the model actually in use; settings only hold the default.
	if session.Model != "Gemini 3.8 Flash (high)" {
		t.Errorf("model = %q, want the pane's", session.Model)
	}
}

// Before its first message agy has no conversation — typically it is sitting
// at the folder trust prompt. That pane is still a session the phone can act on.
func TestAntigravityListsAPaneAtItsTrustPrompt(t *testing.T) {
	pane := tmux.Session{
		Name: "agentman-antigravity-1-a", PanePID: 900, Cwd: "/work/new",
		Created: time.Unix(1790528359, 0),
	}
	s := newTestAntigravity(t, t.TempDir(), 900, map[int]antigravityProcess{900: {cwd: "/work/new"}}, pane)
	s.capturePane = func(context.Context, string) (string, error) {
		return "Accessing workspace:\n/work/new\nDo you trust the contents of this project?\n" +
			"> Yes, I trust this folder\n  No, exit\n  ↑/↓ Navigate · enter Confirm\n", nil
	}
	session, ok := discoverAntigravity(t, s)["antigravity:tmux-agentman-antigravity-1-a"]
	if !ok || session.State != protocol.StateWaitingInput || session.Question == nil ||
		session.Question.Title != "Workspace trust" {
		t.Fatalf("session = %+v", session)
	}
}

func TestAntigravityStateFromTheLastStep(t *testing.T) {
	for name, tc := range map[string]struct {
		lines []string
		want  protocol.State
	}{
		"prompt":      {[]string{agLinePrompt}, protocol.StateBusy},
		"tool call":   {[]string{agLinePrompt, agLineCall}, protocol.StateBusy},
		"tool result": {[]string{agLinePrompt, agLineCall, agLineResult}, protocol.StateBusy},
		"reply":       {[]string{agLinePrompt, agLineCall, agLineResult, agLineReply}, protocol.StateIdle},
		"step still running": {[]string{strings.Replace(agLineReply, `"DONE"`, `"RUNNING"`, 1)},
			protocol.StateBusy},
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			writeAntigravityConversation(t, home, agConversation, tc.lines...)
			s := newTestAntigravity(t, home, 900, map[int]antigravityProcess{
				900: {cwd: "/w", conversations: []string{agConversation}},
			})
			if got := discoverAntigravity(t, s)["antigravity:"+agConversation].State; got != tc.want {
				t.Errorf("state = %s, want %s", got, tc.want)
			}
		})
	}
}

// Output captured from `lsof -w -a -p <pid> -Fpfn` against a live agy.
func TestParseAntigravityOpenFiles(t *testing.T) {
	home := "/Users/mac"
	output := "p64801\nfcwd\nn/Users/mac/Desktop/agentman\n" +
		"f14\nn/Users/mac/.gemini/antigravity-cli/settings.json\n" +
		"f24\nn/Users/mac/.gemini/antigravity-cli/brain/" + agConversation + "\n" +
		"f25\nn/Users/mac/.gemini/antigravity-cli/brain/" + agConversation + "/.user_uploaded\n" +
		"f26\nn/Users/mac/.gemini/antigravity-cli/brain\n" +
		"f27\nn/Users/mac/.gemini/antigravity-cli/conversations\n" +
		"p88260\nfcwd\nn/tmp/probe\n" +
		"f14u\nn/Users/mac/.gemini/antigravity-cli/conversations/c5db7d82-0a26-4772-89df-66b903700eb0.db\n" +
		"f15u\nn/Users/mac/.gemini/antigravity-cli/conversations/c5db7d82-0a26-4772-89df-66b903700eb0.db-wal\n"
	got := parseAntigravityOpenFiles(home, []byte(output))
	if process := got[64801]; process.cwd != "/Users/mac/Desktop/agentman" ||
		len(process.conversations) != 1 || process.conversations[0] != agConversation {
		t.Errorf("idle agy = %+v", process)
	}
	// The directories themselves carry no conversation id and are not one.
	if process := got[88260]; process.cwd != "/tmp/probe" || len(process.conversations) != 1 ||
		process.conversations[0] != "c5db7d82-0a26-4772-89df-66b903700eb0" {
		t.Errorf("busy agy = %+v", process)
	}
}

func TestAntigravityPagesAndFollows(t *testing.T) {
	home := t.TempDir()
	path := writeAntigravityConversation(t, home, agConversation, agLinePrompt, agLineCall)
	s := newTestAntigravity(t, home, 900, map[int]antigravityProcess{
		900: {cwd: "/w", conversations: []string{agConversation}},
	})
	discoverAntigravity(t, s)
	id := "antigravity:" + agConversation

	page, err := s.Page(context.Background(), id, "", 10)
	if err != nil || len(page.Messages) != 2 {
		t.Fatalf("page = %+v, %v", page, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out := make(chan []protocol.Message, 8)
	done := make(chan error, 1)
	go func() { done <- s.Follow(ctx, id, out) }()
	time.Sleep(3 * followInterval)
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	file.WriteString(agLineResult + "\n" + agLineReply + "\n")
	file.Close()

	var got []protocol.Message
	for len(got) < 2 {
		select {
		case batch := <-out:
			got = append(got, batch...)
		case <-ctx.Done():
			t.Fatalf("follow delivered %+v", got)
		}
	}
	cancel()
	<-done
	// The call was made before the follow started; the seeded parser still
	// knows it, so the result settles that row instead of being dropped.
	if got[0].ID != page.Messages[1].ID || got[0].Tool == nil || got[0].Tool.Status != protocol.ToolOK ||
		got[0].Tool.Summary != "ls -la" {
		t.Errorf("result = %+v", got[0])
	}
	if got[1].Text != "There is 1 file." {
		t.Errorf("reply = %+v", got[1])
	}
}

// A subagent is a conversation of its own, and the agy that started it holds
// its brain directory open next to its parent's. While it works, its
// transcript is the newest of the two — and "the newest" is how a process's
// conversation used to be chosen, so the session became the subagent: its
// steps under the folder's name, stuck at busy, the parent's feed gone.
// Captured on 1.2.17 with lsof while a research subagent was blocked.
func TestAntigravityStaysOnTheParentWhileASubagentWorks(t *testing.T) {
	home := t.TempDir()
	const child = "659d2256-2d1f-4ecf-9f18-e0452e64434e"
	writeAntigravityConversation(t, home, agConversation, agLinePrompt, agLineCall, agLineResult, agLineReply)
	childTranscript := writeAntigravityConversation(t, home, child,
		`{"step_index":0,"source":"SYSTEM","type":"SYSTEM_MESSAGE","status":"DONE","created_at":"2026-10-05T09:39:44Z","content":"<SYSTEM_MESSAGE>…</SYSTEM_MESSAGE>"}`,
		`{"step_index":1,"source":"MODEL","type":"PLANNER_RESPONSE","status":"DONE","created_at":"2026-10-05T09:39:44Z","tool_calls":[{"name":"view_file","args":{"AbsolutePath":"/work/api/notes.txt"}}]}`)
	// The child was written last.
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(childTranscript, later, later); err != nil {
		t.Fatal(err)
	}
	record := filepath.Join(home, ".gemini", "antigravity-cli", "brain", agConversation, ".system_generated", "subagents")
	if err := os.MkdirAll(record, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(record, child+".json"),
		[]byte(`{"conversationId":"`+child+`","subagentDescriptor":{"typeName":"research","role":"Workspace Inspector"},"state":"SUBAGENT_STATE_ALIVE","spawnStepIndex":65}`), 0o644); err != nil {
		t.Fatal(err)
	}
	s := newTestAntigravity(t, home, 900, map[int]antigravityProcess{
		900: {cwd: "/work/api", conversations: []string{child, agConversation}},
	})
	sessions := discoverAntigravity(t, s)
	if len(sessions) != 1 {
		t.Fatalf("sessions = %v", sessions)
	}
	session, ok := sessions["antigravity:"+agConversation]
	if !ok || session.NativeID != agConversation || session.Name != "list the files" || session.State != protocol.StateIdle {
		t.Errorf("the subagent was taken for the session: %+v", sessions)
	}
}

// agy's bottom row carries the mode, the model, the effort and running counts,
// joined by " · ". Each line here is copied from a 1.2.17 pane.
func TestAntigravityFooterReadsModeModelAndCounts(t *testing.T) {
	for _, tc := range []struct {
		line string
		want antigravityFooter
	}{
		{"? for shortcuts                                                              Gemini 3.8 Flash · high",
			antigravityFooter{model: "Gemini 3.8 Flash", effort: "high"}},
		{"? for shortcuts                                               accept-edits · Gemini 3.8 Flash · high",
			antigravityFooter{model: "Gemini 3.8 Flash", effort: "high", mode: "accept-edits"}},
		{"esc to cancel                                                         plan · Gemini 3.8 Flash · high",
			antigravityFooter{model: "Gemini 3.8 Flash", effort: "high", mode: "plan"}},
		{"? for shortcuts                                              Gemini 3.8 Flash · high · 1 subagent(s)",
			antigravityFooter{model: "Gemini 3.8 Flash", effort: "high", subagents: 1}},
		{"esc to cancel                                           Gemini 3.8 Flash · high · 1 task(s) · /tasks",
			antigravityFooter{model: "Gemini 3.8 Flash", effort: "high", tasks: 1}},
		// A panel open over the prompt leaves the left side blank.
		{"                                                                      plan · Gemini 3.8 Flash · high",
			antigravityFooter{model: "Gemini 3.8 Flash", effort: "high", mode: "plan"}},
		{"Keyboard: ↑/↓ Navigate  ←/→ Switch View  esc Close", antigravityFooter{}},
	} {
		if got := parseAntigravityFooter([]string{"", tc.line, ""}); got != tc.want {
			t.Errorf("%q\n got %+v\nwant %+v", tc.line, got, tc.want)
		}
	}
}

// The mode used to be read as part of the model's name.
func TestAntigravityModelLeavesOutTheMode(t *testing.T) {
	home := t.TempDir()
	writeAntigravityConversation(t, home, agConversation, agLinePrompt, agLineReply)
	pane := tmux.Session{Name: "agentman-antigravity-1-a", PanePID: 900, Cwd: "/work/api"}
	s := newTestAntigravity(t, home, 900, map[int]antigravityProcess{
		900: {cwd: "/work/api", conversations: []string{agConversation}},
	}, pane)
	s.capturePane = func(context.Context, string) (string, error) {
		return ">\n" + strings.Repeat("─", 40) + "\n" +
			"? for shortcuts                                               accept-edits · Gemini 3.8 Flash · high\n", nil
	}
	session := discoverAntigravity(t, s)["antigravity:tmux-agentman-antigravity-1-a"]
	if session.Model != "Gemini 3.8 Flash (high)" || session.State != protocol.StateIdle {
		t.Errorf("model %q state %s", session.Model, session.State)
	}
	if got := s.sessions["antigravity:tmux-agentman-antigravity-1-a"].footer.mode; got != "accept-edits" {
		t.Errorf("mode = %q", got)
	}
}

// A message sent while agy works waits in its queue, and the footer says so
// instead of "esc to cancel". It is still mid-turn.
func TestAntigravityQueuedMessageFooterIsBusy(t *testing.T) {
	lines := strings.Split("  ▸ @/tmp/a.png what colour is this one? One word.\n"+
		strings.Repeat("─", 40)+"\n>\n"+strings.Repeat("─", 40)+"\n"+
		"  Press up to edit queued messages                                           Gemini 3.8 Flash · high", "\n")
	if state, ok := antigravityPaneState(lines); !ok || state != protocol.StateBusy {
		t.Errorf("state = %q %v", state, ok)
	}
}

// What the transcript's last record says when there is no pane to read.
func TestAntigravityStateFromTheLastRecordOnDisk(t *testing.T) {
	for name, tc := range map[string]struct {
		line string
		want protocol.State
	}{
		// Declining a tool ends the turn; nothing is written after it.
		"a declined tool": {`{"step_index":13,"source":"MODEL","type":"GENERIC","status":"ERROR","error":"permission check failed for write_file \"/w/notes.txt\": user denied permission for write_file(/w/notes.txt)\nDo not attempt to circumvent this denial…","created_at":"2026-10-05T09:31:06Z","content":"Encountered error in step execution: …"}`,
			protocol.StateIdle},
		// A failed tool is followed by the model's next step.
		"a failed tool": {`{"step_index":44,"source":"MODEL","type":"GENERIC","status":"ERROR","error":"declaring permissions: cortex tool view_file: … no such file or directory","created_at":"2026-10-05T09:36:48Z","content":"Encountered error in step execution: …"}`,
			protocol.StateBusy},
		// Handed to the model with the prompt before it, or on its own when a
		// background task finishes.
		"a system message": {`{"step_index":82,"source":"SYSTEM","type":"SYSTEM_MESSAGE","status":"DONE","created_at":"2026-10-05T09:45:23Z","content":"<SYSTEM_MESSAGE>Task id … finished</SYSTEM_MESSAGE>"}`,
			protocol.StateBusy},
		"a background command": {`{"step_index":80,"source":"MODEL","type":"GENERIC","status":"RUNNING","created_at":"2026-10-05T09:45:03Z","content":"Tool is running as a background task with task id: c/task-80"}`,
			protocol.StateBusy},
	} {
		if got := antigravityLineState(tc.line); got != tc.want {
			t.Errorf("%s: state = %s, want %s", name, got, tc.want)
		}
	}
}

// agy titles a conversation after its first turn; that title, not the first
// prompt, is what its own picker shows.
func TestAntigravityNamesAConversationByItsTitle(t *testing.T) {
	home := t.TempDir()
	writeAntigravityConversation(t, home, agConversation, agLinePrompt, agLineReply)
	s := newTestAntigravity(t, home, 900, map[int]antigravityProcess{
		900: {cwd: "/work/api", conversations: []string{agConversation}},
	})
	if got := discoverAntigravity(t, s)["antigravity:"+agConversation].Name; got != "list the files" {
		t.Fatalf("before a title: %q", got)
	}
	annotations := filepath.Join(home, ".gemini", "antigravity-cli", "annotations")
	if err := os.MkdirAll(annotations, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(annotations, agConversation+".pbtxt"),
		[]byte(`title:"Count Files With \"ls\""`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := discoverAntigravity(t, s)["antigravity:"+agConversation].Name; got != `Count Files With "ls"` {
		t.Errorf("after a title: %q", got)
	}
}

// lsof ran every second; its answer only changes when a conversation starts.
func TestAntigravityReusesLsofUntilSomethingCouldHaveChanged(t *testing.T) {
	home := t.TempDir()
	writeAntigravityConversation(t, home, agConversation, agLinePrompt, agLineReply)
	held := map[int]antigravityProcess{900: {cwd: "/work/api", conversations: []string{agConversation}}}
	s := newTestAntigravity(t, home, 900, held)
	calls := 0
	s.openConversations = func(context.Context, string, []int) map[int]antigravityProcess {
		calls++
		return held
	}
	discoverAntigravity(t, s)
	discoverAntigravity(t, s)
	if calls != 1 {
		t.Errorf("lsof ran %d times for an unchanged process table", calls)
	}

	// An agy that holds nothing yet is waiting for its first message, and is
	// asked about on every sweep so it binds to its transcript promptly.
	held[900] = antigravityProcess{cwd: "/work/api"}
	s.lsof = antigravityLsofAnswer{}
	calls = 0
	discoverAntigravity(t, s)
	discoverAntigravity(t, s)
	if calls != 2 {
		t.Errorf("an unbound agy was asked about %d times in two sweeps", calls)
	}

	// And a failed lsof is never remembered as an empty machine.
	held[900] = antigravityProcess{cwd: "/work/api", conversations: []string{agConversation}}
	s.lsof = antigravityLsofAnswer{}
	fail := true
	s.openConversations = func(context.Context, string, []int) map[int]antigravityProcess {
		calls++
		if fail {
			return nil
		}
		return held
	}
	calls = 0
	discoverAntigravity(t, s)
	fail = false
	if len(discoverAntigravity(t, s)) != 1 || calls != 2 {
		t.Errorf("after a failed lsof: %d calls", calls)
	}
}
