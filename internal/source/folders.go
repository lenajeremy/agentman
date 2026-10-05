package source

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// folderIndexTTL is how long a scan of every agent's store is reused.
//
// The index is only ever read to draw a list of folders, which nobody watches
// change: a few seconds stale is invisible, and rescanning per keystroke of
// browsing is not. Opening a folder does not use this — that reads the
// adapters directly — so a session started ten seconds ago is never hidden
// behind it.
const folderIndexTTL = 20 * time.Second

// pastListingTTL is how long one directory's finished sessions are reused.
//
// Only the finished half is cached. Live sessions are merged in fresh on
// every call, so a session that started a second ago is never hidden behind
// this — and a session that ended weeks ago does not become more accurate for
// being re-read. Reopening the same folder, or the refetch that follows a
// reconnect, then costs nothing.
const pastListingTTL = 20 * time.Second

// pastListing is one directory's history as it was last read.
type pastListing struct {
	sessions []protocol.Session
	builtAt  time.Time
}

// FolderIndex answers how many sessions have run under a directory.
//
// It holds one entry per exact working directory. A folder's number is the sum
// of everything beneath it, computed on demand, because selecting a folder
// includes its subtree: a repository's bin/ or mobile/ is the same project as
// its root, and a count that excluded them would not match the list it leads
// to.
type FolderIndex struct {
	leaves []protocol.Folder
}

// Under totals every session recorded at or below dir.
func (x *FolderIndex) Under(dir string) protocol.Folder {
	total := protocol.Folder{Path: filepath.Clean(dir)}
	if x == nil {
		return total
	}
	for _, leaf := range x.leaves {
		if !underDirectory(leaf.Path, dir) {
			continue
		}
		total.Agents += leaf.Agents
		total.Running += leaf.Running
		if leaf.LastActivityAt > total.LastActivityAt {
			total.LastActivityAt = leaf.LastActivityAt
		}
	}
	return total
}

// Recent lists the directories agents actually ran in, most recent first.
//
// These are real working directories rather than anything browsed to, which
// is what makes them the only route to a folder the browser cannot reach: it
// skips dot-directories and cannot leave the Mac user's home, and plenty of
// work happens in both.
func (x *FolderIndex) Recent(limit int) []protocol.Folder {
	if x == nil {
		return nil
	}
	folders := append([]protocol.Folder(nil), x.leaves...)
	sort.Slice(folders, func(i, j int) bool {
		if folders[i].Running != folders[j].Running {
			return folders[i].Running > folders[j].Running
		}
		if folders[i].LastActivityAt != folders[j].LastActivityAt {
			return folders[i].LastActivityAt > folders[j].LastActivityAt
		}
		return folders[i].Path < folders[j].Path
	})
	if limit > 0 && len(folders) > limit {
		folders = folders[:limit]
	}
	return folders
}

// Folders builds, or reuses, the index of every directory agents have run in.
func (r *Registry) Folders(ctx context.Context) (*FolderIndex, error) {
	r.folderMu.Lock()
	if r.folderIndex != nil && time.Since(r.folderBuiltAt) < folderIndexTTL {
		index := r.folderIndex
		r.folderMu.Unlock()
		return index, nil
	}
	r.folderMu.Unlock()

	histories := r.histories()
	var (
		mu       sync.Mutex
		merged   = map[string]*protocol.Folder{}
		failures []string
		wg       sync.WaitGroup
	)
	for kind, h := range histories {
		wg.Add(1)
		go func(kind protocol.Kind, h History) {
			defer wg.Done()
			folders, err := h.Directories(ctx)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failures = append(failures, fmt.Sprintf("%s: %v", kind, err))
			}
			for _, folder := range folders {
				if folder.Path == "" {
					continue
				}
				into := merged[folder.Path]
				if into == nil {
					into = &protocol.Folder{Path: folder.Path}
					merged[folder.Path] = into
				}
				into.Agents += folder.Agents
				if folder.LastActivityAt > into.LastActivityAt {
					into.LastActivityAt = folder.LastActivityAt
				}
			}
		}(kind, h)
	}
	wg.Wait()

	// A live session is counted even when its agent keeps no store we can
	// read, so a folder never reports zero while something is running in it.
	for _, session := range r.lastKnown() {
		path := filepath.Clean(session.Cwd)
		if path == "" || path == "." {
			continue
		}
		into := merged[path]
		if into == nil {
			into = &protocol.Folder{Path: path, Agents: 1}
			merged[path] = into
		}
		into.Running++
		if session.LastActivityAt > into.LastActivityAt {
			into.LastActivityAt = session.LastActivityAt
		}
	}

	index := &FolderIndex{leaves: make([]protocol.Folder, 0, len(merged))}
	for _, folder := range merged {
		index.leaves = append(index.leaves, *folder)
	}

	r.folderMu.Lock()
	r.folderIndex = index
	r.folderBuiltAt = time.Now()
	r.folderMu.Unlock()

	if len(failures) > 0 {
		sort.Strings(failures)
		return index, fmt.Errorf("source: %s", strings.Join(failures, "; "))
	}
	return index, nil
}

// InDirectory returns every session recorded at or below dir, live and ended.
//
// The live reading wins whenever both describe one session: Past finds a
// transcript on disk and cannot tell that its process is still running, while
// Discover knows the state, the model and whether it can be typed into.
func (r *Registry) InDirectory(ctx context.Context, dir string, limit int) ([]protocol.Session, error) {
	dir = filepath.Clean(dir)
	if dir == "" || dir == "." {
		return nil, fmt.Errorf("source: a directory is required")
	}
	limit = limitOrDefault(limit)

	past, failures := r.pastIn(ctx, dir, limit)

	live := map[string]bool{}
	// A live session is not always published under the id its transcript
	// has: Kiro, Codex and Antigravity key a session running in an Agentman
	// pane on the pane, because the pane exists before the agent has chosen
	// an id. The agent's own id is what the two readings share.
	liveNative := map[nativeSession]bool{}
	all := make([]protocol.Session, 0, len(past))
	for _, session := range r.lastKnown() {
		if underDirectory(session.Cwd, dir) {
			live[session.ID] = true
			if session.NativeID != "" {
				liveNative[nativeSession{session.Kind, session.NativeID}] = true
			}
			all = append(all, session)
		}
	}
	for _, session := range past {
		if live[session.ID] ||
			(session.NativeID != "" && liveNative[nativeSession{session.Kind, session.NativeID}]) {
			continue
		}
		all = append(all, session)
	}

	SortSessions(all)
	if len(all) > limit {
		all = all[:limit]
	}
	if len(failures) > 0 {
		sort.Strings(failures)
		return all, fmt.Errorf("source: %s", strings.Join(failures, "; "))
	}
	return all, nil
}

// nativeSession is one conversation as its agent names it.
type nativeSession struct {
	kind     protocol.Kind
	nativeID string
}

// pastIn reads one directory's finished sessions, reusing a recent read.
func (r *Registry) pastIn(ctx context.Context, dir string, limit int) ([]protocol.Session, []string) {
	key := fmt.Sprintf("%s\x00%d", dir, limit)
	r.folderMu.Lock()
	if cached, ok := r.pastByDir[key]; ok && time.Since(cached.builtAt) < pastListingTTL {
		sessions := cached.sessions
		r.folderMu.Unlock()
		return sessions, nil
	}
	r.folderMu.Unlock()

	var (
		mu       sync.Mutex
		past     []protocol.Session
		failures []string
		wg       sync.WaitGroup
	)
	for kind, h := range r.histories() {
		wg.Add(1)
		go func(kind protocol.Kind, h History) {
			defer wg.Done()
			sessions, err := h.Past(ctx, dir, limit)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failures = append(failures, fmt.Sprintf("%s: %v", kind, err))
			}
			past = append(past, sessions...)
		}(kind, h)
	}
	wg.Wait()

	// A partial read is not cached: the next open should try the adapter
	// that failed rather than repeat its absence for twenty seconds.
	if len(failures) == 0 {
		r.folderMu.Lock()
		if r.pastByDir == nil {
			r.pastByDir = map[string]pastListing{}
		}
		r.pastByDir[key] = pastListing{sessions: past, builtAt: time.Now()}
		r.folderMu.Unlock()
	}
	return past, failures
}

// histories returns the adapters that can look past what is running.
func (r *Registry) histories() map[protocol.Kind]History {
	r.mu.RLock()
	defer r.mu.RUnlock()
	found := make(map[protocol.Kind]History, len(r.sources))
	for kind, s := range r.sources {
		if h, ok := s.(History); ok {
			found[kind] = h
		}
	}
	return found
}

// lastKnown is the most recent successful snapshot from every adapter.
//
// Reusing it rather than sweeping again is deliberate: the daemon polls
// discovery on a timer anyway, so this is at most one interval old, and
// browsing folders must not make every agent's CLI re-scanned on each tap.
func (r *Registry) lastKnown() []protocol.Session {
	r.mu.RLock()
	defer r.mu.RUnlock()
	all := make([]protocol.Session, 0, len(r.last)*4)
	for _, sessions := range r.last {
		all = append(all, sessions...)
	}
	return all
}
