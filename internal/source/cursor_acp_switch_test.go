package source

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeACPAgent stands in for `agent acp`: it answers each request with what
// reply returns for its method, and records the methods it was asked.
type fakeACPAgent struct {
	mu      sync.Mutex
	methods []string
	reply   func(method string, params json.RawMessage, notify func(update string)) (any, string)
}

func (f *fakeACPAgent) called() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.methods...)
}

// dial returns a client wired to the fake over pipes, through the same read
// loop a real child's stdout goes through.
func (f *fakeACPAgent) dial(_ string, handle func(cursorACPEnvelope)) (*cursorACPClient, error) {
	toAgentR, toAgentW := io.Pipe()
	fromAgentR, fromAgentW := io.Pipe()
	c := &cursorACPClient{stdin: toAgentW, pending: map[int]chan cursorACPEnvelope{}, handle: handle, closed: make(chan struct{})}
	go c.read(fromAgentR)
	go func() {
		defer fromAgentW.Close()
		var writeMu sync.Mutex
		write := func(v any) {
			data, _ := json.Marshal(v)
			writeMu.Lock()
			defer writeMu.Unlock()
			_, _ = fromAgentW.Write(append(data, '\n'))
		}
		scanner := bufio.NewScanner(toAgentR)
		for scanner.Scan() {
			var request struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			if json.Unmarshal(scanner.Bytes(), &request) != nil || request.Method == "" {
				continue
			}
			f.mu.Lock()
			f.methods = append(f.methods, request.Method)
			f.mu.Unlock()
			notify := func(update string) {
				write(map[string]any{"jsonrpc": "2.0", "method": "session/update",
					"params": map[string]any{"sessionId": "x", "update": json.RawMessage(update)}})
			}
			result, failure := f.reply(request.Method, request.Params, notify)
			if len(request.ID) == 0 {
				continue
			}
			if failure != "" {
				write(map[string]any{"jsonrpc": "2.0", "id": request.ID, "error": map[string]any{"code": -32602, "message": failure}})
				continue
			}
			write(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
		}
	}()
	return c, nil
}

const acpSessionReply = `{"modes":{"currentModeId":"agent","availableModes":[{"id":"agent"},{"id":"plan"},{"id":"ask"}]},
	"models":{"currentModelId":"default","availableModels":[{"modelId":"default","name":"Auto"},
	{"modelId":"claude-opus-5-5","name":"Claude Opus 5.5"},{"modelId":"grok-4.7[effort=high]","name":"variant"}]}}`

func idleACPChat(t *testing.T, agent *fakeACPAgent) (*CursorACPSource, *cursorACPState, string) {
	t.Helper()
	s, st, id, _ := cursorACPFake(t)
	st.mu.Lock()
	st.busy, st.client = false, nil
	st.mu.Unlock()
	s.dial = agent.dial
	return s, st, id
}

// An idle chat has no ACP process: the switch starts one, loads the session,
// switches, and waits for Cursor's announcement before saying it worked.
func TestCursorACPSetModeOnAnIdleChat(t *testing.T) {
	agent := &fakeACPAgent{}
	agent.reply = func(method string, params json.RawMessage, notify func(string)) (any, string) {
		switch method {
		case "session/load":
			return json.RawMessage(acpSessionReply), ""
		case "session/set_mode":
			var p struct {
				ModeID string `json:"modeId"`
			}
			_ = json.Unmarshal(params, &p)
			go func() {
				time.Sleep(20 * time.Millisecond)
				notify(`{"sessionUpdate":"current_mode_update","currentModeId":"` + p.ModeID + `"}`)
			}()
			return map[string]any{}, ""
		}
		return nil, "unexpected " + method
	}
	s, st, id := idleACPChat(t, agent)
	if err := s.SetMode(context.Background(), id, "plan"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(agent.called(), ","); got != "session/load,session/set_mode" {
		t.Fatalf("methods = %s", got)
	}
	st.mu.Lock()
	mode, modes, models, client := st.record.Mode, st.record.Modes, st.record.Models, st.client
	st.mu.Unlock()
	if mode != "plan" || strings.Join(modes, ",") != "agent,plan,ask" || client != nil {
		t.Fatalf("mode %q modes %v client %v", mode, modes, client)
	}
	// Variant ids are not names set_config_option takes back.
	if len(models) != 2 || models[1].ID != "claude-opus-5-5" || models[1].Name != "Claude Opus 5.5" {
		t.Fatalf("models = %+v", models)
	}
	if err := s.SetMode(context.Background(), id, "debug"); err == nil {
		t.Fatal("a mode Cursor does not offer over ACP was sent")
	}
}

// No announcement, no switch: Cursor answering is not the same as the mode
// having changed.
func TestCursorACPSetModeNeedsCursorsConfirmation(t *testing.T) {
	agent := &fakeACPAgent{reply: func(method string, _ json.RawMessage, _ func(string)) (any, string) {
		if method == "session/load" {
			return json.RawMessage(acpSessionReply), ""
		}
		return map[string]any{}, ""
	}}
	s, _, id := idleACPChat(t, agent)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if err := s.SetMode(ctx, id, "ask"); err == nil {
		t.Fatal("an unconfirmed mode switch reported success")
	}
}

// The model is switched by name and checked against the model Cursor's reply
// says is now selected.
func TestCursorACPSetModelReadsTheSelectionBack(t *testing.T) {
	selected := "claude-opus-5-5"
	agent := &fakeACPAgent{reply: func(method string, params json.RawMessage, _ func(string)) (any, string) {
		switch method {
		case "session/load":
			return json.RawMessage(acpSessionReply), ""
		case "session/set_config_option":
			var p struct {
				ConfigID string `json:"configId"`
				Value    string `json:"value"`
			}
			_ = json.Unmarshal(params, &p)
			if p.ConfigID != "model" {
				return nil, "bad config id"
			}
			return map[string]any{"configOptions": []map[string]any{
				{"id": "mode", "currentValue": "agent"},
				{"id": "model", "currentValue": selected},
			}}, ""
		}
		return nil, "unexpected " + method
	}}
	s, st, id := idleACPChat(t, agent)
	st.mu.Lock()
	st.record.Models = []cursorACPModel{{ID: "default", Name: "Auto"}, {ID: "claude-opus-5-5", Name: "Claude Opus 5.5"}}
	st.mu.Unlock()
	if err := s.SetModel(context.Background(), id, "claude-opus-5-5"); err != nil {
		t.Fatal(err)
	}
	selected = "default"
	if err := s.SetModel(context.Background(), id, "claude-opus-5-5"); err == nil {
		t.Fatal("a switch Cursor did not apply reported success")
	}
	if err := s.SetModel(context.Background(), id, "gpt-9"); err == nil {
		t.Fatal("a model Cursor never offered was sent")
	}
}

// While a turn runs, the switch goes to that turn's process; no second one
// is started for the same session.
func TestCursorACPSwitchUsesTheRunningTurn(t *testing.T) {
	agent := &fakeACPAgent{}
	agent.reply = func(method string, params json.RawMessage, notify func(string)) (any, string) {
		if method == "session/set_mode" {
			go notify(`{"sessionUpdate":"current_mode_update","currentModeId":"ask"}`)
			return map[string]any{}, ""
		}
		return nil, "unexpected " + method
	}
	s, st, id, _ := cursorACPFake(t)
	client, _ := agent.dial("", func(event cursorACPEnvelope) { s.handle(st, event) })
	st.mu.Lock()
	st.client = client
	st.mu.Unlock()
	s.dial = func(string, func(cursorACPEnvelope)) (*cursorACPClient, error) {
		t.Fatal("a second ACP process was started during a turn")
		return nil, nil
	}
	if err := s.SetMode(context.Background(), id, "ask"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(agent.called(), ","); got != "session/set_mode" {
		t.Fatalf("methods = %s", got)
	}
}
