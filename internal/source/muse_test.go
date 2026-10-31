package source

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// fakeMuse answers MSP requests over in-memory pipes, as `muse serve` would.
type fakeMuse struct {
	t       *testing.T
	mu      sync.Mutex
	answers map[string]func(params json.RawMessage) (any, *museRPCError)
	calls   []string
	out     *io.PipeWriter
	writeMu sync.Mutex
}

func newFakeMuse(t *testing.T) *fakeMuse {
	f := &fakeMuse{t: t, answers: map[string]func(json.RawMessage) (any, *museRPCError){}}
	f.answers["initialize"] = func(json.RawMessage) (any, *museRPCError) {
		return map[string]any{"serverInfo": map[string]any{"name": "muse", "version": "1.4.14"}}, nil
	}
	f.answers["view/subscribe"] = func(json.RawMessage) (any, *museRPCError) { return map[string]any{"viewCursor": "v4:x:1"}, nil }
	f.answers["view/unsubscribe"] = func(json.RawMessage) (any, *museRPCError) { return map[string]any{}, nil }
	return f
}

func (f *fakeMuse) transport(ctx context.Context) (io.WriteCloser, io.Reader, func(), int, error) {
	toMuse, fromAgentman := io.Pipe()
	toAgentman, fromMuse := io.Pipe()
	f.mu.Lock()
	f.out = fromMuse
	f.mu.Unlock()
	go func() {
		scanner := bufio.NewScanner(toMuse)
		scanner.Buffer(make([]byte, 0, 1<<16), 1<<24)
		for scanner.Scan() {
			var request struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			if json.Unmarshal(scanner.Bytes(), &request) != nil || len(request.ID) == 0 {
				continue
			}
			f.mu.Lock()
			f.calls = append(f.calls, request.Method)
			answer := f.answers[request.Method]
			f.mu.Unlock()
			reply := map[string]any{"jsonrpc": "2.0", "id": request.ID}
			if answer == nil {
				reply["error"] = map[string]any{"code": -32601, "message": "method not found"}
			} else if result, rpcErr := answer(request.Params); rpcErr != nil {
				reply["error"] = rpcErr
			} else {
				reply["result"] = result
			}
			f.send(reply)
		}
	}()
	return fromAgentman, toAgentman, func() { _ = fromAgentman.Close(); _ = fromMuse.Close() }, 0, nil
}

func (f *fakeMuse) send(message any) {
	line, _ := json.Marshal(message)
	f.mu.Lock()
	out := f.out
	f.mu.Unlock()
	f.writeMu.Lock()
	defer f.writeMu.Unlock()
	_, _ = out.Write(append(line, '\n'))
}

func (f *fakeMuse) notify(method string, params any) {
	f.send(map[string]any{"jsonrpc": "2.0", "method": method, "params": params})
}

func (f *fakeMuse) called(method string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, call := range f.calls {
		if call == method {
			return true
		}
	}
	return false
}

func startFakeMuse(t *testing.T, home string) (*MuseSource, *fakeMuse) {
	t.Helper()
	fake := newFakeMuse(t)
	host := newMuseHost(fake.transport)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go host.run(ctx)
	deadline := time.Now().Add(3 * time.Second)
	for !host.isUp() {
		if time.Now().After(deadline) {
			t.Fatal("never connected to the fake Muse")
		}
		time.Sleep(10 * time.Millisecond)
	}
	return newMuseSourceWith(host, home), fake
}

func TestMuseDiscoverListsRecentAndTerminalSessions(t *testing.T) {
	home := t.TempDir()
	source, fake := startFakeMuse(t, home)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	fake.answers["session/list"] = func(json.RawMessage) (any, *museRPCError) {
		return map[string]any{"sessions": []any{
			map[string]any{"sessionId": "busy", "status": "running", "name": "fix-login", "workspaceRoot": "/code/site", "updatedAt": now, "kind": "root"},
			map[string]any{"sessionId": "asks", "status": "idle", "title": "Rate limit uploads", "workspaceRoot": "/code/api", "updatedAt": now, "attention": []string{"approvalPending"}},
			map[string]any{"sessionId": "child", "status": "idle", "kind": "subagent", "workspaceRoot": "/code/api", "updatedAt": now},
		}, "nextCursor": nil}, nil
	}
	fake.answers["session/read"] = func(params json.RawMessage) (any, *museRPCError) {
		return map[string]any{"session": map[string]any{"sessionId": "quiet", "status": "notLoaded", "workspaceRoot": "/code/notes", "firstUserPrompt": "Tidy the notes"}}, nil
	}
	// A terminal has an older session open: its process is this test.
	registry := filepath.Join(home, "runtime", "muse", "sessions")
	if err := os.MkdirAll(registry, 0o755); err != nil {
		t.Fatal(err)
	}
	record := `{"session_id":"quiet","process_generation_hint":"pid=` + strconv.Itoa(os.Getpid()) + `"}`
	if err := os.WriteFile(filepath.Join(registry, "quiet.json"), []byte(record), 0o644); err != nil {
		t.Fatal(err)
	}

	sessions, err := source.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]protocol.Session{}
	for _, session := range sessions {
		byID[session.ID] = session
	}
	if len(byID) != 3 || byID["muse:child"].ID != "" {
		t.Fatalf("want busy, asks and quiet, without the subagent: %+v", sessions)
	}
	if s := byID["muse:busy"]; s.State != protocol.StateBusy || s.Name != "fix-login" || s.Cwd != "/code/site" || s.Kind != protocol.KindMuse {
		t.Errorf("busy: %+v", s)
	}
	if s := byID["muse:asks"]; s.State != protocol.StateWaitingInput || s.Name != "Rate limit uploads" {
		t.Errorf("asks: %+v", s)
	}
	if s := byID["muse:quiet"]; s.Name != "Tidy the notes" || s.Cwd != "/code/notes" {
		t.Errorf("an open terminal's quiet session should still list: %+v", s)
	}
}

func TestMuseDiscoverIsQuietWithoutAConnection(t *testing.T) {
	source := newMuseSourceWith(newMuseHost(func(context.Context) (io.WriteCloser, io.Reader, func(), int, error) {
		return nil, nil, nil, 0, io.EOF
	}), t.TempDir())
	began := time.Now()
	sessions, err := source.Discover(context.Background())
	if err != nil || len(sessions) != 0 || time.Since(began) > time.Second {
		t.Fatalf("got %v, %v after %v", sessions, err, time.Since(began))
	}
}

func TestMusePageReadsHistoryWithoutTakingTheSession(t *testing.T) {
	source, fake := startFakeMuse(t, t.TempDir())
	fake.answers["session/read"] = func(params json.RawMessage) (any, *museRPCError) {
		items := []any{}
		for i := 1; i <= 5; i++ {
			items = append(items, map[string]any{"itemId": "u" + strconv.Itoa(i), "kind": "userMessage", "text": "message " + strconv.Itoa(i)})
		}
		return map[string]any{"session": map[string]any{"sessionId": "s"}, "history": map[string]any{"mode": "inline", "items": items}}, nil
	}
	page, err := source.Page(context.Background(), "muse:s", "", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Messages) != 3 || page.Messages[0].Text != "message 3" || !page.HasMore || page.NextCursor != "u3" {
		t.Fatalf("latest page: %+v", page)
	}
	older, _ := source.Page(context.Background(), "muse:s", page.NextCursor, 3)
	if len(older.Messages) != 2 || older.Messages[0].Text != "message 1" || older.HasMore {
		t.Fatalf("older page: %+v", older)
	}
	if fake.called("session/resume") {
		t.Error("reading history must not take the session")
	}
}

func TestMuseFollowStreamsAReplyInPlace(t *testing.T) {
	source, fake := startFakeMuse(t, t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := make(chan []protocol.Message, 16)
	go func() { _ = source.Follow(ctx, "muse:s", out) }()
	deadline := time.Now().Add(2 * time.Second)
	for !fake.called("view/subscribe") {
		if time.Now().After(deadline) {
			t.Fatal("Follow never watched the session")
		}
		time.Sleep(10 * time.Millisecond)
	}
	fake.notify("item/started", map[string]any{"sessionId": "s", "item": map[string]any{"itemId": "a1", "kind": "agentMessage", "status": "inProgress"}})
	fake.notify("item/delta", map[string]any{"sessionId": "s", "itemId": "a1", "field": "text", "delta": "Hel"})
	fake.notify("item/delta", map[string]any{"sessionId": "s", "itemId": "a1", "field": "text", "delta": "lo"})
	fake.notify("item/delta", map[string]any{"sessionId": "other", "itemId": "x", "field": "text", "delta": "not ours"})
	fake.notify("item/completed", map[string]any{"sessionId": "s", "item": map[string]any{"itemId": "a1", "kind": "agentMessage", "status": "completed", "text": "Hello!"}})

	var texts []string
	timeout := time.After(2 * time.Second)
	for len(texts) < 3 {
		select {
		case rows := <-out:
			for _, row := range rows {
				if row.ID != "a1" {
					t.Fatalf("unexpected row %+v", row)
				}
				texts = append(texts, row.Text)
			}
		case <-timeout:
			t.Fatalf("got %v", texts)
		}
	}
	if texts[0] != "Hel" || texts[1] != "Hello" || texts[2] != "Hello!" {
		t.Fatalf("the reply should grow in place under one id: %v", texts)
	}
}

func TestMuseCommandIDIsAVersion7UUID(t *testing.T) {
	id := museCommandID()
	if len(id) != 36 || id[14] != '7' || (id[19] != '8' && id[19] != '9' && id[19] != 'a' && id[19] != 'b') {
		t.Fatalf("not a UUIDv7: %s", id)
	}
}

func (f *fakeMuse) callsOf(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, call := range f.calls {
		if call == method {
			n++
		}
	}
	return n
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestMuseSendTakesTheSessionAndLetsItGoWhenDone(t *testing.T) {
	source, fake := startFakeMuse(t, t.TempDir())
	var turn map[string]any
	fake.answers["session/resume"] = func(json.RawMessage) (any, *museRPCError) { return map[string]any{"viewCursor": "v"}, nil }
	fake.answers["turn/start"] = func(params json.RawMessage) (any, *museRPCError) {
		_ = json.Unmarshal(params, &turn)
		return map[string]any{"status": "accepted", "disposition": "started"}, nil
	}
	fake.answers["session/read"] = func(json.RawMessage) (any, *museRPCError) {
		return map[string]any{"session": map[string]any{"sessionId": "s", "status": "running"}}, nil
	}
	fake.answers["session/leave"] = func(params json.RawMessage) (any, *museRPCError) {
		var leave struct {
			CommandID string `json:"commandId"`
		}
		_ = json.Unmarshal(params, &leave)
		go fake.notify("session/leaveCompleted", map[string]any{"sessionId": "s", "commandId": leave.CommandID, "outcome": "left"})
		return map[string]any{"status": "accepted"}, nil
	}

	mode, err := source.Inject(context.Background(), "muse:s", "fix the login page")
	if err != nil || mode != protocol.InjectAPI {
		t.Fatalf("send: %v %v", mode, err)
	}
	if turn["sessionId"] != "s" || turn["ifBusy"] != "queue" {
		t.Fatalf("turn/start params: %v", turn)
	}
	if input, _ := turn["input"].([]any); len(input) != 1 || input[0].(map[string]any)["text"] != "fix the login page" {
		t.Fatalf("turn input: %v", turn["input"])
	}
	if !source.holding("s") {
		t.Fatal("Agentman should drive the session while its turn runs")
	}
	// A second message while it works is queued on the same hold.
	if _, err := source.Inject(context.Background(), "muse:s", "and the checkout"); err != nil {
		t.Fatal(err)
	}
	if fake.callsOf("session/resume") != 1 {
		t.Fatalf("resumed %d times, want once", fake.callsOf("session/resume"))
	}
	fake.notify("session/statusChanged", map[string]any{"sessionId": "s", "status": "running"})
	fake.notify("session/statusChanged", map[string]any{"sessionId": "s", "status": "idle"})
	waitFor(t, "the session to be let go", func() bool { return fake.called("session/leave") && !source.holding("s") })
}

func TestMuseSendToATerminalsSessionSaysSo(t *testing.T) {
	source, fake := startFakeMuse(t, t.TempDir())
	inUse := &museRPCError{Code: -32021, Message: "session s is already in use"}
	inUse.Data.Kind = "sessionInUse"
	fake.answers["session/resume"] = func(json.RawMessage) (any, *museRPCError) { return nil, inUse }
	fake.answers["turn/interrupt"] = func(json.RawMessage) (any, *museRPCError) { return nil, inUse }
	if _, err := source.Inject(context.Background(), "muse:s", "hello"); err != errMuseInTerminal {
		t.Fatalf("got %v, want the terminal message", err)
	}
	if source.holding("s") {
		t.Fatal("a refused take must not be held")
	}
	if err := source.Interrupt(context.Background(), "muse:s"); err != errMuseInTerminal {
		t.Fatalf("stop: got %v", err)
	}
}

func TestMuseDiscoverMarksTerminalSessionsReadOnly(t *testing.T) {
	home := t.TempDir()
	source, fake := startFakeMuse(t, home)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	fake.answers["session/list"] = func(json.RawMessage) (any, *museRPCError) {
		return map[string]any{"sessions": []any{
			map[string]any{"sessionId": "mine", "status": "notLoaded", "workspaceRoot": "/a", "updatedAt": now},
			map[string]any{"sessionId": "theirs", "status": "idle", "workspaceRoot": "/b", "updatedAt": now},
		}}, nil
	}
	registry := filepath.Join(home, "runtime", "muse", "sessions")
	_ = os.MkdirAll(registry, 0o755)
	_ = os.WriteFile(filepath.Join(registry, "theirs.json"), []byte(`{"session_id":"theirs","process_generation_hint":"pid=`+strconv.Itoa(os.Getpid())+`"}`), 0o644)
	sessions, _ := source.Discover(context.Background())
	for _, session := range sessions {
		want := protocol.InjectAPI
		if session.NativeID == "theirs" {
			want = protocol.InjectNone
		}
		if session.Inject != want {
			t.Errorf("%s: inject %s, want %s", session.NativeID, session.Inject, want)
		}
	}
	// Agentman's own `muse serve` hosting a session is not a terminal.
	source.host.child.Store(int64(os.Getpid()))
	sessions, _ = source.Discover(context.Background())
	for _, session := range sessions {
		if session.Inject != protocol.InjectAPI {
			t.Errorf("%s should take messages when only Agentman hosts it", session.NativeID)
		}
	}
}
