package source

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/parser"
	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/tmux"
)

// No test in this package starts the real Kiro: every turn spends the
// user's credits.
func init() {
	kiroACPDial = func(string, func(acpEnvelope)) (*acpClient, error) {
		return nil, errors.New("tests never start Kiro")
	}
}

const fakeKiroSession = "6f1d2c3b-4a5e-4f60-8b7c-9d0e1f2a3b4c"

// fakeKiro stands in for `kiro-cli acp`, speaking what Kiro 2.37 was seen to
// send: a turn's words as agent_message_chunk, a call's start as Kiro's own
// tool_call_chunk, then tool_call and tool_call_update, and a permission
// prompt as a request it waits on.
type fakeKiro struct {
	mu      sync.Mutex
	methods []string
	// permission is what the client answered to the permission request, for
	// the turn waiting on it; answered keeps the last answer for the test.
	permission chan json.RawMessage
	answered   json.RawMessage
	// cancel ends a turn waiting for it.
	cancel chan struct{}
}

func (f *fakeKiro) called() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.methods...)
}

func (f *fakeKiro) dial(_ string, handle func(acpEnvelope)) (*acpClient, error) {
	toAgentR, toAgentW := io.Pipe()
	fromAgentR, fromAgentW := io.Pipe()
	c := &acpClient{name: "Kiro", stdin: toAgentW, pending: map[int]chan acpEnvelope{}, handle: handle, closed: make(chan struct{})}
	go c.read(fromAgentR)
	var writeMu sync.Mutex
	write := func(v any) {
		data, _ := json.Marshal(v)
		writeMu.Lock()
		defer writeMu.Unlock()
		_, _ = fromAgentW.Write(append(data, '\n'))
	}
	update := func(method, raw string) {
		write(map[string]any{"jsonrpc": "2.0", "method": method,
			"params": map[string]any{"sessionId": fakeKiroSession, "update": json.RawMessage(raw)}})
	}
	go func() {
		defer fromAgentW.Close()
		scanner := bufio.NewScanner(toAgentR)
		for scanner.Scan() {
			var message struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
				Result json.RawMessage `json:"result"`
			}
			if json.Unmarshal(scanner.Bytes(), &message) != nil {
				continue
			}
			if message.Method == "" {
				// The client answering the permission request.
				f.mu.Lock()
				f.answered = message.Result
				f.mu.Unlock()
				f.permission <- message.Result
				continue
			}
			f.mu.Lock()
			f.methods = append(f.methods, message.Method)
			f.mu.Unlock()
			reply := func(result any) {
				write(map[string]any{"jsonrpc": "2.0", "id": message.ID, "result": result})
			}
			switch message.Method {
			case "session/new", "session/load":
				write(map[string]any{"jsonrpc": "2.0", "method": "_kiro.dev/commands/available", "params": map[string]any{
					"sessionId": fakeKiroSession, "commands": []any{
						map[string]any{"name": "/compact", "description": "Compact conversation history"},
						map[string]any{"name": "/context", "description": "Show token usage", "meta": map[string]any{"inputType": "panel"}},
						map[string]any{"name": "/agent", "description": "Select an agent", "meta": map[string]any{"inputType": "selection"}},
						map[string]any{"name": "/chat", "description": "Save or load", "meta": map[string]any{"local": true}},
					}}})
				write(map[string]any{"jsonrpc": "2.0", "method": "_kiro.dev/metadata", "params": map[string]any{
					"sessionId": fakeKiroSession, "contextUsagePercentage": 3.9}})
				reply(map[string]any{"sessionId": fakeKiroSession,
					"modes": map[string]any{"currentModeId": "kiro_default", "availableModes": []any{
						map[string]any{"id": "kiro_default"}, map[string]any{"id": "kiro_planner"}}},
					"models": map[string]any{"currentModelId": "auto", "availableModels": []any{
						map[string]any{"modelId": "auto"}, map[string]any{"modelId": "claude-sonnet-4.5"}}}})
			case "session/prompt":
				var params struct {
					Prompt []struct {
						Text string `json:"text"`
					} `json:"prompt"`
				}
				_ = json.Unmarshal(message.Params, &params)
				text := params.Prompt[0].Text
				go func(id json.RawMessage) {
					if text == "hang" {
						<-f.cancel
						write(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"stopReason": "cancelled"}})
						return
					}
					update("session/update", `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"OK"}}`)
					if text == "run ls" {
						update("_kiro.dev/session/update", `{"sessionUpdate":"tool_call_chunk","toolCallId":"tooluse_a1","title":"shell","kind":"execute"}`)
						update("session/update", `{"sessionUpdate":"tool_call","toolCallId":"tooluse_a1","title":"Running: ls","kind":"execute","rawInput":{"command":"ls"},"_meta":{"kiro":{"toolName":"shell"}}}`)
						write(map[string]any{"jsonrpc": "2.0", "id": "perm-1", "method": "session/request_permission", "params": map[string]any{
							"sessionId": fakeKiroSession,
							"toolCall":  map[string]any{"toolCallId": "tooluse_a1", "title": "Running: ls", "rawInput": map[string]any{"command": "ls"}},
							"options": []any{
								map[string]any{"optionId": "allow_once", "name": "Yes", "kind": "allow_once"},
								map[string]any{"optionId": "allow_always", "name": "Always", "kind": "allow_always"},
								map[string]any{"optionId": "reject_once", "name": "No", "kind": "reject_once"},
							}}})
						<-f.permission
						update("session/update", `{"sessionUpdate":"tool_call_update","toolCallId":"tooluse_a1","status":"completed"}`)
						update("session/update", `{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"One file."}}`)
					}
					// As Kiro writes: the turn ends a moment after the words.
					time.Sleep(50 * time.Millisecond)
					write(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"stopReason": "end_turn"}})
				}(message.ID)
			case "session/cancel":
				select {
				case f.cancel <- struct{}{}:
				default:
				}
			case "_kiro.dev/commands/execute":
				reply(map[string]any{"success": true, "message": "Context breakdown - 3% used", "data": map[string]any{"model": "auto"}})
			default:
				if len(message.ID) > 0 {
					reply(map[string]any{})
				}
			}
		}
	}()
	return c, nil
}

func kiroWithACP(t *testing.T) (*KiroSource, *fakeKiro) {
	t.Helper()
	src, err := NewKiroSource(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	src.listPanes = func(context.Context) ([]tmux.Session, error) { return nil, nil }
	fake := &fakeKiro{permission: make(chan json.RawMessage, 1), cancel: make(chan struct{}, 1)}
	src.acpDial = fake.dial
	t.Cleanup(func() {
		src.acp.mu.Lock()
		owned := make([]*kiroACPSession, 0)
		for _, st := range src.acp.sessions {
			owned = append(owned, st)
		}
		src.acp.mu.Unlock()
		for _, st := range owned {
			src.dropACP(st)
		}
	})
	return src, fake
}

func discoverKiroACP(t *testing.T, src *KiroSource, id string) protocol.Session {
	t.Helper()
	found, err := src.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range found {
		if session.ID == id {
			return session
		}
	}
	t.Fatalf("%s not discovered among %d", id, len(found))
	return protocol.Session{}
}

func awaitKiro(t *testing.T, src *KiroSource, id, what string, ok func(protocol.Session) bool) protocol.Session {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		session := discoverKiroACP(t, src, id)
		if ok(session) {
			return session
		}
		if time.Now().After(deadline) {
			t.Fatalf("never %s: %+v", what, session)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A Kiro launched from the phone runs through ACP under Kiro's own id, and
// its permission prompt is a card answered from the phone.
func TestKiroLaunchesThroughACP(t *testing.T) {
	src, fake := kiroWithACP(t)
	dir := t.TempDir()
	id, err := src.StartACPSession(context.Background(), dir, "run ls")
	if err != nil || id != "kiro:"+fakeKiroSession {
		t.Fatalf("launch = %q %v", id, err)
	}
	waiting := awaitKiro(t, src, id, "asking", func(s protocol.Session) bool { return s.Question != nil })
	if waiting.Inject != protocol.InjectAPI || waiting.State != protocol.StateWaitingInput || waiting.Cwd != dir ||
		waiting.Mode != "kiro_default" || strings.Join(waiting.Modes, " ") != "kiro_default kiro_planner" ||
		waiting.Model != "auto" || len(waiting.Models) != 2 || waiting.ContextPercent != 4 {
		t.Fatalf("session = %+v", waiting)
	}
	question := waiting.Question
	if len(question.Options) != 3 || question.Options[0].Label != "Yes" || question.Options[1].Label != "Yes, and don't ask again" ||
		question.Options[2].Label != "No" || !strings.Contains(question.Prompt+question.Detail, "ls") {
		t.Fatalf("card = %+v", question)
	}
	if current, _ := src.CurrentQuestion(context.Background(), id); current == nil || current.ID != question.ID {
		t.Fatalf("current question = %+v", current)
	}
	if err := src.Answer(context.Background(), id, protocol.QuestionAnswer{QuestionID: question.ID, OptionKey: "allow_once"}); err != nil {
		t.Fatal(err)
	}
	awaitKiro(t, src, id, "idle", func(s protocol.Session) bool { return s.State == protocol.StateIdle && s.Question == nil })
	fake.mu.Lock()
	answered := string(fake.answered)
	fake.mu.Unlock()
	if !strings.Contains(answered, `"optionId":"allow_once"`) {
		t.Fatalf("answered %s", answered)
	}
	awaitKiro(t, src, id, "idle", func(s protocol.Session) bool { return s.State == protocol.StateIdle && s.Question == nil })
	if err := src.Answer(context.Background(), id, protocol.QuestionAnswer{QuestionID: question.ID, OptionKey: "allow_once"}); err == nil {
		t.Fatal("answered a card twice")
	}
}

// While Kiro writes, the follow shows the reply under the id its finished
// text will have, and each call from its start under Kiro's call id, until
// the transcript has it.
func TestKiroACPFollowStreams(t *testing.T) {
	src, fake := kiroWithACP(t)
	src.home = t.TempDir()
	dir := t.TempDir()
	id, err := src.StartACPSession(context.Background(), dir, "hang")
	if err != nil {
		t.Fatal(err)
	}
	// Kiro's transcript holds the prompt; the reply is still being written.
	if err := os.MkdirAll(src.sessionsDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	prompt := `{"version":"v1","kind":"Prompt","data":{"message_id":"p1","content":[{"kind":"text","data":"run ls"}],"meta":{"timestamp":1793489100}}}`
	if err := os.WriteFile(filepath.Join(src.sessionsDir(), fakeKiroSession+".jsonl"), []byte(prompt+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	discoverKiroACP(t, src, id)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := make(chan []protocol.Message, 32)
	go func() { _ = src.Follow(ctx, id, out) }()
	time.Sleep(50 * time.Millisecond)

	st, _ := src.acpOwned(id)
	src.applyACPUpdate(st, json.RawMessage(`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"Looking"}}`))
	want := parser.KiroTextID("p1", 1)
	await := func(what string, match func(protocol.Message) bool) protocol.Message {
		t.Helper()
		deadline := time.After(5 * time.Second)
		for {
			select {
			case batch := <-out:
				for _, m := range batch {
					if match(m) {
						return m
					}
				}
			case <-deadline:
				t.Fatalf("never %s", what)
			}
		}
	}
	await("the reply being written", func(m protocol.Message) bool { return m.ID == want && m.Text == "Looking" })

	src.applyACPUpdate(st, json.RawMessage(`{"sessionUpdate":"tool_call","toolCallId":"tooluse_b2","title":"Running: ls","rawInput":{"command":"ls"},"_meta":{"kiro":{"toolName":"shell"}}}`))
	row := await("the call from its start", func(m protocol.Message) bool { return m.ID == "tooluse_b2" })
	if row.Tool == nil || row.Tool.Status != protocol.ToolRunning || row.SessionID != id || !strings.Contains(row.Tool.Summary, "ls") {
		t.Fatalf("running row = %+v %+v", row, row.Tool)
	}
	fake.cancel <- struct{}{}
}

// Kiro's own commands are in the "/" menu; a bare one is run as a command,
// a panel one answers in a panel, and pickers and local ones are left out.
func TestKiroACPCommands(t *testing.T) {
	src, fake := kiroWithACP(t)
	id, err := src.StartACPSession(context.Background(), t.TempDir(), "hello")
	if err != nil {
		t.Fatal(err)
	}
	awaitKiro(t, src, id, "idle", func(s protocol.Session) bool { return s.State == protocol.StateIdle })
	commands, err := src.Commands(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, command := range commands {
		if command.Source == "builtin" {
			names = append(names, command.Name)
		}
	}
	if strings.Join(names, " ") != "compact context" || commands[0].Panel || !commands[1].Panel {
		t.Fatalf("commands = %+v", commands)
	}
	if _, err := src.Inject(context.Background(), id, "/compact"); err != nil {
		t.Fatal(err)
	}
	if called := strings.Join(fake.called(), " "); !strings.Contains(called, "_kiro.dev/commands/execute") || strings.Count(called, "session/prompt") != 1 {
		t.Fatalf("called %s", called)
	}
	panel, err := src.CommandPanel(context.Background(), id, "/context")
	if err != nil || panel.Command != "/context" || !strings.HasPrefix(panel.Text, "Context breakdown - 3% used") {
		t.Fatalf("panel = %+v %v", panel, err)
	}
}

// Stop, a message sent mid-turn, and the mode and model switches.
func TestKiroACPStopsQueuesAndSwitches(t *testing.T) {
	src, fake := kiroWithACP(t)
	id, err := src.StartACPSession(context.Background(), t.TempDir(), "hang")
	if err != nil {
		t.Fatal(err)
	}
	awaitKiro(t, src, id, "busy", func(s protocol.Session) bool { return s.State == protocol.StateBusy })
	if _, err := src.Inject(context.Background(), id, "after that, this"); err != nil {
		t.Fatal(err)
	}
	if queued := discoverKiroACP(t, src, id).Queued; len(queued) != 1 || queued[0] != "after that, this" {
		t.Fatalf("queued = %q", queued)
	}
	if err := src.Interrupt(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	awaitKiro(t, src, id, "the waiting message sent", func(s protocol.Session) bool { return len(s.Queued) == 0 && s.State == protocol.StateIdle })
	if prompts := strings.Count(strings.Join(fake.called(), " "), "session/prompt"); prompts != 2 {
		t.Fatalf("prompts = %d", prompts)
	}
	if err := src.SetMode(context.Background(), id, "kiro_planner"); err != nil {
		t.Fatal(err)
	}
	if err := src.SetModel(context.Background(), id, "claude-sonnet-4.5"); err != nil {
		t.Fatal(err)
	}
	session := discoverKiroACP(t, src, id)
	if session.Mode != "kiro_planner" || session.Model != "claude-sonnet-4.5" {
		t.Fatalf("after switching = %+v", session)
	}
	if err := src.SetMode(context.Background(), id, "kiro_sideways"); err == nil {
		t.Fatal("switched to a mode Kiro does not offer")
	}
}

// An ended session reopened from the phone carries on through ACP, under
// the same id.
func TestKiroACPResumes(t *testing.T) {
	src, fake := kiroWithACP(t)
	id := "kiro:" + fakeKiroSession
	reopened, err := src.ResumeACPSession(context.Background(), id, t.TempDir())
	if err != nil || reopened != id {
		t.Fatalf("resume = %q %v", reopened, err)
	}
	if session := discoverKiroACP(t, src, id); session.Inject != protocol.InjectAPI || session.State != protocol.StateIdle {
		t.Fatalf("reopened = %+v", session)
	}
	if _, err := src.Inject(context.Background(), id, "hello again"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for called := strings.Join(fake.called(), " "); !strings.Contains(called, "session/load") || !strings.Contains(called, "session/prompt"); called = strings.Join(fake.called(), " ") {
		if time.Now().After(deadline) {
			t.Fatalf("called %s", called)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A panel's data reads as lines, not as JSON.
func TestKiroPanelLines(t *testing.T) {
	var data any
	_ = json.Unmarshal([]byte(`{"model":"auto","contextUsagePercentage":3.92,"breakdown":{"contextFiles":{"tokens":0,"items":[{"name":"AGENTS.md","tokens":0,"matched":false}]},"tools":{"groups":[]}}}`), &data)
	var lines []string
	kiroPanelLines(&lines, data, "")
	want := "breakdown:\n  contextFiles:\n    items:\n      - AGENTS.md  matched false, tokens 0\n    tokens: 0\ncontextUsagePercentage: 3.9\nmodel: auto"
	if got := strings.Join(lines, "\n"); got != want {
		t.Fatalf("lines:\n%s\nwant:\n%s", got, want)
	}
}
