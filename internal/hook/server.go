package hook

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
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
	// actually working".
	mu        sync.RWMutex
	lastFired map[protocol.Kind]time.Time

	server *http.Server

	// pendingFor returns messages queued for a session, if any. It is what
	// turns a Stop hook into a delivery channel: the response tells the agent
	// to keep going with the user's text as the reason.
	pendingFor func(sessionID string) []string

	// speak reads a finished turn out loud, when the user has asked for it.
	// A function rather than the Speaker itself, so this package does not
	// depend on how speech is produced -- or on it existing at all.
	speak func(text string)
}

// SetSpeaker installs a function to read finished turns aloud. Optional: with
// none set, nothing is spoken and nothing changes.
func (s *Server) SetSpeaker(fn func(text string)) {
	s.speak = fn
}

// SetPendingSource installs the lookup used to answer Stop hooks with queued
// messages.
func (s *Server) SetPendingSource(fn func(sessionID string) []string) {
	s.pendingFor = fn
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

// hookDecision is the response an agent CLI acts on. An empty decision means
// "carry on"; "block" feeds Reason back to the model and continues the turn.
type hookDecision struct {
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

// writeHookDecision instructs the agent to continue with the queued text.
//
// This path is best-effort by design and the UI says so: the CLI can discard a
// block on some end-of-turn routes, and it can never interrupt an agent that is
// still working. It exists so a session the user started normally is not
// completely unreachable.
func writeHookDecision(w http.ResponseWriter, reason string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(hookDecision{Decision: "block", Reason: reason})
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
	if err := ValidateAddr(addr); err != nil {
		return err
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return s.Serve(ctx, listener)
}

// Serve runs the hook receiver on an already-bound listener. Binding can be
// done before persisting a custom address, so a failed port selection never
// leaves future hook processes pointed at a listener that did not start.
func (s *Server) Serve(ctx context.Context, listener net.Listener) error {
	if err := ValidateAddr(listener.Addr().String()); err != nil {
		_ = listener.Close()
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

// ValidateAddr ensures the hook receiver remains local and has a stable port.
// Binding it to a LAN interface would expose session metadata and let anyone
// who obtained the token forge lifecycle events; port zero cannot be persisted
// accurately for the child hook processes that need to call it.
func ValidateAddr(addr string) error {
	if addr == "" {
		return fmt.Errorf("hook: listen address is empty")
	}
	host, portText, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("hook: invalid listen address %q: %w", addr, err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("hook: listen address %q must use a numeric loopback IP", addr)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("hook: listen address %q must use a fixed port from 1 to 65535", addr)
	}
	return nil
}

// handleHook accepts POST /hook/{kind}/{event}.
func (s *Server) handleHook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	provided := r.Header.Get("X-Agentman-Token")
	if s.token == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(s.token)) != 1 {
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
	if !validDelivery(kind, name) {
		http.Error(w, "unsupported hook", http.StatusBadRequest)
		return
	}

	raw, err := io.ReadAll(io.LimitReader(r.Body, maxHookBody+1))
	if err != nil {
		http.Error(w, "unreadable body", http.StatusBadRequest)
		return
	}
	if len(raw) > maxHookBody {
		http.Error(w, "hook body too large", http.StatusRequestEntityTooLarge)
		return
	}
	payload, err := ParsePayload(raw)
	if err != nil {
		// Still acknowledge: refusing here would surface as a hook failure in
		// the user's agent, and a payload we cannot read is our problem.
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if payload.SessionID == "" {
		// Empty stdin and unrelated Codex notify records are not lifecycle
		// events. Acknowledge them without inventing a shared "claude:" session.
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

	// A turn ending is the one moment a session without a live input channel
	// can be handed a message. Delivering it means telling the agent not to
	// stop, with the user's text as the reason it should continue.
	if event.IsTurnComplete() {
		// Before the delivery check, because speaking is fire-and-forget and
		// must happen whether or not there is queued input to hand back.
		if s.speak != nil {
			s.speak(event.Payload.LastAssistantMessage)
		}

		if s.pendingFor != nil {
			if queued := s.pendingFor(event.SessionID); len(queued) > 0 {
				writeHookDecision(w, strings.Join(queued, "\n\n"))
				return
			}
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

func validDelivery(kind protocol.Kind, name Name) bool {
	switch kind {
	case protocol.KindClaude:
		for _, installed := range Installed {
			if name == installed {
				return true
			}
		}
	case protocol.KindCodex:
		return name == NameStop
	}
	return false
}
