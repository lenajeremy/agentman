package hook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// State is what the daemon remembers between runs, in ~/.agentman/state.json.
type State struct {
	// LastFired records the last time each agent kind delivered a hook, in
	// epoch milliseconds.
	//
	// This is the difference between "hooks are configured" and "hooks work".
	// It matters most for Codex, whose config schema we could not verify: with
	// this, `am doctor` can say "registered, but never seen firing" instead of
	// reporting a green check that means nothing.
	LastFired map[protocol.Kind]int64 `json:"lastFired"`
}

// Store persists State, serializing writes across goroutines.
type Store struct {
	path string
	mu   sync.Mutex
}

// NewStore opens the state file under the given home directory.
func NewStore(home string) (*Store, error) {
	dir, err := ConfigDir(home)
	if err != nil {
		return nil, err
	}
	return &Store{path: filepath.Join(dir, "state.json")}, nil
}

// Load reads the state, returning an empty one when absent or unreadable.
// State is a convenience, never a source of truth, so a corrupt file is reset
// rather than reported as an error the user has to act on.
func (s *Store) Load() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked()
}

func (s *Store) loadLocked() State {
	state := State{LastFired: map[protocol.Kind]int64{}}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return state
	}
	var loaded State
	if json.Unmarshal(raw, &loaded) != nil || loaded.LastFired == nil {
		return state
	}
	return loaded
}

// RecordFired notes that an agent kind delivered a hook just now.
func (s *Store) RecordFired(kind protocol.Kind, at time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	state := s.loadLocked()
	state.LastFired[kind] = at.UnixMilli()

	body, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(s.path, body, 0o600)
}
