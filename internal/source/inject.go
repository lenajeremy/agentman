package source

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/question"
	"github.com/lenajeremy/agentman/internal/tmux"
)

// PendingQueue holds messages that could not be delivered immediately.
//
// A session started outside the tmux wrapper has no live input channel, so the
// only way in is to wait for its next Stop hook and hand the text back as the
// reason the agent should keep going. That means delivery happens between
// turns, never during one — and the CLI can discard the injection, which is
// why the app reports these as "queued" rather than sent.
type PendingQueue struct {
	mu       sync.Mutex
	messages map[string][]string
}

// NewPendingQueue creates an empty queue.
func NewPendingQueue() *PendingQueue {
	return &PendingQueue{messages: map[string][]string{}}
}

// Add queues a message for a session.
func (q *PendingQueue) Add(sessionID, text string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	// Cap the backlog: if the agent never stops, an impatient user could
	// otherwise queue a hundred messages that all arrive at once.
	const maxPending = 10
	queued := q.messages[sessionID]
	if len(queued) >= maxPending {
		queued = queued[1:]
	}
	q.messages[sessionID] = append(queued, text)
}

// Take removes and returns everything queued for a session.
func (q *PendingQueue) Take(sessionID string) []string {
	q.mu.Lock()
	defer q.mu.Unlock()
	queued := q.messages[sessionID]
	delete(q.messages, sessionID)
	return queued
}

// Len reports how many messages are waiting for a session.
func (q *PendingQueue) Len(sessionID string) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.messages[sessionID])
}

// SetPending gives a source a queue for messages it cannot deliver live.
func (s *ClaudeSource) SetPending(queue *PendingQueue) { s.pending = queue }

// Inject implements Injector for Claude Code.
//
// Prefers tmux, which types into the session exactly as the user would and
// works even mid-turn. Falls back to the pending queue, whose delivery is
// best-effort — the returned mode tells the caller which happened so the UI
// can be honest about it.
func (s *ClaudeSource) Inject(ctx context.Context, sessionID, text string) (protocol.InjectMode, error) {
	s.mu.RLock()
	session, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if !ok {
		return protocol.InjectNone, fmt.Errorf("source: unknown claude session %q", sessionID)
	}

	if session.tmuxName != "" {
		if err := tmux.Send(ctx, session.tmuxName, text); err != nil {
			return protocol.InjectNone, err
		}
		return protocol.InjectTmux, nil
	}

	if s.pending == nil {
		return protocol.InjectNone, fmt.Errorf(
			"source: this session cannot receive messages — start it with `am claude` to enable sending")
	}
	s.pending.Add(sessionID, text)
	return protocol.InjectHook, nil
}

// Interrupt stops a running turn, where the session supports it.
func (s *ClaudeSource) Interrupt(ctx context.Context, sessionID string) error {
	s.mu.RLock()
	session, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("source: unknown claude session %q", sessionID)
	}
	if session.tmuxName == "" {
		return fmt.Errorf("source: only sessions started with `am claude` can be interrupted")
	}
	return tmux.Interrupt(ctx, session.tmuxName)
}

// Inject implements Injector for Codex.
func (s *CodexSource) Inject(ctx context.Context, sessionID, text string) (protocol.InjectMode, error) {
	s.mu.RLock()
	session, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if !ok {
		return protocol.InjectNone, fmt.Errorf("source: unknown codex session %q", sessionID)
	}

	if session.tmuxName != "" {
		if err := tmux.Send(ctx, session.tmuxName, text); err != nil {
			return protocol.InjectNone, err
		}
		return protocol.InjectTmux, nil
	}

	// Codex's supported notify callback is fire-and-forget: stdout is discarded
	// and it cannot feed a blocking decision or queued prompt back into the
	// turn. Advertising hook delivery here made the app report "queued" for a
	// message that could never arrive.
	return protocol.InjectNone, fmt.Errorf(
		"source: this session cannot receive messages — start it with `am codex` to enable sending")
}

// detectQuestion reads a pane and reports any decision the agent is blocked on.
//
// Runs on every discovery sweep for every tmux-backed session, so it must stay
// cheap: one capture-pane per session per second, parsed in memory. Failures
// are silent — a pane that cannot be read simply means no question, which is
// the same conclusion as an agent that is working normally.
func detectQuestion(ctx context.Context, tmuxName string) *protocol.Question {
	found, err := captureQuestion(ctx, tmuxName)
	if err != nil {
		return nil
	}
	return protocolQuestion(found)
}

func captureQuestion(ctx context.Context, tmuxName string) (*question.Question, error) {
	pane, err := tmux.Capture(ctx, tmuxName)
	if err != nil {
		return nil, err
	}
	found := question.Detect(pane)
	if found == nil {
		return nil, fmt.Errorf("source: no answerable question is on screen")
	}
	return found, nil
}

func protocolQuestion(found *question.Question) *protocol.Question {
	options := make([]protocol.QuestionOption, 0, len(found.Options))
	for _, option := range found.Options {
		options = append(options, protocol.QuestionOption{
			Key:         option.Key,
			Label:       option.Label,
			Description: option.Description,
			Preview:     option.Preview,
			Selected:    option.Selected,
			Checked:     option.Checked,
		})
	}
	return &protocol.Question{
		Prompt:   found.Prompt,
		Title:    found.Title,
		Detail:   found.Detail,
		Options:  options,
		Multiple: found.Multiple,
		Custom:   found.Custom,
	}
}

// Answer completes the question a Claude session is showing. Permission and
// ordinary single-select menus take one numeric key. AskUserQuestion can also
// expose a free-text row or a checkbox form, which must be reconciled and then
// explicitly advanced with Next/Submit.
func (s *ClaudeSource) Answer(ctx context.Context, sessionID string, answer protocol.QuestionAnswer) error {
	s.mu.RLock()
	session, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("source: unknown claude session %q", sessionID)
	}
	if session.tmuxName == "" {
		return fmt.Errorf("source: only sessions started with `am claude` can be answered")
	}
	current, err := captureQuestion(ctx, session.tmuxName)
	if err != nil {
		return fmt.Errorf("source: that question is no longer on screen; refresh the session")
	}
	s.enrichClaudeQuestion(sessionID, session.transcript, current)
	if !sameQuestion(session.meta.Question, protocolQuestion(current)) {
		return fmt.Errorf("source: that question is no longer on screen; refresh the session")
	}

	if current.Multiple {
		return answerClaudeMultiple(ctx, session.tmuxName, current, answer)
	}
	if len(answer.Options) > 0 {
		return fmt.Errorf("source: this Claude question accepts only one answer")
	}
	if answer.Text != "" {
		if answer.OptionKey != "" || !current.Custom || current.CustomKey == "" {
			return fmt.Errorf("source: this Claude question does not accept that custom answer")
		}
		if current.AdvanceWithTab {
			return tmux.AnswerCustomAndAdvance(
				ctx, session.tmuxName, current.CustomKey, answer.Text,
			)
		}
		return tmux.AnswerCustom(ctx, session.tmuxName, current.CustomKey, answer.Text)
	}
	if !questionHasOption(protocolQuestion(current), answer.OptionKey) {
		return fmt.Errorf("source: that question or option is no longer current; refresh the session")
	}
	if current.AdvanceWithTab || current.PreviewLayout {
		targetIndex := -1
		for index, option := range current.Options {
			if option.Key == answer.OptionKey {
				targetIndex = index
				break
			}
		}
		if targetIndex < 0 || current.FocusIndex < 0 {
			return fmt.Errorf("source: Claude's current option focus could not be identified; refresh the session")
		}
		return tmux.AnswerSingleForm(
			ctx, session.tmuxName, answer.OptionKey,
			targetIndex-current.FocusIndex, current.AdvanceWithTab,
		)
	}
	return tmux.Answer(ctx, session.tmuxName, answer.OptionKey)
}

func answerClaudeMultiple(
	ctx context.Context,
	tmuxName string,
	current *question.Question,
	answer protocol.QuestionAnswer,
) error {
	plan, err := planClaudeMultiple(current, answer)
	if err != nil {
		return err
	}
	return tmux.AnswerForm(
		ctx, tmuxName, plan.toggleKeys,
		plan.safeMove, plan.targetMove, plan.afterTextMove, plan.text,
	)
}

type claudeFormPlan struct {
	toggleKeys    []string
	safeMove      int
	targetMove    int
	afterTextMove int
	text          string
}

func planClaudeMultiple(
	current *question.Question,
	answer protocol.QuestionAnswer,
) (claudeFormPlan, error) {
	var plan claudeFormPlan
	keys := answer.Options
	if answer.OptionKey != "" {
		if len(keys) > 0 {
			return plan, fmt.Errorf("source: provide either optionKey or optionKeys, not both")
		}
		// Accept an older app choosing one checkbox as a useful compatibility
		// path; current clients always send optionKeys for multi-select forms.
		keys = []string{answer.OptionKey}
	}
	if answer.Text != "" && (!current.Custom || current.CustomKey == "") {
		return plan, fmt.Errorf("source: this Claude question does not accept custom text")
	}
	if len(keys) == 0 && strings.TrimSpace(answer.Text) == "" {
		return plan, fmt.Errorf("source: select at least one answer")
	}

	desired := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		if key == "" {
			return plan, fmt.Errorf("source: an answer option is empty")
		}
		if _, duplicate := desired[key]; duplicate {
			return plan, fmt.Errorf("source: answer option %q was selected more than once", key)
		}
		desired[key] = struct{}{}
	}

	var toggles []string
	for _, option := range current.Options {
		_, want := desired[option.Key]
		if want {
			delete(desired, option.Key)
		}
		if want != option.Checked {
			toggles = append(toggles, option.Key)
		}
	}
	if len(desired) > 0 {
		for key := range desired {
			return plan, fmt.Errorf("source: option %q is no longer available; refresh the session", key)
		}
	}

	// If Claude already has its custom row checked but the phone omitted text,
	// remove it just like any other checkbox. Typing into the focused custom
	// input checks it automatically, so the positive case needs no digit toggle.
	if answer.Text == "" && current.CustomChecked {
		toggles = append(toggles, current.CustomKey)
	}

	position, err := questionFocusPosition(current)
	if err != nil {
		return plan, err
	}
	plan.toggleKeys = toggles
	plan.text = answer.Text
	// Claude still considers the custom input its focused option while Submit
	// is highlighted. Digits sent in that state are swallowed by the input
	// instead of toggling checkboxes. Move to a real option before any numeric
	// shortcuts, then navigate from that known-safe position.
	if len(toggles) > 0 {
		plan.safeMove = -position
		position = 0
	}
	if answer.Text != "" {
		if current.CustomIndex < 0 {
			return plan, fmt.Errorf("source: Claude's custom input is no longer on screen")
		}
		plan.targetMove = current.CustomIndex - position
		plan.afterTextMove = current.ChoiceCount - current.CustomIndex
	} else {
		plan.targetMove = current.ChoiceCount - position
	}
	return plan, nil
}

func questionFocusPosition(current *question.Question) (int, error) {
	if current.SubmitFocused {
		return current.ChoiceCount, nil
	}
	if current.FocusIndex >= 0 && current.FocusIndex < current.ChoiceCount {
		return current.FocusIndex, nil
	}
	return 0, fmt.Errorf("source: Claude's current form focus could not be identified; refresh the session")
}

// Answer selects an option in a question a Codex session is showing.
func (s *CodexSource) Answer(ctx context.Context, sessionID string, answer protocol.QuestionAnswer) error {
	s.mu.RLock()
	session, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("source: unknown codex session %q", sessionID)
	}
	if session.tmuxName == "" {
		return fmt.Errorf("source: only sessions started with `am codex` can be answered")
	}
	if len(answer.Options) > 0 || answer.Text != "" {
		return fmt.Errorf("source: this terminal question only accepts one listed option")
	}
	if err := validateCurrentQuestion(ctx, session.tmuxName, session.meta.Question, answer.OptionKey); err != nil {
		return err
	}
	return tmux.Answer(ctx, session.tmuxName, answer.OptionKey)
}

// validateCurrentQuestion prevents a delayed phone tap from being typed into
// an unrelated prompt. The pane is re-read immediately before the keystroke;
// both the question identity and the selected key must still match what the
// app was shown during discovery.
func validateCurrentQuestion(
	ctx context.Context,
	tmuxName string,
	shown *protocol.Question,
	optionKey string,
) error {
	if shown == nil || !questionHasOption(shown, optionKey) {
		return fmt.Errorf("source: that question or option is no longer current; refresh the session")
	}
	current, err := currentQuestion(ctx, tmuxName, shown)
	if err != nil || !questionHasOption(protocolQuestion(current), optionKey) {
		return fmt.Errorf("source: that question is no longer on screen; refresh the session")
	}
	return nil
}

func currentQuestion(
	ctx context.Context,
	tmuxName string,
	shown *protocol.Question,
) (*question.Question, error) {
	if shown == nil {
		return nil, fmt.Errorf("source: that question is no longer current; refresh the session")
	}
	current, err := captureQuestion(ctx, tmuxName)
	if err != nil || !sameQuestion(shown, protocolQuestion(current)) {
		return nil, fmt.Errorf("source: that question is no longer on screen; refresh the session")
	}
	return current, nil
}

func questionHasOption(question *protocol.Question, key string) bool {
	if question == nil || key == "" {
		return false
	}
	for _, option := range question.Options {
		if option.Key == key {
			return true
		}
	}
	return false
}

func sameQuestion(left, right *protocol.Question) bool {
	if left == nil || right == nil || left.Prompt != right.Prompt || left.Title != right.Title ||
		left.Detail != right.Detail || left.Multiple != right.Multiple || left.Custom != right.Custom ||
		len(left.Options) != len(right.Options) {
		return false
	}
	for i := range left.Options {
		if left.Options[i].Key != right.Options[i].Key ||
			left.Options[i].Label != right.Options[i].Label ||
			left.Options[i].Description != right.Options[i].Description ||
			left.Options[i].Preview != right.Options[i].Preview {
			return false
		}
	}
	return true
}
