package source

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/lenajeremy/agentman/internal/parser"
	"github.com/lenajeremy/agentman/internal/protocol"
)

// MuseSource follows Meta Muse through its protocol (MSP), never through a
// terminal: the sessions come from session/list and Muse's live registry,
// history from session/read, and a thread live from its view. See
// muse_host.go for the connection.

const (
	// museLiveWindow keeps a session listed for this long after its last
	// activity, as Codex's are.
	museLiveWindow = 30 * time.Minute
	// musePoll is how often a thread hosted by a plain terminal, which the
	// daemon cannot watch, is read again while someone has it open.
	musePoll   = 2 * time.Second
	musePrefix = "muse:"
)

type MuseSource struct {
	host    *museHost
	home    string // Muse's data directory; for the live registry
	holds   museHolds
	answers museAnswers

	mu     sync.Mutex
	cancel context.CancelFunc
}

// NewMuseSource returns the adapter; Connect starts its connection.
func NewMuseSource() *MuseSource {
	return &MuseSource{host: newMuseHost(museServe)}
}

func newMuseSourceWith(host *museHost, home string) *MuseSource {
	return &MuseSource{host: host, home: home}
}

// Kind implements Source.
func (s *MuseSource) Kind() protocol.Kind { return protocol.KindMuse }

// Connect starts the connection to Muse; only the long-running daemon does.
func (s *MuseSource) Connect() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	go s.host.run(ctx)
}

// Close lets go of any session Agentman is driving, so a terminal can open
// it, and ends the connection.
func (s *MuseSource) Close() {
	s.holds.mu.Lock()
	held := make([]string, 0, len(s.holds.held))
	for native := range s.holds.held {
		held = append(held, native)
	}
	s.holds.mu.Unlock()
	for _, native := range held {
		s.letGo(native)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
}

// museSession is MSP's Session, with the fields Agentman shows.
type museSession struct {
	SessionID     string   `json:"sessionId"`
	Status        string   `json:"status"`
	Name          string   `json:"name"`
	GeneratedName string   `json:"generatedName"`
	Title         string   `json:"title"`
	FirstPrompt   string   `json:"firstUserPrompt"`
	WorkspaceRoot string   `json:"workspaceRoot"`
	ModelID       string   `json:"modelId"`
	CreatedAt     string   `json:"createdAt"`
	UpdatedAt     string   `json:"updatedAt"`
	LastActivity  string   `json:"lastActivityAt"`
	Attention     []string `json:"attention"`
	Kind          string   `json:"kind"`
	TurnCount     int      `json:"turnCount"`
}

type museSessionList struct {
	Sessions   []museSession `json:"sessions"`
	NextCursor *string       `json:"nextCursor"`
}

type museRead struct {
	Session museSession `json:"session"`
	History struct {
		Mode  string            `json:"mode"`
		Items []parser.MuseItem `json:"items"`
	} `json:"history"`
	LastTurn *parser.MuseTurnEnd `json:"lastTurn"`
}

// Discover implements Source: sessions active in the last half hour, and
// every session a terminal has open now however quiet it is.
func (s *MuseSource) Discover(ctx context.Context) ([]protocol.Session, error) {
	// Only the long-running daemon connects; elsewhere (`am list`) Muse is
	// simply not looked at, rather than waited for.
	if !s.host.isUp() {
		return nil, nil
	}
	var recent museSessionList
	params := map[string]any{"limit": 200, "updatedAfter": time.Now().Add(-museLiveWindow).UTC().Format(time.RFC3339)}
	if err := s.host.call(ctx, "session/list", params, &recent); err != nil {
		// Muse not installed, or not reachable: nothing to show.
		return nil, nil
	}
	open := s.openInTerminal()
	found := make([]protocol.Session, 0, len(recent.Sessions)+len(open))
	seen := map[string]bool{}
	for _, session := range recent.Sessions {
		if session.Kind != "" && session.Kind != "root" {
			continue
		}
		seen[session.SessionID] = true
		found = append(found, museToSession(session, open[session.SessionID]))
		if s.holding(session.SessionID) {
			found[len(found)-1].Inject = protocol.InjectAPI
		}
	}
	for id := range open {
		if seen[id] {
			continue
		}
		var read museRead
		if s.host.call(ctx, "session/read", map[string]any{"sessionId": id, "excludeItems": true}, &read) == nil {
			found = append(found, museToSession(read.Session, true))
		}
	}
	// What a waiting agent asks, for the apps' question card.
	for i := range found {
		if found[i].State == protocol.StateWaitingInput {
			if question, err := s.CurrentQuestion(ctx, found[i].ID); err == nil {
				found[i].Question = question
			}
		}
	}
	return found, nil
}

func museToSession(session museSession, inTerminal bool) protocol.Session {
	meta := protocol.Session{
		ID:             musePrefix + session.SessionID,
		Kind:           protocol.KindMuse,
		NativeID:       session.SessionID,
		Name:           museName(session),
		Cwd:            session.WorkspaceRoot,
		Model:          session.ModelID,
		StartedAt:      museMillis(session.CreatedAt),
		LastActivityAt: max(museMillis(session.UpdatedAt), museMillis(session.LastActivity)),
		State:          protocol.StateIdle,
		// Agentman sends by taking the session for a turn; while a terminal
		// drives it, Muse refuses, so the apps show it to read.
		Inject: protocol.InjectAPI,
	}
	if inTerminal {
		meta.Inject = protocol.InjectNone
	}
	switch {
	case museAttention(session.Attention):
		meta.State = protocol.StateWaitingInput
	case session.Status == "running":
		meta.State = protocol.StateBusy
	}
	return meta
}

func museAttention(flags []string) bool {
	for _, flag := range flags {
		if flag == "approvalPending" || flag == "inputPending" {
			return true
		}
	}
	return false
}

// museName is what Muse calls the session: the name it was given, its
// title, the prompt it opened with, else its folder.
func museName(session museSession) string {
	for _, name := range []string{session.Name, session.Title, session.GeneratedName, session.FirstPrompt} {
		name = strings.TrimSpace(name)
		if name != "" && name != "New session" {
			return name
		}
	}
	if base := filepath.Base(session.WorkspaceRoot); base != "." && base != "/" {
		return base
	}
	return "muse"
}

func museMillis(at string) int64 {
	if at == "" {
		return 0
	}
	if when, err := time.Parse(time.RFC3339Nano, at); err == nil {
		return when.UnixMilli()
	}
	return 0
}

// openInTerminal reads Muse's live registry: each session a running muse
// process hosts, kept only while that process is alive.
func (s *MuseSource) openInTerminal() map[string]bool {
	open := map[string]bool{}
	home := s.museHome()
	if home == "" {
		return open
	}
	entries, err := os.ReadDir(filepath.Join(home, "runtime", "muse", "sessions"))
	if err != nil {
		return open
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(home, "runtime", "muse", "sessions", entry.Name()))
		if err != nil {
			continue
		}
		var record struct {
			SessionID string `json:"session_id"`
			Process   string `json:"process_generation_hint"`
		}
		if json.Unmarshal(data, &record) != nil || record.SessionID == "" {
			continue
		}
		pid := musePid(record.Process)
		if pid > 0 && int64(pid) != s.host.child.Load() && syscall.Kill(pid, 0) == nil {
			open[record.SessionID] = true
		}
	}
	return open
}

func (s *MuseSource) museHome() string {
	if s.home != "" {
		return s.home
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	if data := os.Getenv("XDG_DATA_HOME"); data != "" {
		return filepath.Join(data, "muse")
	}
	return filepath.Join(home, ".local", "share", "muse")
}

// musePid reads "pid=1234".
func musePid(hint string) int {
	value, ok := strings.CutPrefix(hint, "pid=")
	if !ok {
		return 0
	}
	pid := 0
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0
		}
		pid = pid*10 + int(r-'0')
	}
	return pid
}

// Page implements Source: the session's history, read without taking it.
func (s *MuseSource) Page(ctx context.Context, sessionID, before string, limit int) (protocol.Page, error) {
	messages, err := s.read(ctx, sessionID)
	if err != nil {
		return protocol.Page{}, err
	}
	return musePage(sessionID, messages, before, limit), nil
}

func (s *MuseSource) read(ctx context.Context, sessionID string) ([]protocol.Message, error) {
	messages, _, err := s.readWithStatus(ctx, sessionID)
	return messages, err
}

// readWithStatus also says whether the session is loaded where Muse's
// daemon can watch it ("idle", "running") or hosted elsewhere ("notLoaded").
func (s *MuseSource) readWithStatus(ctx context.Context, sessionID string) ([]protocol.Message, string, error) {
	native := strings.TrimPrefix(sessionID, musePrefix)
	var read museRead
	if err := s.host.call(ctx, "session/read", map[string]any{"sessionId": native, "excludeItems": false}, &read); err != nil {
		return nil, "", err
	}
	messages := parser.MuseMessages(sessionID, read.History.Items)
	// Muse keeps how the last turn ended beside the items; a failure shows
	// after that turn's rows (earlier failures are not kept).
	if read.LastTurn != nil {
		at := int64(0)
		if len(messages) > 0 {
			at = messages[len(messages)-1].Ts + 1
		}
		if row, ok := parser.MuseTurnFailed(sessionID, *read.LastTurn, at); ok {
			messages = append(messages, row)
		}
	}
	return messages, read.Session.Status, nil
}

// musePage cuts the messages before a cursor (a message id) to a page.
func musePage(sessionID string, messages []protocol.Message, before string, limit int) protocol.Page {
	end := len(messages)
	if before != "" {
		for i, message := range messages {
			if message.ID == before {
				end = i
				break
			}
		}
	}
	if limit <= 0 {
		limit = 50
	}
	start := max(0, end-limit)
	page := append([]protocol.Message(nil), messages[start:end]...)
	cursor := ""
	if start > 0 && len(page) > 0 {
		cursor = page[0].ID
	}
	return protocol.NewPage(sessionID, page, cursor, start > 0)
}

// Follow implements Source. A session in Muse's daemon streams its view: a
// reply grows in place as it is written, under the id its finished item
// keeps. One a plain terminal hosts in its own process, which the daemon
// cannot watch, is read again every few seconds, and sent on when it changed.
func (s *MuseSource) Follow(ctx context.Context, sessionID string, out chan<- []protocol.Message) error {
	native := strings.TrimPrefix(sessionID, musePrefix)
	live := newMuseLive(sessionID)
	updates := make(chan []protocol.Message, 64)
	stopListening := s.host.listen(func(message museMessage) {
		if rows := live.apply(native, message); len(rows) > 0 {
			select {
			case updates <- rows:
			default: // a slow reader catches up on the next read
			}
		}
	})
	defer stopListening()
	unwatch := s.host.watch(ctx, native)
	defer unwatch()

	ticker := time.NewTicker(musePoll)
	defer ticker.Stop()
	last := ""
	for {
		select {
		case <-ctx.Done():
			return nil
		case rows := <-updates:
			select {
			case out <- rows:
			case <-ctx.Done():
				return nil
			}
		case <-ticker.C:
			if !s.openInTerminal()[native] {
				continue
			}
			messages, status, err := s.readWithStatus(ctx, sessionID)
			if err != nil || len(messages) == 0 || status != "notLoaded" {
				continue
			}
			tail := messages[max(0, len(messages)-20):]
			if digest := museDigest(tail); digest != last {
				last = digest
				select {
				case out <- tail:
				case <-ctx.Done():
					return nil
				}
			}
		}
	}
}

// museDigest stands for a run of rows, to tell when a re-read found news.
func museDigest(messages []protocol.Message) string {
	var b strings.Builder
	for _, message := range messages {
		b.WriteString(message.ID)
		b.WriteByte(':')
		b.WriteString(message.Text)
		if message.Tool != nil {
			b.WriteString(string(message.Tool.Status))
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// museLive turns a session's view notifications into rows: a finished item
// as it is, and a reply still being written as the text so far.
type museLive struct {
	sessionID string
	mu        sync.Mutex
	kinds     map[string]string
	texts     map[string]*strings.Builder
}

func newMuseLive(sessionID string) *museLive {
	return &museLive{sessionID: sessionID, kinds: map[string]string{}, texts: map[string]*strings.Builder{}}
}

func (l *museLive) apply(native string, message museMessage) []protocol.Message {
	var params struct {
		SessionID string          `json:"sessionId"`
		Item      parser.MuseItem `json:"item"`
		ItemID    string          `json:"itemId"`
		Field     string          `json:"field"`
		Delta     string          `json:"delta"`
	}
	if json.Unmarshal(message.Params, &params) != nil || params.SessionID != native {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	switch message.Method {
	case "item/started", "item/updated":
		l.kinds[params.Item.ItemID] = params.Item.Kind
		if row, ok := parser.MuseMessage(l.sessionID, params.Item); ok {
			return []protocol.Message{row}
		}
	case "item/delta":
		if params.Field != "text" || l.kinds[params.ItemID] != "agentMessage" {
			return nil
		}
		text := l.texts[params.ItemID]
		if text == nil {
			text = &strings.Builder{}
			l.texts[params.ItemID] = text
		}
		text.WriteString(params.Delta)
		return []protocol.Message{{
			ID: params.ItemID, SessionID: l.sessionID, Role: protocol.RoleAssistant,
			Ts: time.Now().UnixMilli(), Text: text.String(),
		}}
	case "item/completed":
		delete(l.kinds, params.Item.ItemID)
		delete(l.texts, params.Item.ItemID)
		if row, ok := parser.MuseMessage(l.sessionID, params.Item); ok {
			return []protocol.Message{row}
		}
	case "turn/completed":
		var end parser.MuseTurnEnd
		if json.Unmarshal(message.Params, &end) == nil {
			if row, ok := parser.MuseTurnFailed(l.sessionID, end, time.Now().UnixMilli()); ok {
				return []protocol.Message{row}
			}
		}
	}
	return nil
}
