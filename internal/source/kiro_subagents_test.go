package source

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/parser"
	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/tmux"
)

// The shapes Kiro CLI 2.27.1 wrote for one subagent call: the parent's call
// and pipeline result, and the child session's own transcript. Paths are
// under /work; the redacted thinking blocks are dropped.
const (
	kiroSubagentTask = "Read /work/notes.txt and report its first line."

	kiroLineSubagentCall = `{"version":"v1","kind":"AssistantMessage","data":{"message_id":"a1","content":[{"kind":"text","data":""},` +
		`{"kind":"toolUse","data":{"toolUseId":"tooluse_A2En","name":"subagent","input":{"__tool_use_purpose":"Delegate reading notes.txt",` +
		`"task":"` + kiroSubagentTask + `","stages":[{"name":"read_notes","role":"kiro_default","prompt_template":"` + kiroSubagentTask + `"}]}}}]}}`
	kiroLineSubagentResult = `{"version":"v1","kind":"ToolResults","data":{"message_id":"r1","content":[{"kind":"toolResult","data":{"toolUseId":"tooluse_A2En",` +
		`"content":[{"kind":"text","data":"Pipeline completed: 1 stages finished.\n\n## read_notes\n\nThe first line is: alpha"}],"status":"success"}}],"results":{}}}`
	kiroLineSubagentReply = `{"version":"v1","kind":"AssistantMessage","data":{"message_id":"a2","content":[{"kind":"text","data":"The subagent says the first line is alpha."}]}}`

	kiroLineChildPrompt = `{"version":"v1","kind":"Prompt","data":{"message_id":"c-p","content":[{"kind":"text","data":"` + kiroSubagentTask + `"}],"meta":{"timestamp":1790528361}}}`
	kiroLineChildCall   = `{"version":"v1","kind":"AssistantMessage","data":{"message_id":"c-a1","content":[{"kind":"text","data":""},` +
		`{"kind":"toolUse","data":{"toolUseId":"tooluse_lpDO","name":"read","input":{"operations":[{"mode":"Line","path":"/work/notes.txt","limit":1}]}}}]}}`
	kiroLineChildResult = `{"version":"v1","kind":"ToolResults","data":{"message_id":"c-r1","content":[{"kind":"toolResult","data":{"toolUseId":"tooluse_lpDO",` +
		`"content":[{"kind":"text","data":"alpha"}],"status":"success"}}],"results":{}}}`
	kiroLineChildReply = `{"version":"v1","kind":"AssistantMessage","data":{"message_id":"c-a2","content":[{"kind":"text","data":"The first line is **alpha**"},` +
		`{"kind":"toolUse","data":{"toolUseId":"tooluse_ihQK","name":"summary","input":{"taskDescription":"` + kiroSubagentTask + `","taskResult":"alpha"}}}]}}`
)

// writeKiroChild lays out a subagent's session as Kiro does: titled with its
// task, no agent name, no input history, no lock.
func writeKiroChild(t *testing.T, home, id, cwd, title, created string, lines ...string) string {
	t.Helper()
	path := writeKiroSessionFiles(t, home, id, cwd, 0,
		`{"agent_name":null,"rts_model_state":{"conversation_id":"`+id+`","model_info":null}}`, fmt.Sprintf("%q", title), lines...)
	meta := strings.TrimSuffix(path, ".jsonl") + ".json"
	raw, err := os.ReadFile(meta)
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.Replace(string(raw), `"created_at":"2026-09-27T16:59:07.644671Z"`, `"created_at":"`+created+`"`, 1))
	if err := os.WriteFile(meta, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// During the parent's turn: its prompt is stamped 2026-09-27T16:59:19Z.
const kiroChildCreated = "2026-09-27T16:59:21.120000Z"

func pastKiro(t *testing.T, s *KiroSource, cwd, native string) string {
	t.Helper()
	past, err := s.Past(context.Background(), cwd, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range past {
		if session.NativeID == native {
			return session.ID
		}
	}
	t.Fatalf("%s not in history: %+v", native, past)
	return ""
}

// What a subagent read, ran and said lives in its own session. Claude's feed
// shows that work collapsed behind a chip, as sidechain rows, and Kiro's now
// does too: straight after the call that asked for it, in its own order, and
// under ids that cannot collide with the parent's.
func TestKiroSubagentWorkAppearsAsSidechainRows(t *testing.T) {
	home := t.TempDir()
	writeKiroSession(t, home, "parent", "/work", 0,
		kiroLinePrompt, kiroLineSubagentCall, kiroLineSubagentResult, kiroLineSubagentReply)
	writeKiroChild(t, home, "child", "/work", kiroSubagentTask, kiroChildCreated,
		kiroLineChildPrompt, kiroLineChildCall, kiroLineChildResult, kiroLineChildReply)
	s := newTestKiro(t, home, "")
	id := pastKiro(t, s, "/work", "parent")

	page, err := s.Page(context.Background(), id, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, m := range page.Messages {
		got = append(got, fmt.Sprintf("%s|%s|%v", m.ID, m.Role, m.IsSidechain))
	}
	want := []string{
		"p1|user|false",
		"tooluse_A2En|tool|false",
		"tooluse_A2En/sub01/000001|tool|true",      // its read, settled in place
		"tooluse_A2En/sub01/000002|assistant|true", // its answer
		"tooluse_A2En/sub01/000003|tool|true",      // its report
		parser.KiroTextID("p1", 1) + "|assistant|false",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("feed:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	call := page.Messages[1]
	for _, m := range page.Messages[2:5] {
		if m.Ts != call.Ts || m.SessionID != id {
			t.Errorf("child row %s: ts %d session %q, want the call's %d and %q", m.ID, m.Ts, m.SessionID, call.Ts, id)
		}
	}
	if read := page.Messages[2]; read.Tool == nil || read.Tool.Summary != "/work/notes.txt" ||
		read.Tool.Status != protocol.ToolOK || read.Text != "alpha" {
		t.Errorf("child read = %+v", read)
	}
	// The app orders by time, then by id. That must give the same order.
	sorted := append([]protocol.Message(nil), page.Messages...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Ts != sorted[j].Ts {
			return sorted[i].Ts < sorted[j].Ts
		}
		return sorted[i].ID < sorted[j].ID
	})
	for i := range sorted {
		if sorted[i].ID != page.Messages[i].ID {
			t.Fatalf("the app would show %s at %d, the page has %s", sorted[i].ID, i, page.Messages[i].ID)
		}
	}
	// Read again, every row is named the same.
	again, err := s.Page(context.Background(), id, "", 50)
	if err != nil || len(again.Messages) != len(page.Messages) || again.Messages[4].ID != page.Messages[4].ID {
		t.Errorf("a second read differs: %+v", again.Messages)
	}
}

// A child is bound to a call only beyond doubt: same folder, created during
// the call's turn, titled with exactly the prompt the stage was given, and
// the one such session for that stage. Anything less shows nothing rather
// than another conversation's work.
func TestKiroSubagentBindingIsConservative(t *testing.T) {
	stages := `"stages":[{"name":"a","prompt_template":"` + kiroSubagentTask + `"}]`
	twin := `"stages":[{"name":"a","prompt_template":"` + kiroSubagentTask + `"},{"name":"b","prompt_template":"` + kiroSubagentTask + `"}]`
	cases := []struct {
		name  string
		input string // the call's stages
		setup func(t *testing.T, home string)
		bound bool
	}{
		{"exact", stages, func(t *testing.T, home string) {
			writeKiroChild(t, home, "child", "/work", kiroSubagentTask, kiroChildCreated, kiroLineChildPrompt)
		}, true},
		{"another folder", stages, func(t *testing.T, home string) {
			writeKiroChild(t, home, "child", "/elsewhere", kiroSubagentTask, kiroChildCreated, kiroLineChildPrompt)
		}, false},
		{"before the turn", stages, func(t *testing.T, home string) {
			writeKiroChild(t, home, "child", "/work", kiroSubagentTask, "2026-09-27T16:58:00Z", kiroLineChildPrompt)
		}, false},
		{"after the turn", stages, func(t *testing.T, home string) {
			writeKiroChild(t, home, "child", "/work", kiroSubagentTask, "2026-09-27T17:45:00Z", kiroLineChildPrompt)
		}, false},
		{"different title", stages, func(t *testing.T, home string) {
			writeKiroChild(t, home, "child", "/work", kiroSubagentTask+" Then stop.", kiroChildCreated, kiroLineChildPrompt)
		}, false},
		{"two candidates", stages, func(t *testing.T, home string) {
			writeKiroChild(t, home, "child", "/work", kiroSubagentTask, kiroChildCreated, kiroLineChildPrompt)
			writeKiroChild(t, home, "child2", "/work", kiroSubagentTask, kiroChildCreated, kiroLineChildPrompt)
		}, false},
		{"one child, two identical stages", twin, func(t *testing.T, home string) {
			writeKiroChild(t, home, "child", "/work", kiroSubagentTask, kiroChildCreated, kiroLineChildPrompt)
		}, false},
		{"a conversation of the user's with that title", stages, func(t *testing.T, home string) {
			path := writeKiroChild(t, home, "child", "/work", kiroSubagentTask, kiroChildCreated, kiroLineChildPrompt)
			if err := os.WriteFile(strings.TrimSuffix(path, ".jsonl")+".history", []byte("x\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			call := strings.Replace(kiroLineSubagentCall, `"stages":[{"name":"read_notes","role":"kiro_default","prompt_template":"`+kiroSubagentTask+`"}]`, tc.input, 1)
			// The next prompt, forty minutes on, closes the call's turn.
			next := strings.Replace(strings.Replace(kiroLinePrompt, `"p1"`, `"p2"`, 1), "1790528359", "1790530759", 1)
			parent := writeKiroSession(t, home, "parent", "/work", 0, kiroLinePrompt, call, kiroLineSubagentResult, next)
			tc.setup(t, home)
			s := newTestKiro(t, home, "")
			p := parser.NewKiroParser("kiro:parent")
			for i, line := range []string{kiroLinePrompt, call, kiroLineSubagentResult, next} {
				p.Parse(line, int64(i))
			}
			bound := s.bindSubagents(parent, p.SubagentCalls(), time.Now())
			want := 0
			if tc.bound {
				want = 1
			}
			if len(bound) != want {
				t.Errorf("bound = %+v, want %d", bound, want)
			}
		})
	}
}

// While a subagent works, its rows reach a follower as its transcript grows,
// and the last of them once the call that started it finishes.
func TestKiroFollowStreamsASubagentsWork(t *testing.T) {
	home := t.TempDir()
	pid := os.Getpid()
	parent := writeKiroSession(t, home, "parent", "/work", pid, kiroLinePrompt, kiroLineSubagentCall)
	child := writeKiroChild(t, home, "child", "/work", kiroSubagentTask, time.Now().UTC().Format(time.RFC3339Nano),
		kiroLineChildPrompt)
	// The call is recent, so its turn's window covers a child created now.
	recent := strings.Replace(kiroLinePrompt, "1790528359", fmt.Sprint(time.Now().Unix()-1), 1)
	if err := os.WriteFile(parent, []byte(recent+"\n"+kiroLineSubagentCall+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := newTestKiro(t, home, fmt.Sprintf("%d 1 kiro-cli-chat\n", pid))
	s.listPanes = func(context.Context) ([]tmux.Session, error) { return nil, nil }
	discoverKiro(t, s)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out := make(chan []protocol.Message, 32)
	done := make(chan error, 1)
	go func() { done <- s.Follow(ctx, "kiro:parent", out) }()

	seen := map[string]protocol.Message{}
	waitFor := func(id string) protocol.Message {
		t.Helper()
		for {
			if m, ok := seen[id]; ok {
				return m
			}
			select {
			case batch := <-out:
				for _, m := range batch {
					seen[m.ID] = m
				}
			case <-ctx.Done():
				t.Fatalf("follow never sent %s; saw %v", id, seen)
			}
		}
	}
	appendLines(t, child, kiroLineChildCall)
	if read := waitFor("tooluse_A2En/sub01/000001"); !read.IsSidechain || read.Tool == nil || read.Tool.Status != protocol.ToolRunning {
		t.Errorf("subagent's read = %+v", read)
	}
	// The subagent finishes and, in the same moment, the parent's call does.
	appendLines(t, child, kiroLineChildResult, kiroLineChildReply)
	appendLines(t, parent, kiroLineSubagentResult)
	if answer := waitFor("tooluse_A2En/sub01/000002"); answer.Text != "The first line is **alpha**" || !answer.IsSidechain {
		t.Errorf("subagent's answer = %+v", answer)
	}
	cancel()
	<-done
}

func appendLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString(strings.Join(lines, "\n") + "\n"); err != nil {
		t.Fatal(err)
	}
}

// A quick subagent can start and finish between two looks for it. The look
// taken as its call finishes still finds it, and its work is read in full.
func TestKiroChildFollowLooksOnceMoreAsACallFinishes(t *testing.T) {
	home := t.TempDir()
	recent := strings.Replace(kiroLinePrompt, "1790528359", fmt.Sprint(time.Now().Unix()-1), 1)
	parent := writeKiroSession(t, home, "parent", "/work", 0, recent, kiroLineSubagentCall)
	s := newTestKiro(t, home, "")
	p := parser.NewKiroParser("kiro:parent")
	p.Parse(recent, 0)
	p.Parse(kiroLineSubagentCall, 1)

	follow := newKiroChildFollow("kiro:parent", parent, p.SubagentCalls())
	now := time.Now()
	if rows := follow.read(context.Background(), s, p.SubagentCalls(), now); len(rows) != 0 {
		t.Fatalf("rows before any subagent = %+v", rows)
	}
	// Within the same second: the subagent runs to the end, and the call
	// returns.
	writeKiroChild(t, home, "child", "/work", kiroSubagentTask, now.UTC().Format(time.RFC3339Nano),
		kiroLineChildPrompt, kiroLineChildCall, kiroLineChildResult, kiroLineChildReply)
	p.Parse(kiroLineSubagentResult, 2)
	rows := follow.read(context.Background(), s, p.SubagentCalls(), now.Add(100*time.Millisecond))
	if len(rows) < 3 {
		t.Fatalf("rows = %+v, want the subagent's whole transcript", rows)
	}
	if follow.read(context.Background(), s, p.SubagentCalls(), now.Add(2*time.Second)) != nil || len(follow.tails) != 0 {
		t.Error("a finished call's subagents are still being followed")
	}
}
