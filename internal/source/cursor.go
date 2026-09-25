package source

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lenajeremy/agentman/internal/jsonl"
	"github.com/lenajeremy/agentman/internal/parser"
	"github.com/lenajeremy/agentman/internal/protocol"
)

// Cursor keeps no live session registry — unlike Claude Code there is no
// per-pid file to read. What it does have is a transcript at
// ~/.cursor/projects/<project>/agent-transcripts/<uuid>/<uuid>.jsonl, one
// directory per session holding exactly that session's file.
//
// Liveness is therefore inferred from recency alone: a transcript written
// within cursorLiveWindow counts as live. That is the same inference Codex
// makes, minus its process check — no Cursor process name identifies one
// session (the IDE is a single Electron app for every open project), so a
// process gate could only say "Cursor is running", never which transcript is
// live. A quit IDE leaves ghosts for up to one window, which discovery
// accepts the way Codex accepts its own.
//
// Busy/idle comes from the transcript tail: the latest complete line decides,
// a trailing turn_ended meaning the turn is over. The transcript carries no
// model identifier (model hints live in ~/.cursor/chats/*/store.db, which is
// deliberately not opened) and no question markers, so the transcript alone
// never reports waiting_input and the model stays empty. The IDE composer
// index (see cursor_state.go) upgrades this where available: exact
// timestamps, Cursor's own subtitle, and the blocking flag, which is the one
// path that reports waiting_input — with a nil question, since answering
// still has no channel.
//
// Delivery is read-only for now: without a managed-pty wrapper there is no
// input channel, and the app renders such sessions with the composer
// disabled. The separate Cursor Agent CLI adapter can control terminal chats
// started with `am cursor`, but CLI chats cannot resume IDE conversations.
const cursorLiveWindow = 30 * time.Minute

// cursorActivityScanBytes bounds the once-per-change search for the latest
// turn boundary. Transcripts stay small (tool results are not recorded), but
// a corrupt prefix must still not cost an unbounded scan every second.
const cursorActivityScanBytes int64 = 1024 * 1024

// maxCursorSessions caps how many live transcripts one sweep turns into
// sessions. Projects accumulate history forever; only the newest window can
// be live, so anything beyond this is either stale or pathological.
const maxCursorSessions = 200

// cursorNameChars is how much of the opening user query becomes the session
// label. Enough to tell three sessions in one project apart on a phone.
const cursorNameChars = 48

// CursorSource observes Cursor agent transcripts.
type CursorSource struct {
	home string
	// models is kept for interface symmetry with the other file-backed
	// adapters. Cursor transcripts name no model, so it only ever caches
	// misses — which still saves a re-read per sweep per session.
	models *modelCache

	cacheMu sync.Mutex
	cache   map[string]cursorCacheEntry

	// stateDBInfo and stateHeaders cache the IDE composer index; see
	// cursor_state.go. queryHeaders is injectable so tests can stub the
	// sqlite3 subprocess.
	stateMu      sync.Mutex
	stateDBInfo  os.FileInfo
	stateWALInfo os.FileInfo
	stateHeaders map[string]cursorHeader
	queryHeaders func(ctx context.Context, dbPath string) (map[string]cursorHeader, error)

	mu       sync.RWMutex
	sessions map[string]cursorSession
}

type cursorSession struct {
	meta       protocol.Session
	transcript string
}

type cursorCacheEntry struct {
	identity os.FileInfo

	activityVersion cursorFileVersion
	activityState   protocol.State
	activitySet     bool

	name    string
	nameSet bool
}

type cursorFileVersion struct {
	size    int64
	modTime time.Time
}

func cursorVersion(info os.FileInfo) cursorFileVersion {
	return cursorFileVersion{size: info.Size(), modTime: info.ModTime()}
}

func (v cursorFileVersion) matches(info os.FileInfo) bool {
	return v.size == info.Size() && v.modTime.Equal(info.ModTime())
}

// NewCursorSource creates an adapter rooted at the given home directory.
// Passing an empty string uses the current user's home.
func NewCursorSource(home string) (*CursorSource, error) {
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return nil, err
		}
	}
	return &CursorSource{
		home:     home,
		models:   newModelCache(),
		cache:    map[string]cursorCacheEntry{},
		sessions: map[string]cursorSession{},
	}, nil
}

// Kind implements Source.
func (s *CursorSource) Kind() protocol.Kind { return protocol.KindCursor }

func (s *CursorSource) projectsDir() string {
	return filepath.Join(s.home, ".cursor", "projects")
}

// decodeCursorProjectDir reverses Cursor's project encoding: the absolute
// path with its leading separator stripped and every remaining separator
// replaced by '-'. The mapping is lossy — a literal '-' in a directory name
// decodes as a separator — so the result is a best-effort label and cwd,
// the same class of ambiguity as Claude's project slugs.
func decodeCursorProjectDir(name string) string {
	return string(filepath.Separator) + strings.ReplaceAll(name, "-", string(filepath.Separator))
}

// Discover implements Source.
func (s *CursorSource) Discover(ctx context.Context) ([]protocol.Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	projects, err := os.ReadDir(s.projectsDir())
	if err != nil {
		// Cursor simply is not installed, or has never run. Not an error.
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	type candidate struct {
		sessionID  string
		cwd        string
		transcript string
		dirInfo    os.FileInfo
		fileInfo   os.FileInfo
	}
	var candidates []candidate
	now := time.Now()
	cutoff := now.Add(-cursorLiveWindow)

	for _, project := range projects {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// ReadDir never returns "." or "..", and a single entry cannot
		// hold a separator, so joining stays inside projectsDir. Hidden
		// entries (".DS_Store" and friends) are skipped outright.
		if !project.IsDir() || strings.HasPrefix(project.Name(), ".") {
			continue
		}
		cwd := decodeCursorProjectDir(project.Name())
		transcriptsDir := filepath.Join(s.projectsDir(), project.Name(), "agent-transcripts")
		sessions, err := os.ReadDir(transcriptsDir)
		if err != nil {
			continue // no agent runs in this project yet
		}
		for _, entry := range sessions {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if !entry.IsDir() || entry.Name() == "" {
				continue
			}
			sessionID := entry.Name()
			transcript := filepath.Join(transcriptsDir, sessionID, sessionID+".jsonl")
			info, err := os.Stat(transcript)
			// The session directory must hold exactly its own transcript
			// as a regular file. Anything else — a stray directory, a
			// symlink, a half-written name — is skipped rather than read.
			if err != nil || !info.Mode().IsRegular() || info.ModTime().Before(cutoff) {
				continue
			}
			dirInfo, err := entry.Info()
			if err != nil {
				continue
			}
			candidates = append(candidates, candidate{
				sessionID:  sessionID,
				cwd:        cwd,
				transcript: transcript,
				dirInfo:    dirInfo,
				fileInfo:   info,
			})
		}
	}

	// Newest first, so the cap below keeps the sessions the user most
	// likely has open rather than an arbitrary readdir prefix.
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].fileInfo.ModTime().After(candidates[j].fileInfo.ModTime())
	})
	if len(candidates) > maxCursorSessions {
		candidates = candidates[:maxCursorSessions]
	}

	found := make([]protocol.Session, 0, len(candidates))
	next := make(map[string]cursorSession, len(candidates))
	live := make(map[string]bool, len(candidates))
	livePaths := make(map[string]bool, len(candidates))

	// One index read per sweep, merged into every session below. A nil map
	// means the IDE database is absent or unreadable, and discovery stays
	// transcript-only.
	headers := s.cursorHeaders(ctx)
	cliChats := cursorCLIChatIDs(s.home)

	for _, c := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// The CLI also writes observe transcripts into this directory, but
		// those conversations belong to its separate chat store. Presenting
		// them here would duplicate one CLI chat as a read-only IDE chat.
		if cliChats[c.sessionID] {
			continue
		}
		header, enriched := headers[c.sessionID]
		if enriched && header.archived {
			// Hidden in the IDE. Transcript recency alone must not
			// resurrect a session the user deliberately archived.
			continue
		}

		id := string(protocol.KindCursor) + ":" + c.sessionID
		state := s.cachedCursorActivity(ctx, c.transcript, c.fileInfo)
		cwd := c.cwd
		name := s.cachedCursorName(c.transcript)
		startedAt := c.dirInfo.ModTime().UnixMilli()
		lastActivityAt := c.fileInfo.ModTime().UnixMilli()
		if enriched {
			// The index carries exact values where transcripts only have
			// approximations: the real workspace path, Cursor's own
			// subtitle, and millisecond timestamps.
			if header.cwd != "" {
				cwd = header.cwd
			}
			if header.subtitle != "" {
				name = clipRunes(header.subtitle, cursorHeaderNameChars)
			}
			if header.createdAt > 0 {
				startedAt = header.createdAt
			}
			if header.updatedAt > 0 {
				lastActivityAt = header.updatedAt
			}
			if header.blocking {
				// The transcript never records this: the agent is
				// waiting on the user in the IDE. Question stays nil —
				// there is no answer channel yet — which the app
				// already renders as an unanswerable blocked banner
				// rather than a dead answer card.
				state = protocol.StateWaitingInput
			}
		}
		if name == "" {
			name = filepath.Base(cwd)
		}

		session := protocol.Session{
			ID:       id,
			Kind:     protocol.KindCursor,
			NativeID: c.sessionID,
			Name:     name,
			Cwd:      cwd,
			State:    state,
			// Read-only: no Injector, so the registry reports InjectNone
			// and the app disables the composer. See the package note.
			Inject:         protocol.InjectNone,
			StartedAt:      startedAt,
			LastActivityAt: lastActivityAt,
		}
		if model, cached := s.models.get(id); cached {
			session.Model = model
		} else {
			// Transcripts name no model; remembering the miss keeps the
			// next sweep from looking again.
			s.models.put(id, "")
		}

		found = append(found, session)
		next[id] = cursorSession{meta: session, transcript: c.transcript}
		live[id] = true
		livePaths[c.transcript] = true
	}

	// Newest first, so the caller's ordering does not depend on readdir order.
	sort.Slice(found, func(i, j int) bool {
		return found[i].LastActivityAt > found[j].LastActivityAt
	})

	s.models.forget(live)
	s.forgetCursorPaths(livePaths)

	s.mu.Lock()
	s.sessions = next
	s.mu.Unlock()

	return found, nil
}

// cachedCursorActivity reuses the last tail scan while size and mtime match.
// Any append invalidates it; an atomic replacement changes identity and
// correctly forces a fresh scan.
func (s *CursorSource) cachedCursorActivity(ctx context.Context, path string, info os.FileInfo) protocol.State {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()

	entry := s.cursorEntryLocked(path, info)
	if entry.activitySet && entry.activityVersion.matches(info) {
		return entry.activityState
	}
	state := cursorActivity(ctx, path)
	entry.activityVersion = cursorVersion(info)
	entry.activityState = state
	entry.activitySet = true
	s.cache[path] = entry
	return state
}

// cachedCursorName reads the opening user query once per underlying file.
// Transcripts are append-only, so the first record never changes; atomic
// replacement changes identity and correctly re-reads it.
func (s *CursorSource) cachedCursorName(path string) string {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()

	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	entry := s.cursorEntryLocked(path, info)
	if entry.nameSet {
		return entry.name
	}
	entry.name = parser.CursorSessionName(path)
	entry.nameSet = true
	s.cache[path] = entry
	return entry.name
}

func (s *CursorSource) cursorEntryLocked(path string, info os.FileInfo) cursorCacheEntry {
	if s.cache == nil {
		s.cache = map[string]cursorCacheEntry{}
	}
	if entry, ok := s.cache[path]; ok && entry.identity != nil && os.SameFile(entry.identity, info) {
		entry.identity = info
		return entry
	}
	return cursorCacheEntry{identity: info}
}

func (s *CursorSource) forgetCursorPaths(live map[string]bool) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	for path := range s.cache {
		if !live[path] {
			delete(s.cache, path)
		}
	}
}

// cursorActivity determines busy/idle by reading the tail for the most recent
// turn boundary. Without a registry or hooks this is the only signal
// available, and reading a bounded tail is cheap enough to do whenever the
// transcript changes.
func cursorActivity(ctx context.Context, path string) protocol.State {
	result, err := jsonl.CollectBackwardContext(ctx, path, jsonl.BackwardOptions{
		Want:         1,
		ChunkSize:    32 * 1024,
		MaxScanBytes: cursorActivityScanBytes,
		Map: func(line string, offset int64) []protocol.Message {
			state, ok := parser.CursorStateFromLine(line)
			if !ok {
				return nil
			}
			// Smuggle the state out through a throwaway message:
			// CollectBackward stops at the first match, which is the latest
			// transition.
			return []protocol.Message{{ID: string(state)}}
		},
	})
	if err != nil || len(result.Messages) != 1 {
		// Unreadable or indecisive transcripts read as idle rather than
		// failing discovery: one corrupt session must not hide the rest.
		return protocol.StateIdle
	}
	return protocol.State(result.Messages[0].ID)
}

// Page implements Source.
func (s *CursorSource) Page(ctx context.Context, sessionID, before string, limit int) (protocol.Page, error) {
	s.mu.RLock()
	session, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if !ok {
		return protocol.Page{}, fmt.Errorf("source: unknown cursor session %q", sessionID)
	}

	opts := jsonl.BackwardOptions{
		Want:         limit,
		Map:          parser.NewCursorParser(sessionID).Parse,
		MaxScanBytes: jsonl.DefaultScanBytes,
	}
	if before != "" {
		offset, err := strconv.ParseInt(before, 10, 64)
		if err != nil {
			return protocol.Page{}, fmt.Errorf("source: bad cursor %q: %w", before, err)
		}
		opts.Before = &offset
	}

	result, err := jsonl.CollectBackwardContext(ctx, session.transcript, opts)
	if err != nil {
		return protocol.Page{}, err
	}
	stampCursorTimes(session.transcript, result.Messages)

	cursor := ""
	if result.HasMore {
		cursor = strconv.FormatInt(result.NextCursor, 10)
	}
	return protocol.NewPage(sessionID, result.Messages, cursor, result.HasMore), nil
}

// stampCursorTimes fills in message timestamps from the transcript's mtime.
// Cursor records carry no per-line timestamps, so every message in a read
// shares the file's modification time; ordering still comes from file order,
// and the clock shown is the recency discovery already reports.
func stampCursorTimes(transcript string, messages []protocol.Message) {
	info, err := os.Stat(transcript)
	if err != nil {
		return
	}
	ts := info.ModTime().UnixMilli()
	for i := range messages {
		if messages[i].Ts == 0 {
			messages[i].Ts = ts
		}
	}
}

// Follow implements Source.
func (s *CursorSource) Follow(ctx context.Context, sessionID string, out chan<- []protocol.Message) error {
	s.mu.RLock()
	session, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("source: unknown cursor session %q", sessionID)
	}

	tail := jsonl.NewTail(session.transcript)
	if err := tail.SeekToEnd(); err != nil && !os.IsNotExist(err) {
		return err
	}
	p := parser.NewCursorParser(sessionID)

	ticker := time.NewTicker(followInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			lines, err := tail.Read()
			if err != nil {
				if os.IsNotExist(err) {
					continue
				}
				return err
			}
			var batch []protocol.Message
			for _, line := range lines {
				batch = append(batch, p.Parse(line.Text, line.Offset)...)
			}
			if len(batch) == 0 {
				continue
			}
			stampCursorTimes(session.transcript, batch)
			select {
			case out <- batch:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}
