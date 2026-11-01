package source

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// testClock is a clock tests move by hand.
type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) Add(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// liveWithStream is an openCodeLive with one service's stream in place and
// nothing reading it: tests hand it events themselves.
func liveWithStream(base string) (*openCodeLive, *testClock) {
	clock := &testClock{now: time.UnixMilli(1_700_000_000_000)}
	live := newOpenCodeLive()
	live.now = clock.Now
	live.streams[base] = &openCodeLiveStream{
		cancel:   func() {},
		sessions: map[string]*openCodeLiveSession{},
		watchers: map[string]map[chan struct{}]struct{}{},
	}
	return live, clock
}

func ocEvent(kind string, data map[string]any) string {
	raw, _ := json.Marshal(map[string]any{"type": kind, "data": data})
	return string(raw)
}

// A turn as OpenCode 2.2's service streamed it (a shell call, then a reply
// after it), with the saved messages it ended as: every preview is under
// the id of the saved row it becomes.
func TestOpenCodeLiveRowsAreTheSavedRows(t *testing.T) {
	live, clock := liveWithStream("http://svc")
	_, stop := live.watch("http://svc", "ses_1")
	defer stop()
	at := func(kind string, data map[string]any) {
		data["sessionID"] = "ses_1"
		live.apply("http://svc", []byte(ocEvent(kind, data)))
	}
	at("session.step.started", map[string]any{"assistantMessageID": "msg_a", "started": 1000})
	at("session.reasoning.started", map[string]any{"assistantMessageID": "msg_a", "ordinal": 0})
	at("session.tool.input.started", map[string]any{"assistantMessageID": "msg_a", "id": "call_1", "name": "shell"})

	rows := live.rows("http://svc", "ses_1", "opencode:ses_1")
	if len(rows) != 1 || rows[0].ID != "msg_a:call_1" || rows[0].Tool == nil ||
		rows[0].Tool.Status != protocol.ToolRunning || rows[0].Tool.Name != "shell" {
		t.Fatalf("a call being written = %+v", rows)
	}

	at("session.tool.input.ended", map[string]any{"assistantMessageID": "msg_a", "id": "call_1", "text": `{"command": "ls"}`})
	at("session.tool.called", map[string]any{"assistantMessageID": "msg_a", "id": "call_1", "input": map[string]string{"command": "ls"}})
	at("session.tool.success", map[string]any{"assistantMessageID": "msg_a", "id": "call_1"})
	at("session.step.started", map[string]any{"assistantMessageID": "msg_b", "started": 2000})
	at("session.reasoning.started", map[string]any{"assistantMessageID": "msg_b", "ordinal": 0})
	at("session.text.started", map[string]any{"assistantMessageID": "msg_b", "ordinal": 0})
	at("session.text.delta", map[string]any{"assistantMessageID": "msg_b", "ordinal": 0, "delta": "The folder"})
	at("session.text.delta", map[string]any{"assistantMessageID": "msg_b", "ordinal": 0, "delta": " is empty."})

	rows = live.rows("http://svc", "ses_1", "opencode:ses_1")
	var got []string
	for _, row := range rows {
		entry := row.ID + " " + row.Text
		if row.Tool != nil {
			entry = row.ID + " " + row.Tool.Name + " " + row.Tool.Summary + " " + string(row.Tool.Status)
		}
		got = append(got, entry)
	}
	want := []string{"msg_a:call_1 shell ls ok", "msg_b:1 The folder is empty."}
	if !slices.Equal(got, want) {
		t.Fatalf("previews = %q, want %q", got, want)
	}

	// The same turn as the service saved it.
	saved := []string{
		`{"id":"msg_a","type":"assistant","time":{"created":1000,"completed":1500},"content":[` +
			`{"type":"reasoning","text":"Let me look."},` +
			`{"type":"tool","id":"call_1","name":"shell","state":{"status":"completed","input":{"command":"ls"},"content":[{"type":"text","text":"(no output)"}]}}]}`,
		`{"id":"msg_b","type":"assistant","time":{"created":2000,"completed":2500},"content":[` +
			`{"type":"reasoning","text":"Empty."},{"type":"text","text":"The folder is empty."}]}`,
	}
	ids := map[string]protocol.Message{}
	for _, raw := range saved {
		message, ok := openCodeServiceMessage(json.RawMessage(raw))
		if !ok {
			t.Fatalf("could not read %s", raw)
		}
		for _, row := range openCodeMessages("opencode:ses_1", message) {
			ids[row.ID] = row
		}
	}
	for _, row := range rows {
		savedRow, ok := ids[row.ID]
		if !ok {
			t.Fatalf("preview %s has no saved row; saved ids are %v", row.ID, ids)
		}
		if savedRow.Ts != row.Ts {
			t.Errorf("%s: preview at %d, saved at %d", row.ID, row.Ts, savedRow.Ts)
		}
	}

	// Once the turn is over, previews last long enough to be replaced, then
	// go, and the session with them.
	at("session.execution.succeeded", map[string]any{})
	if rows := live.rows("http://svc", "ses_1", "opencode:ses_1"); len(rows) != 2 {
		t.Fatalf("just after the turn = %+v", rows)
	}
	clock.Add(openCodeLiveLinger + time.Second)
	if rows := live.rows("http://svc", "ses_1", "opencode:ses_1"); len(rows) != 0 {
		t.Fatalf("well after the turn = %+v", rows)
	}
	if len(live.streams["http://svc"].sessions) != 0 {
		t.Fatal("kept a finished session")
	}
}

// A message whose start the stream missed can't place its text, so its text
// is left to the saved message; its calls have ids of their own and show.
func TestOpenCodeLiveMissedStartShowsCallsOnly(t *testing.T) {
	live, _ := liveWithStream("http://svc")
	_, stop := live.watch("http://svc", "ses_1")
	defer stop()
	for _, event := range []string{
		ocEvent("session.text.delta", map[string]any{"sessionID": "ses_1", "assistantMessageID": "msg_a", "ordinal": 0, "delta": "…half a sentence"}),
		ocEvent("session.tool.input.started", map[string]any{"sessionID": "ses_1", "assistantMessageID": "msg_a", "id": "call_2", "name": "read"}),
	} {
		live.apply("http://svc", []byte(event))
	}
	rows := live.rows("http://svc", "ses_1", "opencode:ses_1")
	if len(rows) != 1 || rows[0].ID != "msg_a:call_2" {
		t.Fatalf("rows = %+v", rows)
	}
}

// What nobody follows is forgotten when its turn ends.
func TestOpenCodeLiveForgetsUnwatchedTurns(t *testing.T) {
	live, _ := liveWithStream("http://svc")
	live.apply("http://svc", []byte(ocEvent("session.step.started", map[string]any{"sessionID": "ses_9", "assistantMessageID": "msg_a"})))
	live.apply("http://svc", []byte(ocEvent("session.text.started", map[string]any{"sessionID": "ses_9", "assistantMessageID": "msg_a", "ordinal": 0})))
	if len(live.streams["http://svc"].sessions) != 1 {
		t.Fatal("did not keep a reply being written")
	}
	live.apply("http://svc", []byte(ocEvent("session.execution.succeeded", map[string]any{"sessionID": "ses_9"})))
	if len(live.streams["http://svc"].sessions) != 0 {
		t.Fatal("kept a finished turn nobody follows")
	}
}

// The stream is read event by event; a line too long to keep is skipped
// whole, and the events after it still arrive.
func TestOpenCodeLiveReadsTheStream(t *testing.T) {
	live, _ := liveWithStream("http://svc")
	_, stop := live.watch("http://svc", "ses_1")
	defer stop()
	start := ocEvent("session.step.started", map[string]any{"sessionID": "ses_1", "assistantMessageID": "msg_a"})
	text := ocEvent("session.text.started", map[string]any{"sessionID": "ses_1", "assistantMessageID": "msg_a", "ordinal": 0})
	huge := ocEvent("session.text.delta", map[string]any{"sessionID": "ses_1", "assistantMessageID": "msg_a", "ordinal": 0,
		"delta": strings.Repeat("x", maxOpenCodeLiveLine)})
	delta := ocEvent("session.text.delta", map[string]any{"sessionID": "ses_1", "assistantMessageID": "msg_a", "ordinal": 0, "delta": "kept"})
	stream := ": heartbeat\n\ndata: " + start + "\n\ndata: " + text + "\n\ndata: " + huge + "\n\ndata: " + delta + "\n\n"
	live.read(context.Background(), "http://svc", strings.NewReader(stream))
	rows := live.rows("http://svc", "ses_1", "opencode:ses_1")
	if len(rows) != 1 || rows[0].Text != "kept" {
		t.Fatalf("rows = %+v", rows)
	}
}

// Following an OpenCode 2 session: the reply shows as it is written, from
// the service's event stream, under the id the saved message gives it; the
// saved row then takes its place, and a preview nothing replaces is taken
// back.
func TestOpenCodeFollowStreamsFromTheService(t *testing.T) {
	fake := &ocServiceFake{active: true, events: make(chan string, 16), connected: make(chan struct{}, 4),
		messages: []map[string]any{
			{"id": "msg_u", "type": "user", "time": map[string]float64{"created": 1000}, "text": "say hello"},
		}}
	server := fake.start(t)
	src := openCodeServiceSource(t, server.URL, "secret")
	clock := &testClock{now: time.Now()}
	src.live.now = clock.Now
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := src.Discover(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-fake.connected:
	case <-time.After(5 * time.Second):
		t.Fatal("never opened the event stream")
	}

	out := make(chan []protocol.Message, 16)
	go func() { _ = src.Follow(ctx, "opencode:ses_1", out) }()
	// Follow reads the saved messages before it watches; give it a moment.
	time.Sleep(100 * time.Millisecond)
	send := func(kind string, data map[string]any) {
		data["sessionID"] = "ses_1"
		fake.events <- ocEvent(kind, data)
	}
	// Rows arrive in batches; expect reads them in order, keeping what is
	// after a match for the next expect.
	var pending []protocol.Message
	expect := func(what string, match func(protocol.Message) bool) {
		t.Helper()
		deadline := time.After(5 * time.Second)
		for {
			for i, row := range pending {
				if match(row) {
					pending = pending[i+1:]
					return
				}
			}
			pending = nil
			select {
			case batch := <-out:
				pending = batch
			case <-deadline:
				t.Fatalf("never got %s", what)
			}
		}
	}

	send("session.step.started", map[string]any{"assistantMessageID": "msg_a", "started": 2000})
	send("session.text.started", map[string]any{"assistantMessageID": "msg_a", "ordinal": 0})
	send("session.text.delta", map[string]any{"assistantMessageID": "msg_a", "ordinal": 0, "delta": "Hello"})
	expect("the first words", func(row protocol.Message) bool { return row.ID == "msg_a:0" && row.Text == "Hello" })
	send("session.text.delta", map[string]any{"assistantMessageID": "msg_a", "ordinal": 0, "delta": " there"})
	expect("the next words", func(row protocol.Message) bool { return row.ID == "msg_a:0" && row.Text == "Hello there" })

	// The reply is saved, and the turn ends with a call the saved messages
	// never show.
	fake.mu.Lock()
	fake.messages = append([]map[string]any{{"id": "msg_a", "type": "assistant",
		"time":    map[string]float64{"created": 2000, "completed": 3000},
		"content": []map[string]any{{"type": "text", "text": "Hello there."}}}}, fake.messages...)
	fake.mu.Unlock()
	send("session.text.ended", map[string]any{"assistantMessageID": "msg_a", "ordinal": 0, "text": "Hello there."})
	send("session.tool.input.started", map[string]any{"assistantMessageID": "msg_a", "id": "call_9", "name": "shell"})
	expect("the saved reply", func(row protocol.Message) bool { return row.ID == "msg_a:0" && row.Text == "Hello there." })
	expect("the call", func(row protocol.Message) bool { return row.ID == "msg_a:call_9" && row.Tool != nil })
	send("session.execution.succeeded", map[string]any{})

	time.Sleep(300 * time.Millisecond)
	clock.Add(openCodeLiveLinger + time.Second)
	expect("the call taken back", func(row protocol.Message) bool {
		return row.ID == "msg_a:call_9" && row.Tool == nil && row.Text == ""
	})
	// The saved reply is never taken back.
	for {
		for _, row := range pending {
			if row.ID == "msg_a:0" && row.Text == "" {
				t.Fatalf("took back the saved reply: %+v", pending)
			}
		}
		pending = nil
		select {
		case batch := <-out:
			for _, row := range batch {
				if row.ID == "msg_a:0" && row.Text == "" {
					t.Fatalf("took back the saved reply: %+v", batch)
				}
			}
		case <-time.After(400 * time.Millisecond):
			return
		}
	}
}
