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
}

// NewRegistry creates an empty registry.
func NewRegistry() *Registry {
	return &Registry{sources: map[protocol.Kind]Source{}}
}

// Add registers an adapter.
func (r *Registry) Add(s Source) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sources[s.Kind()] = s
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
		mu       sync.Mutex
		all      []protocol.Session
		failures []string
		wg       sync.WaitGroup
	)

	for _, s := range sources {
		wg.Add(1)
		go func(s Source) {
			defer wg.Done()
			sessions, err := s.Discover(ctx)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failures = append(failures, fmt.Sprintf("%s: %v", s.Kind(), err))
				return
			}
			all = append(all, sessions...)
		}(s)
	}
	wg.Wait()

	SortSessions(all)

	if len(failures) > 0 {
		sort.Strings(failures)
		return all, fmt.Errorf("source: %s", strings.Join(failures, "; "))
	}
	return all, nil
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
