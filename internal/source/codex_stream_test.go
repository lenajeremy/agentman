package source

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// fakeJoiner records joins and leaves.
type fakeJoiner struct {
	mu     sync.Mutex
	joins  []string
	leaves []string
	refuse bool
}

func (f *fakeJoiner) join(_ context.Context, threadID string, _ any) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.refuse {
		return &codexRPCError{Message: "no rollout found"}
	}
	f.joins = append(f.joins, threadID)
	return nil
}

func (f *fakeJoiner) leave(_ context.Context, threadID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.leaves = append(f.leaves, threadID)
	return nil
}

func (f *fakeJoiner) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.joins), len(f.leaves)
}

func daemonEvent(t *testing.T, method string, params any) codexRPCMessage {
	t.Helper()
	data, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	return codexRPCMessage{Method: method, Params: data}
}

// The phone and the Mac watching one session join its thread once, and it
// is left only when both have stopped.
func TestCodexStreamsJoinOnceAndLeaveLast(t *testing.T) {
	joiner := &fakeJoiner{}
	streams := newCodexStreams(joiner)
	phone, err := streams.open(context.Background(), "t1")
	if err != nil {
		t.Fatal(err)
	}
	mac, err := streams.open(context.Background(), "t1")
	if err != nil {
		t.Fatal(err)
	}
	if joins, _ := joiner.counts(); joins != 1 {
		t.Fatalf("joined %d times, want once", joins)
	}

	streams.dispatch(daemonEvent(t, "item/agentMessage/delta", map[string]any{"threadId": "t1", "itemId": "msg_1", "delta": "hi"}))
	streams.dispatch(daemonEvent(t, "item/agentMessage/delta", map[string]any{"threadId": "t2", "itemId": "msg_2", "delta": "other"}))
	streams.dispatch(daemonEvent(t, "thread/status/changed", map[string]any{"threadId": "t1"}))
	for name, stream := range map[string]*codexStream{"phone": phone, "mac": mac} {
		if len(stream.events) != 1 {
			t.Fatalf("%s got %d events, want its thread's one delta", name, len(stream.events))
		}
	}

	streams.close(phone)
	if _, leaves := joiner.counts(); leaves != 0 {
		t.Fatal("left while the Mac still watched")
	}
	streams.close(mac)
	if _, leaves := joiner.counts(); leaves != 1 {
		t.Fatalf("left %d times, want once after the last", leaves)
	}
}

// Previews are not worth stalling the connection for: a feed that falls far
// behind loses events rather than holding up every other thread.
func TestCodexStreamsNeverWaitOnASlowFollow(t *testing.T) {
	streams := newCodexStreams(&fakeJoiner{})
	stream, err := streams.open(context.Background(), "t1")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < codexStreamBuffer+100; i++ {
			streams.dispatch(daemonEvent(t, "item/agentMessage/delta", map[string]any{"threadId": "t1", "itemId": "m", "delta": "x"}))
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("dispatch blocked on a full feed")
	}
	if len(stream.events) != codexStreamBuffer {
		t.Fatalf("buffered %d", len(stream.events))
	}
}

// End to end through Follow: the reply streams in under its item id, and the
// rollout's finished row takes its place and keeps it.
func TestCodexFollowStreamsTheReplyThenTakesTheRolloutsRow(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	path := writeDaemonRollout(t, home, "t1", "/work/api", now.Add(-time.Hour), now.Add(-time.Second), "task_started")
	src := daemonSource(t, home, true, openThread("t1", path, "active"))
	joiner := &fakeJoiner{}
	src.streams = newCodexStreams(joiner)
	if _, err := src.Discover(context.Background()); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	out := make(chan []protocol.Message, 64)
	done := make(chan error, 1)
	go func() { done <- src.Follow(ctx, "codex:t1", out) }()

	deadline := time.Now().Add(5 * time.Second)
	for joins, _ := joiner.counts(); joins == 0; joins, _ = joiner.counts() {
		if time.Now().After(deadline) {
			t.Fatal("Follow never joined the thread")
		}
		time.Sleep(10 * time.Millisecond)
	}

	send := func(method string, params any) { src.streams.dispatch(daemonEvent(t, method, params)) }
	send("item/started", map[string]any{"threadId": "t1", "turnId": "u1", "startedAtMs": now.UnixMilli(),
		"item": map[string]any{"type": "agentMessage", "id": "msg_1", "text": ""}})
	send("item/agentMessage/delta", map[string]any{"threadId": "t1", "turnId": "u1", "itemId": "msg_1", "delta": "The kettle"})
	preview := awaitRow(t, out, "msg_1", func(m protocol.Message) bool { return m.Text == "The kettle" })
	if preview.Role != protocol.RoleAssistant {
		t.Fatalf("preview = %+v", preview)
	}

	appendJSONL(t, path, codexAgentMessage("msg_1", "The kettle hums."))
	awaitRow(t, out, "msg_1", func(m protocol.Message) bool { return m.Text == "The kettle hums." })

	// A late event for the same reply must not undo the finished row.
	send("item/agentMessage/delta", map[string]any{"threadId": "t1", "turnId": "u1", "itemId": "msg_1", "delta": " late"})
	select {
	case batch := <-out:
		for _, m := range batch {
			if m.ID == "msg_1" {
				t.Fatalf("a preview came after the finished row: %+v", m)
			}
		}
	case <-time.After(600 * time.Millisecond):
	}

	cancel()
	<-done
	if _, leaves := joiner.counts(); leaves != 1 {
		t.Fatalf("left %d times, want once when the follow ended", leaves)
	}
}

// A session the daemon does not have open is followed from its rollout
// alone, as before.
func TestCodexFollowWithoutTheDaemonJoinsNothing(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	path := writeRolloutFrom(t, home, "x1", "/work/api", "codex_exec", "exec", now.Add(-time.Hour), now.Add(-time.Second), "task_started")
	src := daemonSource(t, home, true)
	joiner := &fakeJoiner{}
	src.streams = newCodexStreams(joiner)
	if _, err := src.Discover(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	out := make(chan []protocol.Message, 64)
	go src.Follow(ctx, "codex:x1", out)
	// Follow reads from the end once it has started; a line written before
	// then is history, not news.
	awaitCodexAttached(t, path, out)
	appendJSONL(t, path, codexAgentMessage("msg_9", "from the rollout"))
	awaitRow(t, out, "msg_9", func(m protocol.Message) bool { return m.Text == "from the rollout" })
	if joins, _ := joiner.counts(); joins != 0 {
		t.Fatalf("joined %d threads for a session the daemon does not have", joins)
	}
}

func awaitRow(t *testing.T, out <-chan []protocol.Message, id string, ok func(protocol.Message) bool) protocol.Message {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case batch := <-out:
			for _, m := range batch {
				if m.ID == id && ok(m) {
					return m
				}
			}
		case <-deadline:
			t.Fatalf("row %s never arrived as expected", id)
			return protocol.Message{}
		}
	}
}
