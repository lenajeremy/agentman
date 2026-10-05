package source

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/question"
)

// Claude Code 2.1.289, captured on a private tmux server:
//
//   - shift+tab cycles auto → manual → accept edits → plan → auto, and the
//     line under the prompt box says which: "⏵⏵ auto mode on", "⏸ manual mode
//     on", "⏵⏵ accept edits on", "⏸ plan mode on".
//   - /model opens "Select model". Enter sets the pick as the default for new
//     sessions; "s" uses it for this session only and prints "Set model to
//     <model> for this session only". settings.json was unchanged after it.

// claudeModes is the shift+tab cycle, in the footer's words. "bypass
// permissions" joins it only for a session started with that allowed, and is
// offered only when it is the mode on screen.
var claudeModes = []string{"auto", "manual", "accept edits", "plan"}

var claudeModeLine = regexp.MustCompile(`(?:⏵⏵|⏸)\s+(.+?)\s+on\b`)

// claudePaneMode reads the mode from the line under Claude's prompt box. It
// reports false when that line is not on screen: a picker or dialog has the
// focus, or this is a Claude whose footer names no mode.
func claudePaneMode(pane string) (string, bool) {
	lines := strings.Split(strings.TrimRight(pane, "\n"), "\n")
	rule := -1
	for index := len(lines) - 1; index >= 0; index-- {
		if isBoxRule(lines[index]) {
			rule = index
			break
		}
	}
	if rule < 0 {
		return "", false
	}
	for _, line := range lines[rule+1:] {
		if match := claudeModeLine.FindStringSubmatch(line); match != nil {
			return strings.TrimSuffix(match[1], " mode"), true
		}
	}
	return "", false
}

func isBoxRule(line string) bool {
	trimmed := strings.TrimSpace(line)
	return len(trimmed) >= 20*len("─") && strings.Trim(trimmed, "─") == ""
}

// claudeModesFor is what the phone may switch to from the mode on screen.
func claudeModesFor(current string) []string {
	modes := append([]string(nil), claudeModes...)
	for _, mode := range modes {
		if mode == current {
			return modes
		}
	}
	if current == "bypass permissions" {
		return append(modes, current)
	}
	return nil // a mode this cycle does not know: offer nothing
}

// claudePickerModels are the rows Claude Code 2.1.289's /model picker lists
// for an account with every model. They are what the phone is offered until
// agentman next opens the picker, which replaces them with what it shows.
var claudePickerModels = []string{
	"Opus 5.5", "Fable 5.1", "Sonnet 5.5", "Haiku 4.5", "Sonnet 5", "Opus 5",
	"Fable 5", "Opus 4.8", "Opus 4.7", "Opus 4.6", "Sonnet 4.6",
}

var (
	claudeModelIDPattern    = regexp.MustCompile(`^claude-([a-z]+)-(\d+)(?:-(\d{1,2}))?(?:-\d{8})?(?:\[1m\])?$`)
	claudeModelLabelPattern = regexp.MustCompile(`^([A-Z][a-z]+) (\d+)(?:\.(\d+))?$`)
)

// claudeModelLabel is how the picker names a model id: claude-opus-5-5 is
// "Opus 5.5". Empty for an id that does not follow that form.
func claudeModelLabel(id string) string {
	match := claudeModelIDPattern.FindStringSubmatch(id)
	if match == nil {
		return ""
	}
	label := strings.ToUpper(match[1][:1]) + match[1][1:] + " " + match[2]
	if match[3] != "" {
		label += "." + match[3]
	}
	return label
}

// claudeModelID is the id a picker row stands for: "Opus 5.5" is
// claude-opus-5-5, the form transcripts record.
func claudeModelID(label string) string {
	match := claudeModelLabelPattern.FindStringSubmatch(label)
	if match == nil {
		return ""
	}
	id := "claude-" + strings.ToLower(match[1]) + "-" + match[2]
	if match[3] != "" {
		id += "-" + match[3]
	}
	return id
}

func (s *ClaudeSource) offeredModels() []string {
	s.switchMu.Lock()
	labels := s.pickerLabels
	s.switchMu.Unlock()
	if len(labels) == 0 {
		labels = claudePickerModels
	}
	var models []string
	for _, label := range labels {
		if id := claudeModelID(label); id != "" {
			models = append(models, id)
		}
	}
	return models
}

// applyPane fills in what a pane-bound session's screen says about it: the
// question it is blocked on, and the mode and models it can be switched to.
func (s *ClaudeSource) applyPane(meta *protocol.Session, transcript, pane string) {
	if detected := question.Detect(pane); detected != nil {
		s.enrichClaudeQuestion(meta.ID, transcript, detected)
		meta.Question = protocolQuestion(detected)
		meta.State = protocol.StateWaitingInput
		return
	}
	mode, ok := claudePaneMode(pane)
	if !ok {
		return
	}
	meta.Mode = mode
	meta.Modes = claudeModesFor(mode)
	meta.Models = s.offeredModels()
	meta.ModelScope = protocol.ModelScopeSession
}

// switched returns the model this session was switched to from the phone,
// while its transcript has not yet recorded a reply since. The switch is
// not written anywhere Claude's transcript shows until the next reply.
func (s *ClaudeSource) switched(id, transcript string) (string, bool) {
	s.switchMu.Lock()
	change, ok := s.modelSwitches[id]
	s.switchMu.Unlock()
	if !ok {
		return "", false
	}
	if _, at := latestClaudeModel(transcript); at.After(change.at) {
		s.switchMu.Lock()
		delete(s.modelSwitches, id)
		s.switchMu.Unlock()
		s.models.put(id, modelFromTranscript(transcript, claudeModelOf))
		return "", false
	}
	return change.model, true
}

type claudeModelSwitch struct {
	model string
	at    time.Time
}

// latestClaudeModel is the model of the newest assistant reply in a
// transcript, and when it was written.
func latestClaudeModel(path string) (string, time.Time) {
	var model string
	var at time.Time
	scanTail(path, modelScanBytes, func(line []byte) bool {
		if !bytes.Contains(line, []byte(`"assistant"`)) {
			return false
		}
		var record struct {
			Type      string `json:"type"`
			Timestamp string `json:"timestamp"`
			Message   struct {
				Model string `json:"model"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &record) != nil || record.Type != "assistant" {
			return false
		}
		if found := cleanModel(record.Message.Model); found != "" {
			model = found
			at, _ = time.Parse(time.RFC3339Nano, record.Timestamp)
			return true
		}
		return false
	})
	return model, at
}

// claudeSwitchable returns the pane of a session that can be switched.
func (s *ClaudeSource) claudeSwitchable(sessionID string) (string, error) {
	s.mu.RLock()
	session, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("source: unknown claude session %q", sessionID)
	}
	if session.tmuxName == "" {
		return "", errors.New("source: only sessions started with `am claude` can be switched from the phone")
	}
	return session.tmuxName, nil
}

// SetMode implements ModeSetter: shift+tab, reading the mode back from the
// footer after each press, until it is the one asked for.
func (s *ClaudeSource) SetMode(ctx context.Context, sessionID, mode string) error {
	name, err := s.claudeSwitchable(sessionID)
	if err != nil {
		return err
	}
	d := s.switcher
	for presses := 0; presses <= len(claudeModes)+1; presses++ {
		pane, err := d.capture(ctx, name)
		if err != nil {
			return fmt.Errorf("source: could not read Claude's screen: %w", err)
		}
		if question.Detect(pane) != nil {
			return errors.New("source: answer Claude's question first")
		}
		current, ok := claudePaneMode(pane)
		if !ok {
			return errors.New("source: Claude's mode is not on screen; close what is open in the terminal first")
		}
		if current == mode {
			return nil
		}
		if err := d.keys(ctx, name, "BTab"); err != nil {
			return fmt.Errorf("source: could not switch Claude's mode: %w", err)
		}
		if _, err := d.await(ctx, name, func(pane string) bool {
			now, ok := claudePaneMode(pane)
			return !ok || now != current
		}); err != nil {
			return errors.New("source: Claude's mode did not change")
		}
	}
	return fmt.Errorf("source: Claude never showed %s mode", mode)
}

// claudeSwitchWarning heads the dialog Claude shows before switching the
// model of a session with history: the switch costs a cache miss.
const claudeSwitchWarning = "Switch model?"

// claudeSwitchOpen reports the model picker or its warning on screen.
func claudeSwitchOpen(pane string) bool {
	return claudeModelPickerOpen(pane) || strings.Contains(pane, claudeSwitchWarning)
}

// claudeConfirmsSwitchTo reports the warning with "Yes, switch to <label>"
// focused, so Enter confirms that switch and nothing else.
func claudeConfirmsSwitchTo(pane, label string) bool {
	index := strings.Index(pane, claudeSwitchWarning)
	if index < 0 {
		return false
	}
	for _, row := range pickerRows(strings.Split(pane[index:], "\n"), "❯") {
		if row.focused {
			return row.label == "Yes, switch to "+label
		}
	}
	return false
}

func claudeModelPickerOpen(pane string) bool {
	return strings.Contains(pane, "Select model") && strings.Contains(pane, "s to use this session only")
}

// claudeModelRows reads the model picker's rows. The first is "Default
// (recommended) ✔  <model> · …", whose label ends at the check mark.
func claudeModelRows(pane string) ([]paneRow, bool) {
	if !claudeModelPickerOpen(pane) {
		return nil, false
	}
	lines := strings.Split(pane, "\n")
	for index, line := range lines {
		if strings.Contains(line, "Select model") {
			lines = lines[index:]
			break
		}
	}
	rows := pickerRows(lines, "❯")
	for index := range rows {
		rows[index].label = strings.TrimSpace(strings.TrimSuffix(rows[index].label, "✔"))
	}
	return rows, true
}

// SetModel implements ModelSetter: /model, the model's row, then "s" for
// this session only. Enter would also make it the default for new sessions,
// and is never pressed.
func (s *ClaudeSource) SetModel(ctx context.Context, sessionID, model string) error {
	name, err := s.claudeSwitchable(sessionID)
	if err != nil {
		return err
	}
	label := claudeModelLabel(model)
	if label == "" {
		return fmt.Errorf("source: %q is not a Claude model this can switch to", model)
	}
	d := s.switcher
	pane, err := d.capture(ctx, name)
	if err != nil {
		return fmt.Errorf("source: could not read Claude's screen: %w", err)
	}
	if _, ok := claudePaneMode(pane); !ok || question.Detect(pane) != nil {
		return errors.New("source: Claude is not at its prompt; close what is open in the terminal first")
	}
	if err := d.line(ctx, name, "/model"); err != nil {
		return fmt.Errorf("source: could not open Claude's model picker: %w", err)
	}
	if _, err := d.await(ctx, name, claudeModelPickerOpen); err != nil {
		return errors.New("source: Claude's model picker did not open")
	}
	picker, err := d.focusRow(ctx, name, func(row string) bool { return row == label }, claudeModelRows, 24)
	if err != nil {
		d.closePicker(ctx, name, claudeSwitchOpen, 3)
		return fmt.Errorf("source: could not choose %s in Claude's picker: %w", label, err)
	}
	s.rememberPicker(picker)
	fail := func(err error) error {
		d.closePicker(ctx, name, claudeSwitchOpen, 3)
		return err
	}
	if err := d.literal(ctx, name, "s"); err != nil {
		return fail(fmt.Errorf("source: could not choose %s: %w", label, err))
	}
	done := "Set model to " + label + " for this session only"
	switched := func(pane string) bool {
		return !claudeSwitchOpen(pane) && strings.Contains(strings.Join(bottomLines(pane, 8), "\n"), done)
	}
	pane, err = d.await(ctx, name, func(pane string) bool {
		return switched(pane) || strings.Contains(pane, claudeSwitchWarning)
	})
	if err == nil && !switched(pane) {
		// A session with history warns that switching re-reads all of it on
		// the next message. The phone asked for this switch; it is confirmed
		// only when the warning names that model, on the focused row.
		if !claudeConfirmsSwitchTo(pane, label) {
			return fail(fmt.Errorf("source: Claude asked to confirm a switch to another model than %s", label))
		}
		if err := d.keys(ctx, name, "Enter"); err != nil {
			return fail(fmt.Errorf("source: could not confirm %s: %w", label, err))
		}
		_, err = d.await(ctx, name, switched)
	}
	if err != nil {
		return fail(fmt.Errorf("source: Claude did not confirm %s", label))
	}
	s.switchMu.Lock()
	if s.modelSwitches == nil {
		s.modelSwitches = map[string]claudeModelSwitch{}
	}
	s.modelSwitches[sessionID] = claudeModelSwitch{model: model, at: time.Now()}
	s.switchMu.Unlock()
	return nil
}

// rememberPicker keeps the rows the picker showed, so the phone is offered
// what this Claude actually lists. Rows scrolled out of sight are kept from
// before.
func (s *ClaudeSource) rememberPicker(pane string) {
	rows, ok := claudeModelRows(pane)
	if !ok {
		return
	}
	s.switchMu.Lock()
	defer s.switchMu.Unlock()
	known := s.pickerLabels
	if len(known) == 0 {
		known = claudePickerModels
	}
	seen := map[string]bool{}
	var labels []string
	for _, row := range rows {
		if claudeModelID(row.label) != "" && !seen[row.label] {
			seen[row.label] = true
			labels = append(labels, row.label)
		}
	}
	for _, label := range known {
		if !seen[label] {
			seen[label] = true
			labels = append(labels, label)
		}
	}
	s.pickerLabels = labels
}
