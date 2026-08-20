package source

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lenajeremy/agentman/internal/jsonl"
	"github.com/lenajeremy/agentman/internal/parser"
	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/tmux"
)

// Codex keeps no live session registry — unlike Claude Code there is no
// per-pid file to read. What it does have is a dated rollout transcript at
// ~/.codex/sessions/YYYY/MM/DD/rollout-<ts>-<uuid>.jsonl whose first line
// carries the session id and cwd.
//
// Liveness therefore has to be inferred, from two signals together:
//
//   - at least one codex process is running, which rules out the whole class
//     of ghost sessions left behind after Codex exits; and
//   - the rollout was written within codexLiveWindow.
//
// Neither is sufficient alone — the process check cannot say *which* rollout
// is live, and recency alone keeps showing sessions long after Codex quits.
// This is the weakest part of Codex support and it is deliberately contained
// here. Codex's supported notifier reports turn completion but not a complete
// live-session registry, so polling still owns discovery and busy state.
const codexLiveWindow = 30 * time.Minute

// processCheckTimeout bounds the liveness probe so a wedged pgrep cannot stall
// a discovery sweep.
const processCheckTimeout = 2 * time.Second

// codexActivityScanBytes bounds the once-per-discovery search for a recent
// turn boundary. Large tool records may require finishing one bounded JSONL
// line beyond this budget, but a transcript containing no state events can no
// longer consume an unbounded amount of work every second.
const codexActivityScanBytes int64 = 1024 * 1024

// tmux reports session creation at whole-second precision while Codex records
// rollout timestamps with sub-second precision. Allow a small boundary margin,
// but never bind a clearly older conversation to a newly launched pane.
const codexPaneStartTolerance = 2 * time.Second

// codexRunning reports whether any codex process is alive.
//
// A missing pgrep (or any other failure) returns true rather than false: it is
// far better to show a session that has ended than to hide one that is running.
func codexRunning(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, processCheckTimeout)
	defer cancel()

	if _, err := exec.LookPath("pgrep"); err != nil {
		return true
	}
	err := exec.CommandContext(ctx, "pgrep", "-x", "codex").Run()
	if err == nil {
		return true
	}
	// pgrep exits 1 when nothing matched, which is a real negative. Any other
	// failure is inconclusive, so assume it is running.
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false
	}
	return true
}

// CodexSource observes Codex rollout transcripts.
type CodexSource struct {
	home string
	// processCheck is injectable so discovery can be tested without depending
	// on whether the machine running the tests happens to have Codex open.
	processCheck func(context.Context) bool
	// listPanes is injectable for the same reason as processCheck: tmux is a
	// machine-wide server, so without this a test with its own fake home would
	// still discover whatever panes happen to be open on the host.
	listPanes func(context.Context) ([]tmux.Session, error)
	// capturePane is injectable for the last-moment send safety check.
	capturePane func(context.Context, string) (string, error)
	// models remembers each session's model; see modelCache.
	models *modelCache
	// readMeta and readActivity are injectable for cache instrumentation tests.
	readMeta     func(string) (codexMeta, error)
	readActivity func(context.Context, string, time.Time) (protocol.State, int64, error)

	cacheMu      sync.Mutex
	rolloutCache map[string]codexRolloutCacheEntry

	mu       sync.RWMutex
	sessions map[string]codexSession
}

type codexSession struct {
	meta       protocol.Session
	transcript string
	// tmuxName is set for sessions launched through `am codex`, which is what
	// allows a message to be typed in mid-turn.
	tmuxName string
}

type codexFileVersion struct {
	size    int64
	modTime time.Time
}

func codexVersion(info os.FileInfo) codexFileVersion {
	return codexFileVersion{size: info.Size(), modTime: info.ModTime()}
}

func (v codexFileVersion) matches(info os.FileInfo) bool {
	return v.size == info.Size() && v.modTime.Equal(info.ModTime())
}

type codexRolloutCacheEntry struct {
	identity os.FileInfo

	meta    codexMeta
	metaSet bool

	activityVersion codexFileVersion
	activityState   protocol.State
	activitySet     bool
}

// codexMeta is the session_meta record that opens every rollout file.
type codexMeta struct {
	Type    string `json:"type"`
	Payload struct {
		SessionID  string `json:"session_id"`
		Cwd        string `json:"cwd"`
		Timestamp  string `json:"timestamp"`
		Originator string `json:"originator"`
	} `json:"payload"`
}

// NewCodexSource creates an adapter rooted at the given home directory.
// Passing an empty string uses the current user's home.
func NewCodexSource(home string) (*CodexSource, error) {
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return nil, err
		}
	}
	return &CodexSource{
		home:         home,
		processCheck: codexRunning,
		listPanes:    tmux.List,
		capturePane:  tmux.Capture,
		models:       newModelCache(),
		readMeta:     readCodexMeta,
		readActivity: codexActivity,
		rolloutCache: map[string]codexRolloutCacheEntry{},
		sessions:     map[string]codexSession{},
	}, nil
}

// Kind implements Source.
func (s *CodexSource) Kind() protocol.Kind { return protocol.KindCodex }

func (s *CodexSource) sessionsDir() string {
	return filepath.Join(s.home, ".codex", "sessions")
}

// Discover implements Source.
func (s *CodexSource) Discover(ctx context.Context) ([]protocol.Session, error) {
	if s.processCheck != nil && !s.processCheck(ctx) {
		s.mu.Lock()
		s.sessions = map[string]codexSession{}
		s.mu.Unlock()
		s.forgetCodexRollouts(nil)
		return nil, nil
	}

	// Rollouts are filed by date, so only today and yesterday can hold a live
	// session. Scanning those two directories keeps this cheap regardless of
	// how much history has accumulated.
	now := time.Now()
	dirs := []string{
		s.dayDir(now),
		s.dayDir(now.AddDate(0, 0, -1)),
	}

	found := []protocol.Session{}
	next := map[string]codexSession{}
	cutoff := now.Add(-codexLiveWindow)

	// Codex writes no pid to its rollout, so a session is matched to a tmux
	// pane by working directory. That is weaker than Claude's pid ancestry —
	// two Codex sessions in one directory would be ambiguous — so a directory
	// running more than one is left unmatched rather than risking delivery to
	// the wrong agent.
	var panes []tmux.Session
	if s.listPanes != nil {
		panes, _ = s.listPanes(ctx)
	}
	paneByCwd := map[string]tmux.Session{}
	ambiguous := map[string]bool{}
	for _, pane := range panes {
		if pane.Command != "codex" {
			continue
		}
		if _, seen := paneByCwd[pane.Cwd]; seen {
			ambiguous[pane.Cwd] = true
			continue
		}
		paneByCwd[pane.Cwd] = pane
	}
	// Panes still waiting for their first rollout, filled in below.
	unmatched := map[string]tmux.Session{}
	for cwd, pane := range paneByCwd {
		if !ambiguous[cwd] {
			unmatched[cwd] = pane
		}
	}

	// Rollouts are gathered before they are turned into sessions so they can be
	// walked newest first. Order decides which rollout gets to claim a tmux
	// pane, and only the newest one should: a directory accumulates a rollout
	// per Codex run, but the pane holds exactly one live session.
	type rollout struct {
		path    string
		modTime time.Time
		info    os.FileInfo
	}
	var rollouts []rollout
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue // no sessions that day, or Codex is not installed
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasPrefix(entry.Name(), "rollout-") {
				continue
			}
			info, err := entry.Info()
			if err != nil || info.ModTime().Before(cutoff) {
				continue
			}
			rollouts = append(rollouts, rollout{
				path:    filepath.Join(dir, entry.Name()),
				modTime: info.ModTime(),
				info:    info,
			})
		}
	}
	sort.Slice(rollouts, func(i, j int) bool {
		return rollouts[i].modTime.After(rollouts[j].modTime)
	})

	// Panes already spoken for. Without this, two rollouts in one directory
	// both key themselves on the same pane and the session is reported twice
	// under one id — which the app keys its rows by, so the row renders twice
	// with conflicting states and becomes unreachable.
	claimed := map[string]bool{}
	liveRollouts := make(map[string]bool, len(rollouts))

	{
		for _, entry := range rollouts {
			path := entry.path
			liveRollouts[path] = true
			meta, err := s.cachedCodexMeta(path, entry.info)
			if err != nil {
				continue
			}

			id := string(protocol.KindCodex) + ":" + meta.Payload.SessionID
			state, lastActivity, err := s.cachedCodexActivity(ctx, path, entry.info)
			if err != nil {
				return nil, err
			}

			tmuxName := ""
			inject := protocol.InjectNone
			pane, hasPane := paneByCwd[meta.Payload.Cwd]
			if hasPane && !ambiguous[meta.Payload.Cwd] && !claimed[pane.Name] &&
				codexRolloutCanClaimPane(meta, pane) {
				claimed[pane.Name] = true
				tmuxName = pane.Name
				inject = protocol.InjectTmux
				delete(unmatched, meta.Payload.Cwd)
				// Key a tmux-backed session on the pane rather than the
				// rollout. The pane exists from launch and the rollout does
				// not, so this keeps one stable id across the moment the
				// rollout appears — otherwise the session would vanish and
				// return under a new id on the user's first prompt.
				id = string(protocol.KindCodex) + ":" + tmuxID(pane.Name)
			}

			session := protocol.Session{
				ID:             id,
				Kind:           protocol.KindCodex,
				NativeID:       meta.Payload.SessionID,
				Name:           filepath.Base(meta.Payload.Cwd),
				Cwd:            meta.Payload.Cwd,
				State:          state,
				Inject:         inject,
				StartedAt:      parseCodexTime(meta.Payload.Timestamp),
				LastActivityAt: lastActivity,
			}
			if tmuxName != "" {
				if q := detectQuestion(ctx, tmuxName); q != nil {
					session.Question = q
					session.State = protocol.StateWaitingInput
				}
			}

			model, cached := s.models.get(id)
			if !cached {
				model = modelFromTranscript(path, codexModelOf)
				s.models.put(id, model)
			}
			session.Model = model

			found = append(found, session)
			next[id] = codexSession{meta: session, transcript: path, tmuxName: tmuxName}
		}
	}

	// A pane running codex that has written no rollout yet is still a real
	// session the user may need to reach — Codex writes nothing to disk until
	// its first turn, so without this a session sitting on its trust dialog,
	// or simply idle before the first prompt, is invisible to the phone.
	for cwd, pane := range unmatched {
		id := string(protocol.KindCodex) + ":" + tmuxID(pane.Name)
		// Timestamps come from tmux, not the clock. Using the current time
		// made every sweep look like a change, so an idle session emitted a
		// session_update every second — defeating the point of sending only
		// differences, and to a phone on cell data at that.
		started := pane.Created
		if started.IsZero() {
			started = now
		}
		session := protocol.Session{
			ID:             id,
			Kind:           protocol.KindCodex,
			NativeID:       pane.Name,
			Name:           filepath.Base(cwd),
			Cwd:            cwd,
			State:          protocol.StateIdle,
			Inject:         protocol.InjectTmux,
			StartedAt:      started.UnixMilli(),
			LastActivityAt: started.UnixMilli(),
		}
		if q := detectQuestion(ctx, pane.Name); q != nil {
			session.Question = q
			session.State = protocol.StateWaitingInput
		}
		found = append(found, session)
		// No transcript yet: Page returns an empty feed rather than failing.
		next[id] = codexSession{meta: session, transcript: "", tmuxName: pane.Name}
	}

	// Newest first, so the caller's ordering does not depend on readdir order.
	sort.Slice(found, func(i, j int) bool {
		return found[i].LastActivityAt > found[j].LastActivityAt
	})

	live := make(map[string]bool, len(next))
	for id := range next {
		live[id] = true
	}
	s.models.forget(live)
	s.forgetCodexRollouts(liveRollouts)

	s.mu.Lock()
	s.sessions = next
	s.mu.Unlock()

	return found, nil
}

// tmuxID derives a stable session identifier from a tmux session name.
func tmuxID(tmuxName string) string {
	return "tmux-" + tmuxName
}

func (s *CodexSource) dayDir(t time.Time) string {
	return filepath.Join(s.sessionsDir(),
		t.Format("2006"), t.Format("01"), t.Format("02"))
}

// cachedCodexMeta reads a rollout header once per path and underlying file.
// Appends change size and mtime but never the first record, so invalidating on
// every write would defeat the cache. Atomic path replacement changes file
// identity and correctly forces a fresh header read.
func (s *CodexSource) cachedCodexMeta(path string, info os.FileInfo) (codexMeta, error) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()

	entry := s.rolloutEntryLocked(path, info)
	if entry.metaSet {
		return entry.meta, nil
	}
	read := s.readMeta
	if read == nil {
		read = readCodexMeta
	}
	meta, err := read(path)
	if err != nil {
		return codexMeta{}, err
	}
	entry.meta = meta
	entry.metaSet = true
	s.rolloutCache[path] = entry
	return meta, nil
}

// cachedCodexActivity reuses the last state scan only while file identity,
// size, and modification time all match. Any append invalidates it; an atomic
// replacement invalidates both activity and immutable metadata.
func (s *CodexSource) cachedCodexActivity(
	ctx context.Context, path string, info os.FileInfo,
) (protocol.State, int64, error) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()

	entry := s.rolloutEntryLocked(path, info)
	if entry.activitySet && entry.activityVersion.matches(info) {
		return entry.activityState, info.ModTime().UnixMilli(), nil
	}
	read := s.readActivity
	if read == nil {
		read = codexActivity
	}
	state, lastActivity, err := read(ctx, path, info.ModTime())
	if err != nil {
		if ctx.Err() != nil {
			return protocol.StateIdle, info.ModTime().UnixMilli(), ctx.Err()
		}
		// A rollout can be caught between an append and a complete record. Keep
		// the last known state when possible and retry on the next sweep;
		// otherwise preserve the historical conservative idle fallback.
		if entry.activitySet {
			return entry.activityState, info.ModTime().UnixMilli(), nil
		}
		return protocol.StateIdle, info.ModTime().UnixMilli(), nil
	}
	entry.activityVersion = codexVersion(info)
	entry.activityState = state
	entry.activitySet = true
	s.rolloutCache[path] = entry
	return state, lastActivity, nil
}

func (s *CodexSource) rolloutEntryLocked(path string, info os.FileInfo) codexRolloutCacheEntry {
	if s.rolloutCache == nil {
		s.rolloutCache = map[string]codexRolloutCacheEntry{}
	}
	entry, ok := s.rolloutCache[path]
	if !ok || entry.identity == nil || !os.SameFile(entry.identity, info) {
		return codexRolloutCacheEntry{identity: info}
	}
	// Refresh the retained FileInfo snapshot. Identity is stable, while this
	// keeps diagnostics and future comparisons tied to the newest observation.
	entry.identity = info
	return entry
}

func (s *CodexSource) forgetCodexRollouts(live map[string]bool) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	for path := range s.rolloutCache {
		if !live[path] {
			delete(s.rolloutCache, path)
		}
	}
}

// readCodexMeta reads just the first line of a rollout, which is the
// session_meta record. Rollouts reach hundreds of megabytes, so this must
// never read the whole file.
func readCodexMeta(path string) (codexMeta, error) {
	f, err := os.Open(path)
	if err != nil {
		return codexMeta{}, err
	}
	defer f.Close()

	// The first record carries a full system prompt, so allow generous room
	// while still bounding the read.
	buf := make([]byte, 256*1024)
	n, err := f.Read(buf)
	if n == 0 && err != nil {
		return codexMeta{}, err
	}
	line, _, found := strings.Cut(string(buf[:n]), "\n")
	if !found {
		return codexMeta{}, fmt.Errorf("source: %s: no complete first line", path)
	}

	var meta codexMeta
	if err := json.Unmarshal([]byte(line), &meta); err != nil {
		return codexMeta{}, err
	}
	if meta.Type != "session_meta" || meta.Payload.SessionID == "" {
		return codexMeta{}, fmt.Errorf("source: %s: not a rollout header", path)
	}
	return meta, nil
}

// codexActivity determines busy/idle by reading the tail for the most recent
// turn boundary. Without hooks this is the only signal available, and reading
// a bounded tail is cheap enough to do whenever the rollout changes.
func codexActivity(ctx context.Context, path string, modTime time.Time) (protocol.State, int64, error) {
	state := protocol.StateIdle

	result, err := jsonl.CollectBackwardContext(ctx, path, jsonl.BackwardOptions{
		Want:         1,
		ChunkSize:    32 * 1024,
		MaxScanBytes: codexActivityScanBytes,
		Map: func(line string, offset int64) []protocol.Message {
			s, ok := parser.CodexStateFromLine(line)
			if !ok {
				return nil
			}
			// Smuggle the state out through a throwaway message: CollectBackward
			// stops at the first match, which is the latest transition.
			return []protocol.Message{{ID: string(s)}}
		},
	})
	if err != nil {
		if ctx.Err() != nil {
			return state, modTime.UnixMilli(), ctx.Err()
		}
		return state, modTime.UnixMilli(), err
	}
	if len(result.Messages) == 1 {
		state = protocol.State(result.Messages[0].ID)
	}
	return state, modTime.UnixMilli(), nil
}

func parseCodexTime(value string) int64 {
	if value == "" {
		return 0
	}
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return 0
	}
	return t.UnixMilli()
}

func codexRolloutCanClaimPane(meta codexMeta, pane tmux.Session) bool {
	if pane.Created.IsZero() {
		// Older tmux versions/configurations may not expose session_created. Keep
		// the previous newest-rollout behavior when there is no timing evidence.
		return true
	}
	started, err := time.Parse(time.RFC3339Nano, meta.Payload.Timestamp)
	if err != nil {
		// Working directory alone is ambiguous. When pane timing is available,
		// declining injection is safer than typing into the wrong conversation.
		return false
	}
	return !started.Before(pane.Created.Add(-codexPaneStartTolerance))
}

// Page implements Source.
func (s *CodexSource) Page(ctx context.Context, sessionID, before string, limit int) (protocol.Page, error) {
	s.mu.RLock()
	session, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if !ok {
		return protocol.Page{}, fmt.Errorf("source: unknown codex session %q", sessionID)
	}

	// A pane discovered before its first turn has no rollout to read.
	if session.transcript == "" {
		return protocol.NewPage(sessionID, nil, "", false), nil
	}

	opts := jsonl.BackwardOptions{
		Want:         limit,
		Map:          parser.NewCodexParser(sessionID).Parse,
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

	cursor := ""
	if result.HasMore {
		cursor = strconv.FormatInt(result.NextCursor, 10)
	}
	return protocol.NewPage(sessionID, result.Messages, cursor, result.HasMore), nil
}

// Follow implements Source.
func (s *CodexSource) Follow(ctx context.Context, sessionID string, out chan<- []protocol.Message) error {
	s.mu.RLock()
	session, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("source: unknown codex session %q", sessionID)
	}

	startedWithoutTranscript := session.transcript == ""
	if startedWithoutTranscript {
		// A wrapped Codex pane exists before its first rollout. Keep this same
		// subscription alive until discovery attaches the rollout to the stable
		// tmux-backed id; otherwise the app remains subscribed to a tail that can
		// never emit anything after the first prompt.
		wait := time.NewTicker(followInterval)
		defer wait.Stop()
		for session.transcript == "" {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-wait.C:
				s.mu.RLock()
				updated, stillLive := s.sessions[sessionID]
				s.mu.RUnlock()
				if !stillLive {
					return fmt.Errorf("source: codex session %q ended", sessionID)
				}
				session = updated
			}
		}
	}

	tail := jsonl.NewTail(session.transcript)
	if !startedWithoutTranscript {
		if err := tail.SeekToEnd(); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	p := parser.NewCodexParser(sessionID)

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
			select {
			case out <- batch:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}
