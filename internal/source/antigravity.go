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
	"slices"
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

// antigravityLsofTTL is how long one lsof answer is reused.
//
// Discovery sweeps every second and lsof was the most expensive thing in it:
// a fork and a walk of every descriptor each agy holds, for an answer that
// changes only when a conversation starts. An agy that holds no conversation
// yet is the exception, and is asked about again on the next sweep — that is
// a phone launch or a fresh pane waiting for its first message, and binding it
// to its transcript a few seconds late would show the user an empty session.
const antigravityLsofTTL = 5 * time.Second

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
	states  map[string]antigravityStateEntry
	names   map[string]string
	titles  map[string]antigravityTitle

	lsofMu sync.Mutex
	lsof   antigravityLsofAnswer

	// keys is how text and answers reach a pane. See antigravityKeys.
	keys antigravityKeys

	// index is agy's conversation index, read for history.
	index antigravityIndex
	// logs caches the tails of running processes' CLI logs.
	logs antigravityLogCache
}

// antigravityStateEntry caches the state a transcript's last line implies,
// keyed by the file's size and mtime so an unchanged file is never re-read.
type antigravityStateEntry struct {
	size  int64
	mtime time.Time
	state protocol.State
	// step is the last record's step_index, -1 when there is none.
	step int
}

// antigravityTitle is a conversation's title as agy's annotations file held it
// at one mtime.
type antigravityTitle struct {
	mtime time.Time
	title string
}

// antigravityLsofAnswer is one lsof run, kept for antigravityLsofTTL.
type antigravityLsofAnswer struct {
	at   time.Time
	pids string
	held map[int]antigravityProcess
}

type antigravitySession struct {
	meta       protocol.Session
	transcript string
	tmuxName   string
	// footer is what the pane's bottom row said on the last sweep: the mode
	// agy is in, and how many subagents and background tasks it is running.
	footer antigravityFooter
}

// antigravityProcess is what lsof shows one agy process holding.
type antigravityProcess struct {
	cwd           string
	conversations []string
	// log is the process's own CLI log, log/cli-<started>.log, which it
	// holds open for as long as it runs. See antigravity_log.go.
	log string
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
		keys:              defaultAntigravityKeys(),
		sessions:          map[string]antigravitySession{},
		states:            map[string]antigravityStateEntry{},
		names:             map[string]string{},
		titles:            map[string]antigravityTitle{},
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
		open = s.heldConversations(ctx, pids)
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
		conversation := s.latestConversation(s.withoutSubagents(process.conversations))
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
				// which a phone launch knows before agy picks a conversation
				// — unless the pane was opened to resume this conversation,
				// when the phone already knows it by the conversation's id.
				id = antigravityPaneSessionID(pane.Name, conversation)
				break
			}
		}

		transcript := s.transcriptPath(conversation)
		started := fileMillis(filepath.Join(s.root(), "presence", conversation+".lock"), time.Now().UnixMilli())
		session := protocol.Session{
			ID:             id,
			Kind:           protocol.KindAntigravity,
			NativeID:       conversation,
			Name:           s.conversationName(conversation, transcript, process.cwd),
			Cwd:            process.cwd,
			State:          s.transcriptState(transcript),
			Inject:         protocol.InjectNone,
			StartedAt:      started,
			LastActivityAt: fileMillis(transcript, started),
			Model:          s.defaultModel(),
			AgentPID:       pid,
		}
		var footer antigravityFooter
		if tmuxName != "" {
			session.Inject = protocol.InjectTmux
			footer = s.applyPane(ctx, &session, tmuxName)
		} else if process.log != "" {
			// No pane to read: the process's own log says what the
			// transcript cannot — a prompt waiting on the user, or a turn
			// cancelled with Esc, which writes nothing to the transcript.
			_, step := s.transcriptEnd(transcript)
			if state, ok := s.logState(process.log, conversation, step); ok {
				session.State = state
			}
		}
		found = append(found, session)
		next[id] = antigravitySession{meta: session, transcript: transcript, tmuxName: tmuxName, footer: footer}
	}

	// A pane with no conversation yet: agy at its folder trust prompt, or
	// waiting for a first message. Both are things the phone can act on.
	for _, pane := range panes {
		if claimed[pane.Name] {
			continue
		}
		resumed := antigravityResumedConversation(pane.Name)
		id := antigravityPaneSessionID(pane.Name, resumed)
		started := pane.Created.UnixMilli()
		if pane.Created.IsZero() {
			started = time.Now().UnixMilli()
		}
		session := protocol.Session{
			ID: id, Kind: protocol.KindAntigravity, NativeID: resumed, Name: filepath.Base(pane.Cwd), Cwd: pane.Cwd,
			State: protocol.StateIdle, Inject: protocol.InjectTmux, Model: s.defaultModel(),
			StartedAt: started, LastActivityAt: started, AgentPID: pane.PanePID,
		}
		footer := s.applyPane(ctx, &session, pane.Name)
		found = append(found, session)
		next[id] = antigravitySession{meta: session, tmuxName: pane.Name, footer: footer}
	}

	s.mu.Lock()
	s.sessions = next
	s.mu.Unlock()
	s.forgetCaches(next)
	liveLogs := map[string]bool{}
	for _, process := range open {
		if process.log != "" {
			liveLogs[process.log] = true
		}
	}
	s.forgetLogs(liveLogs)
	return found, nil
}

// antigravityResumePrefix names the pane a resume opens: the conversation's
// own id follows it. See ResumedSession.
const antigravityResumePrefix = antigravityPanePrefix + "resume-"

// ResumedSession implements ResumeNamer.
//
// Reopening an ended session from the phone used to land on an id that never
// came back. The phone waits on the id it already knows, "antigravity:<conv>",
// while discovery keyed every pane-backed session on its pane — so the
// reopened session appeared under a name the phone was not watching, and the
// one it was watching timed out as "did not come back up". A resume pane is
// named after the conversation instead, and discovery keeps that
// conversation's id for it.
func (s *AntigravitySource) ResumedSession(native, defaultPane string) (pane, sessionID string) {
	if !isUUID(native) {
		return "", ""
	}
	return antigravityResumePrefix + native, string(protocol.KindAntigravity) + ":" + native
}

var _ ResumeNamer = (*AntigravitySource)(nil)

// antigravityResumedConversation is the conversation a resume pane was opened
// for, or "".
func antigravityResumedConversation(paneName string) string {
	native, ok := strings.CutPrefix(paneName, antigravityResumePrefix)
	if !ok || !isUUID(native) {
		return ""
	}
	return native
}

// antigravityPaneSessionID keys a pane-backed session: on the conversation, if
// the pane was opened to resume exactly that one, and on the pane otherwise.
// A resume pane where the user has since started another conversation is
// keyed on the pane again, so the id never claims a conversation it no longer
// shows.
func antigravityPaneSessionID(paneName, conversation string) string {
	if resumed := antigravityResumedConversation(paneName); resumed != "" && resumed == conversation {
		return string(protocol.KindAntigravity) + ":" + resumed
	}
	return string(protocol.KindAntigravity) + ":" + tmuxID(paneName)
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

// withoutSubagents drops the conversations a process holds only because one of
// its own conversations started them.
//
// A subagent is a conversation of its own, with its own brain directory and
// transcript, and the agy that spawned it holds that directory open beside its
// parent's. While the subagent works its transcript is the newest, so picking
// "the one written to last" moved the whole session onto it: the phone showed
// the subagent's steps under the folder's name, stuck at busy, and the
// parent's own feed was replaced. The parent records each child it spawns
// under .system_generated/subagents/<child>.json, which is what tells them
// apart. If every held conversation turns out to be someone's child, which
// should not happen, they are all kept rather than none.
func (s *AntigravitySource) withoutSubagents(conversations []string) []string {
	if len(conversations) < 2 {
		return conversations
	}
	kept := make([]string, 0, len(conversations))
	for _, candidate := range conversations {
		child := false
		for _, parent := range conversations {
			if parent == candidate {
				continue
			}
			record := filepath.Join(s.root(), "brain", parent, ".system_generated", "subagents", candidate+".json")
			if _, err := os.Stat(record); err == nil {
				child = true
				break
			}
		}
		if !child {
			kept = append(kept, candidate)
		}
	}
	if len(kept) == 0 {
		return conversations
	}
	return kept
}

// heldConversations is antigravityOpenConversations, reused for a few seconds.
// See antigravityLsofTTL.
func (s *AntigravitySource) heldConversations(ctx context.Context, pids []int) map[int]antigravityProcess {
	key := pidKey(pids)
	s.lsofMu.Lock()
	cached := s.lsof
	s.lsofMu.Unlock()
	if cached.pids == key && time.Since(cached.at) < antigravityLsofTTL && allBound(cached.held, pids) {
		return cached.held
	}
	held := s.openConversations(ctx, s.home, pids)
	if held == nil {
		// lsof missing or timed out: do not remember a blank answer as if it
		// were a real one, or every session would vanish for the whole TTL.
		return nil
	}
	s.lsofMu.Lock()
	s.lsof = antigravityLsofAnswer{at: time.Now(), pids: key, held: held}
	s.lsofMu.Unlock()
	return held
}

func pidKey(pids []int) string {
	sorted := append([]int(nil), pids...)
	slices.Sort(sorted)
	parts := make([]string, len(sorted))
	for i, pid := range sorted {
		parts[i] = strconv.Itoa(pid)
	}
	return strings.Join(parts, ",")
}

// allBound reports whether every agy already holds a conversation, which is
// when an earlier answer can stand in for a new one.
func allBound(held map[int]antigravityProcess, pids []int) bool {
	for _, pid := range pids {
		if len(held[pid].conversations) == 0 {
			return false
		}
	}
	return true
}

var columnGap = regexp.MustCompile(`\s{2,}`)

// antigravityEffort is the reasoning effort agy prints after the model name.
var antigravityEffort = regexp.MustCompile(`^(?:low|medium|high|max)$`)

// antigravityCount is a running count agy appends to its footer:
// "1 subagent(s)", "2 task(s)".
var antigravityCount = regexp.MustCompile(`^(\d+) (subagent|task)\(s\)$`)

// antigravityFooter is agy's bottom row, read apart.
//
// The right-hand side is a list joined by " · ": an optional mode, the
// model, the effort, then optional counts —
//
//	? for shortcuts                       accept-edits · Gemini 3.8 Flash · high
//	? for shortcuts                Gemini 3.8 Flash · high · 1 subagent(s)
//	esc to cancel           Gemini 3.8 Flash · high · 1 task(s) · /tasks
//
// The model used to be matched as "whatever precedes the effort", which made
// the mode part of the model's name ("accept-edits · Gemini 3.8 Flash") and
// lost the model altogether once a count followed the effort. Anchoring on the
// effort word and reading outwards from it handles every shape agy draws.
type antigravityFooter struct {
	model     string
	effort    string
	mode      string
	subagents int
	tasks     int
}

func parseAntigravityFooter(lines []string) antigravityFooter {
	var footer antigravityFooter
	last := strings.TrimSpace(lastNonBlankLine(lines))
	if last == "" {
		return footer
	}
	columns := columnGap.Split(last, -1)
	parts := strings.Split(columns[len(columns)-1], " · ")
	effort := -1
	for i, part := range parts {
		if antigravityEffort.MatchString(strings.TrimSpace(part)) {
			effort = i
			break
		}
	}
	if effort < 1 {
		return footer
	}
	footer.effort = strings.TrimSpace(parts[effort])
	footer.model = strings.TrimSpace(parts[effort-1])
	if effort >= 2 {
		footer.mode = strings.TrimSpace(parts[effort-2])
	}
	for _, part := range parts[effort+1:] {
		match := antigravityCount.FindStringSubmatch(strings.TrimSpace(part))
		if match == nil {
			continue
		}
		count, _ := strconv.Atoi(match[1])
		if match[2] == "subagent" {
			footer.subagents = count
		} else {
			footer.tasks = count
		}
	}
	return footer
}

// applyPane refines a session from its terminal. agy writes a step only once
// it completes, so a reply still being generated looks, on disk, exactly like
// a turn that has not started — and a permission prompt is not on disk at all.
func (s *AntigravitySource) applyPane(
	ctx context.Context, session *protocol.Session, tmuxName string,
) antigravityFooter {
	capture := s.capturePane
	if capture == nil {
		capture = tmux.Capture
	}
	pane, err := capture(ctx, tmuxName)
	if err != nil {
		return antigravityFooter{}
	}
	lines := strings.Split(strings.TrimRight(pane, "\n"), "\n")
	footer := parseAntigravityFooter(lines)
	if footer.model != "" {
		session.Model = footer.model + " (" + footer.effort + ")"
	}
	if found := question.DetectAntigravity(pane); found != nil {
		session.Question = protocolQuestion(found)
		session.State = protocol.StateWaitingInput
		return footer
	}
	if state, ok := antigravityPaneState(lines); ok {
		session.State = state
	}
	return footer
}

// antigravityPaneState reads agy's footer, which says what it is doing. Only
// the bottom rows are read: the same words could appear in the conversation.
//
// "Press up to edit queued messages" replaces "esc to cancel" when a message
// was sent while the agent was working; it only ever appears mid-turn.
func antigravityPaneState(lines []string) (protocol.State, bool) {
	for i := len(lines) - 1; i >= 0 && i >= len(lines)-3; i-- {
		switch {
		case strings.Contains(lines[i], "esc to cancel"),
			strings.Contains(lines[i], "Press up to edit queued messages"):
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

// conversationName is the title agy gave the conversation — the one its own
// /resume picker shows, generated after the first turn or set with /rename —
// and until there is one, the first thing the user asked, which reads better
// in a list than a directory name. The first step never changes, so it is read
// once; the title is re-read only when its file changes.
func (s *AntigravitySource) conversationName(conversation, transcript, cwd string) string {
	if title := s.conversationTitle(conversation); title != "" {
		return title
	}
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

// antigravityTitleField is the one field agy writes to a conversation's
// annotations file: `title:"Create And Edit Notes File"`, protobuf text format.
var antigravityTitleField = regexp.MustCompile(`title:("(?:[^"\\]|\\.)*")`)

// conversationTitle reads annotations/<conversation>.pbtxt.
func (s *AntigravitySource) conversationTitle(conversation string) string {
	if !isUUID(conversation) {
		return ""
	}
	path := filepath.Join(s.root(), "annotations", conversation+".pbtxt")
	info, err := os.Stat(path)
	if err != nil {
		return ""
	}
	s.cacheMu.Lock()
	cached, ok := s.titles[conversation]
	s.cacheMu.Unlock()
	if ok && cached.mtime.Equal(info.ModTime()) {
		return cached.title
	}
	title := ""
	if raw, err := readBoundedFile(path, 64<<10); err == nil {
		title = parseAntigravityTitle(string(raw))
	}
	s.cacheMu.Lock()
	s.titles[conversation] = antigravityTitle{mtime: info.ModTime(), title: title}
	s.cacheMu.Unlock()
	return title
}

func parseAntigravityTitle(text string) string {
	match := antigravityTitleField.FindStringSubmatch(text)
	if match == nil {
		return ""
	}
	title, err := strconv.Unquote(match[1])
	if err != nil {
		return ""
	}
	return strings.Join(strings.Fields(title), " ")
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
	state, _ := s.transcriptEnd(path)
	return state
}

// transcriptEnd is transcriptState and the index of the last step on disk.
func (s *AntigravitySource) transcriptEnd(path string) (protocol.State, int) {
	info, err := os.Stat(path)
	if err != nil {
		return protocol.StateIdle, -1
	}
	s.cacheMu.Lock()
	cached, ok := s.states[path]
	s.cacheMu.Unlock()
	if ok && cached.size == info.Size() && cached.mtime.Equal(info.ModTime()) {
		return cached.state, cached.step
	}
	state, step := protocol.StateIdle, -1
	if line, err := lastLine(path, 32<<20); err == nil && line != "" {
		state = antigravityLineState(line)
		var last struct {
			StepIndex *int `json:"step_index"`
		}
		if json.Unmarshal([]byte(line), &last) == nil && last.StepIndex != nil {
			step = *last.StepIndex
		}
	}
	s.cacheMu.Lock()
	s.states[path] = antigravityStateEntry{size: info.Size(), mtime: info.ModTime(), state: state, step: step}
	s.cacheMu.Unlock()
	return state, step
}

func antigravityLineState(line string) protocol.State {
	var step struct {
		Type      string            `json:"type"`
		Status    string            `json:"status"`
		Error     string            `json:"error"`
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
	case "USER_INPUT", "SYSTEM_MESSAGE":
		// A system message is handed to the model with the next prompt, or on
		// its own when a background task or subagent reports back; either way
		// a response is now being written.
		return protocol.StateBusy
	case "GENERIC":
		// Declining a tool ends the turn there and then — agy prints
		// "Interrupted" and writes nothing after the denial — so a denial last
		// is a finished turn, not one waiting on its next step. Every other
		// tool result, failures included, is followed by the model's reply.
		if antigravityDenied(step.Error) {
			return protocol.StateIdle
		}
		return protocol.StateBusy
	case "PLANNER_RESPONSE":
		if len(step.ToolCalls) > 0 {
			return protocol.StateBusy
		}
	}
	return protocol.StateIdle
}

// antigravityDenied reports a tool result that is the user turning the call
// down: "permission check failed for write_file …: user denied permission for
// write_file(…)". A hook's refusal reads differently and does not end the
// turn, so it is not matched.
func antigravityDenied(errorText string) bool {
	return strings.Contains(errorText, "user denied permission for")
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
	for conversation := range s.titles {
		if !keep[s.transcriptPath(conversation)] {
			delete(s.titles, conversation)
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

	// The reply being written, as last read off the pane, and the id it went
	// out under. Kept so an unchanged preview is not re-sent every tick, and so
	// a preview no record replaces can be taken back.
	var preview, previewID string

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
		// A record that just landed supersedes anything scraped for its step.
		// When it carries no text of its own — the model called a tool and
		// said nothing — the preview is taken back, or it would stay in the
		// feed as a reply nobody wrote.
		if len(batch) > 0 && previewID != "" {
			if !hasMessage(batch, previewID) {
				batch = append(batch, withdrawnPreview(sessionID, previewID))
			}
			preview, previewID = "", ""
		}

		// Sent under the id the finished step will carry, so the app updates
		// that row in place. A message whose text grows under a stable id is
		// already how OpenCode streams, so nothing downstream is new.
		partial, step, streaming := s.streamingReply(ctx, current, p)
		switch {
		case streaming && partial != preview:
			preview, previewID = partial, antigravityStreamID(step)
			batch = append(batch, protocol.Message{
				ID:        previewID,
				SessionID: sessionID,
				Role:      protocol.RoleAssistant,
				Ts:        time.Now().UnixMilli(),
				Text:      partial,
			})
		case !streaming && previewID != "" && len(batch) == 0:
			// The turn stopped without writing the step — interrupted, or a
			// tool call drawn where the text was. Page would not show this
			// preview, so the live feed should not keep it either.
			batch = append(batch, withdrawnPreview(sessionID, previewID))
			preview, previewID = "", ""
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

func hasMessage(batch []protocol.Message, id string) bool {
	for _, message := range batch {
		if message.ID == id {
			return true
		}
	}
	return false
}

// withdrawnPreview takes a streamed preview back: the same id with no text,
// which the app drops from the feed.
func withdrawnPreview(sessionID, id string) protocol.Message {
	return protocol.Message{
		ID: id, SessionID: sessionID, Role: protocol.RoleAssistant, Ts: time.Now().UnixMilli(),
	}
}

// streamingReply reads the reply agy is writing from its pane.
//
// Two things must agree that one is being written. The transcript's newest
// record must be one the model answers (see AwaitingResponse): outside a turn
// the pane shows the last finished answer, which the transcript already
// carries and which must not be re-sent as though it were new. And the very
// capture the text is read from must show agy busy: a turn that was
// interrupted, or that ended on a declined call, leaves the transcript waiting
// for a response that is never coming, while the pane says it is idle.
func (s *AntigravitySource) streamingReply(
	ctx context.Context, session antigravitySession, p *parser.AntigravityParser,
) (string, int, bool) {
	if session.tmuxName == "" || s.captureScrollback == nil {
		return "", 0, false
	}
	if !p.AwaitingResponse() {
		return "", 0, false
	}
	pane, err := s.captureScrollback(ctx, session.tmuxName, antigravityScrollbackLines)
	if err != nil {
		return "", 0, false
	}
	lines := strings.Split(strings.TrimRight(pane, "\n"), "\n")
	if state, ok := antigravityPaneState(lines); !ok || state != protocol.StateBusy {
		return "", 0, false
	}
	partial := antigravityPartialReply(pane)
	if partial == "" {
		return "", 0, false
	}
	return partial, p.NextStepIndex(), true
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
	pane, err := s.capturePane(ctx, session.tmuxName)
	if err != nil {
		// Fail closed: without the pane there is no proof it is at a prompt.
		return protocol.InjectNone, fmt.Errorf("source: could not safely inspect the terminal before sending: %w", err)
	}
	if question.DetectAntigravity(pane) != nil {
		return protocol.InjectNone, fmt.Errorf("source: answer the pending question before sending a message")
	}
	// A panel or picker takes keys of its own: a message typed into /model
	// is a search, into the review panel it is approvals and rejections.
	if question.AntigravityPanelOpen(pane) {
		return protocol.InjectNone, fmt.Errorf(
			"source: a panel is open in Antigravity on the Mac; close it there before sending")
	}
	if err := s.keys.typeText(ctx, session.tmuxName, text); err != nil {
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
			if parts[0] == "log" && len(parts) == 2 && strings.HasSuffix(parts[1], ".log") {
				process.log = path
				result[pid] = process
				continue
			}
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
