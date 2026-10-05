package source

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/question"
)

// Codex 0.159.3, captured on a private tmux server:
//
//   - shift+tab toggles Plan mode. The line under the composer names the
//     model and effort ("GPT-6.1-Sol xhigh · <folder>") and ends with
//     "Plan mode" in it; the default mode adds nothing.
//   - /model opens "Select Model and Effort"; a model leads to "Select
//     Reasoning Level for <model>", where "s" applies it for this session
//     only ("Model changed to gpt-6-luna medium for this session only") and
//     Enter saves it as the default. config.toml was unchanged after "s".

// codexModes are the modes shift+tab moves between.
var codexModes = []string{"default", "plan"}

var codexFooterPattern = regexp.MustCompile(`^\s*(\S.*?)\s+(?:minimal|low|medium|high|xhigh|max|ultra)\s+·`)

// codexFooter reads the line under Codex's composer: the model's display
// name, and the mode. It reports false when that line is not on screen,
// which is the case whenever a picker or a question has the focus.
func codexFooter(pane string) (model, mode string, ok bool) {
	lines := bottomLines(pane, 10)
	composer := -1
	for index, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "› ") && !pickerRowPattern.MatchString(line) {
			composer = index
		}
	}
	if composer < 0 {
		return "", "", false
	}
	for _, line := range lines[composer+1:] {
		match := codexFooterPattern.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		mode = "default"
		if strings.HasSuffix(strings.TrimSpace(line), "Plan mode") {
			mode = "plan"
		}
		return match[1], mode, true
	}
	return "", "", false
}

// codexWorking reports a turn in progress: Codex's status line sits just
// above the composer while one runs.
func codexWorking(pane string) bool {
	for _, line := range bottomLines(pane, 8) {
		if strings.Contains(line, "esc to interrupt") {
			return true
		}
	}
	return false
}

// codexModel is one model Codex's picker lists.
type codexModelEntry struct {
	slug, display string
}

// codexModelsTTL is how long the model list read from Codex's own cache is
// reused before its file is looked at again.
const codexModelsTTL = 2 * time.Minute

// codexModels lists the models Codex's /model picker offers, from the cache
// Codex keeps of them (~/.codex/models_cache.json): the ones it lists, in
// its order. A model with no reasoning levels is left out, because the
// picker would apply it at once, as the default, with no step to choose
// "this session only" in.
func (s *CodexSource) codexModels() []codexModelEntry {
	path := filepath.Join(s.home, ".codex", "models_cache.json")
	s.switchMu.Lock()
	defer s.switchMu.Unlock()
	if time.Since(s.modelsReadAt) < codexModelsTTL {
		return s.modelList
	}
	s.modelsReadAt = time.Now()
	info, err := os.Stat(path)
	if err != nil {
		s.modelList = nil
		return nil
	}
	if info.ModTime().Equal(s.modelsModTime) && info.Size() == s.modelsSize {
		return s.modelList
	}
	if info.Size() > 8<<20 {
		s.modelList = nil
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return s.modelList
	}
	var cache struct {
		Models []struct {
			Slug        string `json:"slug"`
			DisplayName string `json:"display_name"`
			Visibility  string `json:"visibility"`
			Priority    int    `json:"priority"`
			Levels      []struct {
				Effort string `json:"effort"`
			} `json:"supported_reasoning_levels"`
		} `json:"models"`
	}
	if json.Unmarshal(raw, &cache) != nil {
		return s.modelList
	}
	sort.SliceStable(cache.Models, func(i, j int) bool { return cache.Models[i].Priority < cache.Models[j].Priority })
	var list []codexModelEntry
	for _, model := range cache.Models {
		if model.Visibility != "list" || model.Slug == "" || model.DisplayName == "" || len(model.Levels) == 0 {
			continue
		}
		list = append(list, codexModelEntry{slug: model.Slug, display: model.DisplayName})
	}
	s.modelList, s.modelsModTime, s.modelsSize = list, info.ModTime(), info.Size()
	return list
}

// applyFooter fills in what the line under the composer says: the mode, the
// model, and what the phone can switch them to.
func (s *CodexSource) applyFooter(session *protocol.Session, pane string) {
	display, mode, ok := codexFooter(pane)
	if !ok || session.Question != nil {
		return
	}
	session.Mode = mode
	session.Modes = append([]string(nil), codexModes...)
	models := s.codexModels()
	if len(models) == 0 {
		return
	}
	for _, model := range models {
		session.Models = append(session.Models, model.slug)
		// The footer is the model in use now; the rollout only records a
		// switch at the next turn.
		if strings.EqualFold(model.display, display) {
			session.Model = model.slug
		}
	}
	session.ModelScope = protocol.ModelScopeSession
}

func (s *CodexSource) codexSwitchable(sessionID string) (string, error) {
	s.mu.RLock()
	session, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("source: unknown codex session %q", sessionID)
	}
	if session.tmuxName == "" {
		return "", errors.New("source: only sessions started with `am codex` can be switched from the phone")
	}
	return session.tmuxName, nil
}

// codexAtComposer reads the pane and requires Codex to be waiting at its
// composer: no question, no turn running, the footer on screen.
func codexAtComposer(pane string) error {
	if question.Detect(pane) != nil || question.CodexQueued(pane) {
		return errors.New("source: answer Codex's question first")
	}
	if codexWorking(pane) {
		return errors.New("source: Codex is working; switch when the turn ends")
	}
	if _, _, ok := codexFooter(pane); !ok {
		return errors.New("source: Codex is not at its prompt; close what is open in the terminal first")
	}
	return nil
}

// SetMode implements ModeSetter: shift+tab, reading the mode back from the
// footer after each press, until it is the one asked for.
func (s *CodexSource) SetMode(ctx context.Context, sessionID, mode string) error {
	name, err := s.codexSwitchable(sessionID)
	if err != nil {
		return err
	}
	d := s.switcher
	for presses := 0; presses <= len(codexModes)+1; presses++ {
		pane, err := d.capture(ctx, name)
		if err != nil {
			return fmt.Errorf("source: could not read Codex's screen: %w", err)
		}
		if err := codexAtComposer(pane); err != nil {
			return err
		}
		_, current, _ := codexFooter(pane)
		if current == mode {
			return nil
		}
		if err := d.keys(ctx, name, "BTab"); err != nil {
			return fmt.Errorf("source: could not switch Codex's mode: %w", err)
		}
		if _, err := d.await(ctx, name, func(pane string) bool {
			_, now, ok := codexFooter(pane)
			return !ok || now != current
		}); err != nil {
			return errors.New("source: Codex's mode did not change")
		}
	}
	return fmt.Errorf("source: Codex never showed %s mode", mode)
}

func codexModelPickerOpen(pane string) bool {
	return strings.Contains(pane, "Select Model and Effort") && strings.Contains(pane, "esc back")
}

func codexEffortPickerOpen(display string) func(string) bool {
	return func(pane string) bool {
		return strings.Contains(pane, "Select Reasoning Level for "+display) && strings.Contains(pane, "s session")
	}
}

func codexPickerOpen(pane string) bool {
	return codexModelPickerOpen(pane) || strings.Contains(pane, "Select Reasoning Level for ")
}

// codexModelRows reads the model picker's rows; a row's label is the model's
// display name, with "(current)" after the one in use.
func codexModelRows(pane string) ([]paneRow, bool) {
	if !codexModelPickerOpen(pane) {
		return nil, false
	}
	lines := strings.Split(pane, "\n")
	for index, line := range lines {
		if strings.Contains(line, "Select Model and Effort") {
			lines = lines[index:]
			break
		}
	}
	rows := pickerRows(lines, "›")
	for index := range rows {
		rows[index].label = strings.TrimSpace(strings.TrimSuffix(rows[index].label, "(current)"))
	}
	return rows, true
}

// SetModel implements ModelSetter: /model, the model's row, Enter to reach
// its reasoning level, then "s" for this session only. Enter on the
// reasoning level would save the model as the default, and is never pressed.
func (s *CodexSource) SetModel(ctx context.Context, sessionID, model string) error {
	name, err := s.codexSwitchable(sessionID)
	if err != nil {
		return err
	}
	display := ""
	for _, entry := range s.codexModels() {
		if entry.slug == model {
			display = entry.display
		}
	}
	if display == "" {
		return fmt.Errorf("source: %q is not a model Codex lists", model)
	}
	d := s.switcher
	pane, err := d.capture(ctx, name)
	if err != nil {
		return fmt.Errorf("source: could not read Codex's screen: %w", err)
	}
	if err := codexAtComposer(pane); err != nil {
		return err
	}
	if err := d.line(ctx, name, "/model"); err != nil {
		return fmt.Errorf("source: could not open Codex's model picker: %w", err)
	}
	if _, err := d.await(ctx, name, codexModelPickerOpen); err != nil {
		return errors.New("source: Codex's model picker did not open")
	}
	fail := func(err error) error {
		d.closePicker(ctx, name, codexPickerOpen, 3)
		return err
	}
	if _, err := d.focusRow(ctx, name, func(label string) bool { return label == display }, codexModelRows, 16); err != nil {
		return fail(fmt.Errorf("source: could not choose %s in Codex's picker: %w", display, err))
	}
	if err := d.keys(ctx, name, "Enter"); err != nil {
		return fail(fmt.Errorf("source: could not choose %s: %w", display, err))
	}
	if _, err := d.await(ctx, name, codexEffortPickerOpen(display)); err != nil {
		return fail(fmt.Errorf("source: Codex did not ask for %s's reasoning level", display))
	}
	if err := d.literal(ctx, name, "s"); err != nil {
		return fail(fmt.Errorf("source: could not choose %s: %w", display, err))
	}
	done := "Model changed to " + model + " "
	if _, err := d.await(ctx, name, func(pane string) bool {
		if codexPickerOpen(pane) {
			return false
		}
		for _, line := range bottomLines(pane, 10) {
			if strings.Contains(line, done) && strings.Contains(line, "for this session only") {
				return true
			}
		}
		return false
	}); err != nil {
		return fail(fmt.Errorf("source: Codex did not confirm %s", display))
	}
	return nil
}
