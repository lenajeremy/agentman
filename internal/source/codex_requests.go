package source

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Approvals and questions from Codex's shared daemon.
//
// When Codex needs the user (to approve a command or an edit, or to answer a
// question it asked) the daemon flags the thread as waiting, which every
// client hears, and sends the request itself to the clients that have joined
// the thread. A client that joins while a request is pending is sent it too
// (checked against Codex 0.166: same request id). So Agentman joins a thread
// only while it is flagged as waiting, and leaves once it is not.
//
// The request becomes the question the apps show, with Codex's own choices in
// Codex's own order (the daemon lists them), and the answer goes back as the
// daemon's reply, which clears the prompt in the terminal too. A request
// Codex resolves elsewhere (answered in the terminal, or the turn stopped) is
// withdrawn when the daemon says it is resolved.
//
// Command and edit approvals and request_user_input questions are read here.
// Anything else (a permissions grant, an MCP form) is left to the terminal
// and to reading its screen, as before.

// codexRequestMethods are the requests answered through Agentman.
var codexRequestMethods = map[string]bool{
	"item/commandExecution/requestApproval": true,
	"item/fileChange/requestApproval":       true,
	"item/tool/requestUserInput":            true,
}

// codexNoteTimeout bounds waiting for a refused turn to end before its note
// is sent as the next message.
const codexNoteTimeout = 10 * time.Second

// codexRequests holds the requests waiting on the user, by thread.
type codexRequests struct {
	mu       sync.Mutex
	byThread map[string][]*codexRequest
}

// codexRequest is one request waiting on the user.
type codexRequest struct {
	id     json.RawMessage
	method string
	params json.RawMessage
	// A request_user_input asks its questions one at a time; answers holds
	// those given so far, by question id, and index is the one showing.
	answers map[string][]string
	index   int
}

func newCodexRequests() *codexRequests {
	return &codexRequests{byThread: map[string][]*codexRequest{}}
}

// apply updates the requests from one message from the daemon.
func (r *codexRequests) apply(msg codexRPCMessage) {
	switch {
	case msg.serverRequest() && codexRequestMethods[msg.Method]:
		var params struct {
			ThreadID string `json:"threadId"`
		}
		if json.Unmarshal(msg.Params, &params) != nil || params.ThreadID == "" {
			return
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		for _, pending := range r.byThread[params.ThreadID] {
			if string(pending.id) == string(msg.ID) {
				return // sent again on a rejoin
			}
		}
		r.byThread[params.ThreadID] = append(r.byThread[params.ThreadID], &codexRequest{
			id: msg.ID, method: msg.Method, params: msg.Params, answers: map[string][]string{},
		})
	case msg.Method == "serverRequest/resolved":
		var params struct {
			ThreadID  string          `json:"threadId"`
			RequestID json.RawMessage `json:"requestId"`
		}
		if json.Unmarshal(msg.Params, &params) == nil {
			r.remove(params.ThreadID, params.RequestID)
		}
	case msg.Method == "thread/closed":
		var params struct {
			ThreadID string `json:"threadId"`
		}
		if json.Unmarshal(msg.Params, &params) == nil {
			r.mu.Lock()
			delete(r.byThread, params.ThreadID)
			r.mu.Unlock()
		}
	}
}

func (r *codexRequests) remove(thread string, id json.RawMessage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	pending := r.byThread[thread]
	for i, request := range pending {
		if string(request.id) == string(id) {
			r.byThread[thread] = append(pending[:i:i], pending[i+1:]...)
			break
		}
	}
	if len(r.byThread[thread]) == 0 {
		delete(r.byThread, thread)
	}
}

// clear forgets everything when the connection drops: a rejoin is sent
// whatever is still pending.
func (r *codexRequests) clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byThread = map[string][]*codexRequest{}
}

// current is the oldest request a thread is waiting on, and the question
// it shows.
func (r *codexRequests) current(thread string) (*codexRequest, *protocol.Question) {
	r.mu.Lock()
	defer r.mu.Unlock()
	pending := r.byThread[thread]
	if len(pending) == 0 {
		return nil, nil
	}
	question := pending[0].question(thread)
	if question == nil {
		return nil, nil
	}
	return pending[0], question
}

func (r *codexRequests) waiting(thread string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.byThread[thread]) > 0
}

// codexApproval is an approval request, as much of it as is shown.
type codexApproval struct {
	Command        string `json:"command"`
	CommandActions []struct {
		Command string `json:"command"`
	} `json:"commandActions"`
	Reason    string            `json:"reason"`
	GrantRoot string            `json:"grantRoot"`
	Decisions []json.RawMessage `json:"availableDecisions"`
}

func (a codexApproval) commandLine() string {
	if len(a.CommandActions) > 0 && a.CommandActions[0].Command != "" {
		return a.CommandActions[0].Command
	}
	return a.Command
}

// decisions are the choices an approval offers, in Codex's order. Older
// daemons do not list them; these are what Codex offers then.
func (a codexApproval) decisions(method string) []json.RawMessage {
	if len(a.Decisions) > 0 {
		return a.Decisions
	}
	if method == "item/fileChange/requestApproval" {
		return []json.RawMessage{json.RawMessage(`"accept"`), json.RawMessage(`"acceptForSession"`), json.RawMessage(`"cancel"`)}
	}
	return []json.RawMessage{json.RawMessage(`"accept"`), json.RawMessage(`"cancel"`)}
}

// codexUserInput is a request_user_input request.
type codexUserInput struct {
	Questions []struct {
		ID       string `json:"id"`
		Header   string `json:"header"`
		Question string `json:"question"`
		IsOther  bool   `json:"isOther"`
		Options  []struct {
			Label       string `json:"label"`
			Description string `json:"description"`
		} `json:"options"`
	} `json:"questions"`
}

// question is what the apps show for the request. Called with the requests'
// lock held.
func (q *codexRequest) question(thread string) *protocol.Question {
	id := fmt.Sprintf("codex-request:%s:%s:%d", thread, string(q.id), q.index)
	switch q.method {
	case "item/commandExecution/requestApproval", "item/fileChange/requestApproval":
		var approval codexApproval
		if json.Unmarshal(q.params, &approval) != nil {
			return nil
		}
		shown := &protocol.Question{ID: id}
		var detail []string
		if q.method == "item/commandExecution/requestApproval" {
			shown.Prompt = "$ " + approval.commandLine()
			detail = append(detail, "Would you like to run the following command?")
		} else {
			shown.Prompt = "Would you like to make the following edits?"
			if approval.GrantRoot != "" {
				detail = append(detail, "Codex is also asking to write anywhere under "+approval.GrantRoot+" for the rest of the session.")
			}
		}
		if approval.Reason != "" {
			detail = append(detail, "Reason: "+approval.Reason)
		}
		shown.Detail = strings.Join(detail, "\n\n")
		for i, decision := range approval.decisions(q.method) {
			label, note := codexDecisionLabel(decision, approval.commandLine(), q.method)
			if label == "" {
				continue
			}
			shown.Options = append(shown.Options, protocol.QuestionOption{Key: fmt.Sprint(i + 1), Label: label, WithText: note})
		}
		return shown

	case "item/tool/requestUserInput":
		var input codexUserInput
		if json.Unmarshal(q.params, &input) != nil || q.index >= len(input.Questions) {
			return nil
		}
		asked := input.Questions[q.index]
		shown := &protocol.Question{ID: id, Title: asked.Header, Prompt: asked.Question, Custom: asked.IsOther || len(asked.Options) == 0}
		if len(input.Questions) > 1 {
			shown.Detail = fmt.Sprintf("Question %d of %d", q.index+1, len(input.Questions))
		}
		for _, option := range asked.Options {
			shown.Options = append(shown.Options, protocol.QuestionOption{Key: option.Label, Label: option.Label, Description: option.Description})
		}
		return shown
	}
	return nil
}

// codexDecisionLabel names one approval choice as Codex's terminal does, and
// whether it takes a note. "" is a choice Agentman does not offer.
func codexDecisionLabel(decision json.RawMessage, command, method string) (string, bool) {
	var plain string
	if json.Unmarshal(decision, &plain) == nil {
		switch plain {
		case "accept":
			return "Yes, proceed", false
		case "acceptForSession":
			if method == "item/fileChange/requestApproval" {
				return "Yes, and don't ask again for these files", false
			}
			return "Yes, and don't ask again this session", false
		case "decline":
			return "No, continue without it", false
		case "cancel":
			// Codex ends the turn on it and takes the note as the next
			// message, as in its terminal.
			return codexRefusalLabel, true
		}
		return "", false
	}
	var amended struct {
		Exec *struct {
			Amendment []string `json:"execpolicy_amendment"`
		} `json:"acceptWithExecpolicyAmendment"`
		Network *struct {
			Amendment struct {
				Host   string `json:"host"`
				Action string `json:"action"`
			} `json:"network_policy_amendment"`
		} `json:"applyNetworkPolicyAmendment"`
	}
	if json.Unmarshal(decision, &amended) != nil {
		return "", false
	}
	switch {
	case amended.Exec != nil:
		return "Yes, and don't ask again for commands that start with `" + command + "`", false
	case amended.Network != nil && amended.Network.Amendment.Host != "":
		if amended.Network.Amendment.Action == "deny" {
			return "No, and always block " + amended.Network.Amendment.Host, false
		}
		return "Yes, and always allow " + amended.Network.Amendment.Host, false
	}
	return "", false
}

// reply works out the daemon's reply to an answer. done is false while a
// request_user_input has questions left; note is a refusal's note, to send
// once the turn has ended. Called with the requests' lock held.
func (q *codexRequest) reply(thread string, answer protocol.QuestionAnswer) (result any, done bool, note string, err error) {
	shown := q.question(thread)
	if shown == nil || answer.QuestionID != shown.ID {
		return nil, false, "", errors.New("source: that question is no longer current; refresh the session")
	}
	if len(answer.Options) > 0 {
		return nil, false, "", errors.New("source: choose one listed option or provide one custom answer")
	}
	switch q.method {
	case "item/commandExecution/requestApproval", "item/fileChange/requestApproval":
		var approval codexApproval
		json.Unmarshal(q.params, &approval)
		decisions := approval.decisions(q.method)
		for i, decision := range decisions {
			if fmt.Sprint(i+1) != answer.OptionKey {
				continue
			}
			_, takesNote := codexDecisionLabel(decision, approval.commandLine(), q.method)
			if answer.Text != "" && !takesNote {
				return nil, false, "", errors.New("source: choose one listed option or provide one custom answer")
			}
			return map[string]any{"decision": decision}, true, strings.TrimSpace(answer.Text), nil
		}
		return nil, false, "", errors.New("source: that question or option is no longer current; refresh the session")

	case "item/tool/requestUserInput":
		var input codexUserInput
		json.Unmarshal(q.params, &input)
		asked := input.Questions[q.index]
		value := answer.OptionKey
		if answer.Text != "" {
			if !shown.Custom || answer.OptionKey != "" {
				return nil, false, "", errors.New("source: this question takes one of its options")
			}
			value = answer.Text
		} else if !questionHasOption(shown, value) {
			return nil, false, "", errors.New("source: that question or option is no longer current; refresh the session")
		}
		q.answers[asked.ID] = []string{value}
		if q.index+1 < len(input.Questions) {
			q.index++
			return nil, false, "", nil
		}
		answers := map[string]any{}
		for id, given := range q.answers {
			answers[id] = map[string]any{"answers": given}
		}
		return map[string]any{"answers": answers}, true, "", nil
	}
	return nil, false, "", errors.New("source: Agentman cannot answer this Codex request")
}

// daemonQuestion is the question a session's thread is waiting on, from the
// daemon, or nil.
func (s *CodexSource) daemonQuestion(thread string) *protocol.Question {
	requests := s.codexRequests()
	if requests == nil || thread == "" {
		return nil
	}
	_, shown := requests.current(thread)
	return shown
}

// answerThroughDaemon answers the request a session's thread is waiting on.
// handled is false when the daemon has none for it, and the terminal's screen
// decides as before.
func (s *CodexSource) answerThroughDaemon(ctx context.Context, session codexSession, answer protocol.QuestionAnswer) (bool, error) {
	thread := s.daemonThreadOf(session)
	requests := s.codexRequests()
	server := s.connection()
	if thread == "" || requests == nil || server == nil {
		return false, nil
	}
	requests.mu.Lock()
	pending := requests.byThread[thread]
	if len(pending) == 0 {
		requests.mu.Unlock()
		return false, nil
	}
	request := pending[0]
	result, done, note, err := request.reply(thread, answer)
	requests.mu.Unlock()
	if err != nil || !done {
		return true, err
	}
	if err := server.respond(request.id, result); err != nil {
		return true, fmt.Errorf("source: Codex did not take the answer: %w", err)
	}
	requests.remove(thread, request.id)
	if note == "" {
		return true, nil
	}
	// The refusal ends the turn; the note is the next message, as it is
	// when typed after refusing in the terminal.
	wait, cancel := context.WithTimeout(ctx, codexNoteTimeout)
	defer cancel()
	for {
		if turn, err := s.activeTurn(wait, server, thread); err == nil && turn == "" {
			break
		}
		select {
		case <-wait.Done():
			return true, errors.New("source: Codex did not end the turn; send the note as a message")
		case <-time.After(150 * time.Millisecond):
		}
	}
	input := []any{map[string]any{"type": "text", "text": note, "text_elements": []any{}}}
	if err := server.call(ctx, "turn/start", map[string]any{"threadId": thread, "input": input}, nil); err != nil {
		return true, fmt.Errorf("source: the refusal went through but the note did not: %w", err)
	}
	return true, nil
}

func (s *CodexSource) codexRequests() *codexRequests {
	s.appMu.Lock()
	defer s.appMu.Unlock()
	return s.requests
}

// holdWaiting joins each thread Codex has flagged as waiting on the user, so
// the daemon sends Agentman the request, and leaves each that no longer is.
// Joining is a call to the daemon, so it happens off the discovery sweep.
func (s *CodexSource) holdWaiting(threads map[string]codexDaemonThread) {
	streams := s.codexStreams()
	requests := s.codexRequests()
	if streams == nil || requests == nil {
		return
	}
	s.holdMu.Lock()
	defer s.holdMu.Unlock()
	if s.holds == nil {
		s.holds = map[string]*codexHold{}
	}
	for id, thread := range threads {
		if !thread.session() || len(thread.Flags) == 0 || s.holds[id] != nil {
			continue
		}
		hold := &codexHold{}
		s.holds[id] = hold
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), codexJoinTimeout)
			defer cancel()
			stream, err := streams.hold(ctx, id)
			s.holdMu.Lock()
			defer s.holdMu.Unlock()
			if err != nil {
				delete(s.holds, id)
				return
			}
			hold.stream = stream
		}()
	}
	for id, hold := range s.holds {
		thread, open := threads[id]
		if hold.stream == nil || open && (len(thread.Flags) > 0 || requests.waiting(id)) {
			continue
		}
		delete(s.holds, id)
		go streams.close(hold.stream)
	}
}

// codexHold is a join kept while a thread waits on the user. stream is nil
// until the join has gone through.
type codexHold struct {
	stream *codexStream
}
