package source

import (
	"context"
	"encoding/json"
	"maps"
	"sync"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// What Codex's shared daemon says about the threads it has loaded, kept
// current without joining any of them.
//
// A thread is loaded while a client has it open, so for a terminal Codex
// running through the daemon "loaded" is the exact liveness the rollout scan
// can only guess at with a thirty-minute window and a process check (which
// the daemon's own process now always passes). The daemon tells every client
// when a thread starts, when its status changes (busy, idle, waiting on an
// approval or a question) and when it closes, joined or not. Checked against
// Codex 0.166 on 2026-10-31.
//
// Nothing here joins a thread. A joined thread stays loaded while Agentman is
// attached, so joining every thread to watch it would keep each one "live"
// after its terminal had quit.

// codexThreadLoadTimeout bounds reading the loaded threads after a connect.
const codexThreadLoadTimeout = 20 * time.Second

// codexClosedMemory is how long a close is remembered, so a read of a thread
// that was under way when it closed does not bring it back.
const codexClosedMemory = 2 * time.Minute

// codexDaemonThread is one loaded thread, as the daemon describes it.
type codexDaemonThread struct {
	ID string
	// Cwd and Path are the thread's folder and rollout. Path is empty until
	// the first message, when Codex writes the rollout.
	Cwd  string
	Path string
	// Model is the model the thread's next turn uses.
	Model string
	// Mode is Codex's collaboration mode, "plan" or "default", when known.
	Mode string
	// Ephemeral threads (the TUI's title generation) and subagents' threads
	// (ParentThreadID set) are never sessions of their own.
	Ephemeral      bool
	ParentThreadID string
	// Status is the daemon's: "idle", "active", "systemError" (the last turn
	// failed) or "notLoaded". Flags qualify an active thread.
	Status string
	Flags  []string
	// CreatedAt and UpdatedAt are in seconds.
	CreatedAt int64
	UpdatedAt int64
}

// session reports whether the thread is a conversation the user is having,
// rather than one Codex runs for its own purposes.
func (t codexDaemonThread) session() bool {
	return !t.Ephemeral && t.ParentThreadID == "" && t.Status != "notLoaded"
}

// state maps the daemon's status onto Agentman's.
func (t codexDaemonThread) state() protocol.State {
	if t.Status != "active" {
		return protocol.StateIdle
	}
	if len(t.Flags) > 0 {
		return protocol.StateWaitingInput
	}
	return protocol.StateBusy
}

// codexWireThread is the daemon's Thread, as much of it as is read.
type codexWireThread struct {
	ID             string          `json:"id"`
	Cwd            string          `json:"cwd"`
	Path           *string         `json:"path"`
	Model          string          `json:"model"`
	Ephemeral      bool            `json:"ephemeral"`
	ParentThreadID *string         `json:"parentThreadId"`
	Status         codexWireStatus `json:"status"`
	CreatedAt      int64           `json:"createdAt"`
	UpdatedAt      int64           `json:"updatedAt"`
}

type codexWireStatus struct {
	Type  string   `json:"type"`
	Flags []string `json:"activeFlags"`
}

func (w codexWireThread) thread() codexDaemonThread {
	t := codexDaemonThread{
		ID:        w.ID,
		Cwd:       w.Cwd,
		Model:     w.Model,
		Ephemeral: w.Ephemeral,
		Status:    w.Status.Type,
		Flags:     w.Status.Flags,
		CreatedAt: w.CreatedAt,
		UpdatedAt: w.UpdatedAt,
	}
	if w.Path != nil {
		t.Path = *w.Path
	}
	if w.ParentThreadID != nil {
		t.ParentThreadID = *w.ParentThreadID
	}
	return t
}

// codexLiveThreads is the table of loaded threads.
type codexLiveThreads struct {
	// call reaches the daemon; see codexAppServer.call.
	call func(ctx context.Context, method string, params, dest any) error

	mu sync.Mutex
	// ready is true once the threads loaded at connect have been read, and
	// until the connection drops. Before that the table is not the whole
	// truth, and discovery must not hide a session for being missing from it.
	ready   bool
	epoch   int
	threads map[string]codexDaemonThread
	closed  map[string]time.Time
}

func newCodexLiveThreads(call func(ctx context.Context, method string, params, dest any) error) *codexLiveThreads {
	return &codexLiveThreads{call: call, threads: map[string]codexDaemonThread{}, closed: map[string]time.Time{}}
}

// snapshot returns a copy of the table, and whether it can be trusted.
func (l *codexLiveThreads) snapshot() (map[string]codexDaemonThread, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.ready {
		return nil, false
	}
	return maps.Clone(l.threads), true
}

// connected reads every loaded thread on a new connection. The daemon's
// notifications are applied meanwhile, and win: they are newer than any read.
func (l *codexLiveThreads) connected() {
	l.mu.Lock()
	l.epoch++
	epoch := l.epoch
	l.ready = false
	l.threads = map[string]codexDaemonThread{}
	l.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), codexThreadLoadTimeout)
	defer cancel()
	started := time.Now()
	var ids []string
	cursor := ""
	for {
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		var page struct {
			Data       []string `json:"data"`
			NextCursor *string  `json:"nextCursor"`
		}
		if err := l.call(ctx, "thread/loaded/list", params, &page); err != nil {
			return // the connection went; the next one reads again
		}
		ids = append(ids, page.Data...)
		if page.NextCursor == nil || *page.NextCursor == "" {
			break
		}
		cursor = *page.NextCursor
	}
	read := map[string]codexDaemonThread{}
	for _, id := range ids {
		thread, err := l.read(ctx, id)
		if err != nil {
			continue
		}
		read[id] = thread
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.epoch != epoch {
		return
	}
	for id, thread := range read {
		if _, newer := l.threads[id]; newer || l.closedSince(id, started) {
			continue
		}
		l.threads[id] = thread
	}
	l.ready = true
}

// lost forgets the table when the connection drops: until the next connect
// has read it again, discovery goes by the rollouts.
func (l *codexLiveThreads) lost() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.epoch++
	l.ready = false
	l.threads = map[string]codexDaemonThread{}
}

func (l *codexLiveThreads) read(ctx context.Context, id string) (codexDaemonThread, error) {
	var reply struct {
		Thread codexWireThread `json:"thread"`
	}
	if err := l.call(ctx, "thread/read", map[string]any{"threadId": id}, &reply); err != nil {
		return codexDaemonThread{}, err
	}
	return reply.Thread.thread(), nil
}

// closedSince reports whether the thread closed after the moment given.
// Called with mu held.
func (l *codexLiveThreads) closedSince(id string, moment time.Time) bool {
	at, ok := l.closed[id]
	return ok && !at.Before(moment)
}

// apply updates the table from one notification. It runs on the
// connection's read goroutine, so anything slow happens elsewhere.
func (l *codexLiveThreads) apply(msg codexRPCMessage) {
	switch msg.Method {
	case "thread/started":
		var params struct {
			Thread codexWireThread `json:"thread"`
		}
		if json.Unmarshal(msg.Params, &params) != nil || params.Thread.ID == "" {
			return
		}
		l.mu.Lock()
		delete(l.closed, params.Thread.ID)
		l.threads[params.Thread.ID] = params.Thread.thread()
		l.mu.Unlock()

	case "thread/status/changed":
		var params struct {
			ThreadID string          `json:"threadId"`
			Status   codexWireStatus `json:"status"`
		}
		if json.Unmarshal(msg.Params, &params) != nil || params.ThreadID == "" {
			return
		}
		if params.Status.Type == "notLoaded" {
			l.close(params.ThreadID)
			return
		}
		l.mu.Lock()
		thread, known := l.threads[params.ThreadID]
		if known {
			thread.Status, thread.Flags = params.Status.Type, params.Status.Flags
			l.threads[params.ThreadID] = thread
		}
		epoch := l.epoch
		l.mu.Unlock()
		// A thread loaded before this connection read the list, or opened
		// again after it closed: read it to learn what it is.
		if !known {
			go l.fetch(params.ThreadID, epoch)
		}

	case "thread/closed":
		var params struct {
			ThreadID string `json:"threadId"`
		}
		if json.Unmarshal(msg.Params, &params) == nil && params.ThreadID != "" {
			l.close(params.ThreadID)
		}

	case "thread/settings/updated":
		var params struct {
			ThreadID string `json:"threadId"`
			Settings struct {
				Model             string `json:"model"`
				CollaborationMode *struct {
					Mode string `json:"mode"`
				} `json:"collaborationMode"`
			} `json:"threadSettings"`
		}
		if json.Unmarshal(msg.Params, &params) != nil || params.ThreadID == "" {
			return
		}
		l.mu.Lock()
		if thread, known := l.threads[params.ThreadID]; known {
			if params.Settings.Model != "" {
				thread.Model = params.Settings.Model
			}
			if params.Settings.CollaborationMode != nil {
				thread.Mode = params.Settings.CollaborationMode.Mode
			}
			l.threads[params.ThreadID] = thread
		}
		l.mu.Unlock()
	}
}

func (l *codexLiveThreads) close(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.threads, id)
	now := time.Now()
	l.closed[id] = now
	for other, at := range l.closed {
		if now.Sub(at) > codexClosedMemory {
			delete(l.closed, other)
		}
	}
}

// fetch reads one thread the table did not know.
func (l *codexLiveThreads) fetch(id string, epoch int) {
	ctx, cancel := context.WithTimeout(context.Background(), codexAppServerInitTimeout)
	defer cancel()
	started := time.Now()
	thread, err := l.read(ctx, id)
	if err != nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, known := l.threads[id]; known || l.epoch != epoch || l.closedSince(id, started) {
		return
	}
	l.threads[id] = thread
}

// ConnectAppServer connects to Codex's shared daemon and keeps the
// connection up until Close: `am serve` calls it, and short-lived commands
// leave it alone. version is Agentman's, which the daemon is told.
func (s *CodexSource) ConnectAppServer(version string) {
	s.appMu.Lock()
	defer s.appMu.Unlock()
	if s.appServer != nil {
		return
	}
	server := newCodexAppServer(codexAppServerSocket(s.home), version)
	live := newCodexLiveThreads(server.call)
	server.handle = live.apply
	server.up = live.connected
	server.down = live.lost
	ctx, stop := context.WithCancel(context.Background())
	s.appServer, s.live, s.appStop = server, live, stop
	go server.run(ctx)
}

// Close drops the connection to Codex's shared daemon.
func (s *CodexSource) Close() {
	s.appMu.Lock()
	defer s.appMu.Unlock()
	if s.appStop != nil {
		s.appStop()
	}
}

// liveThreads is the daemon's table of open threads, and whether it can be
// trusted right now.
func (s *CodexSource) liveThreads() (map[string]codexDaemonThread, bool) {
	s.appMu.Lock()
	live := s.live
	s.appMu.Unlock()
	if live == nil {
		return nil, false
	}
	return live.snapshot()
}
