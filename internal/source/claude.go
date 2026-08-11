package source

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/lenajeremy/agentman/internal/jsonl"
	"github.com/lenajeremy/agentman/internal/parser"
	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/tmux"
)

const maxClaudeRegistryBytes = 1 << 20

// Claude Code maintains a live registry at ~/.claude/sessions/<pid>.json, one
// file per running session, carrying the pid, session id, cwd, a friendly
// name, and — usefully — a busy/idle status. Watching those files is both
// cheaper and faster than shelling out to `claude agents --json`, which reports
// the same set but costs a process spawn.
//
// None of this is a published API. Everything that touches the layout is in
// this file so a CLI upgrade breaks in one place.
type ClaudeSource struct {
	// models remembers each session's model so the transcript tail is not
	// re-read on every one-second discovery sweep.
	models *modelCache

	home string
	// pending holds messages for sessions with no live input channel, handed
	// over at the next Stop hook.
	pending *PendingQueue

	mu       sync.RWMutex
	sessions map[string]claudeSession
}

type claudeSession struct {
	meta       protocol.Session
	transcript string
	// tmuxName is set when this session was launched through `am claude`, and
	// is what makes mid-turn delivery possible. Empty means the session was
	// started normally and can only be reached through the hook queue.
	tmuxName string
	pid      int
}

// claudeSessionFile is the subset of ~/.claude/sessions/<pid>.json we read.
type claudeSessionFile struct {
	PID       int    `json:"pid"`
	SessionID string `json:"sessionId"`
	Cwd       string `json:"cwd"`
	StartedAt int64  `json:"startedAt"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Status    string `json:"status"`
	UpdatedAt int64  `json:"updatedAt"`
}

// NewClaudeSource creates an adapter rooted at the given home directory.
// Passing an empty string uses the current user's home.
func NewClaudeSource(home string) (*ClaudeSource, error) {
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return nil, err
		}
	}
	return &ClaudeSource{
		home:     home,
		models:   newModelCache(),
		sessions: map[string]claudeSession{},
	}, nil
}

// Kind implements Source.
func (s *ClaudeSource) Kind() protocol.Kind { return protocol.KindClaude }

func (s *ClaudeSource) sessionsDir() string {
	return filepath.Join(s.home, ".claude", "sessions")
}

func (s *ClaudeSource) projectsDir() string {
	return filepath.Join(s.home, ".claude", "projects")
}

// Discover implements Source.
func (s *ClaudeSource) Discover(ctx context.Context) ([]protocol.Session, error) {
	entries, err := os.ReadDir(s.sessionsDir())
	if err != nil {
		// Claude Code simply is not installed, or has never run. Not an error.
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	found := make([]protocol.Session, 0, len(entries))
	next := make(map[string]claudeSession, len(entries))

	// One tmux lookup per sweep, not per session: the process-ancestry walk
	// below is what maps an agent to the tmux session that can type into it.
	panes, _ := tmux.List(ctx)

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		raw, err := readBoundedFile(filepath.Join(s.sessionsDir(), entry.Name()), maxClaudeRegistryBytes)
		if err != nil {
			continue // the session ended mid-read; it will be gone next sweep
		}

		var file claudeSessionFile
		if json.Unmarshal(raw, &file) != nil || !validClaudeSessionID(file.SessionID) {
			continue
		}
		// A registry file outlives a crashed session, so the pid is the real
		// source of truth for whether this session still exists.
		if !processAlive(file.PID) {
			continue
		}

		id := string(protocol.KindClaude) + ":" + file.SessionID

		// A session launched through the wrapper can be typed into at any
		// time, including mid-turn. Everything else falls back to the hook
		// queue, which only delivers between turns and can be dropped.
		tmuxName := ""
		inject := protocol.InjectHook
		for _, pane := range panes {
			if tmux.OwnsPID(pane.PanePID, file.PID) {
				tmuxName = pane.Name
				inject = protocol.InjectTmux
				break
			}
		}

		meta := protocol.Session{
			ID:             id,
			Kind:           protocol.KindClaude,
			NativeID:       file.SessionID,
			Name:           claudeName(file),
			Cwd:            file.Cwd,
			State:          claudeState(file.Status),
			Inject:         inject,
			StartedAt:      file.StartedAt,
			LastActivityAt: file.UpdatedAt,
		}

		// The registry reports a session blocked on a permission prompt as
		// "idle", and no hook fires for one — so a pending question is only
		// visible by reading the terminal. Finding one overrides the state,
		// because "waiting on you" is the truth and "idle" is not.
		if tmuxName != "" {
			if q := detectQuestion(ctx, tmuxName); q != nil {
				meta.Question = q
				meta.State = protocol.StateWaitingInput
			}
		}
		if meta.LastActivityAt == 0 {
			meta.LastActivityAt = meta.StartedAt
		}

		transcript := s.transcriptPath(file.Cwd, file.SessionID)
		model, cached := s.models.get(id)
		if !cached {
			model = modelFromTranscript(transcript, claudeModelOf)
			s.models.put(id, model)
		}
		meta.Model = model

		found = append(found, meta)
		next[id] = claudeSession{
			meta:       meta,
			transcript: transcript,
			tmuxName:   tmuxName,
			pid:        file.PID,
		}
	}

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

func readBoundedFile(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var body bytes.Buffer
	if _, err := body.ReadFrom(io.LimitReader(file, limit+1)); err != nil {
		return nil, err
	}
	if int64(body.Len()) > limit {
		return nil, fmt.Errorf("source: %s exceeds %d bytes", path, limit)
	}
	return body.Bytes(), nil
}

func validClaudeSessionID(id string) bool {
	if id == "" || len(id) > 256 {
		return false
	}
	for _, r := range id {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') &&
			(r < '0' || r > '9') && r != '-' && r != '_' {
			return false
		}
	}
	return true
}

// transcriptPath resolves a session's JSONL file.
//
// Claude derives the directory by replacing both '/' and '.' in the cwd with
// '-', so /Users/me/.config/app becomes -Users-me--config-app. We compute that
// directly, then fall back to scanning for the session id — the rule is
// undocumented, and a wrong guess would silently show an empty feed.
func (s *ClaudeSource) transcriptPath(cwd, sessionID string) string {
	slug := strings.NewReplacer("/", "-", ".", "-").Replace(cwd)
	direct := filepath.Join(s.projectsDir(), slug, sessionID+".jsonl")
	if _, err := os.Stat(direct); err == nil {
		return direct
	}

	matches, err := filepath.Glob(filepath.Join(s.projectsDir(), "*", sessionID+".jsonl"))
	if err == nil && len(matches) > 0 {
		return matches[0]
	}
	// Return the computed path anyway: the session may be new enough that the
	// transcript has not been created yet, and it will appear at this path.
	return direct
}

func claudeName(file claudeSessionFile) string {
	if file.Name != "" {
		return file.Name
	}
	if base := filepath.Base(file.Cwd); base != "." && base != string(filepath.Separator) {
		return base
	}
	return "claude"
}

func claudeState(status string) protocol.State {
	switch status {
	case "busy":
		return protocol.StateBusy
	case "idle":
		return protocol.StateIdle
	default:
		// Older CLI versions omit the field; assume idle rather than inventing
		// activity the user would see as a spinning dot that never settles.
		return protocol.StateIdle
	}
}

// Page implements Source.
func (s *ClaudeSource) Page(ctx context.Context, sessionID, before string, limit int) (protocol.Page, error) {
	s.mu.RLock()
	session, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if !ok {
		return protocol.Page{}, fmt.Errorf("source: unknown claude session %q", sessionID)
	}

	opts := jsonl.BackwardOptions{
		Want: limit,
		// A fresh parser per page is correct: reading backwards, a tool result
		// is met before its call, so pairing resolves within the page.
		Map: parser.NewClaudeParser(sessionID).Parse,
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
		if os.IsNotExist(err) {
			// The session exists but has not written anything yet.
			return protocol.NewPage(sessionID, nil, "", false), nil
		}
		return protocol.Page{}, err
	}

	cursor := ""
	if result.HasMore {
		cursor = strconv.FormatInt(result.NextCursor, 10)
	}
	return protocol.NewPage(sessionID, result.Messages, cursor, result.HasMore), nil
}

// followInterval is how often a followed transcript is checked for new lines.
// Only the session the user currently has open is followed, so this is one
// stat call per tick, not one per session.
const followInterval = 250 * time.Millisecond

// Follow implements Source.
func (s *ClaudeSource) Follow(ctx context.Context, sessionID string, out chan<- []protocol.Message) error {
	s.mu.RLock()
	session, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("source: unknown claude session %q", sessionID)
	}

	tail := jsonl.NewTail(session.transcript)
	// Start at the end: the backlog belongs to Page, which the app calls
	// separately. Streaming it here would duplicate the whole history.
	if err := tail.SeekToEnd(); err != nil && !os.IsNotExist(err) {
		return err
	}

	// One parser for the lifetime of the follow, so a tool call recorded now
	// can be settled by a result that arrives seconds later.
	p := parser.NewClaudeParser(sessionID)

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
					continue // transcript not created yet
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

// processAlive reports whether a pid is still running.
//
// Signal 0 performs the permission and existence checks without delivering
// anything. EPERM means the process exists but belongs to another user, which
// still counts as alive.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	return err == nil || err == syscall.EPERM
}
