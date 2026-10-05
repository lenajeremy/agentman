package source

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Switching a terminal chat's mode and model, by pressing what a person
// would and reading the pane back.
//
// Mode: shift+tab cycles Agent → Plan → Debug → Ask (the CLI's own list,
// "default", "plan", "debug", "search"), and the row under the prompt names
// the mode — "Plan (shift+tab to cycle)", nothing in Agent mode. Each press is
// followed by a read of that row; the switch stops on the mode asked for, and
// fails if a press changes nothing (Cursor ignores shift+tab while it waits on
// a decision) or a whole cycle never reaches it.
//
// Model: with modelSlashCommands on, which is Cursor's default, every model
// has its own command, named from its display name the way the CLI does it
// (lower case, every run of other characters a "-"), or from its model name
// when another command already has that one. Typing it opens Cursor's
// command list; Enter runs the highlighted command, which selects that model
// and prints "Model: <name>". The command is typed only into an empty prompt,
// Enter is pressed only once the highlighted row is exactly that command for
// exactly that model, and the printed line is the read-back. Anything else is
// undone: the typed text is deleted, nothing is selected. The command saves
// its choice as Cursor's default, like every other way of choosing a model.

// cursorCLIPaneModes is the shift+tab cycle, in order, by the names the
// phone shows.
var cursorCLIPaneModes = []string{"agent", "plan", "debug", "ask"}

const (
	// cursorCLISwitchSettle bounds how long a switch waits for the pane to
	// show what a key did.
	cursorCLISwitchSettle = 1500 * time.Millisecond
	cursorCLISwitchPoll   = 60 * time.Millisecond
)

// cursorCLIIdlePrompts are the placeholders of an empty prompt: Cursor shows
// one only when nothing is typed, so a draft of the user's is never there to
// be overwritten.
var cursorCLIIdlePrompts = []string{
	"Add a follow-up", "Plan, search, build anything",
}

func (s *CursorCLISource) paneFor(sessionID string) (string, error) {
	s.mu.RLock()
	session, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("source: unknown Cursor CLI session %q", sessionID)
	}
	if session.pane == "" {
		return "", errors.New("source: only a chat in an `am cursor` pane can be switched from the phone")
	}
	return session.pane, nil
}

// readyToSwitch refuses a pane that is not showing its prompt.
func readyToSwitch(screen cursorCLIScreen) error {
	switch {
	case screen.question != nil:
		return errors.New("source: answer Cursor's pending question first")
	case screen.blocked != "":
		return errors.New(screen.blocked)
	case screen.needsUser:
		return errors.New("source: Cursor is waiting for a decision on the Mac")
	case screen.mode == "":
		return errors.New("source: Cursor's prompt is not on screen")
	}
	return nil
}

// SetMode switches a terminal chat's mode with shift+tab.
func (s *CursorCLISource) SetMode(ctx context.Context, sessionID, mode string) error {
	pane, err := s.paneFor(sessionID)
	if err != nil {
		return err
	}
	if !slices.Contains(cursorCLIPaneModes, mode) {
		return fmt.Errorf("source: Cursor has no %q mode", mode)
	}
	screen, err := s.paneScreen(ctx, pane)
	if err != nil {
		return err
	}
	if err := readyToSwitch(screen); err != nil {
		return err
	}
	start, current := screen.mode, screen.mode
	for range cursorCLIPaneModes {
		if current == mode {
			s.rememberPaneMode(pane, mode)
			return nil
		}
		if err := s.pressKeys(ctx, pane, "BTab"); err != nil {
			return err
		}
		next, err := s.awaitScreen(ctx, pane, func(screen cursorCLIScreen) bool {
			return screen.mode != "" && screen.mode != current
		})
		if err != nil {
			return fmt.Errorf("source: Cursor did not change mode (still %s)", current)
		}
		current = next.mode
		// Back where it started: this Cursor's cycle has no such mode (Plan
		// is behind a feature flag). It is left in the mode it was in.
		if current == start {
			break
		}
	}
	if current == mode {
		s.rememberPaneMode(pane, mode)
		return nil
	}
	s.rememberPaneMode(pane, current)
	return fmt.Errorf("source: Cursor does not offer %s mode here; it is in %s mode", mode, current)
}

func (s *CursorCLISource) rememberPaneMode(pane, mode string) {
	s.turnMu.Lock()
	defer s.turnMu.Unlock()
	if s.paneStatus == nil {
		s.paneStatus = map[string]cursorCLIPaneStatus{}
	}
	status := s.paneStatus[pane]
	status.mode = mode
	s.paneStatus[pane] = status
}

// awaitScreen polls the pane until ready accepts it or the settle time runs
// out.
func (s *CursorCLISource) awaitScreen(ctx context.Context, pane string, ready func(cursorCLIScreen) bool) (cursorCLIScreen, error) {
	deadline := time.Now().Add(cursorCLISwitchSettle)
	for {
		select {
		case <-ctx.Done():
			return cursorCLIScreen{}, ctx.Err()
		case <-time.After(cursorCLISwitchPoll):
		}
		text, err := s.capturePane(ctx, pane)
		if err == nil {
			if screen := parseCursorCLIScreen(text); ready(screen) {
				return screen, nil
			}
		}
		if time.Now().After(deadline) {
			return cursorCLIScreen{}, errors.New("source: Cursor's pane did not change")
		}
	}
}

var cursorCLISlugRun = regexp.MustCompile(`[^a-z0-9]+`)

// cursorCLISlug is the CLI's own command-name function.
func cursorCLISlug(text string) string {
	return strings.Trim(cursorCLISlugRun.ReplaceAllString(strings.ToLower(strings.TrimSpace(text)), "-"), "-")
}

// cursorCLIModelDisplay is the name Cursor shows for a model.
func cursorCLIModelDisplay(model cursorACPModel) string {
	if display := strings.TrimSpace(model.Name); display != "" {
		return display
	}
	return strings.TrimSpace(model.ID)
}

// cursorCLIModelCommands are the commands Cursor may have given a model, in
// the order it tries them: its display name, then its model name.
func cursorCLIModelCommands(model cursorACPModel) []string {
	var commands []string
	for _, name := range []string{cursorCLIModelDisplay(model), model.ID} {
		if command := cursorCLISlug(name); command != "" && !slices.Contains(commands, "/"+command) {
			commands = append(commands, "/"+command)
		}
	}
	return commands
}

// cursorCLIHighlightedCommand reads the command list's highlighted row,
// "→ /claude-opus-5-5   Claude Opus 5.5 · 300K Medium (Tab to modify)",
// returning the command and the text beside it.
func cursorCLIHighlightedCommand(lines []string) (string, string, bool) {
	for i := len(lines) - 1; i >= 0 && i >= len(lines)-30; i-- {
		text := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(text, "→ /") {
			continue
		}
		rest := strings.TrimPrefix(text, "→ ")
		command, description, _ := strings.Cut(rest, " ")
		return command, strings.TrimSpace(description), true
	}
	return "", "", false
}

// SetModelTo switches a terminal chat to a model through its slash command.
func (s *CursorCLISource) SetModelTo(ctx context.Context, sessionID string, model cursorACPModel) error {
	pane, err := s.paneFor(sessionID)
	if err != nil {
		return err
	}
	display := cursorCLIModelDisplay(model)
	commands := cursorCLIModelCommands(model)
	if len(commands) == 0 {
		return fmt.Errorf("source: %q has no Cursor command", model.ID)
	}
	screen, err := s.paneScreen(ctx, pane)
	if err != nil {
		return err
	}
	if err := readyToSwitch(screen); err != nil {
		return err
	}
	text, err := s.capturePane(ctx, pane)
	if err != nil {
		return err
	}
	before := cursorCLIModelLines(cursorCLILines(text), display)
	prompt := cursorCLIPromptLine(cursorCLILines(text))
	if !slices.ContainsFunc(cursorCLIIdlePrompts, func(idle string) bool { return strings.HasPrefix(prompt, idle) }) {
		return errors.New("source: Cursor's prompt has text in it on the Mac; clear it before switching models")
	}
	chosen := ""
	for _, command := range commands {
		if err := s.typeKey(ctx, pane, command); err != nil {
			return err
		}
		// Enter only once the highlighted row is exactly this command for
		// this model; otherwise take the typed text back out.
		_, err = s.awaitScreen(ctx, pane, func(cursorCLIScreen) bool {
			text, _ := s.capturePane(ctx, pane)
			got, description, ok := cursorCLIHighlightedCommand(cursorCLILines(text))
			return ok && got == command && cursorCLIDescribes(description, display)
		})
		if err == nil {
			chosen = command
			break
		}
		if err := s.typeKey(ctx, pane, strings.Repeat("\x7f", len([]rune(command)))); err != nil {
			return err
		}
	}
	if chosen == "" {
		return fmt.Errorf("source: Cursor's command list has no command for %s", display)
	}
	if err := s.pressKeys(ctx, pane, "Enter"); err != nil {
		return err
	}
	var outcome string
	_, err = s.awaitScreen(ctx, pane, func(cursorCLIScreen) bool {
		text, _ := s.capturePane(ctx, pane)
		outcome = cursorCLIModelOutcome(cursorCLILines(text), display, before)
		return outcome != ""
	})
	switch {
	case err != nil:
		return fmt.Errorf("source: Cursor did not confirm the switch to %s", display)
	case outcome != "switched":
		return errors.New("source: " + outcome)
	}
	return nil
}

// cursorCLIDescribes reports whether a command row's description is this
// model's: its display name, alone or followed by " · " and its parameters.
func cursorCLIDescribes(description, display string) bool {
	rest, ok := strings.CutPrefix(description, display)
	if !ok {
		return false
	}
	rest = strings.TrimSpace(rest)
	return rest == "" || strings.HasPrefix(rest, "·") || strings.HasPrefix(rest, "(Tab to modify)")
}

// cursorCLIModelOutcome reads what the model command printed: a new
// "Model: <name>" line on success, or Cursor's reason it could not switch.
// One already on screen from an earlier switch is not this one's.
func cursorCLIModelOutcome(lines []string, display string, before int) string {
	if cursorCLIModelLines(lines, display) > before {
		return "switched"
	}
	for i := len(lines) - 1; i >= 0 && i >= len(lines)-20; i-- {
		text := strings.TrimSpace(lines[i])
		if text == "Could not switch to "+display || text == display+" is no longer available" {
			return text
		}
	}
	return ""
}

func cursorCLIModelLines(lines []string, display string) int {
	count := 0
	for _, line := range lines {
		if strings.TrimSpace(line) == "Model: "+display {
			count++
		}
	}
	return count
}
