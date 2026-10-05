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
	// cursorCLIChatPrefix names a chat no managed pane holds; a pane-bound
	// chat is published under cursorCLIPaneIDPrefix and the pane's name.
	cursorCLIChatPrefix   = "cursor-cli:chat:"
	cursorCLIPaneIDPrefix = "cursor-cli:pane:"
	cursorCLIPaneMargin   = 2 * time.Second
	cursorCLILsofTimeout  = 3 * time.Second
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
	// openStores maps processes to the chat store each has open, and
	// reports whether the answer can be trusted; processes lists every
	// process so the Cursor agents among them can be found.
	openStores func(context.Context, []int) (map[int]string, bool)
	processes  func(context.Context) (*tmux.ProcessTree, error)
	// sendKey and sendText type into a pane; nil means tmux. Tests record
	// what would have been typed instead.
	sendKey  func(ctx context.Context, pane, key string) error
	sendText func(ctx context.Context, pane, text string) error
	sendKeys func(ctx context.Context, pane string, keys ...string) error
	modelMu  sync.Mutex
	models   map[string]cursorCLIModelEntry
	mu       sync.RWMutex
	sessions map[string]cursorCLISession

	// past holds the stores of chats that have already ended, found by Past
	// rather than by a sweep. See pastSessions.
	past pastSessions

	// live is the last trustworthy answer to which stores are open; see
	// cursor_cli_liveness.go.
	live cursorCLILiveness
	// turnMu guards the transcript locations and turn states read from them.
	turnMu       sync.Mutex
	transcripts  map[string]string
	turns        map[string]cursorCLITurnEntry
	hooksChecked cursorCLIHooksEntry
	paneStatus   map[string]cursorCLIPaneStatus
	storeModes   map[string]cursorCLIModeEntry

	// pending holds messages for chats running outside a managed pane,
	// delivered by Cursor's stop hook; see cursorCLIHooksInstalled.
	pending *PendingQueue
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
		openStores: func(ctx context.Context, pids []int) (map[int]string, bool) {
			return cursorCLIOpenStores(ctx, home, pids)
		},
		processes: tmux.SnapshotProcessTree,
		models:    make(map[string]cursorCLIModelEntry),
		sessions:  make(map[string]cursorCLISession),
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
func cursorCLIOpenStores(ctx context.Context, home string, pids []int) (map[int]string, bool) {
	result := make(map[int]string)
	ids := make([]string, 0, len(pids))
	seen := make(map[int]bool, len(pids))
	for _, pid := range pids {
		if pid > 1 && !seen[pid] {
			seen[pid] = true
			ids = append(ids, strconv.Itoa(pid))
		}
	}
	if len(ids) == 0 {
		return result, true
	}
	bin, err := exec.LookPath("lsof")
	if err != nil {
		return result, false
	}
	ctx, cancel := context.WithTimeout(ctx, cursorCLILsofTimeout)
	defer cancel()
	output, err := exec.CommandContext(ctx, bin, "-w", "-a", "-p", strings.Join(ids, ","), "-Fpn").Output()
	if err != nil && (len(output) == 0 || ctx.Err() != nil) {
		// lsof also exits non-zero when one of the processes ended between
		// the process scan and this call; what it printed for the others is
		// still a true answer. No output at all is not.
		return result, false
	}
	return parseCursorCLIOpenStores(home, output), true
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
	openStores, live, liveKnown := s.observeStores(ctx, panePIDs)

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
		// Live means a Cursor process has the store open. Only when that
		// cannot be read does recency stand in for it.
		_, active := live[store]
		if !active && (liveKnown || meta.UpdatedAtMs < cutoff) {
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
		id := cursorCLIChatPrefix + chat.id
		mode := protocol.InjectNone
		state := protocol.StateIdle
		var currentQuestion *protocol.Question
		var status cursorCLIPaneStatus
		if wrapped {
			id = cursorCLIPaneIDPrefix + pane.Name
			mode = protocol.InjectTmux
			state, currentQuestion, status = s.cursorCLIPaneReading(ctx, pane.Name)
		} else if turn, ok := s.cursorCLITurnState(ctx, chat.id); ok && liveKnown {
			// No pane to read: the transcript says whether a turn is open.
			// Only for a chat a process holds; a crashed one would read busy
			// forever.
			state = turn.state
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
		entry.Mode, entry.ContextPercent = status.mode, status.context
		if entry.Mode == "" {
			// No pane to read it from: the chat's store records it.
			entry.Mode = s.cursorCLIStoreMode(ctx, chat.store)
		}
		if wrapped {
			entry.AgentPID = pane.PanePID
			entry.Question = currentQuestion
		} else if pid, ok := live[chat.store]; ok {
			entry.AgentPID = pid
			if s.pending != nil && s.cursorCLIHooksInstalled() {
				entry.Inject = protocol.InjectHook
			}
		}
		result = append(result, entry)
		next[id] = cursorCLISession{meta: entry, store: chat.store, pane: pane.Name}
	}
	for _, pane := range managed {
		if claimedPane[pane.Name] {
			continue
		}
		id := cursorCLIPaneIDPrefix + pane.Name
		entry := protocol.Session{
			ID: id, Kind: protocol.KindCursorCLI, NativeID: pane.Name,
			Name: "Cursor CLI", Cwd: pane.Cwd, State: protocol.StateIdle,
			Inject: protocol.InjectTmux, StartedAt: pane.Created.UnixMilli(),
			LastActivityAt: pane.Created.UnixMilli(), AgentPID: pane.PanePID,
		}
		var status cursorCLIPaneStatus
		entry.State, entry.Question, status = s.cursorCLIPaneReading(ctx, pane.Name)
		entry.Mode, entry.ContextPercent = status.mode, status.context
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
	s.turnMu.Lock()
	for name := range s.paneStatus {
		if !slices.ContainsFunc(managed, func(pane tmux.Session) bool { return pane.Name == name }) {
			delete(s.paneStatus, name)
		}
	}
	for store := range s.storeModes {
		if !slices.ContainsFunc(chats, func(chat foundChat) bool { return chat.store == store }) {
			delete(s.storeModes, store)
		}
	}
	s.turnMu.Unlock()
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

func queryCursorCLIRows(ctx context.Context, store string, before int64, limit int) ([]cursorCLIRow, error) {
	where := ""
	if before > 0 {
		where = " AND rowid < " + strconv.FormatInt(before, 10)
	}
	query := "SELECT rowid,id,CAST(data AS TEXT) AS data FROM blobs WHERE json_valid(data)=1" +
		" AND json_extract(data,'$.role') IN ('user','assistant','tool')" + where +
		" ORDER BY rowid DESC LIMIT " + strconv.Itoa(limit)
	return runCursorCLIQuery(ctx, store, query)
}

func runCursorCLIQuery(ctx context.Context, store, query string) ([]cursorCLIRow, error) {
	bin, err := exec.LookPath("sqlite3")
	if err != nil {
		return nil, fmt.Errorf("source: sqlite3 is required to read Cursor CLI chats: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, cursorCLIDBTimeout)
	defer cancel()
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
	if stdout.Len() == 0 {
		// sqlite3 -json prints nothing at all for an empty result.
		return nil, nil
	}
	var rows []cursorCLIRow
	if err := json.Unmarshal(stdout.Bytes(), &rows); err != nil {
		return nil, fmt.Errorf("source: cursor CLI message page: %w", err)
	}
	return rows, nil
}

func (s *CursorCLISource) Page(ctx context.Context, sessionID, before string, limit int) (protocol.Page, error) {
	s.mu.RLock()
	session, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if !ok {
		// A chat Past found from the folder list has a store and no pane, so
		// it reads exactly like a live one with nothing appending.
		if session, ok = s.pastCursorCLISession(sessionID); !ok {
			return protocol.Page{}, fmt.Errorf("source: unknown Cursor CLI session %q", sessionID)
		}
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
	ordered := make([]parser.CursorCLIRow, 0, len(rows))
	for i := len(rows) - 1; i >= 0; i-- {
		ordered = append(ordered, parser.CursorCLIRow{RowID: rows[i].RowID, ID: rows[i].ID, Data: rows[i].Data})
	}
	// A result is always written after its call. On the newest page it is
	// therefore on the page already, or does not exist yet; an older page
	// can end between the two, and the call would arrive running forever.
	if before != "" && len(ordered) > 0 {
		if pending := parser.CursorCLIPendingCalls(ordered); len(pending) > 0 {
			results, err := queryCursorCLIResults(ctx, session.store, ordered[len(ordered)-1].RowID, pending)
			if err != nil {
				return protocol.Page{}, err
			}
			for _, row := range results {
				ordered = append(ordered, parser.CursorCLIRow{RowID: row.RowID, ID: row.ID, Data: row.Data})
			}
		}
	}
	messages := parser.CursorCLIMessages(sessionID, session.meta.StartedAt, ordered)
	if before == "" && len(ordered) > 0 {
		chatID := filepath.Base(filepath.Dir(session.store))
		if turn, ok := s.cursorCLITurnState(ctx, chatID); ok && turn.failure != "" {
			messages = append(messages, cursorCLIFailureNotice(sessionID, turn, session.meta.StartedAt+ordered[len(ordered)-1].RowID))
		}
	}
	position := ""
	if hasMore && len(rows) > 0 {
		position = strconv.FormatInt(rows[len(rows)-1].RowID, 10)
	}
	return protocol.NewPage(sessionID, messages, position, hasMore), nil
}

// cursorCLIMaxLookups bounds one page's result lookup. A page holds at most
// MaxPageMessages rows, so this is a backstop, not a limit anyone meets.
const cursorCLIMaxLookups = 100

// queryCursorCLIResults reads the results of the given tool calls written
// after rowid after.
func queryCursorCLIResults(ctx context.Context, store string, after int64, callIDs []string) ([]cursorCLIRow, error) {
	if len(callIDs) > cursorCLIMaxLookups {
		callIDs = callIDs[:cursorCLIMaxLookups]
	}
	quoted := make([]string, 0, len(callIDs))
	for _, id := range callIDs {
		quoted = append(quoted, "'"+strings.ReplaceAll(id, "'", "''")+"'")
	}
	query := "SELECT rowid,id,CAST(data AS TEXT) AS data FROM blobs WHERE rowid > " + strconv.FormatInt(after, 10) +
		" AND json_valid(CAST(data AS TEXT))=1 AND json_extract(CAST(data AS TEXT),'$.role')='tool'" +
		" AND EXISTS (SELECT 1 FROM json_each(CAST(data AS TEXT),'$.content')" +
		" WHERE json_extract(value,'$.toolCallId') IN (" + strings.Join(quoted, ",") + "))" +
		" ORDER BY rowid LIMIT " + strconv.Itoa(2*cursorCLIMaxLookups)
	return runCursorCLIQuery(ctx, store, query)
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
	// Each poll is a sqlite3 process reading up to a hundred blobs, some of
	// them tens of kilobytes. Cursor writes through the WAL, so an unchanged
	// store and WAL mean there is nothing new to read.
	s.mu.RLock()
	store := s.sessions[sessionID].store
	s.mu.RUnlock()
	if store == "" {
		if past, ok := s.pastCursorCLISession(sessionID); ok {
			store = past.store
		}
	}
	chatID := filepath.Base(filepath.Dir(store))
	last := cursorCLIStoreVersion(store) + s.cursorCLITranscriptVersion(chatID)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			version := cursorCLIStoreVersion(store) + s.cursorCLITranscriptVersion(chatID)
			if store != "" && version == last && failures == 0 {
				continue
			}
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
			last = version
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
		if session.meta.Inject == protocol.InjectHook && s.pending != nil && session.store != "" {
			// Handed over by the chat's stop hook when its turn ends.
			s.pending.Add(cursorCLIHookKey(filepath.Base(filepath.Dir(session.store))), message)
			return protocol.InjectHook, nil
		}
		return protocol.InjectNone, errors.New("source: start Cursor with `am cursor` to send messages remotely")
	}
	// Every Cursor menu answers single letters, so text typed into one is a
	// string of choices: "yes, but…" approves, "build it" builds the plan.
	if err := s.refuseCursorCLISend(ctx, session.pane); err != nil {
		return protocol.InjectNone, err
	}
	if err := s.typeText(ctx, session.pane, message); err != nil {
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
	// Ctrl-C is only "stop" while a turn runs. At a plan menu it rejects the
	// plan, and at an idle prompt a second one quits the CLI.
	screen, err := s.paneScreen(ctx, session.pane)
	if err != nil {
		return fmt.Errorf("source: could not inspect Cursor CLI before interrupting: %w", err)
	}
	if !screen.busy || screen.question != nil {
		return errors.New("source: Cursor is not running a turn")
	}
	return tmux.Interrupt(ctx, session.pane)
}

// cursorCLIStoreVersion identifies the state of a chat store by the size and
// modification time of the database and its write-ahead log.
func cursorCLIStoreVersion(store string) string {
	if store == "" {
		return ""
	}
	var version strings.Builder
	for _, path := range []string{store, store + "-wal"} {
		if info, err := os.Stat(path); err == nil {
			fmt.Fprintf(&version, "%d:%d;", info.Size(), info.ModTime().UnixNano())
		} else {
			version.WriteString("-;")
		}
	}
	return version.String()
}
