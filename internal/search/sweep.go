package search

import (
	"context"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Reader is where conversations come from: the daemon's adapters.
type Reader interface {
	// Folders is every directory agents have run in.
	Folders(ctx context.Context) ([]protocol.Folder, error)
	// Past is every conversation that ran at or below dir, newest first.
	Past(ctx context.Context, dir string, limit int) ([]protocol.Session, error)
	// Live is the sessions running now.
	Live() []protocol.Session
	// Page reads a conversation backward from before, as the apps do.
	Page(ctx context.Context, sessionID, before string, limit int) (protocol.Page, error)
}

const (
	// pastLimit asks for every conversation in a folder. The folder list
	// asks for the newest 200, which is all a screen shows, but a search
	// that cannot find last month's work is not much of one.
	pastLimit = 100_000
	// pageSize is the most an adapter returns at once.
	pageSize = 100
	// liveBudget is how long a search waits for running conversations to
	// be read up to their newest message first.
	liveBudget = 1500 * time.Millisecond
)

// Indexer keeps an Index up to date with what the agents have said.
type Indexer struct {
	index  *Index
	reader Reader
	logf   func(format string, args ...any)

	// sweep is held for a whole sweep, so two never read at once.
	sweep sync.Mutex
	// swept is the last activity each folder had when it was last read, so
	// a sweep skips the folders nothing has happened in.
	swept map[string]int64
	// building is true while a sweep has conversations to read, which on the
	// first run means the whole history.
	building atomic.Bool
}

// NewIndexer reads conversations from reader into index.
func NewIndexer(index *Index, reader Reader, logf func(string, ...any)) *Indexer {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Indexer{index: index, reader: reader, logf: logf, swept: map[string]int64{}}
}

// Building reports whether conversations are still being read, so a search
// can say its answer may not be complete.
func (x *Indexer) Building() bool {
	return x.building.Load()
}

// Run sweeps after delay and then every interval until ctx ends.
func (x *Indexer) Run(ctx context.Context, delay, interval time.Duration) {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		started := time.Now()
		read, err := x.Sweep(ctx)
		if err != nil && ctx.Err() == nil {
			x.logf("search: %v", err)
		}
		if read > 0 {
			x.logf("search: read %d conversations in %s", read, time.Since(started).Round(time.Millisecond))
		}
		// The first sweep reads gigabytes of transcripts; the daemon runs
		// for weeks after it, and should not keep the memory that took.
		if read > 50 {
			debug.FreeOSMemory()
		}
		timer.Reset(interval)
	}
}

// Sweep reads every conversation that has changed since it was last read,
// newest first, and reports how many it read.
func (x *Indexer) Sweep(ctx context.Context) (int, error) {
	x.sweep.Lock()
	defer x.sweep.Unlock()

	folders, err := x.reader.Folders(ctx)
	if err != nil && len(folders) == 0 {
		return 0, err
	}
	var (
		sessions []protocol.Session
		roots    = rootFolders(folders)
		failures []string
	)
	for _, root := range roots {
		if last, ok := x.swept[root.Path]; ok && root.LastActivityAt <= last {
			continue
		}
		past, err := x.reader.Past(ctx, root.Path, pastLimit)
		if err != nil {
			failures = append(failures, err.Error())
		}
		sessions = append(sessions, past...)
	}
	sessions = append(sessions, x.reader.Live()...)

	read, err := x.readAll(ctx, sessions, true)
	if err != nil {
		return read, err
	}
	// Only now, so a sweep cut short reads these folders again.
	for _, root := range roots {
		x.swept[root.Path] = max(x.swept[root.Path], root.LastActivityAt)
	}
	if len(failures) > 0 {
		return read, &sweepError{failures}
	}
	return read, nil
}

type sweepError struct{ failures []string }

func (e *sweepError) Error() string { return strings.Join(e.failures, "; ") }

// Refresh reads the running conversations up to their newest message, for
// as long as budget allows, so a search finds what was said a moment ago.
func (x *Indexer) Refresh(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, liveBudget)
	defer cancel()
	if _, err := x.readAll(ctx, x.reader.Live(), false); err != nil && ctx.Err() == nil {
		x.logf("search: %v", err)
	}
}

// readAll reads the conversations that changed since they were last read,
// one reading of each, the most recent first. A sweep says while it reads;
// a search reading the running conversations first does not.
func (x *Indexer) readAll(ctx context.Context, sessions []protocol.Session, sweeping bool) (int, error) {
	// The ended reading of a conversation is the one to keep: its id names
	// the transcript, which outlasts the pane a live one may be named after.
	byKey := map[string]protocol.Session{}
	for _, session := range sessions {
		key := Key(session)
		if existing, ok := byKey[key]; ok {
			if existing.State == protocol.StateEnded {
				existing.LastActivityAt = max(existing.LastActivityAt, session.LastActivityAt)
				byKey[key] = existing
				continue
			}
			session.LastActivityAt = max(existing.LastActivityAt, session.LastActivityAt)
		}
		byKey[key] = session
	}
	pending := make([]protocol.Session, 0, len(byKey))
	for key, session := range byKey {
		indexed, err := x.index.indexed(ctx, key)
		if err != nil {
			return 0, err
		}
		if indexed == 0 || session.LastActivityAt > indexed {
			pending = append(pending, session)
		}
	}
	if len(pending) == 0 {
		return 0, nil
	}
	sort.Slice(pending, func(i, j int) bool { return pending[i].LastActivityAt > pending[j].LastActivityAt })

	if sweeping {
		x.building.Store(true)
		defer x.building.Store(false)
	}
	read := 0
	for _, session := range pending {
		if err := ctx.Err(); err != nil {
			return read, err
		}
		if err := x.read(ctx, session); err != nil {
			// One unreadable transcript should not stop the rest.
			x.logf("search: %s: %v", session.ID, err)
			continue
		}
		read++
	}
	return read, nil
}

// read stores what a conversation has said since it was last read.
//
// It pages backward from the newest message, as the apps scroll, and stops
// at the first message already stored. A conversation never read all the
// way (a sweep cut short, say) is read to its start, since what it already
// holds is its newest end, not its oldest.
func (x *Indexer) read(ctx context.Context, session protocol.Session) error {
	key := Key(session)
	indexed, err := x.index.indexed(ctx, key)
	if err != nil {
		return err
	}
	before := ""
	for {
		page, err := x.reader.Page(ctx, session.ID, before, pageSize)
		if err != nil {
			return err
		}
		var ids []string
		for _, message := range page.Messages {
			if indexable(message) {
				ids = append(ids, message.ID)
			}
		}
		known, err := x.index.known(ctx, key, ids)
		if err != nil {
			return err
		}
		var fresh []protocol.Message
		caughtUp := false
		for i := len(page.Messages) - 1; i >= 0; i-- {
			message := page.Messages[i]
			if !indexable(message) {
				continue
			}
			if known[message.ID] {
				if indexed > 0 {
					caughtUp = true
					break
				}
				continue
			}
			fresh = append(fresh, message)
		}
		if err := x.index.add(ctx, session, fresh); err != nil {
			return err
		}
		if caughtUp || !page.HasMore || page.NextCursor == "" || page.NextCursor == before {
			break
		}
		before = page.NextCursor
	}
	return x.index.done(ctx, key, max(session.LastActivityAt, 1))
}

// indexable reports whether a message is conversation: what someone typed
// or an agent answered, not a tool's work or a subagent's.
func indexable(message protocol.Message) bool {
	if message.Tool != nil || message.IsSidechain || strings.TrimSpace(message.Text) == "" {
		return false
	}
	return message.Role == protocol.RoleUser || message.Role == protocol.RoleAssistant
}

// rootFolders keeps the folders that are not inside another, each with the
// latest activity anywhere beneath it: a conversation's history is listed
// with its whole subtree, so reading the inner ones too would read it twice.
func rootFolders(folders []protocol.Folder) []protocol.Folder {
	sorted := append([]protocol.Folder(nil), folders...)
	for i := range sorted {
		sorted[i].Path = filepath.Clean(sorted[i].Path)
	}
	// The separator sorts first, so a folder's subfolders follow it straight
	// away, ahead of a sibling such as "app-old" after "app".
	byTree := func(path string) string { return strings.ReplaceAll(path, string(filepath.Separator), "\x00") }
	sort.Slice(sorted, func(i, j int) bool { return byTree(sorted[i].Path) < byTree(sorted[j].Path) })
	var roots []protocol.Folder
	for _, folder := range sorted {
		if folder.Path == "" || folder.Path == "." {
			continue
		}
		if n := len(roots); n > 0 && under(folder.Path, roots[n-1].Path) {
			roots[n-1].LastActivityAt = max(roots[n-1].LastActivityAt, folder.LastActivityAt)
			continue
		}
		roots = append(roots, protocol.Folder{Path: folder.Path, LastActivityAt: folder.LastActivityAt})
	}
	return roots
}

func under(path, dir string) bool {
	if dir == string(filepath.Separator) {
		return true
	}
	return path == dir || strings.HasPrefix(path, dir+string(filepath.Separator))
}

// Search answers a query, from what has been read plus the running
// conversations' newest messages. A conversation that is running is
// returned as it runs, so opening it opens the live session.
func (x *Indexer) Search(ctx context.Context, q Query) ([]Hit, error) {
	x.Refresh(ctx)
	hits, err := x.index.Search(ctx, q, time.Now())
	if err != nil {
		return nil, err
	}
	live := map[string]protocol.Session{}
	for _, session := range x.reader.Live() {
		live[Key(session)] = session
	}
	for i := range hits {
		if session, ok := live[hits[i].Key]; ok {
			hits[i].Session = session
		} else if hits[i].Session.State != protocol.StateEnded {
			// Read while it ran, and over since.
			hits[i].Session.State = protocol.StateEnded
			hits[i].Session.Inject = protocol.InjectNone
		}
	}
	return hits, nil
}

// Index is the index the indexer fills.
func (x *Indexer) Index() *Index {
	return x.index
}
