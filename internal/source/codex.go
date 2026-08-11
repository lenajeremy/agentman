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
// here: hooks (Phase 2) replace the guess with an exact signal.
const codexLiveWindow = 30 * time.Minute

// processCheckTimeout bounds the liveness probe so a wedged pgrep cannot stall
// a discovery sweep.
const processCheckTimeout = 2 * time.Second

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
	// pending holds messages for sessions with no live input channel.
	pending *PendingQueue
	// listPanes is injectable for the same reason as processCheck: tmux is a
	// machine-wide server, so without this a test with its own fake home would
	// still discover whatever panes happen to be open on the host.
	listPanes func(context.Context) ([]tmux.Session, error)
	// models remembers each session's model; see modelCache.
	models *modelCache

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
		models:       newModelCache(),
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

			path := filepath.Join(dir, entry.Name())
			meta, err := readCodexMeta(path)
			if err != nil {
				continue
			}

			id := string(protocol.KindCodex) + ":" + meta.Payload.SessionID
			state, lastActivity := codexActivity(path, info.ModTime())

			tmuxName := ""
			inject := protocol.InjectHook
			if pane, ok := paneByCwd[meta.Payload.Cwd]; ok && !ambiguous[meta.Payload.Cwd] {
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
// a few KB from the end is cheap enough to do on every discovery sweep.
func codexActivity(path string, modTime time.Time) (protocol.State, int64) {
	state := protocol.StateIdle

	result, err := jsonl.CollectBackward(path, jsonl.BackwardOptions{
		Want:      1,
		ChunkSize: 32 * 1024,
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
	if err == nil && len(result.Messages) == 1 {
		state = protocol.State(result.Messages[0].ID)
	}
	return state, modTime.UnixMilli()
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
		Want: limit,
		Map:  parser.NewCodexParser(sessionID).Parse,
	}
	if before != "" {
		offset, err := strconv.ParseInt(before, 10, 64)
		if err != nil {
			return protocol.Page{}, fmt.Errorf("source: bad cursor %q: %w", before, err)
		}
		opts.Before = &offset
	}

	result, err := jsonl.CollectBackward(session.transcript, opts)
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

	if session.transcript == "" {
		// Nothing to follow yet; discovery will re-key this session with a
		// transcript once the first turn writes one.
		<-ctx.Done()
		return ctx.Err()
	}

	tail := jsonl.NewTail(session.transcript)
	if err := tail.SeekToEnd(); err != nil && !os.IsNotExist(err) {
		return err
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
