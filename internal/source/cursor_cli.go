package source

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lenajeremy/agentman/internal/parser"
	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/question"
	"github.com/lenajeremy/agentman/internal/tmux"
)

// Cursor's interactive CLI stores chats separately from IDE composer
// transcripts. A chat can be read from its local SQLite store, but only an
// `am cursor` tmux pane gives Agentman a supported terminal input channel.
const (
	cursorCLIWindow    = 12 * time.Hour
	cursorCLIDBTimeout = 5 * time.Second
	// cursorCLIBusyTimeout is how long a read waits for Cursor to finish
	// writing before giving up.
	//
	// Cursor owns this database and writes to it throughout a turn, so a reader
	// meeting a write lock is ordinary rather than exceptional. Without this the
	// sqlite3 CLI returns "database is locked" immediately, which used to end a
	// live subscription outright and leave the phone silently not updating.
	// Kept below cursorCLIDBTimeout so the command still exits before its own
	// deadline does.
	cursorCLIBusyTimeout = 3 * time.Second
	// cursorCLIFollowFailures is how many consecutive failed polls end a
	// subscription. At the follow interval this is a few seconds of a store
	// that cannot be read at all, which is a real fault worth reporting.
	cursorCLIFollowFailures = 20
)

// cursorCLITimeoutCommand sets the busy timeout through sqlite3's dot-command
// form, which prints nothing. A `PRAGMA busy_timeout` would return its value as
// a row and, under -json, land in the output the caller is about to parse.
var cursorCLITimeoutCommand = fmt.Sprintf(".timeout %d", cursorCLIBusyTimeout.Milliseconds())

const (
	cursorCLIMaxDBOutput = 16 * 1024 * 1024
	cursorCLIMaxChats    = 200
	cursorCLIPanePrefix  = tmux.Prefix + "cursor-"
	cursorCLIPaneMargin  = 2 * time.Second
	cursorCLILsofTimeout = 3 * time.Second
)

type cursorCLIChat struct {
	SchemaVersion   int    `json:"schemaVersion"`
	CreatedAtMs     int64  `json:"createdAtMs"`
	UpdatedAtMs     int64  `json:"updatedAtMs"`
	HasConversation bool   `json:"hasConversation"`
	Title           string `json:"title"`
	Cwd             string `json:"cwd"`
}

type cursorCLISession struct {
	meta  protocol.Session
	store string
	pane  string
}

type cursorCLIModelEntry struct {
	updatedAt int64
	model     string
}

// CursorCLISource observes Cursor Agent CLI chats. Cursor IDE sessions remain
// in CursorSource because the two stores have no shared session identity.
type CursorCLISource struct {
	home        string
	listPanes   func(context.Context) ([]tmux.Session, error)
	capturePane func(context.Context, string) (string, error)
	openStores  func(context.Context, []int) map[int]string
	modelMu     sync.Mutex
	models      map[string]cursorCLIModelEntry
	mu          sync.RWMutex
	sessions    map[string]cursorCLISession
}

func NewCursorCLISource(home string) (*CursorCLISource, error) {
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return nil, err
		}
	}
	return &CursorCLISource{
		home: home, listPanes: tmux.List, capturePane: tmux.Capture,
		openStores: func(ctx context.Context, pids []int) map[int]string {
			return cursorCLIOpenStores(ctx, home, pids)
		},
		models:   make(map[string]cursorCLIModelEntry),
		sessions: make(map[string]cursorCLISession),
	}, nil
}

func (s *CursorCLISource) Kind() protocol.Kind { return protocol.KindCursorCLI }

// CLI chats also write observe transcripts in the IDE's project tree. Their
// store directory IDs distinguish them from native IDE composer sessions.
func cursorCLIChatIDs(home string) map[string]bool {
	paths, _ := filepath.Glob(filepath.Join(home, ".cursor", "chats", "*", "*", "meta.json"))
	ids := make(map[string]bool, len(paths))
	for _, path := range paths {
		ids[filepath.Base(filepath.Dir(path))] = true
	}
	return ids
}

// cursorCLIOpenStores identifies the active chat by the database the agent
// process actually has open. This remains exact when two panes share a cwd,
// when an old chat is resumed, and when the user switches chats in one pane.
// lsof is optional: environments without it retain the conservative fallback.
func cursorCLIOpenStores(ctx context.Context, home string, pids []int) map[int]string {
	result := make(map[int]string)
	if len(pids) == 0 {
		return result
	}
	bin, err := exec.LookPath("lsof")
	if err != nil {
		return result
	}
	ids := make([]string, 0, len(pids))
	for _, pid := range pids {
		if pid > 1 {
			ids = append(ids, strconv.Itoa(pid))
		}
	}
	if len(ids) == 0 {
		return result
	}
	ctx, cancel := context.WithTimeout(ctx, cursorCLILsofTimeout)
	defer cancel()
	output, err := exec.CommandContext(ctx, bin, "-w", "-a", "-p", strings.Join(ids, ","), "-Fpn").Output()
	if err != nil {
		return result
	}
	return parseCursorCLIOpenStores(home, output)
}

func parseCursorCLIOpenStores(home string, output []byte) map[int]string {
	result := make(map[int]string)
	root := filepath.Join(home, ".cursor", "chats") + string(filepath.Separator)
	var pid int
	ambiguous := make(map[int]bool)
	for _, line := range strings.Split(string(output), "\n") {
		if len(line) < 2 {
			continue
		}
		switch line[0] {
		case 'p':
			pid, _ = strconv.Atoi(line[1:])
		case 'n':
			path := filepath.Clean(line[1:])
			if pid <= 1 || !strings.HasPrefix(path, root) || filepath.Base(path) != "store.db" {
				continue
			}
			rel, err := filepath.Rel(root, path)
			if err != nil || len(strings.Split(rel, string(filepath.Separator))) != 3 {
				continue
			}
			if prev, ok := result[pid]; ok && prev != path {
				ambiguous[pid] = true
			}
			result[pid] = path
		}
	}
	for pid := range ambiguous {
		// Keep a non-path marker so discovery will not fall back to a guess.
		result[pid] = "ambiguous"
	}
	return result
}

func queryCursorCLIModel(ctx context.Context, store string) string {
	bin, err := exec.LookPath("sqlite3")
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, cursorCLIDBTimeout)
	defer cancel()
	const query = "SELECT json_extract(data,'$.content[0].providerOptions.cursor.modelName') AS model " +
		"FROM blobs WHERE json_valid(data)=1 AND json_extract(data,'$.role')='assistant' " +
		"AND model IS NOT NULL ORDER BY rowid DESC LIMIT 1"
	output, err := exec.CommandContext(ctx, bin, "-json", "-readonly",
		"-cmd", cursorCLITimeoutCommand, "file:"+store+"?mode=ro", query).Output()
	if err != nil || len(output) > 4096 {
		return ""
	}
	var rows []struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(output, &rows) != nil || len(rows) == 0 {
		return ""
	}
	return clipRunes(rows[0].Model, 80)
}

func (s *CursorCLISource) cursorCLIModel(ctx context.Context, chatID, store string, updatedAt int64) string {
	s.modelMu.Lock()
	defer s.modelMu.Unlock()
	if cached, ok := s.models[chatID]; ok && cached.updatedAt == updatedAt {
		return cached.model
	}
	model := queryCursorCLIModel(ctx, store)
	s.models[chatID] = cursorCLIModelEntry{updatedAt: updatedAt, model: model}
	return model
}

func (s *CursorCLISource) Discover(ctx context.Context) ([]protocol.Session, error) {
	panes, _ := s.listPanes(ctx)
	managed := make([]tmux.Session, 0, len(panes))
	paneCount := make(map[string]int)
	var panePIDs []int
	for _, pane := range panes {
		// Cursor's launcher runs a Node process on macOS; tmux reports
		// `node` as the current command after the initial `agent` exec.
		if strings.HasPrefix(pane.Name, cursorCLIPanePrefix) &&
			(pane.Command == "agent" || pane.Command == "node") {
			managed = append(managed, pane)
			paneCount[pane.Cwd]++
			panePIDs = append(panePIDs, pane.PanePID)
		}
	}
	openStores := map[int]string{}
	if s.openStores != nil {
		openStores = s.openStores(ctx, panePIDs)
	}

	// CLI stores are grouped by a workspace hash. Read metadata only; the
	// potentially large message blobs are opened on history/follow requests.
	paths, err := filepath.Glob(filepath.Join(s.home, ".cursor", "chats", "*", "*", "meta.json"))
	if err != nil {
		return nil, err
	}
	type foundChat struct {
		id, store string
		meta      cursorCLIChat
	}
	chats := make([]foundChat, 0, len(paths))
	cutoff := time.Now().Add(-cursorCLIWindow).UnixMilli()
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil || len(data) > 64*1024 {
			continue
		}
		var meta cursorCLIChat
		if json.Unmarshal(data, &meta) != nil || !meta.HasConversation || meta.Cwd == "" {
			continue
		}
		store := filepath.Join(filepath.Dir(path), "store.db")
		if _, err := os.Stat(store); err != nil {
			continue
		}
		active := false
		for _, opened := range openStores {
			if opened == store {
				active = true
				break
			}
		}
		if meta.UpdatedAtMs < cutoff && !active {
			continue
		}
		chats = append(chats, foundChat{
			id: filepath.Base(filepath.Dir(path)), store: store, meta: meta,
		})
	}
	slices.SortFunc(chats, func(a, b foundChat) int {
		if a.meta.UpdatedAtMs > b.meta.UpdatedAtMs {
			return -1
		}
		if a.meta.UpdatedAtMs < b.meta.UpdatedAtMs {
			return 1
		}
		return strings.Compare(a.id, b.id)
	})
	if len(chats) > cursorCLIMaxChats {
		chats = chats[:cursorCLIMaxChats]
	}

	// A pane is only bound to a transcript when there is exactly one managed
	// pane in that directory and exactly one chat updated since it started.
	// Guessing would show or send against the wrong conversation.
	chatPane := make(map[string]tmux.Session)
	claimedPane := make(map[string]bool)
	claimedChat := make(map[string]bool)
	for _, pane := range managed {
		opened := openStores[pane.PanePID]
		if opened == "" {
			continue
		}
		for _, chat := range chats {
			if chat.store == opened && !claimedChat[chat.id] {
				chatPane[chat.id] = pane
				claimedPane[pane.Name] = true
				claimedChat[chat.id] = true
				break
			}
		}
	}
	for _, pane := range managed {
		if claimedPane[pane.Name] || openStores[pane.PanePID] != "" || paneCount[pane.Cwd] != 1 {
			continue
		}
		var candidate *foundChat
		for i := range chats {
			chat := &chats[i]
			if claimedChat[chat.id] || chat.meta.Cwd != pane.Cwd ||
				chat.meta.UpdatedAtMs < pane.Created.Add(-cursorCLIPaneMargin).UnixMilli() {
				continue
			}
			if candidate != nil {
				candidate = nil
				break
			}
			candidate = chat
		}
		if candidate != nil {
			chatPane[candidate.id] = pane
			claimedPane[pane.Name] = true
			claimedChat[candidate.id] = true
		}
	}

	result := make([]protocol.Session, 0, len(chats)+len(managed))
	next := make(map[string]cursorCLISession, len(chats)+len(managed))
	for _, chat := range chats {
		pane, wrapped := chatPane[chat.id]
		id := "cursor-cli:chat:" + chat.id
		mode := protocol.InjectNone
		state := protocol.StateIdle
		var currentQuestion *protocol.Question
		var running bool
		if wrapped {
			id = "cursor-cli:pane:" + pane.Name
			mode = protocol.InjectTmux
			currentQuestion, running = s.cursorCLIPaneStatus(ctx, pane.Name)
			if currentQuestion != nil {
				state = protocol.StateWaitingInput
			} else if running {
				state = protocol.StateBusy
			}
		}
		name := strings.TrimSpace(chat.meta.Title)
		if name == "" {
			name = filepath.Base(chat.meta.Cwd)
		}
		if len([]rune(name)) > 120 {
			name = string([]rune(name)[:120])
		}
		entry := protocol.Session{
			ID: id, Kind: protocol.KindCursorCLI, NativeID: chat.id,
			Name: name, Cwd: chat.meta.Cwd, State: state, Inject: mode,
			StartedAt: chat.meta.CreatedAtMs, LastActivityAt: chat.meta.UpdatedAtMs,
		}
		entry.Model = s.cursorCLIModel(ctx, chat.id, chat.store, chat.meta.UpdatedAtMs)
		if wrapped {
			entry.AgentPID = pane.PanePID
			entry.Question = currentQuestion
		}
		result = append(result, entry)
		next[id] = cursorCLISession{meta: entry, store: chat.store, pane: pane.Name}
	}
	for _, pane := range managed {
		if claimedPane[pane.Name] {
			continue
		}
		id := "cursor-cli:pane:" + pane.Name
		entry := protocol.Session{
			ID: id, Kind: protocol.KindCursorCLI, NativeID: pane.Name,
			Name: "Cursor CLI", Cwd: pane.Cwd, State: protocol.StateIdle,
			Inject: protocol.InjectTmux, StartedAt: pane.Created.UnixMilli(),
			LastActivityAt: pane.Created.UnixMilli(), AgentPID: pane.PanePID,
		}
		q, running := s.cursorCLIPaneStatus(ctx, pane.Name)
		if q != nil {
			entry.State, entry.Question = protocol.StateWaitingInput, q
		} else if running {
			entry.State = protocol.StateBusy
		}
		result = append(result, entry)
		next[id] = cursorCLISession{meta: entry, pane: pane.Name}
	}
	liveModels := make(map[string]bool, len(chats))
	for _, chat := range chats {
		liveModels[chat.id] = true
	}
	s.modelMu.Lock()
	for id := range s.models {
		if !liveModels[id] {
			delete(s.models, id)
		}
	}
	s.modelMu.Unlock()
	s.mu.Lock()
	s.sessions = next
	s.mu.Unlock()
	return result, nil
}

type cursorCLIRow struct {
	RowID int64  `json:"rowid"`
	ID    string `json:"id"`
	Data  string `json:"data"`
}

type cursorCLIPart struct {
	Type     string          `json:"type"`
	Text     string          `json:"text"`
	ToolName string          `json:"toolName"`
	Args     json.RawMessage `json:"args"`
	Result   json.RawMessage `json:"result"`
}

func queryCursorCLIRows(ctx context.Context, store string, before int64, limit int) ([]cursorCLIRow, error) {
	bin, err := exec.LookPath("sqlite3")
	if err != nil {
		return nil, fmt.Errorf("source: sqlite3 is required to read Cursor CLI chats: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, cursorCLIDBTimeout)
	defer cancel()
	where := ""
	if before > 0 {
		where = " AND rowid < " + strconv.FormatInt(before, 10)
	}
	query := "SELECT rowid,id,CAST(data AS TEXT) AS data FROM blobs WHERE json_valid(data)=1" +
		" AND json_extract(data,'$.role') IN ('user','assistant','tool')" + where +
		" ORDER BY rowid DESC LIMIT " + strconv.Itoa(limit)
	cmd := exec.CommandContext(ctx, bin, "-json", "-readonly",
		"-cmd", cursorCLITimeoutCommand, "file:"+store+"?mode=ro", query)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("source: cursor CLI store: %w: %s", err, bytes.TrimSpace(stderr.Bytes()))
	}
	if stdout.Len() > cursorCLIMaxDBOutput {
		return nil, fmt.Errorf("source: cursor CLI message page exceeds %d bytes", cursorCLIMaxDBOutput)
	}
	var rows []cursorCLIRow
	if err := json.Unmarshal(stdout.Bytes(), &rows); err != nil {
		return nil, fmt.Errorf("source: cursor CLI message page: %w", err)
	}
	return rows, nil
}

func cursorCLIMessages(sessionID string, row cursorCLIRow, started int64) []protocol.Message {
	var item struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal([]byte(row.Data), &item) != nil {
		return nil
	}
	role := protocol.Role(item.Role)
	if role != protocol.RoleUser && role != protocol.RoleAssistant && role != protocol.RoleTool {
		return nil
	}
	base := protocol.Message{
		ID: "cursor-cli:" + row.ID, SessionID: sessionID,
		Ts: started + row.RowID,
	}
	var parts []cursorCLIPart
	if len(item.Content) > 0 && item.Content[0] == '"' {
		var body string
		_ = json.Unmarshal(item.Content, &body)
		parts = append(parts, cursorCLIPart{Type: "text", Text: body})
	} else if json.Unmarshal(item.Content, &parts) != nil {
		return nil
	}
	var out []protocol.Message
	var texts []string
	for i, part := range parts {
		switch part.Type {
		case "text":
			if part.Text != "" && role != protocol.RoleTool {
				texts = append(texts, part.Text)
			}
		case "tool-call", "tool-result":
			name := part.ToolName
			if name == "" {
				name = "tool"
			}
			msg := base
			msg.ID = fmt.Sprintf("%s:%d", base.ID, i)
			msg.Role = protocol.RoleTool
			msg.Tool = &protocol.Tool{Name: name}
			if part.Type == "tool-call" {
				msg.Tool.Summary = cursorCLIToolSummary(part.Args)
			} else {
				var result string
				if json.Unmarshal(part.Result, &result) == nil {
					msg.Text = clipRunes(result, parser.PreviewChars)
				}
			}
			out = append(out, msg)
		}
	}
	body := strings.Join(texts, "\n")
	if strings.TrimSpace(body) == "" {
		return out
	}
	if role == protocol.RoleUser {
		// Cursor stores its generated workspace/rules preamble as a user-role
		// blob. Real prompts are wrapped in <user_query>; keep only that text
		// instead of leaking the internal context into the phone transcript.
		if start := strings.Index(body, "<user_query>"); start >= 0 {
			body = body[start+len("<user_query>"):]
			if end := strings.Index(body, "</user_query>"); end >= 0 {
				body = body[:end]
			}
			body = strings.TrimSpace(body)
		} else if strings.HasPrefix(strings.TrimSpace(body), "<user_info>") {
			return out
		}
	}
	if body == "" {
		return out
	}
	base.Role = role
	base.Text = clipRunes(body, parser.PreviewChars)
	return append([]protocol.Message{base}, out...)
}

func cursorCLIToolSummary(raw json.RawMessage) string {
	var args map[string]any
	if json.Unmarshal(raw, &args) != nil {
		return ""
	}
	for _, key := range []string{"description", "command", "path", "pattern", "glob_pattern"} {
		if value, ok := args[key].(string); ok && strings.TrimSpace(value) != "" {
			return clipRunes(strings.TrimSpace(value), parser.SummaryChars)
		}
	}
	return ""
}

func (s *CursorCLISource) Page(ctx context.Context, sessionID, before string, limit int) (protocol.Page, error) {
	s.mu.RLock()
	session, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if !ok {
		return protocol.Page{}, fmt.Errorf("source: unknown Cursor CLI session %q", sessionID)
	}
	if session.store == "" {
		return protocol.NewPage(sessionID, nil, "", false), nil
	}
	var bound int64
	if before != "" {
		var err error
		bound, err = strconv.ParseInt(before, 10, 64)
		if err != nil || bound <= 0 {
			return protocol.Page{}, fmt.Errorf("source: invalid Cursor CLI page cursor")
		}
	}
	rows, err := queryCursorCLIRows(ctx, session.store, bound, limit+1)
	if err != nil {
		return protocol.Page{}, err
	}
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	messages := make([]protocol.Message, 0, len(rows))
	for i := len(rows) - 1; i >= 0; i-- {
		messages = append(messages, cursorCLIMessages(sessionID, rows[i], session.meta.StartedAt)...)
	}
	position := ""
	if hasMore && len(rows) > 0 {
		position = strconv.FormatInt(rows[len(rows)-1].RowID, 10)
	}
	return protocol.NewPage(sessionID, messages, position, hasMore), nil
}

func (s *CursorCLISource) Follow(ctx context.Context, sessionID string, out chan<- []protocol.Message) error {
	page, err := s.Page(ctx, sessionID, "", 100)
	if err != nil {
		return err
	}
	seen := make(map[string]openCodeSeenMessage)
	var generation uint64 = 1
	_ = updateOpenCodeSeen(seen, page.Messages, generation)
	failures := 0
	ticker := time.NewTicker(followInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			page, err := s.Page(ctx, sessionID, "", 100)
			if err != nil {
				// Reading Cursor's store can fail for reasons that pass: it
				// holds a write lock for longer than the busy timeout, or is
				// mid-checkpoint. Ending the subscription for one of those
				// stops the phone updating for the rest of the session, which
				// is far worse than a late poll, so keep going and only give
				// up once it has failed for long enough to mean something.
				failures++
				if failures > cursorCLIFollowFailures {
					return err
				}
				continue
			}
			failures = 0
			generation++
			fresh := updateOpenCodeSeen(seen, page.Messages, generation)
			if len(fresh) > 0 {
				select {
				case out <- fresh:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
		}
	}
}

func (s *CursorCLISource) Inject(ctx context.Context, sessionID, message string) (protocol.InjectMode, error) {
	s.mu.RLock()
	session, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if !ok {
		return protocol.InjectNone, fmt.Errorf("source: unknown Cursor CLI session %q", sessionID)
	}
	if session.pane == "" {
		return protocol.InjectNone, errors.New("source: start Cursor with `am cursor` to send messages remotely")
	}
	pane, err := s.capturePane(ctx, session.pane)
	if err != nil {
		return protocol.InjectNone, fmt.Errorf("source: could not inspect Cursor CLI before sending: %w", err)
	}
	if cursorCLIQuestionFromPane(pane) != nil {
		return protocol.InjectNone, errors.New("source: answer the pending Cursor CLI question before sending a message")
	}
	if err := tmux.Send(ctx, session.pane, message); err != nil {
		return protocol.InjectNone, err
	}
	return protocol.InjectTmux, nil
}

func (s *CursorCLISource) Interrupt(ctx context.Context, sessionID string) error {
	s.mu.RLock()
	session, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("source: unknown Cursor CLI session %q", sessionID)
	}
	if session.pane == "" {
		return errors.New("source: only sessions started with `am cursor` can be interrupted")
	}
	return tmux.Interrupt(ctx, session.pane)
}

func (s *CursorCLISource) CurrentQuestion(ctx context.Context, sessionID string) (*protocol.Question, error) {
	s.mu.RLock()
	session, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("source: unknown Cursor CLI session %q", sessionID)
	}
	if session.pane == "" {
		return nil, nil
	}
	pane, err := s.capturePane(ctx, session.pane)
	if err != nil {
		return nil, err
	}
	return cursorCLIQuestionFromPane(pane), nil
}

func (s *CursorCLISource) cursorCLIPaneStatus(ctx context.Context, paneName string) (*protocol.Question, bool) {
	pane, err := s.capturePane(ctx, paneName)
	if err != nil {
		return nil, false
	}
	question := cursorCLIQuestionFromPane(pane)
	if question != nil {
		return question, false
	}
	lines := strings.Split(strings.TrimSpace(pane), "\n")
	if len(lines) > 10 {
		lines = lines[len(lines)-10:]
	}
	for _, line := range lines {
		if strings.Contains(strings.ToLower(line), "ctrl+c to stop") {
			return nil, true
		}
	}
	return nil, false
}

// Cursor's shell approval menu uses letter shortcuts instead of the numbered
// options shared by Claude and Codex. Recognize only a complete, currently
// visible menu; a fragment in old scrollback must never become answerable.
func cursorCLIQuestionFromPane(pane string) *protocol.Question {
	lines := strings.Split(strings.TrimSpace(pane), "\n")
	if len(lines) > 30 {
		lines = lines[len(lines)-30:]
	}
	// Cursor pauses before the first chat in an unfamiliar directory. Its
	// single-key menu can be answered remotely, but only while the footer is
	// still at the bottom of the live pane (not old scrollback).
	footer := -1
	for i := len(lines) - 1; i >= 0 && i >= len(lines)-5; i-- {
		if strings.Contains(lines[i], "Use arrow keys to navigate") {
			footer = i
			break
		}
	}
	if footer >= 0 {
		current := true
		for _, raw := range lines[footer+1:] {
			// Cursor frames a blank row with vertical borders between the
			// footer and bottom edge. It is not newer terminal content.
			line := strings.TrimSpace(strings.Trim(raw, " │"))
			if line != "" && !strings.HasPrefix(line, "╰") {
				current = false
				break
			}
		}
		trust, quit := false, false
		for i := footer - 1; i >= 0 && i >= footer-12; i-- {
			trust = trust || strings.Contains(lines[i], "[a] Trust this workspace")
			quit = quit || strings.Contains(lines[i], "[q] Quit")
		}
		if current && trust && quit {
			result := &protocol.Question{
				Title: "Workspace trust", Prompt: "Trust this workspace?",
				Options: []protocol.QuestionOption{
					{Key: "a", Label: "Trust this workspace"},
					{Key: "q", Label: "Quit"},
				},
			}
			result.ID = terminalQuestionID(result)
			return result
		}
	}
	promptAt, allowAt, skipAt := -1, -1, -1
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		switch {
		case line == "Run this command?":
			promptAt = i
		case strings.Contains(line, "Run (once) (y)"):
			allowAt = i
		case strings.Contains(line, "Skip & tell the agent") && strings.Contains(line, "n)"):
			skipAt = i
		}
	}
	if promptAt >= 0 && allowAt > promptAt && skipAt > promptAt &&
		len(lines)-1-skipAt <= 4 {
		commandAt := -1
		for i := promptAt - 1; i >= 0 && i >= promptAt-10; i-- {
			line := strings.TrimSpace(lines[i])
			if strings.HasPrefix(line, "$") {
				commandAt = i
				break
			}
		}
		// A remote approval must show the command in full. If Cursor's pane
		// does not expose it, leave the decision on the local terminal.
		if commandAt < 0 {
			return nil
		}
		parts := []string{strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[commandAt]), "$"))}
		for i := commandAt + 1; i < promptAt; i++ {
			if line := strings.TrimSpace(lines[i]); line != "" {
				parts = append(parts, line)
			}
		}
		detail := strings.Join(parts, "\n")
		if detail == "" || len(detail) > 16*1024 {
			return nil
		}
		result := &protocol.Question{
			Title: "Shell command", Prompt: "Run this command?", Detail: detail,
			Options: []protocol.QuestionOption{
				{Key: "y", Label: "Run once"},
				{Key: "n", Label: "Skip"},
			},
		}
		result.ID = terminalQuestionID(result)
		return result
	}
	if found := question.Detect(pane); found != nil {
		return protocolQuestion(found)
	}
	return nil
}

func (s *CursorCLISource) Answer(ctx context.Context, sessionID string, answer protocol.QuestionAnswer) error {
	s.mu.RLock()
	session, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("source: unknown Cursor CLI session %q", sessionID)
	}
	if session.pane == "" {
		return errors.New("source: this Cursor CLI chat cannot receive answers remotely")
	}
	if session.meta.Question == nil || answer.QuestionID == "" ||
		answer.QuestionID != session.meta.Question.ID {
		return errors.New("source: that question is no longer current; refresh the session")
	}
	if len(answer.Options) > 0 || answer.Text != "" {
		return errors.New("source: this terminal question only accepts one listed option")
	}
	if !questionHasOption(session.meta.Question, answer.OptionKey) {
		return errors.New("source: that option is no longer current; refresh the session")
	}
	current, err := s.CurrentQuestion(ctx, sessionID)
	if err != nil || !sameQuestion(session.meta.Question, current) {
		return errors.New("source: that question is no longer on screen; refresh the session")
	}
	return tmux.Answer(ctx, session.pane, answer.OptionKey)
}
