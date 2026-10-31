package source

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/lenajeremy/agentman/internal/parser"
	"github.com/lenajeremy/agentman/internal/protocol"
)

// Kiro sessions started from the phone or the Mac run through Kiro's Agent
// Client Protocol server, `kiro-cli acp`, one child per session, rather than
// in a terminal pane. Kiro streams the reply as it writes it and sends tool
// calls, permission prompts, its modes, models, commands and context use as
// data, where a pane has to be read off the screen.
//
// An ACP session is an ordinary Kiro session: Kiro writes it to the same
// store (~/.kiro/sessions/cli/<id>.json and .jsonl) as one in a terminal,
// with the same ids, so it keeps its id here ("kiro:<uuid>"), its transcript
// gives the finished rows exactly as for any session, and `kiro-cli chat
// --resume-id` can carry it on in a terminal. What ACP adds is the turn as it
// happens: the reply being written, shown under the id the finished text
// will carry (as kiro_stream.go does for a pane), and each tool call from its
// start under Kiro's own call id, which its transcript row replaces.
//
// The child lives while the session is in use and is closed after
// kiroACPIdle without a turn; the next message loads the session into a new
// one (session/load). A session the daemon was running when it stopped
// therefore ends with it, and carries on from the next message.

// ErrNoKiroACP means Kiro's ACP server could not be used, and the caller
// should run Kiro in a pane as before.
var ErrNoKiroACP = errors.New("source: Kiro's ACP server is not available")

var kiroACPSpec = acpSpec{
	name: "Kiro", binary: "kiro-cli", args: []string{"acp"},
	missing: "Kiro CLI is not installed",
}

// kiroACPDial starts a real `kiro-cli acp`. The package's tests replace it,
// so no test can start Kiro by accident.
var kiroACPDial = func(cwd string, handle func(acpEnvelope)) (*acpClient, error) {
	return dialACP(kiroACPSpec, cwd, handle)
}

// kiroACPIdle is how long a session's child stays up between turns.
var kiroACPIdle = 30 * time.Minute

// kiroACPTurnLimit bounds one turn.
const kiroACPTurnLimit = 6 * time.Hour

// kiroACP is the sessions Agentman runs through Kiro's ACP server, by
// native id. One lock covers the table and every session in it.
type kiroACP struct {
	mu       sync.Mutex
	sessions map[string]*kiroACPSession
}

type kiroACPSession struct {
	native, cwd     string
	client          *acpClient
	busy            bool
	loading         bool
	started, active int64
	mode            string
	modes           []string
	model           string
	models          []string
	commands        []kiroACPCommand
	context         int
	pending         *kiroACPPermission
	questionSeq     int
	// text is the reply being written: what Kiro has said since the turn
	// began or its last tool call.
	text string
	// tools are the running turn's calls as live rows, by Kiro's call id,
	// and toolOrder the order they started in.
	tools     map[string]protocol.Message
	toolOrder []string
	// queued are messages sent while a turn runs, in order; ACP takes one
	// prompt at a time, so each goes when the turn before it ends.
	queued []string
	idle   *time.Timer
}

type kiroACPPermission struct {
	request  json.RawMessage
	question *protocol.Question
}

// kiroACPCommand is one of Kiro's slash commands, as
// _kiro.dev/commands/available lists it.
type kiroACPCommand struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Meta        struct {
		InputType string `json:"inputType"`
		Hint      string `json:"hint"`
		Local     bool   `json:"local"`
	} `json:"meta"`
}

// kiroACPState is the part of session/new's and session/load's replies kept.
type kiroACPState struct {
	SessionID string `json:"sessionId"`
	Modes     struct {
		CurrentModeID  string `json:"currentModeId"`
		AvailableModes []struct {
			ID string `json:"id"`
		} `json:"availableModes"`
	} `json:"modes"`
	Models struct {
		CurrentModelID  string `json:"currentModelId"`
		AvailableModels []struct {
			ModelID string `json:"modelId"`
		} `json:"availableModels"`
	} `json:"models"`
}

func (st *kiroACPSession) note(state kiroACPState) {
	if mode := strings.TrimSpace(state.Modes.CurrentModeID); mode != "" {
		st.mode = clipRunes(mode, 64)
	}
	var modes []string
	for _, mode := range state.Modes.AvailableModes {
		if id := strings.TrimSpace(mode.ID); id != "" && len(id) <= 64 && len(modes) < 16 {
			modes = append(modes, id)
		}
	}
	if len(modes) > 0 {
		st.modes = modes
	}
	if model := strings.TrimSpace(state.Models.CurrentModelID); model != "" {
		st.model = clipRunes(model, 128)
	}
	var models []string
	for _, model := range state.Models.AvailableModels {
		if id := strings.TrimSpace(model.ModelID); id != "" && len(id) <= 128 && len(models) < 64 {
			models = append(models, id)
		}
	}
	if len(models) > 0 {
		st.models = models
	}
}

func (s *KiroSource) dialACP(cwd string, handle func(acpEnvelope)) (*acpClient, error) {
	if s.acpDial != nil {
		return s.acpDial(cwd, handle)
	}
	return kiroACPDial(cwd, handle)
}

// acpOwned returns the ACP session behind a session id, if Agentman runs it.
func (s *KiroSource) acpOwned(sessionID string) (*kiroACPSession, bool) {
	native, ok := strings.CutPrefix(sessionID, string(protocol.KindKiro)+":")
	if !ok {
		return nil, false
	}
	s.acp.mu.Lock()
	defer s.acp.mu.Unlock()
	st, ok := s.acp.sessions[native]
	return st, ok
}

func (s *KiroSource) addACP(st *kiroACPSession) {
	s.acp.mu.Lock()
	defer s.acp.mu.Unlock()
	if s.acp.sessions == nil {
		s.acp.sessions = map[string]*kiroACPSession{}
	}
	s.acp.sessions[st.native] = st
	s.restartIdleLocked(st)
}

// restartIdleLocked closes the session's child once it has gone
// kiroACPIdle without a turn.
func (s *KiroSource) restartIdleLocked(st *kiroACPSession) {
	if st.idle != nil {
		st.idle.Stop()
	}
	st.idle = time.AfterFunc(kiroACPIdle, func() {
		s.acp.mu.Lock()
		if st.busy || st.pending != nil {
			s.restartIdleLocked(st)
			s.acp.mu.Unlock()
			return
		}
		client := st.client
		if s.acp.sessions[st.native] == st {
			delete(s.acp.sessions, st.native)
		}
		s.acp.mu.Unlock()
		if client != nil {
			client.close()
		}
	})
}

// StartACPSession starts a Kiro session through its ACP server in dir and
// sends it its first message. ErrNoKiroACP means it could not, and Kiro
// should start in a pane instead.
func (s *KiroSource) StartACPSession(ctx context.Context, dir, prompt string) (string, error) {
	now := time.Now().UnixMilli()
	st := &kiroACPSession{cwd: dir, started: now, active: now}
	client, err := s.dialACP(dir, func(event acpEnvelope) { s.handleACP(st, event) })
	if err != nil {
		log.Printf("kiro acp: %v; starting Kiro in a terminal instead", err)
		return "", fmt.Errorf("%w: %v", ErrNoKiroACP, err)
	}
	var created kiroACPState
	if err := client.call(ctx, "session/new", map[string]any{"cwd": dir, "mcpServers": []any{}}, &created); err != nil {
		client.close()
		return "", fmt.Errorf("%w: %v", ErrNoKiroACP, err)
	}
	if !validClaudeSessionID(created.SessionID) {
		client.close()
		return "", fmt.Errorf("%w: Kiro returned an invalid session id", ErrNoKiroACP)
	}
	s.acp.mu.Lock()
	st.native, st.client = created.SessionID, client
	st.note(created)
	s.acp.mu.Unlock()
	s.addACP(st)
	if err := s.promptACP(st, prompt); err != nil {
		s.dropACP(st)
		return "", err
	}
	return string(protocol.KindKiro) + ":" + st.native, nil
}

// ResumeACPSession carries an ended Kiro session on through its ACP server,
// under the same id, in the folder it ran in; the next message goes to it.
// A session already running here is left as it is.
func (s *KiroSource) ResumeACPSession(ctx context.Context, sessionID, cwd string) (string, error) {
	native, ok := strings.CutPrefix(sessionID, string(protocol.KindKiro)+":")
	if !ok || !validClaudeSessionID(native) {
		return "", ErrNoKiroACP
	}
	if _, running := s.acpOwned(sessionID); running {
		return sessionID, nil
	}
	now := time.Now().UnixMilli()
	st := &kiroACPSession{native: native, cwd: cwd, started: now, active: now, loading: true}
	client, err := s.dialACP(cwd, func(event acpEnvelope) { s.handleACP(st, event) })
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrNoKiroACP, err)
	}
	var loaded kiroACPState
	// Loading replays the conversation as updates; the transcript already
	// has it, so they are dropped while it lasts.
	if err := client.call(ctx, "session/load", map[string]any{"sessionId": native, "cwd": cwd, "mcpServers": []any{}}, &loaded); err != nil {
		client.close()
		return "", fmt.Errorf("source: Kiro did not reopen: %w", err)
	}
	s.acp.mu.Lock()
	st.client, st.loading = client, false
	st.note(loaded)
	s.acp.mu.Unlock()
	s.addACP(st)
	return sessionID, nil
}

// dropACP forgets a session and closes its child.
func (s *KiroSource) dropACP(st *kiroACPSession) {
	s.acp.mu.Lock()
	if s.acp.sessions[st.native] == st {
		delete(s.acp.sessions, st.native)
	}
	if st.idle != nil {
		st.idle.Stop()
	}
	client := st.client
	s.acp.mu.Unlock()
	if client != nil {
		client.close()
	}
}

// promptACP starts a turn, or queues the message behind the running one.
func (s *KiroSource) promptACP(st *kiroACPSession, text string) error {
	s.acp.mu.Lock()
	if st.busy {
		st.queued = append(st.queued, text)
		s.acp.mu.Unlock()
		return nil
	}
	client, native := st.client, st.native
	if client == nil {
		s.acp.mu.Unlock()
		return errors.New("source: this Kiro session is not running")
	}
	st.busy, st.text, st.tools, st.toolOrder = true, "", nil, nil
	st.active = time.Now().UnixMilli()
	s.restartIdleLocked(st)
	s.acp.mu.Unlock()

	id, reply, err := client.startCall("session/prompt", map[string]any{
		"sessionId": native, "prompt": []any{map[string]string{"type": "text", "text": text}},
	})
	if err != nil {
		s.acp.mu.Lock()
		st.busy = false
		s.acp.mu.Unlock()
		return fmt.Errorf("source: Kiro did not take the message: %w", err)
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), kiroACPTurnLimit)
		defer cancel()
		if err := client.await(ctx, id, reply, nil); err != nil {
			log.Printf("kiro acp: turn in %s ended: %v", native, err)
		}
		s.acp.mu.Lock()
		st.busy, st.pending, st.text, st.tools, st.toolOrder = false, nil, "", nil, nil
		st.active = time.Now().UnixMilli()
		var next string
		if len(st.queued) > 0 {
			next, st.queued = st.queued[0], st.queued[1:]
		}
		s.restartIdleLocked(st)
		s.acp.mu.Unlock()
		if next != "" {
			if err := s.promptACP(st, next); err != nil {
				log.Printf("kiro acp: a waiting message to %s was not sent: %v", native, err)
			}
		}
	}()
	return nil
}

// handleACP applies one notification or request from a session's child.
func (s *KiroSource) handleACP(st *kiroACPSession, event acpEnvelope) {
	switch event.Method {
	case "session/update", "_kiro.dev/session/update":
		var payload struct {
			Update json.RawMessage `json:"update"`
		}
		if json.Unmarshal(event.Params, &payload) == nil {
			s.applyACPUpdate(st, payload.Update)
		}
	case "_kiro.dev/commands/available":
		var payload struct {
			Commands []kiroACPCommand `json:"commands"`
		}
		if json.Unmarshal(event.Params, &payload) == nil {
			s.acp.mu.Lock()
			st.commands = payload.Commands
			s.acp.mu.Unlock()
		}
	case "_kiro.dev/metadata":
		var payload struct {
			Context *float64 `json:"contextUsagePercentage"`
		}
		if json.Unmarshal(event.Params, &payload) == nil && payload.Context != nil {
			s.acp.mu.Lock()
			st.context = min(100, max(0, int(*payload.Context+0.5)))
			s.acp.mu.Unlock()
		}
	case "session/request_permission":
		s.askPermission(st, event)
	default:
		if len(event.ID) > 0 && event.Method != "" {
			s.acp.mu.Lock()
			client := st.client
			s.acp.mu.Unlock()
			if client != nil {
				_ = client.respondError(event.ID, "Agentman does not handle "+event.Method)
			}
		}
	}
}

// applyACPUpdate applies one session update: the reply's words, a tool call
// starting or changing, the mode.
func (s *KiroSource) applyACPUpdate(st *kiroACPSession, raw json.RawMessage) {
	var update struct {
		SessionUpdate string `json:"sessionUpdate"`
		Content       struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		ToolCallID string          `json:"toolCallId"`
		Title      string          `json:"title"`
		Status     string          `json:"status"`
		RawInput   json.RawMessage `json:"rawInput"`
		Meta       struct {
			Kiro struct {
				ToolName string `json:"toolName"`
			} `json:"kiro"`
		} `json:"_meta"`
		CurrentModeID string `json:"currentModeId"`
	}
	if json.Unmarshal(raw, &update) != nil {
		return
	}
	s.acp.mu.Lock()
	defer s.acp.mu.Unlock()
	if st.loading {
		return
	}
	st.active = time.Now().UnixMilli()
	switch update.SessionUpdate {
	case "agent_message_chunk":
		if update.Content.Type == "text" {
			st.text += update.Content.Text
		}
	case "tool_call_chunk", "tool_call", "tool_call_update":
		if update.ToolCallID == "" {
			return
		}
		// Kiro's words before a call are in its transcript by the time the
		// call is; what follows is a new reply.
		st.text = ""
		if st.tools == nil {
			st.tools = map[string]protocol.Message{}
		}
		row, known := st.tools[update.ToolCallID]
		name := update.Meta.Kiro.ToolName
		if name == "" && update.SessionUpdate == "tool_call_chunk" {
			name = update.Title
		}
		if !known {
			st.toolOrder = append(st.toolOrder, update.ToolCallID)
		}
		if !known || name != "" || len(update.RawInput) > 0 {
			row = kiroACPToolRow(update.ToolCallID, name, update.RawInput, row)
		}
		switch update.Status {
		case "completed":
			row.Tool.Status = protocol.ToolOK
		case "failed":
			row.Tool.Status = protocol.ToolError
		}
		st.tools[update.ToolCallID] = row
	case "current_mode_update":
		if mode := strings.TrimSpace(update.CurrentModeID); mode != "" {
			st.mode = clipRunes(mode, 64)
		}
	}
}

// kiroACPToolRow is the row Kiro's transcript will give a call, made by its
// own parser from the call's name and input, so the transcript's row only
// settles it.
func kiroACPToolRow(callID, name string, input json.RawMessage, previous protocol.Message) protocol.Message {
	if name == "" {
		name = "tool"
	}
	if len(input) == 0 {
		input = json.RawMessage("{}")
	}
	use, _ := json.Marshal(map[string]any{"toolUseId": callID, "name": name, "input": input})
	record, _ := json.Marshal(map[string]any{
		"version": "v1", "kind": "AssistantMessage",
		"data": map[string]any{"message_id": "acp", "content": []any{
			map[string]any{"kind": "toolUse", "data": json.RawMessage(use)},
		}},
	})
	for _, row := range parser.NewKiroParser("").Parse(string(record), 0) {
		if row.ID == callID && row.Tool != nil {
			row.SessionID, row.Ts = "", previous.Ts
			return row
		}
	}
	if previous.Tool != nil {
		return previous
	}
	return protocol.Message{ID: callID, Role: protocol.RoleTool,
		Tool: &protocol.Tool{Name: name, Status: protocol.ToolRunning}}
}

// askPermission turns Kiro's permission request into a card.
func (s *KiroSource) askPermission(st *kiroACPSession, event acpEnvelope) {
	var request struct {
		ToolCall json.RawMessage `json:"toolCall"`
		Options  []struct {
			OptionID string `json:"optionId"`
			Name     string `json:"name"`
			Kind     string `json:"kind"`
		} `json:"options"`
	}
	if json.Unmarshal(event.Params, &request) != nil || len(event.ID) == 0 {
		return
	}
	var options []protocol.QuestionOption
	for _, option := range request.Options {
		if option.OptionID == "" {
			continue
		}
		label := option.Name
		switch option.Kind {
		case "allow_once":
			label = "Yes"
		case "allow_always":
			label = "Yes, and don't ask again"
		case "reject_once", "reject_always":
			label = "No"
		}
		options = append(options, protocol.QuestionOption{Key: option.OptionID, Label: label})
	}
	if len(options) == 0 {
		return
	}
	title, prompt, detail := parser.CursorACPPermission(request.ToolCall)
	if title == "" || strings.HasPrefix(title, "Cursor") {
		title = "Kiro"
	}
	if len(detail) > 16*1024 {
		detail = detail[:16*1024] + "…"
	}
	s.acp.mu.Lock()
	defer s.acp.mu.Unlock()
	st.questionSeq++
	st.pending = &kiroACPPermission{request: event.ID, question: &protocol.Question{
		ID:    fmt.Sprintf("kiro-acp:%s:%d", st.native, st.questionSeq),
		Title: title, Prompt: prompt, Detail: detail, Options: options,
	}}
}

// answerACP answers the session's permission card.
func (s *KiroSource) answerACP(st *kiroACPSession, answer protocol.QuestionAnswer) error {
	s.acp.mu.Lock()
	pending, client := st.pending, st.client
	if pending == nil || client == nil || pending.question.ID != answer.QuestionID {
		s.acp.mu.Unlock()
		return errors.New("source: Kiro's question is no longer current")
	}
	key := answer.OptionKey
	if key == "" && len(answer.Options) == 1 {
		key = answer.Options[0]
	}
	if !slices.ContainsFunc(pending.question.Options, func(o protocol.QuestionOption) bool { return o.Key == key }) {
		s.acp.mu.Unlock()
		return errors.New("source: choose one of Kiro's options")
	}
	st.pending = nil
	s.acp.mu.Unlock()
	return client.respond(pending.request, map[string]any{
		"outcome": map[string]string{"outcome": "selected", "optionId": key},
	})
}

// interruptACP stops the running turn.
func (s *KiroSource) interruptACP(st *kiroACPSession) error {
	s.acp.mu.Lock()
	client, busy, native := st.client, st.busy, st.native
	if pending := st.pending; pending != nil && client != nil {
		// A turn stopped at a card ends with it.
		_ = client.respond(pending.request, map[string]any{"outcome": map[string]string{"outcome": "cancelled"}})
		st.pending = nil
	}
	s.acp.mu.Unlock()
	if !busy || client == nil {
		return errors.New("source: Kiro is not working on anything")
	}
	return client.notify("session/cancel", map[string]string{"sessionId": native})
}

// setModeACP and setModelACP switch through Kiro's ACP methods.
func (s *KiroSource) setModeACP(ctx context.Context, st *kiroACPSession, mode string) error {
	s.acp.mu.Lock()
	client, native, offered := st.client, st.native, slices.Contains(st.modes, mode)
	s.acp.mu.Unlock()
	if !offered || client == nil {
		return fmt.Errorf("source: Kiro has no mode %q", mode)
	}
	if err := client.call(ctx, "session/set_mode", map[string]string{"sessionId": native, "modeId": mode}, nil); err != nil {
		return fmt.Errorf("source: Kiro did not switch: %w", err)
	}
	s.acp.mu.Lock()
	st.mode = mode
	s.acp.mu.Unlock()
	return nil
}

func (s *KiroSource) setModelACP(ctx context.Context, st *kiroACPSession, model string) error {
	s.acp.mu.Lock()
	client, native, offered := st.client, st.native, slices.Contains(st.models, model)
	s.acp.mu.Unlock()
	if !offered || client == nil {
		return fmt.Errorf("source: Kiro has no model %q", model)
	}
	if err := client.call(ctx, "session/set_model", map[string]string{"sessionId": native, "modelId": model}, nil); err != nil {
		return fmt.Errorf("source: Kiro did not switch: %w", err)
	}
	s.acp.mu.Lock()
	st.model = model
	s.acp.mu.Unlock()
	return nil
}

// applyACP lists the sessions Agentman runs through ACP: from discovery when
// Kiro's files have them, from the table when they are not there yet.
func (s *KiroSource) applyACP(found []protocol.Session, next map[string]kiroSession) []protocol.Session {
	s.acp.mu.Lock()
	owned := make([]*kiroACPSession, 0, len(s.acp.sessions))
	for _, st := range s.acp.sessions {
		owned = append(owned, st)
	}
	s.acp.mu.Unlock()
	for _, st := range owned {
		id := string(protocol.KindKiro) + ":" + st.native
		entry, listed := next[id]
		base := filepath.Join(s.sessionsDir(), st.native)
		if !listed {
			entry = kiroSession{meta: protocol.Session{
				ID: id, Kind: protocol.KindKiro, NativeID: st.native,
				Name: filepath.Base(st.cwd), Cwd: st.cwd, StartedAt: st.started,
			}, transcript: base + ".jsonl"}
			if meta, ok := s.readMeta(base + ".json"); ok {
				entry.meta.Name = kiroName(meta)
			}
		}
		meta := entry.meta
		s.acp.mu.Lock()
		meta.Inject = protocol.InjectAPI
		meta.Question = nil
		switch {
		case st.pending != nil:
			meta.State, meta.Question = protocol.StateWaitingInput, st.pending.question
		case st.busy:
			meta.State = protocol.StateBusy
		default:
			meta.State = protocol.StateIdle
		}
		meta.Queued = slices.Clone(st.queued)
		meta.Mode, meta.Modes = st.mode, slices.Clone(st.modes)
		meta.Model, meta.Models = st.model, slices.Clone(st.models)
		if len(st.models) > 0 {
			meta.ModelScope = protocol.ModelScopeSession
		}
		if st.context > 0 {
			meta.ContextPercent = st.context
		}
		if st.active > meta.LastActivityAt {
			meta.LastActivityAt = st.active
		}
		if st.client != nil && st.client.cmd != nil && st.client.cmd.Process != nil {
			meta.AgentPID = st.client.cmd.Process.Pid
		}
		s.acp.mu.Unlock()
		entry.meta, entry.tmuxName = meta, ""
		next[id] = entry
		if i := slices.IndexFunc(found, func(session protocol.Session) bool { return session.ID == id }); i >= 0 {
			found[i] = meta
		} else {
			found = append(found, meta)
		}
	}
	return found
}

// acpReply is the reply being written in an ACP session, for its follow.
func (s *KiroSource) acpReply(sessionID string) (string, bool) {
	st, owned := s.acpOwned(sessionID)
	if !owned {
		return "", false
	}
	s.acp.mu.Lock()
	defer s.acp.mu.Unlock()
	return st.text, true
}

// acpTools are the running turn's calls as live rows.
func (s *KiroSource) acpTools(sessionID string) []protocol.Message {
	st, owned := s.acpOwned(sessionID)
	if !owned {
		return nil
	}
	s.acp.mu.Lock()
	defer s.acp.mu.Unlock()
	rows := make([]protocol.Message, 0, len(st.tools))
	for _, id := range st.toolOrder {
		row := st.tools[id]
		row.SessionID = sessionID
		if row.Tool != nil {
			tool := *row.Tool
			row.Tool = &tool
		}
		rows = append(rows, row)
	}
	return rows
}

// sendACP sends a message to a session run through ACP. A bare slash
// command Kiro lists is run as one (_kiro.dev/commands/execute), as typing
// it in Kiro's terminal would; anything else is a prompt.
func (s *KiroSource) sendACP(ctx context.Context, st *kiroACPSession, text string) error {
	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, "/") && !strings.ContainsAny(trimmed, " \n\t") {
		if command, ok := s.acpCommand(st, strings.TrimPrefix(trimmed, "/")); ok && command.Meta.InputType != "panel" {
			_, err := s.executeACP(ctx, st, strings.TrimPrefix(command.Name, "/"))
			return err
		}
	}
	return s.promptACP(st, text)
}

func (s *KiroSource) acpCommand(st *kiroACPSession, name string) (kiroACPCommand, bool) {
	s.acp.mu.Lock()
	defer s.acp.mu.Unlock()
	for _, command := range st.commands {
		if strings.TrimPrefix(command.Name, "/") == name {
			return command, true
		}
	}
	return kiroACPCommand{}, false
}

// kiroACPResult is what a command answers.
type kiroACPResult struct {
	Success bool            `json:"success"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

// executeACP runs one of Kiro's commands, with no arguments.
func (s *KiroSource) executeACP(ctx context.Context, st *kiroACPSession, name string) (kiroACPResult, error) {
	s.acp.mu.Lock()
	client, native := st.client, st.native
	s.acp.mu.Unlock()
	if client == nil {
		return kiroACPResult{}, errors.New("source: this Kiro session is not running")
	}
	var result kiroACPResult
	err := client.call(ctx, "_kiro.dev/commands/execute", map[string]any{
		"sessionId": native, "command": map[string]any{"command": name, "args": map[string]any{}},
	}, &result)
	if err != nil {
		return result, fmt.Errorf("source: Kiro did not run /%s: %w", name, err)
	}
	if !result.Success {
		return result, fmt.Errorf("source: /%s: %s", name, strings.TrimSpace(result.Message))
	}
	return result, nil
}

// acpCommands is the "/" menu of a session run through ACP: Kiro's own
// commands that run without its terminal. Ones that open a picker (/agent,
// /model: the switch menus do those) or act on local files (/chat) are left
// out; a panel command (/context, /usage) answers in a panel.
func (s *KiroSource) acpCommands(st *kiroACPSession) []protocol.Command {
	s.acp.mu.Lock()
	defer s.acp.mu.Unlock()
	out := make([]protocol.Command, 0, len(st.commands))
	for _, command := range st.commands {
		name := strings.TrimPrefix(command.Name, "/")
		if !validCommandName(name) || command.Meta.Local || command.Meta.InputType == "selection" || kiroTerminalOnly[name] {
			continue
		}
		out = append(out, protocol.Command{
			Name: name, Description: clipCommandText(command.Description, maxCommandDescription),
			ArgumentHint: clipCommandText(command.Meta.Hint, maxCommandHint), Source: "builtin",
			Panel: command.Meta.InputType == "panel",
		})
	}
	return out
}

// kiroTerminalOnly are commands that act on Kiro's own terminal interface.
var kiroTerminalOnly = map[string]bool{"paste": true, "voice": true}

// panelACP runs a panel command and shows what it answered.
func (s *KiroSource) panelACP(ctx context.Context, st *kiroACPSession, command string) (protocol.CommandPanel, error) {
	name := strings.TrimPrefix(strings.TrimSpace(command), "/")
	if _, ok := s.acpCommand(st, name); !ok {
		return protocol.CommandPanel{}, fmt.Errorf("source: Kiro has no command /%s", name)
	}
	result, err := s.executeACP(ctx, st, name)
	if err != nil {
		return protocol.CommandPanel{}, err
	}
	text := strings.TrimSpace(result.Message)
	if len(result.Data) > 0 && string(result.Data) != "null" {
		var data any
		if json.Unmarshal(result.Data, &data) == nil {
			var lines []string
			kiroPanelLines(&lines, data, "")
			text = strings.TrimSpace(text + "\n\n" + clipRunes(strings.Join(lines, "\n"), 16*1024))
		}
	}
	return protocol.CommandPanel{Command: "/" + name, Text: text}, nil
}

// kiroPanelLines writes a command's data as indented lines a person reads:
// "contextFiles:", then "  tokens: 0" under it; a list of named items one
// per line, by name, with the rest of each item after it.
func kiroPanelLines(lines *[]string, value any, indent string) {
	if len(*lines) > 400 {
		return
	}
	switch v := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		for _, key := range keys {
			switch child := v[key].(type) {
			case map[string]any, []any:
				if kiroPanelEmpty(child) {
					continue
				}
				*lines = append(*lines, indent+key+":")
				kiroPanelLines(lines, child, indent+"  ")
			default:
				*lines = append(*lines, indent+key+": "+kiroPanelScalar(child))
			}
		}
	case []any:
		for _, item := range v {
			entry, isMap := item.(map[string]any)
			if !isMap {
				*lines = append(*lines, indent+"- "+kiroPanelScalar(item))
				continue
			}
			name, _ := entry["name"].(string)
			var rest []string
			var nested map[string]any
			for key, field := range entry {
				if key == "name" {
					continue
				}
				switch field.(type) {
				case map[string]any, []any:
					if nested == nil {
						nested = map[string]any{}
					}
					nested[key] = field
				default:
					rest = append(rest, key+" "+kiroPanelScalar(field))
				}
			}
			slices.Sort(rest)
			*lines = append(*lines, strings.TrimRight(indent+"- "+strings.TrimSpace(name+"  "+strings.Join(rest, ", ")), " "))
			if nested != nil {
				kiroPanelLines(lines, nested, indent+"    ")
			}
		}
	default:
		*lines = append(*lines, indent+kiroPanelScalar(v))
	}
}

// kiroPanelEmpty is whether a map or list holds nothing to show, however
// deep.
func kiroPanelEmpty(value any) bool {
	switch v := value.(type) {
	case map[string]any:
		for _, child := range v {
			if !kiroPanelEmpty(child) {
				return false
			}
		}
		return true
	case []any:
		return len(v) == 0
	}
	return false
}

func kiroPanelScalar(value any) string {
	switch v := value.(type) {
	case nil:
		return "—"
	case float64:
		if v == float64(int64(v)) {
			return fmt.Sprintf("%d", int64(v))
		}
		return fmt.Sprintf("%.1f", v)
	case string:
		return clipRunes(v, 200)
	default:
		return fmt.Sprint(v)
	}
}

// kiroACPFollow is what one follow of an ACP session has sent of the
// running turn's calls.
type kiroACPFollow struct {
	settled map[string]bool
	sent    map[string]string
	ts      map[string]int64
}

// merge adds to a batch read from the transcript the calls Kiro is running
// that the transcript does not have yet, each when it changes.
func (f *kiroACPFollow) merge(batch, live []protocol.Message, nextTs int64) []protocol.Message {
	if f.settled == nil {
		f.settled, f.sent, f.ts = map[string]bool{}, map[string]string{}, map[string]int64{}
	}
	for _, row := range batch {
		if row.Role == protocol.RoleTool {
			f.settled[row.ID] = true
		}
	}
	for _, row := range live {
		if f.settled[row.ID] || row.Tool == nil {
			continue
		}
		mark := string(row.Tool.Status) + "\x00" + row.Tool.Name + "\x00" + row.Tool.Summary + "\x00" + row.Text
		if f.sent[row.ID] == mark {
			continue
		}
		f.sent[row.ID] = mark
		if _, ok := f.ts[row.ID]; !ok {
			// After everything shown, and never before the turn's prompt,
			// which may not be in the transcript yet when the call starts.
			f.ts[row.ID] = max(nextTs, time.Now().UnixMilli())
		}
		row.Ts = f.ts[row.ID]
		batch = append(batch, row)
	}
	return batch
}
