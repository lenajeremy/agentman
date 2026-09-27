package source

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lenajeremy/agentman/internal/jsonl"
	"github.com/lenajeremy/agentman/internal/parser"
	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/question"
	"github.com/lenajeremy/agentman/internal/tmux"
)

// Kiro CLI keeps each session as a family of files in ~/.kiro/sessions/cli:
//
//	<id>.json   metadata: cwd, title, created_at, model
//	<id>.jsonl  the conversation, one event per line
//	<id>.lock   {"pid":…,"started_at":…} while a process has the session open
//
// The lock is what makes Kiro nearly as tractable as Claude. It names the
// process that owns the session, and that process descends from the terminal
// running the Kiro interface — so a session is matched to its tmux pane by
// process ancestry, exactly, rather than by guessing from a directory.
//
// None of this is a published interface. Everything that depends on the layout
// is in this file and parser/kiro.go, so a CLI upgrade breaks in one place.

// kiroPanePrefix names the tmux sessions `am kiro` and the phone launch
// create. The name rather than the running command identifies them: the pane
// runs `kiro-cli`, but a bare pane has not written any session files yet.
const kiroPanePrefix = tmux.Prefix + "kiro-"

// maxKiroMetaBytes bounds the metadata file. It grows by a few kilobytes a
// turn, so this is room for a very long session, not a typical one.
const maxKiroMetaBytes = 32 << 20

// maxKiroTranscriptBytes bounds how much of a transcript a page or a follow
// parses. Kiro timestamps only prompts, so replies are ordered by carrying the
// last prompt's time forwards — which needs a forward read. Past this size
// only the most recent part is read, and the parser picks up at the first
// prompt inside it.
const maxKiroTranscriptBytes int64 = 64 << 20

// KiroSource observes Kiro CLI sessions.
type KiroSource struct {
	home string

	// Injectable so discovery can be tested without tmux or a process table.
	listPanes         func(context.Context) ([]tmux.Session, error)
	snapshotProcesses func(context.Context) (*tmux.ProcessTree, error)
	capturePane       func(context.Context, string) (string, error)

	mu       sync.RWMutex
	sessions map[string]kiroSession

	// cache keeps parsed metadata and each transcript's state keyed on file
	// size and time, so an unchanged session costs one stat per sweep.
	cacheMu sync.Mutex
	metas   map[string]kiroMetaEntry
	states  map[string]kiroStateEntry
	pages   map[string]kiroPageEntry
}

type kiroSession struct {
	meta       protocol.Session
	transcript string
	tmuxName   string
}

type kiroLock struct {
	PID       int    `json:"pid"`
	StartedAt string `json:"started_at"`
}

type kiroMeta struct {
	SessionID    string `json:"session_id"`
	Cwd          string `json:"cwd"`
	CreatedAt    string `json:"created_at"`
	Title        string `json:"title"`
	SessionState struct {
		RTSModelState struct {
			ModelInfo struct {
				ModelID string `json:"model_id"`
			} `json:"model_info"`
		} `json:"rts_model_state"`
	} `json:"session_state"`
}

type kiroMetaEntry struct {
	size  int64
	mtime time.Time
	meta  kiroMeta
}

type kiroStateEntry struct {
	size  int64
	mtime time.Time
	state protocol.State
}

type kiroPageEntry struct {
	size     int64
	mtime    time.Time
	messages []protocol.Message
}

// NewKiroSource creates an adapter rooted at the given home directory. An
// empty string uses the current user's home.
func NewKiroSource(home string) (*KiroSource, error) {
	if home == "" {
		var err error
		if home, err = os.UserHomeDir(); err != nil {
			return nil, err
		}
	}
	return &KiroSource{
		home:              home,
		listPanes:         tmux.List,
		snapshotProcesses: tmux.SnapshotProcessTree,
		capturePane:       tmux.Capture,
		sessions:          map[string]kiroSession{},
		metas:             map[string]kiroMetaEntry{},
		states:            map[string]kiroStateEntry{},
		pages:             map[string]kiroPageEntry{},
	}, nil
}

// Kind implements Source.
func (s *KiroSource) Kind() protocol.Kind { return protocol.KindKiro }

func (s *KiroSource) sessionsDir() string {
	return filepath.Join(s.home, ".kiro", "sessions", "cli")
}

// Discover implements Source.
func (s *KiroSource) Discover(ctx context.Context) ([]protocol.Session, error) {
	entries, err := os.ReadDir(s.sessionsDir())
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	var panes []tmux.Session
	if s.listPanes != nil {
		all, _ := s.listPanes(ctx)
		for _, pane := range all {
			if strings.HasPrefix(pane.Name, kiroPanePrefix) {
				panes = append(panes, pane)
			}
		}
	}
	var locks []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".lock") {
			locks = append(locks, strings.TrimSuffix(entry.Name(), ".lock"))
		}
	}
	var processes *tmux.ProcessTree
	if len(locks) > 0 && s.snapshotProcesses != nil {
		processes, _ = s.snapshotProcesses(ctx)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	found := []protocol.Session{}
	next := map[string]kiroSession{}
	claimed := map[string]bool{}

	for _, native := range locks {
		if !validClaudeSessionID(native) {
			continue
		}
		base := filepath.Join(s.sessionsDir(), native)
		raw, err := readBoundedFile(base+".lock", 4096)
		if err != nil {
			continue // released between the listing and the read
		}
		var lock kiroLock
		if json.Unmarshal(raw, &lock) != nil || !processAlive(lock.PID) {
			continue
		}
		// A crash leaves the lock behind, and in time the operating system
		// hands its pid to an unrelated program. When the snapshot says what
		// that pid runs now, require it to still be Kiro.
		if command := processes.Command(lock.PID); command != "" && !isKiroCommand(command) {
			continue
		}
		meta, ok := s.readMeta(base + ".json")
		if !ok {
			continue
		}

		id := string(protocol.KindKiro) + ":" + native
		tmuxName := ""
		for _, pane := range panes {
			if !claimed[pane.Name] && processes.OwnsPID(pane.PanePID, lock.PID) {
				tmuxName = pane.Name
				claimed[pane.Name] = true
				// Key a pane-backed session on the pane. A phone launch knows
				// the pane before Kiro has chosen a session id, so this is
				// the only id that stays the same across that moment.
				id = string(protocol.KindKiro) + ":" + tmuxID(pane.Name)
				break
			}
		}

		transcript := base + ".jsonl"
		session := protocol.Session{
			ID:        id,
			Kind:      protocol.KindKiro,
			NativeID:  native,
			Name:      kiroName(meta),
			Cwd:       meta.Cwd,
			State:     s.transcriptState(transcript),
			Inject:    protocol.InjectNone,
			StartedAt: parseRFC3339Millis(meta.CreatedAt, lock.StartedAt),
			Model:     meta.SessionState.RTSModelState.ModelInfo.ModelID,
			// Kiro's shell tool runs commands under this process, so a dev
			// server started by the agent descends from it.
			AgentPID: lock.PID,
		}
		session.LastActivityAt = fileMillis(transcript, session.StartedAt)
		if tmuxName != "" {
			session.Inject = protocol.InjectTmux
			s.applyPane(ctx, &session, tmuxName)
		}
		found = append(found, session)
		next[id] = kiroSession{meta: session, transcript: transcript, tmuxName: tmuxName}
	}

	// A pane with no session yet: Kiro is starting, or sitting at its first
	// prompt. It is still a session the user can type into.
	for _, pane := range panes {
		if claimed[pane.Name] {
			continue
		}
		id := string(protocol.KindKiro) + ":" + tmuxID(pane.Name)
		started := pane.Created.UnixMilli()
		if pane.Created.IsZero() {
			started = time.Now().UnixMilli()
		}
		session := protocol.Session{
			ID: id, Kind: protocol.KindKiro, Name: filepath.Base(pane.Cwd), Cwd: pane.Cwd,
			State: protocol.StateIdle, Inject: protocol.InjectTmux,
			StartedAt: started, LastActivityAt: started, AgentPID: pane.PanePID,
		}
		s.applyPane(ctx, &session, pane.Name)
		found = append(found, session)
		next[id] = kiroSession{meta: session, tmuxName: pane.Name}
	}

	s.mu.Lock()
	s.sessions = next
	s.mu.Unlock()
	s.forgetCaches(next)
	return found, nil
}

// applyPane refines a session's state from its terminal, which knows more than
// the transcript: Kiro writes an event only once it is complete, so a reply
// being streamed, or a tool waiting on approval, looks the same on disk as a
// turn that has not started.
func (s *KiroSource) applyPane(ctx context.Context, session *protocol.Session, tmuxName string) {
	capture := s.capturePane
	if capture == nil {
		capture = tmux.Capture
	}
	pane, err := capture(ctx, tmuxName)
	if err != nil {
		return
	}
	if found := question.DetectKiro(pane); found != nil {
		session.Question = protocolQuestion(found)
		session.State = protocol.StateWaitingInput
		return
	}
	if state, ok := kiroPaneState(pane); ok {
		session.State = state
	}
}

// kiroPaneState reads Kiro's input line, which it redraws to say what the
// agent is doing. Only the last few lines are read: the same words could
// appear in the conversation above.
func kiroPaneState(pane string) (protocol.State, bool) {
	lines := strings.Split(strings.TrimRight(pane, "\n"), "\n")
	for i := len(lines) - 1; i >= 0 && i >= len(lines)-6; i-- {
		switch line := lines[i]; {
		case strings.Contains(line, "Kiro is working"):
			return protocol.StateBusy, true
		case strings.Contains(line, "ask a question or describe a task"):
			return protocol.StateIdle, true
		}
	}
	return "", false
}

func isKiroCommand(command string) bool {
	return strings.HasPrefix(filepath.Base(command), "kiro-cli")
}

func kiroName(meta kiroMeta) string {
	if title := strings.Join(strings.Fields(meta.Title), " "); title != "" {
		if runes := []rune(title); len(runes) > 60 {
			return string(runes[:59]) + "…"
		}
		return title
	}
	if base := filepath.Base(meta.Cwd); base != "." && base != string(filepath.Separator) {
		return base
	}
	return "kiro"
}

// readMeta parses a session's metadata, reusing the last parse while the file
// is unchanged.
func (s *KiroSource) readMeta(path string) (kiroMeta, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return kiroMeta{}, false
	}
	s.cacheMu.Lock()
	cached, ok := s.metas[path]
	s.cacheMu.Unlock()
	if ok && cached.size == info.Size() && cached.mtime.Equal(info.ModTime()) {
		return cached.meta, true
	}
	raw, err := readBoundedFile(path, maxKiroMetaBytes)
	if err != nil {
		return kiroMeta{}, false
	}
	var meta kiroMeta
	if json.Unmarshal(raw, &meta) != nil {
		return kiroMeta{}, false
	}
	s.cacheMu.Lock()
	s.metas[path] = kiroMetaEntry{size: info.Size(), mtime: info.ModTime(), meta: meta}
	s.cacheMu.Unlock()
	return meta, true
}

// transcriptState decides busy or idle from the last event on disk.
//
// A turn ends with an assistant message that makes no tool call — the reply,
// or Kiro's "Response was interrupted by the user". Anything else last means
// the turn is still going: a prompt awaiting a reply, a tool call awaiting its
// result, or a result awaiting the next step.
func (s *KiroSource) transcriptState(path string) protocol.State {
	info, err := os.Stat(path)
	if err != nil {
		return protocol.StateIdle
	}
	s.cacheMu.Lock()
	cached, ok := s.states[path]
	s.cacheMu.Unlock()
	if ok && cached.size == info.Size() && cached.mtime.Equal(info.ModTime()) {
		return cached.state
	}
	state := protocol.StateIdle
	if line, err := lastLine(path, 32<<20); err == nil && line != "" {
		state = kiroLineState(line)
	}
	s.cacheMu.Lock()
	s.states[path] = kiroStateEntry{size: info.Size(), mtime: info.ModTime(), state: state}
	s.cacheMu.Unlock()
	return state
}

func kiroLineState(line string) protocol.State {
	var event struct {
		Kind string `json:"kind"`
		Data struct {
			Content []struct {
				Kind string `json:"kind"`
			} `json:"content"`
		} `json:"data"`
	}
	if json.Unmarshal([]byte(line), &event) != nil {
		return protocol.StateIdle
	}
	switch event.Kind {
	case "Prompt", "ToolResults":
		return protocol.StateBusy
	case "AssistantMessage":
		for _, block := range event.Data.Content {
			if block.Kind == "toolUse" {
				return protocol.StateBusy
			}
		}
	}
	return protocol.StateIdle
}

func (s *KiroSource) forgetCaches(live map[string]kiroSession) {
	keep := map[string]bool{}
	for _, session := range live {
		if session.transcript != "" {
			keep[session.transcript] = true
			keep[strings.TrimSuffix(session.transcript, ".jsonl")+".json"] = true
		}
	}
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	for path := range s.metas {
		if !keep[path] {
			delete(s.metas, path)
		}
	}
	for path := range s.states {
		if !keep[path] {
			delete(s.states, path)
		}
	}
	for path := range s.pages {
		if !keep[path] {
			delete(s.pages, path)
		}
	}
}

func (s *KiroSource) session(sessionID string) (kiroSession, error) {
	s.mu.RLock()
	session, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if !ok {
		return kiroSession{}, fmt.Errorf("source: unknown kiro session %q", sessionID)
	}
	return session, nil
}

// Page implements Source.
//
// The cursor is the index of the oldest message already shown. Kiro only ever
// appends, and a settled tool call replaces its running row in place, so an
// index never shifts under a client paging backwards.
func (s *KiroSource) Page(ctx context.Context, sessionID, before string, limit int) (protocol.Page, error) {
	session, err := s.session(sessionID)
	if err != nil {
		return protocol.Page{}, err
	}
	if session.transcript == "" {
		return protocol.NewPage(sessionID, nil, "", false), nil
	}
	messages, err := s.readTranscript(ctx, session.transcript, sessionID)
	if err != nil {
		if os.IsNotExist(err) {
			return protocol.NewPage(sessionID, nil, "", false), nil
		}
		return protocol.Page{}, err
	}
	end := len(messages)
	if before != "" {
		index, err := strconv.Atoi(before)
		if err != nil || index < 0 {
			return protocol.Page{}, fmt.Errorf("source: bad cursor %q", before)
		}
		end = min(index, len(messages))
	}
	if limit <= 0 {
		limit = 30
	}
	start := max(0, end-limit)
	page := append([]protocol.Message(nil), messages[start:end]...)
	cursor := ""
	if start > 0 {
		cursor = strconv.Itoa(start)
	}
	return protocol.NewPage(sessionID, page, cursor, start > 0), nil
}

// readTranscript parses a whole transcript into its feed, reusing the last
// parse while the file is unchanged.
func (s *KiroSource) readTranscript(ctx context.Context, path, sessionID string) ([]protocol.Message, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	s.cacheMu.Lock()
	cached, ok := s.pages[path]
	s.cacheMu.Unlock()
	if ok && cached.size == info.Size() && cached.mtime.Equal(info.ModTime()) {
		return cached.messages, nil
	}

	p := parser.NewKiroParser(sessionID)
	var messages []protocol.Message
	at := map[string]int{}
	_, err = kiroReadForward(ctx, path, info, p, func(batch []protocol.Message) {
		for _, message := range batch {
			// A tool result re-emits its call under the same id; replace the
			// running row where it stands rather than adding a second one.
			if index, seen := at[message.ID]; seen {
				messages[index] = message
				continue
			}
			at[message.ID] = len(messages)
			messages = append(messages, message)
		}
	})
	if err != nil {
		return nil, err
	}
	s.cacheMu.Lock()
	s.pages[path] = kiroPageEntry{size: info.Size(), mtime: info.ModTime(), messages: messages}
	s.cacheMu.Unlock()
	return messages, nil
}

// kiroReadForward feeds a transcript to p from its start — or from the start of
// its last maxKiroTranscriptBytes — and returns the tail positioned at its end,
// ready to follow.
func kiroReadForward(
	ctx context.Context,
	path string,
	info os.FileInfo,
	p *parser.KiroParser,
	emit func([]protocol.Message),
) (*jsonl.Tail, error) {
	tail := jsonl.NewTail(path)
	skipPartial := false
	if info != nil && info.Size() > maxKiroTranscriptBytes {
		tail.SeekToOffset(info.Size() - maxKiroTranscriptBytes)
		skipPartial = true
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		lines, err := tail.Read()
		if err != nil {
			return nil, err
		}
		if len(lines) == 0 {
			return tail, nil
		}
		for _, line := range lines {
			if skipPartial {
				// The window starts mid-line; the first line is a fragment.
				skipPartial = false
				continue
			}
			if batch := p.Parse(line.Text, line.Offset); len(batch) > 0 {
				emit(batch)
			}
		}
	}
}

// Follow implements Source.
func (s *KiroSource) Follow(ctx context.Context, sessionID string, out chan<- []protocol.Message) error {
	session, err := s.session(sessionID)
	if err != nil {
		return err
	}

	var (
		path string
		tail *jsonl.Tail
		p    *parser.KiroParser
	)
	// attach reads the transcript up to its end without sending anything: the
	// backlog belongs to Page. What it builds is the parser's memory — the
	// current turn's time and the calls awaiting results — so the next line
	// appended is stamped and paired correctly.
	attach := func(transcript string) error {
		path, tail, p = transcript, nil, parser.NewKiroParser(sessionID)
		if transcript == "" {
			return nil
		}
		info, err := os.Stat(transcript)
		if err != nil {
			if os.IsNotExist(err) {
				tail = jsonl.NewTail(transcript)
				return nil
			}
			return err
		}
		tail, err = kiroReadForward(ctx, transcript, info, p, func([]protocol.Message) {})
		return err
	}
	if err := attach(session.transcript); err != nil {
		return err
	}

	ticker := time.NewTicker(followInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
		// The pane can move to a different session — Kiro resumes one, or a
		// phone-launched pane writes its first files — and a follow held on
		// the old path would go quiet. Everything in a newly bound transcript
		// is new to this subscription, so it is read from the start.
		current, err := s.session(sessionID)
		if err != nil {
			return fmt.Errorf("source: kiro session %q ended", sessionID)
		}
		if current.transcript != path {
			path, tail, p = current.transcript, jsonl.NewTail(current.transcript), parser.NewKiroParser(sessionID)
		}
		if tail == nil {
			continue
		}
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

// Inject implements Injector.
func (s *KiroSource) Inject(ctx context.Context, sessionID, text string) (protocol.InjectMode, error) {
	session, err := s.session(sessionID)
	if err != nil {
		return protocol.InjectNone, err
	}
	if session.tmuxName == "" {
		return protocol.InjectNone, errors.New(
			"source: this session cannot receive messages — start it with `am kiro` to enable sending")
	}
	if err := refuseSendIntoMenu(ctx, s.capturePane, session.tmuxName, question.DetectKiro); err != nil {
		return protocol.InjectNone, err
	}
	// Mid-turn, Kiro reads typed text as steering for the running turn rather
	// than as a new prompt. That is the right behaviour for a message sent
	// while the agent works, so nothing needs to wait for the turn to end.
	if err := tmux.Send(ctx, session.tmuxName, text); err != nil {
		return protocol.InjectNone, err
	}
	return protocol.InjectTmux, nil
}

// Interrupt implements Interrupter.
func (s *KiroSource) Interrupt(ctx context.Context, sessionID string) error {
	session, err := s.session(sessionID)
	if err != nil {
		return err
	}
	if session.tmuxName == "" {
		return errors.New("source: only sessions started with `am kiro` can be stopped from the phone")
	}
	return tmux.Escape(ctx, session.tmuxName)
}

// Answer implements Answerer.
func (s *KiroSource) Answer(ctx context.Context, sessionID string, answer protocol.QuestionAnswer) error {
	session, err := s.session(sessionID)
	if err != nil {
		return err
	}
	return answerMenu(ctx, s.capturePane, session.tmuxName, session.meta.Question, answer, question.DetectKiro)
}

// CurrentQuestion implements QuestionInspector.
func (s *KiroSource) CurrentQuestion(ctx context.Context, sessionID string) (*protocol.Question, error) {
	session, err := s.session(sessionID)
	if err != nil || session.tmuxName == "" {
		return nil, err
	}
	found, err := paneMenu(ctx, s.capturePane, session.tmuxName, question.DetectKiro)
	if err != nil || found == nil {
		return nil, err
	}
	return protocolQuestion(found), nil
}

// parseRFC3339Millis returns the first of values that parses, in milliseconds.
func parseRFC3339Millis(values ...string) int64 {
	for _, value := range values {
		if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
			return t.UnixMilli()
		}
	}
	return time.Now().UnixMilli()
}

// fileMillis is a file's modification time in milliseconds, or fallback.
func fileMillis(path string, fallback int64) int64 {
	if info, err := os.Stat(path); err == nil {
		return info.ModTime().UnixMilli()
	}
	return fallback
}

// lastLine returns the final complete line of a file, reading at most limit
// bytes from its end.
func lastLine(path string, limit int64) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	size := info.Size()
	window := int64(64 << 10)
	for {
		window = min(window, size, limit)
		buf := make([]byte, window)
		if _, err := file.ReadAt(buf, size-window); err != nil {
			return "", err
		}
		text := strings.TrimRight(string(buf), "\n")
		if cut := strings.LastIndexByte(text, '\n'); cut >= 0 {
			return text[cut+1:], nil
		}
		if window == size || window == limit {
			// One line fills the whole window: it is the whole file, or too
			// long to read. Either way this is as much as there is.
			if window == size {
				return text, nil
			}
			return "", fmt.Errorf("source: last line of %s exceeds %d bytes", path, limit)
		}
		window *= 4
	}
}
