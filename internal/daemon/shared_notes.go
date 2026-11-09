package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lenajeremy/agentman/internal/knowledge"
	"github.com/lenajeremy/agentman/internal/protocol"
)

// The shared notes the user marked for every agent. Most agents read a file of
// their own on every turn, so writing it is all it takes. Muse and Cursor read
// theirs only when a session opens, and Cursor's lives in each project, so this
// also remembers which notes each of those had on disk, and since when, for
// the sessions already running then (see shared_notes_deliver.go).

const (
	// notOnDisk is the At of a note Agentman couldn't put where a session
	// would load it, so no session can be assumed to have it.
	notOnDisk = int64(math.MaxInt64)
	// sharedNotesRemembered is how long a removed note, and a session not
	// seen, stays on record.
	sharedNotesRemembered = 30 * 24 * time.Hour
	museNotesTarget       = "muse"
	cursorNotesPrefix     = "cursor:"
)

type noteOnDisk struct {
	Hash  string `json:"hash"`
	Title string `json:"title"`
	// At is when this version went on disk. A session opened since has it.
	At int64 `json:"at"`
	// RemovedAt is when it came off, or zero.
	RemovedAt int64 `json:"removedAt,omitempty"`
}

type sharedNotesState struct {
	// Targets is, for Muse and for each folder Cursor works in, the notes
	// there by name.
	Targets map[string]map[string]noteOnDisk `json:"targets"`
	// Sessions is what each running session has been told.
	Sessions map[string]*notesSession `json:"sessions"`
}

type sharedNotes struct {
	store *knowledge.Store
	homes knowledge.Homes
	path  string
	now   func() time.Time

	mu      sync.Mutex
	state   sharedNotesState
	syncing bool
	again   bool
	// wg is for tests, which wait for a sync to finish.
	wg sync.WaitGroup
}

func newSharedNotes(store *knowledge.Store, homes knowledge.Homes, path string) *sharedNotes {
	n := &sharedNotes{
		store: store, homes: homes, path: path, now: time.Now,
		state: sharedNotesState{Targets: map[string]map[string]noteOnDisk{}, Sessions: map[string]*notesSession{}},
	}
	if raw, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(raw, &n.state)
		if n.state.Targets == nil {
			n.state.Targets = map[string]map[string]noteOnDisk{}
		}
		if n.state.Sessions == nil {
			n.state.Sessions = map[string]*notesSession{}
		}
	}
	return n
}

// SetSharedNotes keeps the notes marked for every agent where each agent reads
// them, starting now and after every change, with what that takes kept in
// path.
func (d *Daemon) SetSharedNotes(store *knowledge.Store, homes knowledge.Homes, path string) {
	notes := newSharedNotes(store, homes, path)
	d.mu.Lock()
	d.sharedNotes = notes
	d.mu.Unlock()
	store.OnChange(notes.kick)
	notes.kick()
}

// observe notes the folders Cursor is working in, since each needs a copy.
func (n *sharedNotes) observe(found []protocol.Session) {
	if n == nil {
		return
	}
	added := false
	n.mu.Lock()
	for _, session := range found {
		if session.Kind != protocol.KindCursor && session.Kind != protocol.KindCursorCLI {
			continue
		}
		if !filepath.IsAbs(session.Cwd) || session.Cwd == n.homes.Home {
			continue
		}
		key := cursorNotesPrefix + session.Cwd
		if _, known := n.state.Targets[key]; !known {
			n.state.Targets[key] = map[string]noteOnDisk{}
			added = true
		}
	}
	n.mu.Unlock()
	if added {
		n.kick()
	}
}

// kick brings the files up to date, in the background: never on the sweep or
// on the save of a note. One already running goes round again.
func (n *sharedNotes) kick() {
	n.mu.Lock()
	if n.syncing {
		n.again = true
		n.mu.Unlock()
		return
	}
	n.syncing = true
	n.wg.Add(1)
	n.mu.Unlock()
	go func() {
		defer n.wg.Done()
		for {
			n.sync()
			n.mu.Lock()
			if !n.again {
				n.syncing = false
				n.mu.Unlock()
				return
			}
			n.again = false
			n.mu.Unlock()
		}
	}()
}

func (n *sharedNotes) sync() {
	always, err := n.store.Always()
	if err != nil {
		Logf("shared notes: %v", err)
		return
	}
	results, err := n.store.SyncPersonal(n.homes)
	if err != nil {
		Logf("shared notes: %v", err)
		return
	}
	museOnDisk := false
	for _, result := range results {
		if result.Err != nil {
			Logf("shared notes: %s: %v", result.Path, result.Err)
		}
		if result.Kind == "muse" {
			museOnDisk = result.Installed && result.Err == nil
		}
	}
	n.mu.Lock()
	folders := make([]string, 0, len(n.state.Targets))
	for key := range n.state.Targets {
		if folder, ok := strings.CutPrefix(key, cursorNotesPrefix); ok {
			folders = append(folders, folder)
		}
	}
	n.mu.Unlock()
	onDisk := make(map[string]bool, len(folders))
	gone := map[string]bool{}
	for _, folder := range folders {
		if _, err := os.Stat(folder); err != nil {
			gone[folder] = true
			continue
		}
		carried, err := n.store.SyncProject(folder)
		if err != nil {
			Logf("shared notes: %s: %v", folder, err)
		}
		onDisk[folder] = carried
	}

	now := n.now()
	n.mu.Lock()
	defer n.mu.Unlock()
	updateTarget(n.state.Targets, museNotesTarget, always, museOnDisk, now)
	for _, folder := range folders {
		if gone[folder] {
			delete(n.state.Targets, cursorNotesPrefix+folder)
			continue
		}
		updateTarget(n.state.Targets, cursorNotesPrefix+folder, always, onDisk[folder], now)
	}
	for id, session := range n.state.Sessions {
		if now.Sub(time.UnixMilli(session.Seen)) > sharedNotesRemembered {
			delete(n.state.Sessions, id)
		}
	}
	n.saveLocked()
}

func updateTarget(targets map[string]map[string]noteOnDisk, key string, always []knowledge.Note, onDisk bool, now time.Time) {
	notes := targets[key]
	if notes == nil {
		notes = map[string]noteOnDisk{}
		targets[key] = notes
	}
	at := notOnDisk
	if onDisk {
		at = now.UnixMilli()
	}
	live := make(map[string]bool, len(always))
	for _, note := range always {
		live[note.Name] = true
		hash := noteHash(note)
		kept, known := notes[note.Name]
		switch {
		case !known || kept.Hash != hash || kept.RemovedAt != 0:
			notes[note.Name] = noteOnDisk{Hash: hash, Title: note.Title, At: at}
		case onDisk != (kept.At != notOnDisk):
			kept.At = at
			notes[note.Name] = kept
		}
	}
	for name, kept := range notes {
		if live[name] {
			continue
		}
		if kept.RemovedAt == 0 {
			kept.RemovedAt = now.UnixMilli()
			notes[name] = kept
		} else if now.Sub(time.UnixMilli(kept.RemovedAt)) > sharedNotesRemembered {
			delete(notes, name)
		}
	}
}

func noteHash(note knowledge.Note) string {
	sum := sha256.Sum256([]byte(note.Title + "\n" + note.Body))
	return hex.EncodeToString(sum[:8])
}

func (n *sharedNotes) saveLocked() {
	if n.path == "" {
		return
	}
	raw, err := json.Marshal(n.state)
	if err != nil {
		return
	}
	if os.MkdirAll(filepath.Dir(n.path), 0o700) != nil {
		return
	}
	tmp := n.path + ".tmp"
	if os.WriteFile(tmp, raw, 0o600) == nil {
		_ = os.Rename(tmp, n.path)
	}
}

// notesSession is what one running session has been told by message.
type notesSession struct {
	// Delivered is the version of each note it was sent, by name.
	Delivered   map[string]string `json:"delivered,omitempty"`
	Compactions int               `json:"compactions,omitempty"`
	Seen        int64             `json:"seen"`
}
