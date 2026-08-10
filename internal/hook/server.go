package hook

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// maxHookBody bounds a single delivery. A Stop payload carries the agent's
// last message, which can be long, but nothing legitimate approaches this.
const maxHookBody = 1 << 20

// Server receives hook deliveries on loopback.
//
// Every handler here runs while the agent is blocked waiting for the hook to
// return, so the whole path is non-blocking: parse, hand off, reply. If nobody
// is consuming events we drop them rather than stall someone's coding session.
type Server struct {
	token  string
	events chan Event
	// lastFired records when each agent kind last delivered anything, which is
	// what lets `am doctor` distinguish "hooks are installed" from "hooks are
	// actually working" — the honest answer for Codex, whose config schema we
	// could not verify without a live session.
	mu        sync.RWMutex
	lastFired map[protocol.Kind]time.Time

	server *http.Server
}

// NewServer creates a hook receiver. The returned channel is buffered; use
// Events to consume it.
func NewServer(token string) *Server {
	return &Server{
		token:     token,
		events:    make(chan Event, 64),
		lastFired: map[protocol.Kind]time.Time{},
	}
}

// Events returns the stream of received hook events.
func (s *Server) Events() <-chan Event { return s.events }

// LastFired reports when a given agent kind last delivered a hook.
func (s *Server) LastFired(kind protocol.Kind) (time.Time, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.lastFired[kind]
	return t, ok
}

// Handler builds the HTTP routes, exposed separately so tests can drive it
// without binding a port.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/hook/", s.handleHook)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	return mux
}

// Listen starts the loopback listener and serves until ctx is cancelled.
func (s *Server) Listen(ctx context.Context, addr string) error {
	if addr == "" {
		addr = DefaultAddr
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}

	s.server = &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	done := make(chan error, 1)
	go func() { done <- s.server.Serve(listener) }()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.server.Shutdown(shutdownCtx)
		return nil
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// handleHook accepts POST /hook/{kind}/{event}.
func (s *Server) handleHook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.token != "" && r.Header.Get("X-Agentman-Token") != s.token {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	// /hook/claude/Stop
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/hook/"), "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		http.Error(w, "expected /hook/{kind}/{event}", http.StatusBadRequest)
		return
	}
	kind := protocol.Kind(parts[0])
	name := Name(parts[1])

	raw, err := io.ReadAll(io.LimitReader(r.Body, maxHookBody))
	if err != nil {
		http.Error(w, "unreadable body", http.StatusBadRequest)
		return
	}
	payload, err := ParsePayload(raw)
	if err != nil {
		// Still acknowledge: refusing here would surface as a hook failure in
		// the user's agent, and a payload we cannot read is our problem.
		w.WriteHeader(http.StatusAccepted)
		return
	}
	// Trust the URL over the body: the body's hook_event_name is absent on
	// some events, and the URL is what we installed.
	if payload.HookEventName == "" {
		payload.HookEventName = string(name)
	}

	event := Event{
		Kind:       kind,
		Name:       name,
		SessionID:  string(kind) + ":" + payload.SessionID,
		Payload:    payload,
		ReceivedAt: time.Now().UnixMilli(),
	}

	s.mu.Lock()
	s.lastFired[kind] = time.Now()
	s.mu.Unlock()

	// Never block the agent. A full buffer means nobody is consuming, and a
	// dropped status update is far cheaper than a stalled coding session.
	select {
	case s.events <- event:
	default:
	}

	w.WriteHeader(http.StatusNoContent)
}
