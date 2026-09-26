package source

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/lenajeremy/agentman/internal/parser"
	"github.com/lenajeremy/agentman/internal/protocol"
)

const cursorACPPrefix = "cursor-cli:acp:"

type cursorACPRecord struct {
	NativeID       string             `json:"nativeId"`
	Cwd            string             `json:"cwd"`
	Name           string             `json:"name,omitempty"`
	StartedAt      int64              `json:"startedAt"`
	LastActivityAt int64              `json:"lastActivityAt"`
	Messages       []protocol.Message `json:"messages"`
	Queued         []string           `json:"queued,omitempty"`
}

type cursorACPPending struct {
	requestID json.RawMessage
	kind      string
	options   []protocol.QuestionOption
	questions []cursorACPAskQuestion
	answers   []cursorACPAskAnswer
	index     int
}

type cursorACPAskQuestion struct {
	ID            string `json:"id"`
	Prompt        string `json:"prompt"`
	AllowMultiple bool   `json:"allowMultiple"`
	Options       []struct {
		ID    string `json:"id"`
		Label string `json:"label"`
	} `json:"options"`
}

type cursorACPAskAnswer struct {
	QuestionID        string   `json:"questionId"`
	SelectedOptionIDs []string `json:"selectedOptionIds"`
}

type cursorACPState struct {
	mu          sync.Mutex
	saveMu      sync.Mutex
	startMu     sync.Mutex
	record      cursorACPRecord
	client      *cursorACPClient
	lockFile    *os.File
	turnDone    chan struct{}
	busy        bool
	replaying   bool
	question    *protocol.Question
	pending     *cursorACPPending
	questionSeq uint64
	messageSeq  uint64
	assistantID string
	turnID      string
	subscribers map[chan struct{}]struct{}
	diskMod     time.Time
}

// CursorACPSource owns only Agentman-created ACP sessions. Existing interactive
// Cursor terminal chats remain in CursorCLISource, with their own store and IDs.
type CursorACPSource struct {
	dir      string
	async    bool
	closing  atomic.Bool
	mu       sync.RWMutex
	sessions map[string]*cursorACPState
}

func (s *CursorACPSource) EnableAsync() { s.async = true }

// Close prevents ACP children from outliving their daemon or CLI process.
func (s *CursorACPSource) Close() {
	s.closing.Store(true)
	s.mu.RLock()
	var clients []*cursorACPClient
	var turns []chan struct{}
	for _, st := range s.sessions {
		st.mu.Lock()
		if st.client != nil {
			clients = append(clients, st.client)
		}
		if st.turnDone != nil {
			turns = append(turns, st.turnDone)
		}
		st.mu.Unlock()
	}
	s.mu.RUnlock()
	for _, client := range clients {
		client.close()
	}
	for _, done := range turns {
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
	}
}

func (s *CursorACPSource) lockTurn(nativeID string) (*os.File, error) {
	path := filepath.Join(s.dir, nativeID+".lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, errors.New("Cursor session is active in another Agentman process")
	}
	return file, nil
}

func releaseCursorACPLock(file *os.File) {
	if file == nil {
		return
	}
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	_ = file.Close()
}

func (s *CursorACPSource) busyElsewhere(nativeID string) bool {
	file, err := os.OpenFile(filepath.Join(s.dir, nativeID+".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return false
	}
	defer file.Close()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return true
	}
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	return false
}

func NewCursorACPSource(dir string) (*CursorACPSource, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, err
	}
	s := &CursorACPSource{dir: dir, sessions: make(map[string]*cursorACPState)}
	if err := s.refreshRecords(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *CursorACPSource) refreshRecords() error {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(s.dir, entry.Name())
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() > cursorCLIMaxDBOutput {
			continue
		}
		nativeID := strings.TrimSuffix(entry.Name(), ".json")
		if !cursorACPValidID(nativeID) {
			continue
		}
		id := cursorACPPrefix + nativeID
		s.mu.RLock()
		st := s.sessions[id]
		s.mu.RUnlock()
		if st != nil {
			st.mu.Lock()
			unchanged := !info.ModTime().After(st.diskMod) || st.busy
			st.mu.Unlock()
			if unchanged {
				continue
			}
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var record cursorACPRecord
		if json.Unmarshal(data, &record) != nil || !cursorACPValidID(record.NativeID) ||
			entry.Name() != record.NativeID+".json" || !filepath.IsAbs(record.Cwd) {
			continue
		}
		if st != nil {
			st.mu.Lock()
			if !st.busy && info.ModTime().After(st.diskMod) {
				st.record = record
				st.messageSeq = uint64(len(record.Messages))
				st.diskMod = info.ModTime()
				st.signalLocked()
			}
			st.mu.Unlock()
			continue
		}
		s.mu.Lock()
		if len(s.sessions) < cursorCLIMaxChats && s.sessions[id] == nil {
			s.sessions[id] = &cursorACPState{record: record, messageSeq: uint64(len(record.Messages)), diskMod: info.ModTime()}
		}
		s.mu.Unlock()
	}
	return nil
}

// ResumeQueued is called by the daemon, never by read-only CLI commands.
func (s *CursorACPSource) ResumeQueued() {
	if s.closing.Load() {
		return
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, st := range s.sessions {
		st.mu.Lock()
		pending := len(st.record.Queued) > 0
		st.mu.Unlock()
		if pending {
			go s.drainQueued(st)
		}
	}
}

func cursorACPValidID(id string) bool {
	if len(id) < 16 || len(id) > 128 {
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

func (s *CursorACPSource) Kind() protocol.Kind { return protocol.KindCursorCLI }

func (s *CursorACPSource) get(id string) (*cursorACPState, error) {
	s.mu.RLock()
	st := s.sessions[id]
	s.mu.RUnlock()
	if st == nil {
		return nil, fmt.Errorf("source: unknown Cursor ACP session %q", id)
	}
	return st, nil
}

func (s *CursorACPSource) save(st *cursorACPState) error {
	st.saveMu.Lock()
	defer st.saveMu.Unlock()
	st.mu.Lock()
	record := st.record
	record.Messages = slices.Clone(st.record.Messages)
	record.Queued = slices.Clone(st.record.Queued)
	st.mu.Unlock()
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if len(data) > cursorCLIMaxDBOutput {
		return errors.New("Cursor ACP transcript exceeds local limit")
	}
	path := filepath.Join(s.dir, record.NativeID+".json")
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	temp := path + "." + hex.EncodeToString(nonce[:]) + ".tmp"
	if err := os.WriteFile(temp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(temp, path); err != nil {
		_ = os.Remove(temp)
		return err
	}
	if info, err := os.Stat(path); err == nil {
		st.mu.Lock()
		st.diskMod = info.ModTime()
		st.mu.Unlock()
	}
	return nil
}

func (s *CursorACPSource) Discover(context.Context) ([]protocol.Session, error) {
	_ = s.refreshRecords()
	s.mu.RLock()
	states := make([]*cursorACPState, 0, len(s.sessions))
	for _, st := range s.sessions {
		states = append(states, st)
	}
	s.mu.RUnlock()
	out := make([]protocol.Session, 0, len(states))
	for _, st := range states {
		st.mu.Lock()
		r := st.record
		state := protocol.StateIdle
		if st.busy {
			state = protocol.StateBusy
		}
		if st.question != nil {
			state = protocol.StateWaitingInput
		}
		q := st.question
		st.mu.Unlock()
		if state == protocol.StateIdle && s.busyElsewhere(r.NativeID) {
			state = protocol.StateBusy
		}
		if r.LastActivityAt < time.Now().Add(-cursorCLIWindow).UnixMilli() && state == protocol.StateIdle {
			continue
		}
		out = append(out, protocol.Session{
			ID: cursorACPPrefix + r.NativeID, Kind: protocol.KindCursorCLI,
			NativeID: r.NativeID, Name: cursorACPName(r), Cwd: r.Cwd,
			State: state, Inject: protocol.InjectAPI, Question: q,
			StartedAt: r.StartedAt, LastActivityAt: r.LastActivityAt,
		})
	}
	return out, nil
}

func cursorACPName(record cursorACPRecord) string {
	if record.Name != "" {
		return record.Name
	}
	return filepath.Base(record.Cwd)
}

func (s *CursorACPSource) Page(_ context.Context, id, before string, limit int) (protocol.Page, error) {
	_ = s.refreshRecords()
	if err := ValidatePageLimit(limit); err != nil {
		return protocol.Page{}, err
	}
	st, err := s.get(id)
	if err != nil {
		return protocol.Page{}, err
	}
	st.mu.Lock()
	messages := slices.Clone(st.record.Messages)
	st.mu.Unlock()
	end := len(messages)
	if before != "" {
		end, err = strconv.Atoi(before)
		if err != nil || end < 1 || end > len(messages) {
			return protocol.Page{}, errors.New("source: invalid Cursor ACP page cursor")
		}
	}
	start := max(0, end-limit)
	cursor := ""
	if start > 0 {
		cursor = strconv.Itoa(start)
	}
	return protocol.NewPage(id, messages[start:end], cursor, start > 0), nil
}

func (s *CursorACPSource) Follow(ctx context.Context, id string, out chan<- []protocol.Message) error {
	st, err := s.get(id)
	if err != nil {
		return err
	}
	signal := make(chan struct{}, 1)
	st.mu.Lock()
	if st.subscribers == nil {
		st.subscribers = make(map[chan struct{}]struct{})
	}
	st.subscribers[signal] = struct{}{}
	st.mu.Unlock()
	defer func() { st.mu.Lock(); delete(st.subscribers, signal); st.mu.Unlock() }()
	page, err := s.Page(ctx, id, "", 100)
	if err != nil {
		return err
	}
	seen := make(map[string]openCodeSeenMessage)
	var generation uint64 = 1
	_ = updateOpenCodeSeen(seen, page.Messages, generation)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-signal:
		case <-ticker.C:
		}
		page, err := s.Page(ctx, id, "", 100)
		if err != nil {
			return err
		}
		generation++
		changed := updateOpenCodeSeen(seen, page.Messages, generation)
		if len(changed) > 0 {
			select {
			case out <- changed:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

func (st *cursorACPState) signalLocked() {
	for subscriber := range st.subscribers {
		select {
		case subscriber <- struct{}{}:
		default:
		}
	}
}

func (s *CursorACPSource) Launch(ctx context.Context, cwd, prompt string) (string, error) {
	st := &cursorACPState{record: cursorACPRecord{Cwd: cwd, StartedAt: time.Now().UnixMilli(), LastActivityAt: time.Now().UnixMilli()}}
	client, err := newCursorACPClient(cwd, func(event cursorACPEnvelope) { s.handle(st, event) })
	if err != nil {
		return "", err
	}
	var created struct {
		SessionID string `json:"sessionId"`
	}
	if err := client.call(ctx, "session/new", map[string]any{"cwd": cwd, "mcpServers": []any{}}, &created); err != nil {
		client.close()
		return "", fmt.Errorf("create Cursor session: %w", err)
	}
	if !cursorACPValidID(created.SessionID) {
		client.close()
		return "", errors.New("Cursor returned an invalid session ID")
	}
	st.mu.Lock()
	st.record.NativeID = created.SessionID
	st.mu.Unlock()
	id := cursorACPPrefix + created.SessionID
	s.mu.Lock()
	s.sessions[id] = st
	s.mu.Unlock()
	if err := s.save(st); err != nil {
		client.close()
		s.mu.Lock()
		delete(s.sessions, id)
		s.mu.Unlock()
		return "", err
	}
	lock, err := s.lockTurn(created.SessionID)
	if err != nil {
		client.close()
		s.mu.Lock()
		delete(s.sessions, id)
		s.mu.Unlock()
		_ = os.Remove(filepath.Join(s.dir, created.SessionID+".json"))
		return "", err
	}
	if _, err := s.begin(st, client, lock, prompt); err != nil {
		s.mu.Lock()
		delete(s.sessions, id)
		s.mu.Unlock()
		_ = os.Remove(filepath.Join(s.dir, created.SessionID+".json"))
		return "", err
	}
	return id, nil
}

func (s *CursorACPSource) Inject(ctx context.Context, id, text string) (protocol.InjectMode, error) {
	st, err := s.get(id)
	if err != nil {
		return protocol.InjectNone, err
	}
	st.startMu.Lock()
	defer st.startMu.Unlock()
	return s.injectLocked(ctx, st, text)
}

// injectLocked serializes a new turn with queue draining and other sends.
func (s *CursorACPSource) injectLocked(ctx context.Context, st *cursorACPState, text string) (protocol.InjectMode, error) {
	st.mu.Lock()
	if st.question != nil {
		st.mu.Unlock()
		return protocol.InjectNone, errors.New("answer Cursor's pending question first")
	}
	if st.busy {
		st.record.Queued = append(st.record.Queued, text)
		st.mu.Unlock()
		if err := s.save(st); err != nil {
			st.mu.Lock()
			st.record.Queued = st.record.Queued[:len(st.record.Queued)-1]
			st.mu.Unlock()
			_ = s.save(st)
			return protocol.InjectNone, err
		}
		return protocol.InjectAPI, nil
	}
	cwd, nativeID := st.record.Cwd, st.record.NativeID
	st.replaying = true
	st.mu.Unlock()
	lock, err := s.lockTurn(nativeID)
	if err != nil {
		st.mu.Lock()
		st.replaying = false
		st.mu.Unlock()
		return protocol.InjectNone, err
	}
	client, err := newCursorACPClient(cwd, func(event cursorACPEnvelope) { s.handle(st, event) })
	if err != nil {
		releaseCursorACPLock(lock)
		st.mu.Lock()
		st.replaying = false
		st.mu.Unlock()
		return protocol.InjectNone, err
	}
	if err := client.call(ctx, "session/load", map[string]any{"sessionId": nativeID, "cwd": cwd, "mcpServers": []any{}}, nil); err != nil {
		client.close()
		releaseCursorACPLock(lock)
		st.mu.Lock()
		st.replaying = false
		st.mu.Unlock()
		return protocol.InjectNone, fmt.Errorf("resume Cursor session: %w", err)
	}
	st.mu.Lock()
	st.replaying = false
	st.mu.Unlock()
	done, err := s.begin(st, client, lock, text)
	if err != nil {
		return protocol.InjectNone, err
	}
	if !s.async {
		select {
		case <-done:
		case <-ctx.Done():
			_ = client.notify("session/cancel", map[string]string{"sessionId": nativeID})
			client.close()
			<-done
			return protocol.InjectNone, ctx.Err()
		}
	}
	return protocol.InjectAPI, nil
}

func (s *CursorACPSource) begin(st *cursorACPState, client *cursorACPClient, lock *os.File, text string) (<-chan struct{}, error) {
	st.mu.Lock()
	st.client, st.busy, st.question, st.pending = client, true, nil, nil
	st.lockFile = lock
	st.turnDone = make(chan struct{})
	done := st.turnDone
	st.assistantID = ""
	st.messageSeq++
	id := cursorACPPrefix + st.record.NativeID
	st.record.Messages = append(st.record.Messages, protocol.Message{
		ID: fmt.Sprintf("%s:%d:user", id, st.messageSeq), SessionID: id,
		Role: protocol.RoleUser, Ts: time.Now().UnixMilli(), Text: text,
	})
	st.turnID = st.record.Messages[len(st.record.Messages)-1].ID
	st.record.LastActivityAt = time.Now().UnixMilli()
	nativeID := st.record.NativeID
	st.mu.Unlock()
	requestID, response, err := client.startCall("session/prompt", map[string]any{
		"sessionId": nativeID, "prompt": []map[string]string{{"type": "text", "text": text}},
	})
	if err != nil {
		st.mu.Lock()
		st.record.Messages = st.record.Messages[:len(st.record.Messages)-1]
		st.messageSeq--
		st.busy, st.client, st.lockFile, st.turnDone, st.turnID = false, nil, nil, nil, ""
		st.mu.Unlock()
		client.close()
		releaseCursorACPLock(lock)
		return nil, fmt.Errorf("send Cursor prompt: %w", err)
	}
	st.mu.Lock()
	st.signalLocked()
	st.mu.Unlock()
	_ = s.save(st)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
		defer cancel()
		err := client.await(ctx, requestID, response, nil)
		st.mu.Lock()
		if err != nil {
			st.messageSeq++
			st.record.Messages = append(st.record.Messages, protocol.Message{
				ID: fmt.Sprintf("%s:%d:error", id, st.messageSeq), SessionID: id,
				Role: protocol.RoleSystem, Ts: time.Now().UnixMilli(), Text: "Cursor stopped: " + err.Error(),
			})
			st.signalLocked()
		}
		st.busy, st.question, st.pending, st.client = false, nil, nil, nil
		st.lockFile, st.turnDone = nil, nil
		st.record.LastActivityAt = time.Now().UnixMilli()
		st.mu.Unlock()
		_ = s.save(st)
		client.close()
		releaseCursorACPLock(lock)
		close(done)
		if !s.closing.Load() {
			go s.drainQueued(st)
		}
	}()
	return done, nil
}

func (s *CursorACPSource) drainQueued(st *cursorACPState) {
	st.startMu.Lock()
	defer st.startMu.Unlock()
	if s.closing.Load() {
		return
	}
	st.mu.Lock()
	if st.busy || len(st.record.Queued) == 0 {
		st.mu.Unlock()
		return
	}
	next := st.record.Queued[0]
	st.record.Queued = st.record.Queued[1:]
	st.mu.Unlock()
	if err := s.save(st); err != nil {
		st.mu.Lock()
		st.record.Queued = append([]string{next}, st.record.Queued...)
		st.mu.Unlock()
		_ = s.save(st)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err := s.injectLocked(ctx, st, next); err != nil {
		st.mu.Lock()
		st.record.Queued = append([]string{next}, st.record.Queued...)
		st.mu.Unlock()
		_ = s.save(st)
	}
}

func (s *CursorACPSource) Interrupt(_ context.Context, id string) error {
	st, err := s.get(id)
	if err != nil {
		return err
	}
	st.mu.Lock()
	client, busy, nativeID := st.client, st.busy, st.record.NativeID
	st.mu.Unlock()
	if !busy || client == nil {
		return errors.New("Cursor is not running a turn")
	}
	return client.notify("session/cancel", map[string]string{"sessionId": nativeID})
}

func (s *CursorACPSource) CurrentQuestion(_ context.Context, id string) (*protocol.Question, error) {
	st, err := s.get(id)
	if err != nil {
		return nil, err
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.question, nil
}

func (s *CursorACPSource) handle(st *cursorACPState, event cursorACPEnvelope) {
	if event.Method == "session/update" {
		var payload struct {
			Update json.RawMessage `json:"update"`
		}
		if json.Unmarshal(event.Params, &payload) != nil {
			return
		}
		var update struct {
			SessionUpdate string `json:"sessionUpdate"`
			Title         string `json:"title"`
			UpdatedAt     string `json:"updatedAt"`
			Content       struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			ToolCallID string `json:"toolCallId"`
			Status     string `json:"status"`
		}
		if json.Unmarshal(payload.Update, &update) != nil {
			return
		}
		st.mu.Lock()
		if st.replaying {
			st.mu.Unlock()
			return
		}
		changed := false
		id := cursorACPPrefix + st.record.NativeID
		switch update.SessionUpdate {
		case "session_info_update":
			if title := strings.TrimSpace(update.Title); title != "" {
				st.record.Name = clipRunes(title, 120)
				changed = true
			}
			if when, err := time.Parse(time.RFC3339Nano, update.UpdatedAt); err == nil {
				st.record.LastActivityAt = when.UnixMilli()
				changed = true
			}
		case "agent_message_chunk":
			if update.Content.Type == "text" && update.Content.Text != "" {
				if st.assistantID != "" {
					for i := len(st.record.Messages) - 1; i >= 0; i-- {
						if st.record.Messages[i].ID == st.assistantID {
							st.record.Messages[i].Text = clipRunes(st.record.Messages[i].Text+update.Content.Text, parser.PreviewChars)
							changed = true
							break
						}
					}
				}
				if !changed {
					st.messageSeq++
					st.assistantID = fmt.Sprintf("%s:%d:assistant", id, st.messageSeq)
					st.record.Messages = append(st.record.Messages, protocol.Message{
						ID: st.assistantID, SessionID: id, Role: protocol.RoleAssistant,
						Ts: time.Now().UnixMilli(), Text: clipRunes(update.Content.Text, parser.PreviewChars),
					})
					changed = true
				}
			}
		case "tool_call", "tool_call_update":
			st.assistantID = ""
			if update.ToolCallID != "" {
				toolID := st.turnID + ":tool:" + update.ToolCallID
				for i := len(st.record.Messages) - 1; i >= 0; i-- {
					if st.record.Messages[i].ID == toolID {
						if update.Title != "" {
							st.record.Messages[i].Tool.Summary = clipRunes(update.Title, parser.SummaryChars)
						}
						if update.Status == "completed" {
							st.record.Messages[i].Tool.Status = protocol.ToolOK
						}
						if update.Status == "failed" {
							st.record.Messages[i].Tool.Status = protocol.ToolError
						}
						changed = true
						break
					}
				}
				if !changed {
					name := update.Title
					if name == "" {
						name = "Cursor tool"
					}
					st.record.Messages = append(st.record.Messages, protocol.Message{
						ID: toolID, SessionID: id, Role: protocol.RoleTool, Ts: time.Now().UnixMilli(),
						Tool: &protocol.Tool{Name: clipRunes(name, parser.SummaryChars), Status: protocol.ToolRunning},
					})
					changed = true
				}
			}
		}
		if changed && update.SessionUpdate != "session_info_update" {
			st.record.LastActivityAt = time.Now().UnixMilli()
			st.signalLocked()
		}
		persistable := cursorACPValidID(st.record.NativeID)
		st.mu.Unlock()
		if changed && persistable {
			_ = s.save(st)
		}
		return
	}
	if len(event.ID) == 0 {
		return
	}
	st.mu.Lock()
	client := st.client
	if client == nil || st.replaying {
		st.mu.Unlock()
		return
	}
	var pending *cursorACPPending
	var question *protocol.Question
	switch event.Method {
	case "session/request_permission":
		var request struct {
			ToolCall struct {
				Title    string          `json:"title"`
				RawInput json.RawMessage `json:"rawInput"`
			} `json:"toolCall"`
			Options []struct {
				OptionID string `json:"optionId"`
				Name     string `json:"name"`
			} `json:"options"`
		}
		if json.Unmarshal(event.Params, &request) == nil {
			options := make([]protocol.QuestionOption, 0, len(request.Options))
			for _, option := range request.Options {
				if option.OptionID != "" {
					options = append(options, protocol.QuestionOption{Key: option.OptionID, Label: option.Name})
				}
			}
			if len(options) > 0 {
				detail := string(request.ToolCall.RawInput)
				if len(detail) > 4096 {
					detail = detail[:4096] + "…"
				}
				question = &protocol.Question{Title: "Cursor permission", Prompt: request.ToolCall.Title, Detail: detail, Options: options}
				if question.Prompt == "" {
					question.Prompt = "Allow this Cursor action?"
				}
				pending = &cursorACPPending{requestID: event.ID, kind: "permission", options: options}
			}
		}
	case "cursor/ask_question":
		var request struct {
			Title     string                 `json:"title"`
			Questions []cursorACPAskQuestion `json:"questions"`
		}
		if json.Unmarshal(event.Params, &request) == nil && len(request.Questions) > 0 {
			pending = &cursorACPPending{requestID: event.ID, kind: "ask", questions: request.Questions}
			question = cursorACPQuestion(request.Title, request.Questions[0])
		}
	case "cursor/create_plan":
		var request struct {
			Name string `json:"name"`
			Plan string `json:"plan"`
		}
		if json.Unmarshal(event.Params, &request) == nil {
			detail := request.Plan
			if len(detail) > 16*1024 {
				detail = detail[:16*1024] + "…"
			}
			question = &protocol.Question{Title: "Cursor plan", Prompt: request.Name, Detail: detail,
				Options: []protocol.QuestionOption{{Key: "accepted", Label: "Accept plan"}, {Key: "rejected", Label: "Reject plan"}}}
			if question.Prompt == "" {
				question.Prompt = "Approve this plan?"
			}
			pending = &cursorACPPending{requestID: event.ID, kind: "plan", options: question.Options}
		}
	}
	if pending != nil && question != nil {
		st.questionSeq++
		question.ID = fmt.Sprintf("acp:%s:%d", st.record.NativeID, st.questionSeq)
		st.pending, st.question = pending, question
		st.mu.Unlock()
		return
	}
	st.mu.Unlock()
	_ = client.respondError(event.ID, "unsupported Cursor ACP request")
}

func cursorACPQuestion(title string, input cursorACPAskQuestion) *protocol.Question {
	options := make([]protocol.QuestionOption, 0, len(input.Options))
	for _, option := range input.Options {
		if option.ID != "" {
			options = append(options, protocol.QuestionOption{Key: option.ID, Label: option.Label})
		}
	}
	return &protocol.Question{Title: title, Prompt: input.Prompt, Options: options, Multiple: input.AllowMultiple}
}

func (s *CursorACPSource) Answer(_ context.Context, id string, answer protocol.QuestionAnswer) error {
	st, err := s.get(id)
	if err != nil {
		return err
	}
	st.mu.Lock()
	if st.pending == nil || st.question == nil || st.question.ID != answer.QuestionID || st.client == nil {
		st.mu.Unlock()
		return errors.New("Cursor question is no longer current")
	}
	pending, question, client := st.pending, st.question, st.client
	keys := answer.Options
	if len(keys) == 0 && answer.OptionKey != "" {
		keys = []string{answer.OptionKey}
	}
	if len(keys) == 0 || (!question.Multiple && len(keys) != 1) || answer.Text != "" {
		st.mu.Unlock()
		return errors.New("select a listed Cursor option")
	}
	for _, key := range keys {
		valid := false
		for _, option := range question.Options {
			if option.Key == key {
				valid = true
				break
			}
		}
		if !valid {
			st.mu.Unlock()
			return errors.New("Cursor option is no longer current")
		}
	}
	var result any
	switch pending.kind {
	case "permission":
		result = map[string]any{"outcome": map[string]string{"outcome": "selected", "optionId": keys[0]}}
	case "plan":
		result = map[string]any{"outcome": map[string]string{"outcome": keys[0]}}
	case "ask":
		pending.answers = append(pending.answers, cursorACPAskAnswer{
			QuestionID: pending.questions[pending.index].ID, SelectedOptionIDs: slices.Clone(keys),
		})
		pending.index++
		if pending.index < len(pending.questions) {
			st.questionSeq++
			next := cursorACPQuestion(question.Title, pending.questions[pending.index])
			next.ID = fmt.Sprintf("acp:%s:%d", st.record.NativeID, st.questionSeq)
			st.question = next
			st.mu.Unlock()
			return nil
		}
		result = map[string]any{"outcome": map[string]any{"outcome": "answered", "answers": pending.answers}}
	}
	err = client.respond(pending.requestID, result)
	if err == nil {
		st.question, st.pending = nil, nil
	} else if pending.kind == "ask" {
		pending.index--
		pending.answers = pending.answers[:len(pending.answers)-1]
	}
	st.mu.Unlock()
	return err
}
