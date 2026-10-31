package source

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/lenajeremy/agentman/internal/parser"
	"github.com/lenajeremy/agentman/internal/protocol"
)

// While the phone or the Mac has a Codex session open, Agentman joins its
// thread on the shared daemon to see the turn as it happens: the reply word
// by word, and each command, edit or MCP call from the moment it starts.
// parser.CodexLive turns that into provisional rows, which the rollout's
// finished rows replace; see it for why the two agree.
//
// A thread is joined while at least one follow watches it, and left when the
// last one stops. Joining is what makes the daemon send a thread's events, and
// it also keeps the thread loaded, so a terminal quit while the phone watches
// stays listed until the phone leaves.

// codexStreamBuffer is how many events one follow can fall behind by. Past it
// events are dropped: they only feed previews, which the rollout's finished
// rows correct.
const codexStreamBuffer = 1024

// codexJoinTimeout bounds joining or leaving a thread.
const codexJoinTimeout = 5 * time.Second

// codexJoiner is what codexStreams needs of the connection.
type codexJoiner interface {
	join(ctx context.Context, threadID string, thread any) error
	leave(ctx context.Context, threadID string) error
	// adopted records a join the connection made by starting the thread.
	adopted(threadID string)
}

// codexStreams hands each thread's live events to the follows watching it.
type codexStreams struct {
	server codexJoiner
	// joinMu keeps a join and a leave of the same thread from crossing.
	joinMu sync.Mutex
	mu     sync.Mutex
	subs   map[string]map[*codexStream]bool
}

// codexStream is one follow's feed of one thread's events.
type codexStream struct {
	thread string
	events chan codexRPCMessage
}

func newCodexStreams(server codexJoiner) *codexStreams {
	return &codexStreams{server: server, subs: map[string]map[*codexStream]bool{}}
}

// codexStreamMethods are the events a follow reads.
var codexStreamMethods = map[string]bool{
	"item/started":                      true,
	"item/completed":                    true,
	"item/agentMessage/delta":           true,
	"item/commandExecution/outputDelta": true,
}

// open starts a feed, joining the thread if nothing else watches it.
func (st *codexStreams) open(ctx context.Context, threadID string) (*codexStream, error) {
	return st.join(ctx, threadID, true)
}

// hold joins a thread, or keeps it joined, without a feed of its events:
// what the daemon sends only to clients that joined (an approval, a
// question) is all that is wanted. See holdWaiting.
func (st *codexStreams) hold(ctx context.Context, threadID string) (*codexStream, error) {
	return st.join(ctx, threadID, false)
}

// adopt counts a join the connection already has: starting a thread joins
// the client that started it. Closing the stream lets it go like any other.
func (st *codexStreams) adopt(threadID string) *codexStream {
	st.joinMu.Lock()
	defer st.joinMu.Unlock()
	st.server.adopted(threadID)
	stream := &codexStream{thread: threadID}
	st.mu.Lock()
	if st.subs[threadID] == nil {
		st.subs[threadID] = map[*codexStream]bool{}
	}
	st.subs[threadID][stream] = true
	st.mu.Unlock()
	return stream
}

func (st *codexStreams) join(ctx context.Context, threadID string, feed bool) (*codexStream, error) {
	st.joinMu.Lock()
	defer st.joinMu.Unlock()
	st.mu.Lock()
	first := len(st.subs[threadID]) == 0
	st.mu.Unlock()
	if first {
		joinCtx, cancel := context.WithTimeout(ctx, codexJoinTimeout)
		err := st.server.join(joinCtx, threadID, nil)
		cancel()
		if err != nil {
			return nil, err
		}
	}
	stream := &codexStream{thread: threadID}
	if feed {
		stream.events = make(chan codexRPCMessage, codexStreamBuffer)
	}
	st.mu.Lock()
	if st.subs[threadID] == nil {
		st.subs[threadID] = map[*codexStream]bool{}
	}
	st.subs[threadID][stream] = true
	st.mu.Unlock()
	return stream, nil
}

// close ends a feed, leaving the thread if nothing else watches it.
func (st *codexStreams) close(stream *codexStream) {
	if stream == nil {
		return
	}
	st.joinMu.Lock()
	defer st.joinMu.Unlock()
	st.mu.Lock()
	delete(st.subs[stream.thread], stream)
	last := len(st.subs[stream.thread]) == 0
	if last {
		delete(st.subs, stream.thread)
	}
	st.mu.Unlock()
	if last {
		ctx, cancel := context.WithTimeout(context.Background(), codexJoinTimeout)
		_ = st.server.leave(ctx, stream.thread)
		cancel()
	}
}

// dispatch hands one event from the daemon to the feeds of its thread. It
// runs on the connection's read goroutine, so it never waits.
func (st *codexStreams) dispatch(msg codexRPCMessage) {
	if !codexStreamMethods[msg.Method] {
		return
	}
	var params struct {
		ThreadID string `json:"threadId"`
	}
	if json.Unmarshal(msg.Params, &params) != nil || params.ThreadID == "" {
		return
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	for stream := range st.subs[params.ThreadID] {
		if stream.events == nil {
			continue
		}
		select {
		case stream.events <- msg:
		default:
		}
	}
}

// apply feeds one event to a session's live rows.
func applyCodexLive(live *parser.CodexLive, msg codexRPCMessage) {
	switch msg.Method {
	case "item/started":
		var params struct {
			Item      json.RawMessage `json:"item"`
			StartedAt int64           `json:"startedAtMs"`
		}
		if json.Unmarshal(msg.Params, &params) == nil {
			live.Started(params.Item, params.StartedAt)
		}
	case "item/completed":
		var params struct {
			Item json.RawMessage `json:"item"`
		}
		if json.Unmarshal(msg.Params, &params) == nil {
			live.Completed(params.Item)
		}
	case "item/agentMessage/delta", "item/commandExecution/outputDelta":
		var params struct {
			ItemID string `json:"itemId"`
			Delta  string `json:"delta"`
		}
		if json.Unmarshal(msg.Params, &params) == nil {
			live.Delta(params.ItemID, params.Delta, time.Now().UnixMilli())
		}
	}
}

// codexFollowLive is a follow's view of its session's thread on the daemon:
// which thread it has joined, and the rows that thread's events have made.
type codexFollowLive struct {
	src    *CodexSource
	rows   *parser.CodexLive
	stream *codexStream
}

func (s *CodexSource) newFollowLive(sessionID string) *codexFollowLive {
	return &codexFollowLive{src: s, rows: parser.NewCodexLive(sessionID)}
}

// events is the feed to read, nil (never ready) when nothing is joined.
func (f *codexFollowLive) events() <-chan codexRPCMessage {
	if f.stream == nil {
		return nil
	}
	return f.stream.events
}

// track joins the thread the session now reads, if the daemon has it open,
// leaving any other. A session's thread changes when Codex starts a new
// conversation in the same pane.
func (f *codexFollowLive) track(ctx context.Context, session codexSession) {
	want := f.src.daemonThreadOf(session)
	if f.stream != nil && f.stream.thread == want {
		return
	}
	streams := f.src.codexStreams()
	if streams == nil {
		return
	}
	streams.close(f.stream)
	f.stream = nil
	if want == "" {
		return
	}
	if stream, err := streams.open(ctx, want); err == nil {
		f.stream = stream
	}
}

func (f *codexFollowLive) stop() {
	if streams := f.src.codexStreams(); streams != nil {
		streams.close(f.stream)
	}
	f.stream = nil
}

// daemonThreadOf is the daemon's thread for a session, or "" when the daemon
// does not have it open.
func (s *CodexSource) daemonThreadOf(session codexSession) string {
	threads, ready := s.liveThreads()
	if !ready || session.transcript == "" {
		return ""
	}
	thread, ok := threads[session.meta.NativeID]
	if !ok || !thread.session() {
		return ""
	}
	return thread.ID
}

func (s *CodexSource) codexStreams() *codexStreams {
	s.appMu.Lock()
	defer s.appMu.Unlock()
	return s.streams
}

// merge puts the rollout's rows and the live ones into one batch: the
// rollout's finished rows settle their items first, so a preview of the same
// item is never sent after them.
func (f *codexFollowLive) merge(finished []protocol.Message) []protocol.Message {
	f.rows.Seen(finished)
	previews := f.rows.Flush()
	if len(previews) == 0 {
		return finished
	}
	return append(previews, finished...)
}
