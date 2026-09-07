package source

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Registry fans discovery out across every adapter and routes per-session
// calls back to whichever adapter owns that session.
//
// Session IDs are prefixed with their kind ("claude:...", "codex:..."), so
// routing is a string split rather than a lookup table that could drift out of
// sync with what was discovered.
type Registry struct {
	mu      sync.RWMutex
	sources map[protocol.Kind]Source
	// last keeps the most recent successful snapshot per adapter. A transient
	// API/read error must not announce every session of that kind as ended and
	// tear down its live follows; the next successful empty snapshot is what
	// confirms they are genuinely gone.
	last map[protocol.Kind][]protocol.Session
}

// MaxPageMessages bounds a direct history request. The relay-facing daemon
// uses a smaller wire-oriented page size, while the terminal can reasonably
// print more at once. Keeping the source boundary finite prevents any caller
// from turning a user-controlled limit into a large allocation or API request.
const MaxPageMessages = 100

// ValidatePageLimit rejects invalid pagination before it reaches an adapter.
func ValidatePageLimit(limit int) error {
	if limit <= 0 || limit > MaxPageMessages {
		return fmt.Errorf("message limit must be between 1 and %d", MaxPageMessages)
	}
	return nil
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		sources: map[protocol.Kind]Source{},
		last:    map[protocol.Kind][]protocol.Session{},
	}
}

// Add registers an adapter.
func (r *Registry) Add(s Source) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sources[s.Kind()] = s
	delete(r.last, s.Kind())
}

// Kinds lists the registered adapter kinds, sorted for stable output.
func (r *Registry) Kinds() []protocol.Kind {
	r.mu.RLock()
	defer r.mu.RUnlock()
	kinds := make([]protocol.Kind, 0, len(r.sources))
	for kind := range r.sources {
		kinds = append(kinds, kind)
	}
	slices.Sort(kinds)
	return kinds
}

// Discover queries every adapter and returns the combined session list.
//
// One adapter failing must not blank the whole list — a broken Codex install
// should not hide running Claude sessions — so errors are collected and
// returned alongside whatever did succeed.
func (r *Registry) Discover(ctx context.Context) ([]protocol.Session, error) {
	r.mu.RLock()
	sources := make([]Source, 0, len(r.sources))
	for _, s := range r.sources {
		sources = append(sources, s)
	}
	r.mu.RUnlock()

	var (
		all      = make([][]protocol.Session, len(sources))
		nextSlot int
		failures []string
		wg       sync.WaitGroup
	)

	for _, s := range sources {
		wg.Add(1)
		go func(s Source) {
			defer wg.Done()
			sessions, err := s.Discover(ctx)
			if err != nil && sessions == nil {
				r.mu.RLock()
				sessions = append([]protocol.Session(nil), r.last[s.Kind()]...)
				r.mu.RUnlock()
			} else {
				// A non-nil slice alongside an error is an adapter-owned partial
				// snapshot. OpenCode uses this to merge the last known routes from
				// one failed local server with fresh data from the others.
				r.mu.Lock()
				r.last[s.Kind()] = append([]protocol.Session(nil), sessions...)
				r.mu.Unlock()
			}
			// Reserve a result slot up front so adapters never contend on one
			// large append lock while copying their session lists.
			slot := nextSlot
			nextSlot++
			if err != nil {
				failures = append(failures, fmt.Sprintf("%s: %v", s.Kind(), err))
			}
			all[slot] = sessions
		}(s)
	}
	wg.Wait()

	combined := make([]protocol.Session, 0)
	for _, sessions := range all {
		combined = append(combined, sessions...)
	}
	SortSessions(combined)

	if len(failures) > 0 {
		sort.Strings(failures)
		return combined, fmt.Errorf("source: %s", strings.Join(failures, "; "))
	}
	return combined, nil
}

// SortSessions puts the list in the order the app's agent screen relies on:
// sessions blocked on the user first (they are the ones going nowhere without
// attention), then busy ones, then everything else by recency.
func SortSessions(sessions []protocol.Session) {
	sort.SliceStable(sessions, func(i, j int) bool {
		if pi, pj := statePriority(sessions[i].State), statePriority(sessions[j].State); pi != pj {
			return pi < pj
		}
		return sessions[i].LastActivityAt > sessions[j].LastActivityAt
	})
}

func statePriority(s protocol.State) int {
	switch s {
	case protocol.StateWaitingInput:
		return 0
	case protocol.StateBusy:
		return 1
	case protocol.StateIdle:
		return 2
	default:
		return 3
	}
}

// Page routes a scrollback request to the owning adapter.
func (r *Registry) Page(ctx context.Context, sessionID, before string, limit int) (protocol.Page, error) {
	if err := ValidatePageLimit(limit); err != nil {
		return protocol.Page{}, err
	}
	s, err := r.forSession(sessionID)
	if err != nil {
		return protocol.Page{}, err
	}
	return s.Page(ctx, sessionID, before, limit)
}

// Follow routes a live-tail request to the owning adapter.
func (r *Registry) Follow(ctx context.Context, sessionID string, out chan<- []protocol.Message) error {
	s, err := r.forSession(sessionID)
	if err != nil {
		return err
	}
	return s.Follow(ctx, sessionID, out)
}

// Inject routes a message to the owning adapter, reporting InjectNone when
// that adapter has no delivery channel.
func (r *Registry) Inject(ctx context.Context, sessionID, text string) (protocol.InjectMode, error) {
	s, err := r.forSession(sessionID)
	if err != nil {
		return protocol.InjectNone, err
	}
	injector, ok := s.(Injector)
	if !ok {
		return protocol.InjectNone, fmt.Errorf("source: %s sessions cannot receive messages yet", s.Kind())
	}
	return injector.Inject(ctx, sessionID, text)
}

// Answerer is implemented by adapters that can resolve a pending question.
type Answerer interface {
	Answer(ctx context.Context, sessionID string, answer protocol.QuestionAnswer) error
}

// QuestionInspector is implemented by terminal adapters that can cheaply
// re-read one live pane. It lets a Stop hook distinguish a real completion
// from an agent that stopped only to ask the user something, without waiting
// for an unrelated slow adapter in the next whole-registry discovery sweep.
type QuestionInspector interface {
	CurrentQuestion(ctx context.Context, sessionID string) (*protocol.Question, error)
}

// Interrupter is implemented by adapters that can stop an active turn.
type Interrupter interface {
	Interrupt(ctx context.Context, sessionID string) error
}

// Answer routes a decision to the adapter owning the session.
func (r *Registry) Answer(ctx context.Context, sessionID string, answer protocol.QuestionAnswer) error {
	s, err := r.forSession(sessionID)
	if err != nil {
		return err
	}
	answerer, ok := s.(Answerer)
	if !ok {
		return fmt.Errorf("source: %s questions cannot be answered remotely", s.Kind())
	}
	return answerer.Answer(ctx, sessionID, answer)
}

// CurrentQuestion performs a targeted live check when the owning adapter
// supports one. A nil question with a nil error means the pane is not blocked.
func (r *Registry) CurrentQuestion(
	ctx context.Context,
	sessionID string,
) (*protocol.Question, error) {
	s, err := r.forSession(sessionID)
	if err != nil {
		return nil, err
	}
	inspector, ok := s.(QuestionInspector)
	if !ok {
		return nil, nil
	}
	return inspector.CurrentQuestion(ctx, sessionID)
}

// Interrupt routes cancellation to the adapter owning the session.
func (r *Registry) Interrupt(ctx context.Context, sessionID string) error {
	s, err := r.forSession(sessionID)
	if err != nil {
		return err
	}
	interrupter, ok := s.(Interrupter)
	if !ok {
		return fmt.Errorf("source: %s sessions cannot be interrupted remotely", s.Kind())
	}
	return interrupter.Interrupt(ctx, sessionID)
}

func (r *Registry) forSession(sessionID string) (Source, error) {
	kind, _, ok := strings.Cut(sessionID, ":")
	if !ok {
		return nil, fmt.Errorf("source: malformed session id %q", sessionID)
	}
	r.mu.RLock()
	s, exists := r.sources[protocol.Kind(kind)]
	r.mu.RUnlock()
	if !exists {
		return nil, fmt.Errorf("source: no adapter for %q", kind)
	}
	return s, nil
}

// EachSource calls fn for every registered adapter.
//
// Used to hand shared state (such as the pending-message queue) to whichever
// adapters support it, without the registry needing to know which those are.
func (r *Registry) EachSource(fn func(Source)) {
	r.mu.RLock()
	sources := make([]Source, 0, len(r.sources))
	for _, s := range r.sources {
		sources = append(sources, s)
	}
	r.mu.RUnlock()
	for _, s := range sources {
		fn(s)
	}
}
