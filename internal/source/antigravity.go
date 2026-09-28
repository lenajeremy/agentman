package source

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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

// Antigravity CLI (`agy`) keeps its state under ~/.gemini/antigravity-cli:
//
//	conversations/<id>.db   the trajectory, as protobuf rows in SQLite
//	brain/<id>/             the conversation's working directory, including
//	                        .system_generated/logs/transcript_full.jsonl
//	presence/<id>.lock      created for a conversation and never removed
//	history.jsonl           typed prompts, written lazily — often at exit
//
// Neither the presence lock nor history.jsonl says what is running now: the
// lock outlives the process, and history lags it. What does is the process
// itself. A running agy holds its conversation's brain directory open —
// verified on idle and busy sessions alike, where the database is sometimes
// released — so lsof on the agy processes maps each one to its conversation
// and its working directory. That is the same technique the Cursor adapter
// uses for its chat stores.
//
// None of this is a published interface. Everything that depends on it is in
// this file and parser/antigravity.go.

// antigravityPanePrefix names the tmux sessions `am antigravity` and the phone
// launch create.
const antigravityPanePrefix = tmux.Prefix + "antigravity-"

const antigravityLsofTimeout = 3 * time.Second

// antigravityFollowSeed is how much of a transcript a follow reads silently
// when it attaches. A tool call made just before the follow started can have
// its result arrive just after; without the call in the parser's memory that
// result has no row to settle.
const antigravityFollowSeed int64 = 256 << 10

// AntigravitySource observes Antigravity CLI conversations.
type AntigravitySource struct {
	home string

	// Injectable so discovery can be tested without tmux, a process table, or
	// lsof.
	listPanes         func(context.Context) ([]tmux.Session, error)
	snapshotProcesses func(context.Context) (*tmux.ProcessTree, error)
	capturePane       func(context.Context, string) (string, error)
	// captureScrollback reads the pane plus its history, which is the only
	// place a reply exists while agy is writing it. Injectable for the same
	// reason as the rest: tests must not read the host's tmux server.
	captureScrollback func(context.Context, string, int) (string, error)
	openConversations func(context.Context, string, []int) map[int]antigravityProcess

	mu       sync.RWMutex
	sessions map[string]antigravitySession

	// past holds transcripts of sessions that have already exited, found by
	// Past rather than by a sweep. See pastSessions.
	past pastSessions

	cacheMu sync.Mutex
	states  map[string]kiroStateEntry
	names   map[string]string
}

type antigravitySession struct {
	meta       protocol.Session
	transcript string
	tmuxName   string
}

// antigravityProcess is what lsof shows one agy process holding.
type antigravityProcess struct {
	cwd           string
	conversations []string
}

// NewAntigravitySource creates an adapter rooted at the given home directory.
// An empty string uses the current user's home.
func NewAntigravitySource(home string) (*AntigravitySource, error) {
	if home == "" {
		var err error
		if home, err = os.UserHomeDir(); err != nil {
			return nil, err
		}
	}
	return &AntigravitySource{
		home:              home,
		listPanes:         tmux.List,
		snapshotProcesses: tmux.SnapshotProcessTree,
		capturePane:       tmux.Capture,
		captureScrollback: tmux.CaptureScrollback,
		openConversations: antigravityOpenConversations,
		sessions:          map[string]antigravitySession{},
		states:            map[string]kiroStateEntry{},
		names:             map[string]string{},
	}, nil
}

// Kind implements Source.
func (s *AntigravitySource) Kind() protocol.Kind { return protocol.KindAntigravity }

func (s *AntigravitySource) root() string {
	return filepath.Join(s.home, ".gemini", "antigravity-cli")
}

func (s *AntigravitySource) transcriptPath(conversation string) string {
	return filepath.Join(s.root(), "brain", conversation, ".system_generated", "logs", "transcript_full.jsonl")
}

// Discover implements Source.
func (s *AntigravitySource) Discover(ctx context.Context) ([]protocol.Session, error) {
	var panes []tmux.Session
	if s.listPanes != nil {
		all, _ := s.listPanes(ctx)
		for _, pane := range all {
			if strings.HasPrefix(pane.Name, antigravityPanePrefix) {
				panes = append(panes, pane)
			}
		}
	}
	var processes *tmux.ProcessTree
	if s.snapshotProcesses != nil {
		processes, _ = s.snapshotProcesses(ctx)
	}
	var pids []int
	for _, pid := range processes.PIDs() {
		if filepath.Base(processes.Command(pid)) == "agy" {
			pids = append(pids, pid)
		}
	}
	var open map[int]antigravityProcess
	if len(pids) > 0 && s.openConversations != nil {
		open = s.openConversations(ctx, s.home, pids)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	found := []protocol.Session{}
	next := map[string]antigravitySession{}
	claimed := map[string]bool{}
	seen := map[string]bool{}

	for _, pid := range pids {
		process, ok := open[pid]
		if !ok || len(process.conversations) == 0 {
			continue // at its trust prompt, or not a chat at all (mic-serve, remote-control)
		}
		conversation := s.latestConversation(process.conversations)
		if seen[conversation] {
			continue
		}
		seen[conversation] = true

		id := string(protocol.KindAntigravity) + ":" + conversation
		tmuxName := ""
		for _, pane := range panes {
			if !claimed[pane.Name] && processes.OwnsPID(pane.PanePID, pid) {
				tmuxName = pane.Name
				claimed[pane.Name] = true
				// As with Kiro, a pane-backed session is keyed on the pane,
				// which a phone launch knows before agy picks a conversation.
				id = string(protocol.KindAntigravity) + ":" + tmuxID(pane.Name)
				break
			}
		}

		transcript := s.transcriptPath(conversation)
		started := fileMillis(filepath.Join(s.root(), "presence", conversation+".lock"), time.Now().UnixMilli())
		session := protocol.Session{
			ID:             id,
			Kind:           protocol.KindAntigravity,
			NativeID:       conversation,
			Name:           s.conversationName(transcript, process.cwd),
			Cwd:            process.cwd,
			State:          s.transcriptState(transcript),
			Inject:         protocol.InjectNone,
			StartedAt:      started,
			LastActivityAt: fileMillis(transcript, started),
			Model:          s.defaultModel(),
			AgentPID:       pid,
		}
		if tmuxName != "" {
			session.Inject = protocol.InjectTmux
			s.applyPane(ctx, &session, tmuxName)
		}
		found = append(found, session)
		next[id] = antigravitySession{meta: session, transcript: transcript, tmuxName: tmuxName}
	}

	// A pane with no conversation yet: agy at its folder trust prompt, or
	// waiting for a first message. Both are things the phone can act on.
	for _, pane := range panes {
		if claimed[pane.Name] {
			continue
		}
		id := string(protocol.KindAntigravity) + ":" + tmuxID(pane.Name)
		started := pane.Created.UnixMilli()
		if pane.Created.IsZero() {
			started = time.Now().UnixMilli()
		}
		session := protocol.Session{
			ID: id, Kind: protocol.KindAntigravity, Name: filepath.Base(pane.Cwd), Cwd: pane.Cwd,
			State: protocol.StateIdle, Inject: protocol.InjectTmux, Model: s.defaultModel(),
			StartedAt: started, LastActivityAt: started, AgentPID: pane.PanePID,
		}
		s.applyPane(ctx, &session, pane.Name)
		found = append(found, session)
		next[id] = antigravitySession{meta: session, tmuxName: pane.Name}
	}

	s.mu.Lock()
	s.sessions = next
	s.mu.Unlock()
	s.forgetCaches(next)
	return found, nil
}

// latestConversation picks the conversation a process is actually in. agy can
// hold more than one open after switching; the one written to last is it.
func (s *AntigravitySource) latestConversation(conversations []string) string {
	best, bestTime := conversations[0], int64(-1)
	for _, conversation := range conversations {
		if at := fileMillis(s.transcriptPath(conversation), -1); at > bestTime {
			best, bestTime = conversation, at
		}
	}
	return best
}

// antigravityModelLabel is the model agy right-aligns on its bottom row:
// "? for shortcuts            Gemini 3.8 Flash · high". It is matched against
// the last column only — split on runs of spaces — because a pattern over the
// whole row happily swallows the footer to its left.
var antigravityModelLabel = regexp.MustCompile(`^(\S.*?\S)\s+·\s+(low|medium|high|max)$`)

var columnGap = regexp.MustCompile(`\s{2,}`)

// applyPane refines a session from its terminal. agy writes a step only once
// it completes, so a reply still being generated looks, on disk, exactly like
// a turn that has not started — and a permission prompt is not on disk at all.
func (s *AntigravitySource) applyPane(ctx context.Context, session *protocol.Session, tmuxName string) {
	capture := s.capturePane
	if capture == nil {
		capture = tmux.Capture
	}
	pane, err := capture(ctx, tmuxName)
	if err != nil {
		return
	}
	lines := strings.Split(strings.TrimRight(pane, "\n"), "\n")
	if last := strings.TrimSpace(lastNonBlankLine(lines)); last != "" {
		columns := columnGap.Split(last, -1)
		if match := antigravityModelLabel.FindStringSubmatch(columns[len(columns)-1]); match != nil {
			session.Model = match[1] + " (" + match[2] + ")"
		}
	}
	if found := question.DetectAntigravity(pane); found != nil {
		session.Question = protocolQuestion(found)
		session.State = protocol.StateWaitingInput
		return
	}
	if state, ok := antigravityPaneState(lines); ok {
		session.State = state
	}
}

// antigravityPaneState reads agy's footer, which says what it is doing. Only
// the bottom rows are read: the same words could appear in the conversation.
func antigravityPaneState(lines []string) (protocol.State, bool) {
	for i := len(lines) - 1; i >= 0 && i >= len(lines)-3; i-- {
		switch {
		case strings.Contains(lines[i], "esc to cancel"):
			return protocol.StateBusy, true
		case strings.Contains(lines[i], "? for shortcuts"):
			return protocol.StateIdle, true
		}
	}
	return "", false
}

// AntigravityReadyForInput reports whether an agy pane is idle at its prompt,
// with no menu open — past the folder trust prompt, in particular.
func AntigravityReadyForInput(pane string) bool {
	if question.DetectAntigravity(pane) != nil {
		return false
	}
	state, ok := antigravityPaneState(strings.Split(strings.TrimRight(pane, "\n"), "\n"))
	return ok && state == protocol.StateIdle
}

func lastNonBlankLine(lines []string) string {
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			return lines[i]
		}
	}
	return ""
}

// defaultModel is the model agy starts new conversations with. A pane shows
// the one actually in use and overrides it; without a pane this is the best
// available answer.
func (s *AntigravitySource) defaultModel() string {
	raw, err := readBoundedFile(filepath.Join(s.root(), "settings.json"), 1<<20)
	if err != nil {
		return ""
	}
	var settings struct {
		Model string `json:"model"`
	}
	if json.Unmarshal(raw, &settings) != nil {
		return ""
	}
	return settings.Model
}

// conversationName is the first thing the user asked, which reads better in a
// list than a directory name. The first step never changes, so it is read once.
func (s *AntigravitySource) conversationName(transcript, cwd string) string {
	s.cacheMu.Lock()
	name, ok := s.names[transcript]
	s.cacheMu.Unlock()
	if !ok {
		name = firstAntigravityRequest(transcript)
		if name != "" {
			s.cacheMu.Lock()
			s.names[transcript] = name
			s.cacheMu.Unlock()
		}
	}
	if name != "" {
		return name
	}
	if base := filepath.Base(cwd); base != "." && base != string(filepath.Separator) {
		return base
	}
	return "antigravity"
}

func firstAntigravityRequest(transcript string) string {
	tail := jsonl.NewTail(transcript)
	for range 4 { // the first request is on the first line; allow a few chunks
		lines, err := tail.Read()
		if err != nil || len(lines) == 0 {
			return ""
		}
		for _, line := range lines {
			var step struct {
				Type    string `json:"type"`
				Content string `json:"content"`
			}
			if json.Unmarshal([]byte(line.Text), &step) != nil || step.Type != "USER_INPUT" {
				continue
			}
			if text := strings.Join(strings.Fields(parser.AntigravityRequest(step.Content)), " "); text != "" {
				if runes := []rune(text); len(runes) > 60 {
					return string(runes[:59]) + "…"
				}
				return text
			}
		}
	}
	return ""
}

// transcriptState decides busy or idle from the last step on disk. A turn ends
// with a response that calls no tool; a prompt, a tool call, or a tool's
// result last means the turn is still going.
func (s *AntigravitySource) transcriptState(path string) protocol.State {
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
		state = antigravityLineState(line)
	}
	s.cacheMu.Lock()
	s.states[path] = kiroStateEntry{size: info.Size(), mtime: info.ModTime(), state: state}
	s.cacheMu.Unlock()
	return state
}

func antigravityLineState(line string) protocol.State {
	var step struct {
		Type      string            `json:"type"`
		Status    string            `json:"status"`
		ToolCalls []json.RawMessage `json:"tool_calls"`
	}
	if json.Unmarshal([]byte(line), &step) != nil {
		return protocol.StateIdle
	}
	switch strings.ToUpper(step.Status) {
	case "", "DONE", "ERROR", "FAILED", "CANCELED", "CANCELLED":
	default:
		return protocol.StateBusy // a step still in progress
	}
	switch step.Type {
	case "USER_INPUT", "GENERIC":
		return protocol.StateBusy
	case "PLANNER_RESPONSE":
		if len(step.ToolCalls) > 0 {
			return protocol.StateBusy
		}
	}
	return protocol.StateIdle
}

func (s *AntigravitySource) forgetCaches(live map[string]antigravitySession) {
	keep := map[string]bool{}
	for _, session := range live {
		keep[session.transcript] = true
	}
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	for path := range s.states {
		if !keep[path] {
			delete(s.states, path)
		}
	}
	for path := range s.names {
		if !keep[path] {
			delete(s.names, path)
		}
	}
}

func (s *AntigravitySource) session(sessionID string) (antigravitySession, error) {
	s.mu.RLock()
	session, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if ok {
		return session, nil
	}
	// A session Past found from the folder list reads like a live one with
	// nothing appending to it.
	if past, isPast := s.pastAntigravitySession(sessionID); isPast {
		return past, nil
	}
	return antigravitySession{}, fmt.Errorf("source: unknown antigravity session %q", sessionID)
}

// Page implements Source. Every step carries its own time, so unlike Kiro this
// reads backwards from the cursor like Claude does.
func (s *AntigravitySource) Page(ctx context.Context, sessionID, before string, limit int) (protocol.Page, error) {
	session, err := s.session(sessionID)
	if err != nil {
		return protocol.Page{}, err
	}
	if session.transcript == "" {
		return protocol.NewPage(sessionID, nil, "", false), nil
	}
	opts := jsonl.BackwardOptions{
		Want:         limit,
		MaxScanBytes: jsonl.DefaultScanBytes,
		Map:          parser.NewAntigravityParser(sessionID).Parse,
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
		if os.IsNotExist(err) {
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

// Follow implements Source.
func (s *AntigravitySource) Follow(ctx context.Context, sessionID string, out chan<- []protocol.Message) error {
	session, err := s.session(sessionID)
	if err != nil {
		return err
	}
	var (
		path string
		tail *jsonl.Tail
		p    *parser.AntigravityParser
	)
	attach := func(transcript string, fromStart bool) {
		path, tail, p = transcript, nil, parser.NewAntigravityParser(sessionID)
		if transcript == "" {
			return
		}
		tail = jsonl.NewTail(transcript)
		if fromStart {
			return
		}
		info, err := os.Stat(transcript)
		if err != nil {
			return
		}
		// Seed the parser with the recent past so a result for a call made
		// just before this follow still settles its row, then resume at the
		// end: the backlog itself belongs to Page.
		start := info.Size() - antigravityFollowSeed
		tail.SeekToOffset(max(start, 0))
		first := start > 0
		for {
			lines, err := tail.Read()
			if err != nil || len(lines) == 0 {
				break
			}
			for _, line := range lines {
				if first {
					first = false // began mid-line
					continue
				}
				p.Parse(line.Text, line.Offset)
			}
		}
	}
	attach(session.transcript, false)

	// The reply being written, as last read off the pane. Kept so an
	// unchanged preview is not re-sent every tick, and so the provisional
	// message can be withdrawn the moment the real record lands.
	var preview string

	ticker := time.NewTicker(followInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
		current, err := s.session(sessionID)
		if err != nil {
			return fmt.Errorf("source: antigravity session %q ended", sessionID)
		}
		// A pane moves to a conversation of its own when agy writes its first
		// step, or when the user starts another. Everything in a newly bound
		// transcript is new to this subscription, so it is read from the top.
		if current.transcript != path {
			attach(current.transcript, true)
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
		// The record that just landed supersedes anything scraped off the
		// pane for it. Clearing the preview here is what stops a finished
		// reply being followed by a stale, pane-wrapped copy of itself.
		if len(batch) > 0 {
			preview = ""
		}

		// Sent under the id the finished step will carry, so the app updates
		// that row in place. A message whose text grows under a stable id is
		// already how OpenCode streams, so nothing downstream is new.
		if partial, step, ok := s.streamingReply(ctx, current, p.NextStepIndex()); ok &&
			partial != preview {
			preview = partial
			batch = append(batch, protocol.Message{
				ID:        antigravityStreamID(step),
				SessionID: sessionID,
				Role:      protocol.RoleAssistant,
				Ts:        time.Now().UnixMilli(),
				Text:      partial,
			})
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

// streamingReply reads the reply agy is writing from its pane.
//
// Only while a turn is actually running: outside one the pane shows the last
// finished answer, which the transcript already carries and which must not be
// re-sent as though it were new.
func (s *AntigravitySource) streamingReply(
	ctx context.Context, session antigravitySession, nextStep int,
) (string, int, bool) {
	if session.tmuxName == "" || s.captureScrollback == nil {
		return "", 0, false
	}
	if session.meta.State != protocol.StateBusy {
		return "", 0, false
	}
	pane, err := s.captureScrollback(ctx, session.tmuxName, antigravityScrollbackLines)
	if err != nil {
		return "", 0, false
	}
	partial := antigravityPartialReply(pane)
	if partial == "" {
		return "", 0, false
	}
	return partial, nextStep, true
}

// Inject implements Injector.
func (s *AntigravitySource) Inject(ctx context.Context, sessionID, text string) (protocol.InjectMode, error) {
	session, err := s.session(sessionID)
	if err != nil {
		return protocol.InjectNone, err
	}
	if session.tmuxName == "" {
		return protocol.InjectNone, errors.New(
			"source: this session cannot receive messages — start it with `am antigravity` to enable sending")
	}
	if err := refuseSendIntoMenu(ctx, s.capturePane, session.tmuxName, question.DetectAntigravity); err != nil {
		return protocol.InjectNone, err
	}
	if err := tmux.Send(ctx, session.tmuxName, text); err != nil {
		return protocol.InjectNone, err
	}
	return protocol.InjectTmux, nil
}

// Interrupt implements Interrupter.
func (s *AntigravitySource) Interrupt(ctx context.Context, sessionID string) error {
	session, err := s.session(sessionID)
	if err != nil {
		return err
	}
	if session.tmuxName == "" {
		return errors.New("source: only sessions started with `am antigravity` can be stopped from the phone")
	}
	return tmux.Escape(ctx, session.tmuxName)
}

// Answer implements Answerer.
func (s *AntigravitySource) Answer(ctx context.Context, sessionID string, answer protocol.QuestionAnswer) error {
	session, err := s.session(sessionID)
	if err != nil {
		return err
	}
	return answerMenu(ctx, s.capturePane, session.tmuxName, session.meta.Question, answer, question.DetectAntigravity)
}

// CurrentQuestion implements QuestionInspector.
func (s *AntigravitySource) CurrentQuestion(ctx context.Context, sessionID string) (*protocol.Question, error) {
	session, err := s.session(sessionID)
	if err != nil || session.tmuxName == "" {
		return nil, err
	}
	found, err := paneMenu(ctx, s.capturePane, session.tmuxName, question.DetectAntigravity)
	if err != nil || found == nil {
		return nil, err
	}
	return protocolQuestion(found), nil
}

// antigravityOpenConversations asks lsof what each agy process holds open.
// Missing lsof, or a timeout, returns nothing: the sessions stay hidden rather
// than guessed at.
func antigravityOpenConversations(ctx context.Context, home string, pids []int) map[int]antigravityProcess {
	bin, err := exec.LookPath("lsof")
	if err != nil {
		return nil
	}
	ids := make([]string, 0, len(pids))
	for _, pid := range pids {
		if pid > 1 {
			ids = append(ids, strconv.Itoa(pid))
		}
	}
	if len(ids) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, antigravityLsofTimeout)
	defer cancel()
	// -Fpfn adds the descriptor to each path, which is how the working
	// directory is told apart: lsof reports it as descriptor "cwd".
	output, err := exec.CommandContext(ctx, bin, "-w", "-a", "-p", strings.Join(ids, ","), "-Fpfn").Output()
	if err != nil && len(output) == 0 {
		return nil
	}
	return parseAntigravityOpenFiles(home, output)
}

func parseAntigravityOpenFiles(home string, output []byte) map[int]antigravityProcess {
	root := filepath.Join(home, ".gemini", "antigravity-cli") + string(filepath.Separator)
	result := map[int]antigravityProcess{}
	pid, fd := 0, ""
	held := map[int]map[string]bool{}
	for _, line := range strings.Split(string(output), "\n") {
		if len(line) < 2 {
			continue
		}
		switch line[0] {
		case 'p':
			pid, _ = strconv.Atoi(line[1:])
			fd = ""
		case 'f':
			fd = line[1:]
		case 'n':
			if pid <= 1 {
				continue
			}
			path := filepath.Clean(line[1:])
			process := result[pid]
			if fd == "cwd" {
				process.cwd = path
				result[pid] = process
				continue
			}
			rel, ok := strings.CutPrefix(path, root)
			if !ok {
				continue
			}
			parts := strings.Split(rel, string(filepath.Separator))
			conversation := ""
			switch {
			case parts[0] == "brain" && len(parts) >= 2:
				conversation = parts[1]
			case parts[0] == "conversations" && len(parts) == 2:
				conversation = strings.SplitN(parts[1], ".", 2)[0]
			}
			if !isUUID(conversation) {
				continue
			}
			if held[pid] == nil {
				held[pid] = map[string]bool{}
			}
			if !held[pid][conversation] {
				held[pid][conversation] = true
				process.conversations = append(process.conversations, conversation)
				result[pid] = process
			}
		}
	}
	return result
}

func isUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i, character := range value {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if character != '-' {
				return false
			}
			continue
		}
		if !(character >= '0' && character <= '9') && !(character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}
