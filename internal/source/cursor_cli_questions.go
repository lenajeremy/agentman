package source

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/tmux"
)

// What a Cursor CLI pane is showing, read from one capture.
//
// Cursor draws every decision in the same few shapes, all found in its own
// bundle and checked against captures from a real pane
// (testdata/cursor-cli-pane-*.txt):
//
//   - an approval block: the subject ("$  ls in .", "Delete file: x"), a
//     question line ("Run this command?", "Delete this file?" …), optional
//     reason lines, then one option per row, each ending in its keys:
//     "→ Run (once) (y)", "Keep (n)", "Skip & tell the agent what to do
//     instead (esc or n)";
//   - the plan box, "Suggested Plan … Ready to build?" with numbered options
//     "1. Yes, build locally (b)" and "2. No, propose changes (p or Esc)";
//   - the workspace trust box, "[a] Trust this workspace / [q] Quit";
//   - two free-text boxes that replace the prompt's placeholder: "Tell the
//     agent what to do instead (…)" after a skip, and "Describe how to revise
//     the plan (…)" after asking for plan changes.
//
// The free-text boxes matter as much as the menus. Answering the shell
// prompt with "n" does not skip: it opens the first box and Cursor waits there
// for an instruction. Before this was read, the phone showed a busy session
// that never moved.
type cursorCLIScreen struct {
	// question is the decision on screen, ready for the phone.
	question *protocol.Question
	// customKey opens the free-text box a Custom answer is typed into. Empty
	// when the screen already is that box.
	customKey string
	textBox   bool
	// needsUser is a decision Cursor is waiting on whose layout is not one
	// the phone can answer: the session is reported as waiting without
	// options rather than as idle.
	needsUser bool
	// blocked explains why a send would land somewhere other than the prompt:
	// a picker, a panel or shell mode holds the keyboard.
	blocked string
	busy    bool
}

var (
	// The question line of every approval Cursor's TUI draws, mapped to the
	// title the phone shows. From the TUI's own switch over operation types.
	cursorCLIApprovals = map[string]string{
		"Run this command?":                     "Shell command",
		"Run this command outside the sandbox?": "Shell command",
		"Run this MCP tool?":                    "MCP tool",
		"Delete this file?":                     "Delete file",
		"Write to this file?":                   "Write file",
		"Proceed with this edit?":               "Edit file",
		"Read this file?":                       "Read file",
		"Allow this web search?":                "Web search",
		"Allow web search?":                     "Web search",
		"Allow this web fetch?":                 "Web fetch",
		"Allow web fetch?":                      "Web fetch",
		"Approve mode switch?":                  "Mode switch",
	}
	// "→ Run (once) (y)", "2. No, propose changes (p or Esc)",
	// "Add Shell(echo) to allowlist? (tab)". The keys are always the last
	// parenthesised group; the label may hold parentheses of its own.
	cursorCLIOption = regexp.MustCompile(`^(?:→\s*)?(?:\d+\.\s+)?(\S.*?)\s+\(([^()]+)\)$`)
	// The prompt line's placeholder while Cursor waits in a free-text box,
	// and while it waits on something the phone cannot answer.
	cursorCLITextBoxes = map[string]string{
		"Tell the agent what to do instead": "Tell the agent what to do instead",
		"Describe how to revise the plan":   "Describe how to revise the plan",
	}
	cursorCLIWaitingPlaceholders = []string{
		"Answer questions (Enter to select/next, Esc to skip)",
		"Approve mode switch (y/n)",
		"Waiting for decision (y/n/p)",
		"Edit the image prompt, then press Enter to generate",
	}
	// Footers of Cursor's pickers and panels (/resume, /model, /config …),
	// from its pager tokens. Text typed while one is open drives the picker.
	cursorCLIPickerFooters = []string{
		"Enter to select", "Esc to close", "Esc to go back", "Tab to switch sections",
		"Enter to confirm", "Use arrow keys to navigate",
	}
)

// cursorCLIQuestionFromPane reports the decision a pane is waiting on.
func cursorCLIQuestionFromPane(pane string) *protocol.Question {
	return parseCursorCLIScreen(pane).question
}

func parseCursorCLIScreen(pane string) cursorCLIScreen {
	lines := cursorCLILines(pane)
	var screen cursorCLIScreen
	screen.busy = cursorCLIBusy(lines)
	if q := cursorCLITrustQuestion(lines); q != nil {
		screen.question = q
		return screen
	}
	if q, custom := cursorCLIMenuQuestion(lines); q != nil {
		screen.question, screen.customKey = q, custom
		return screen
	}
	prompt := cursorCLIPromptLine(lines)
	for marker, title := range cursorCLITextBoxes {
		if strings.HasPrefix(prompt, marker) {
			q := &protocol.Question{Title: title, Prompt: title + ".", Custom: true,
				Detail: cursorCLITextBoxDetail(lines)}
			q.ID = terminalQuestionID(q)
			screen.question, screen.textBox = q, true
			return screen
		}
	}
	for _, placeholder := range cursorCLIWaitingPlaceholders {
		if strings.HasPrefix(prompt, placeholder) {
			screen.needsUser = true
			screen.blocked = "source: Cursor is waiting for a decision on the Mac; answer it there"
			return screen
		}
	}
	if strings.HasPrefix(prompt, "Run a command") {
		// "!" on an empty prompt switches Cursor into shell mode, where Enter
		// runs the line as a shell command without asking. A message from the
		// phone typed there would be executed, not read by the agent.
		screen.blocked = "source: Cursor is in shell mode on the Mac; leave it there before sending"
		return screen
	}
	if footer := cursorCLIOpenPicker(lines); footer != "" {
		screen.blocked = "source: a Cursor menu is open on the Mac; close it there before sending"
	}
	return screen
}

// cursorCLILines splits a capture, trimming the right edge and the blank rows
// tmux pads the bottom of a pane with.
func cursorCLILines(pane string) []string {
	lines := strings.Split(pane, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t\r")
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// cursorCLIBoxText strips a box's borders and the indentation inside them.
func cursorCLIBoxText(line string) string {
	return strings.TrimSpace(strings.Trim(strings.TrimSpace(line), "│┃|"))
}

func cursorCLIRule(line string) bool {
	text := strings.Trim(strings.TrimSpace(line), "│┃")
	text = strings.TrimSpace(text)
	return len([]rune(text)) >= 8 && strings.Trim(text, "─━┌┐└┘╭╮╰╯") == ""
}

// cursorCLIBarLine is the half-block row Cursor draws above and below its
// prompt. It is not drawn on every terminal, so nothing depends on it alone.
func cursorCLIBarLine(line string) bool {
	text := strings.TrimSpace(line)
	return len([]rune(text)) >= 8 && (strings.Trim(text, "▄") == "" || strings.Trim(text, "▀") == "")
}

// cursorCLIPromptLine returns the text of the prompt row ("→ Add a
// follow-up"), or "" when the bottom of the pane holds no prompt. The search
// reaches well up the pane because Cursor prints its slash-command list,
// hints and errors underneath the prompt.
func cursorCLIPromptLine(lines []string) string {
	for i := len(lines) - 1; i >= 0 && i >= len(lines)-30; i-- {
		if !cursorCLIBarLine(lines[i]) || !strings.HasPrefix(strings.TrimSpace(lines[i]), "▄") {
			continue
		}
		for j := i + 1; j < len(lines) && j <= i+3; j++ {
			if text, ok := cursorCLIPromptText(lines[j]); ok {
				return text
			}
		}
		return ""
	}
	// Without the half-block bars, the prompt is the last arrow row that is
	// not part of a menu.
	for i := len(lines) - 1; i >= 0 && i >= len(lines)-8; i-- {
		if cursorCLIOption.MatchString(strings.TrimSpace(lines[i])) {
			continue
		}
		if text, ok := cursorCLIPromptText(lines[i]); ok {
			return text
		}
	}
	return ""
}

// cursorCLIPromptText reads one "→ …" row, dropping the right-aligned hint
// ("ctrl+c to stop") Cursor pads onto the same line.
func cursorCLIPromptText(line string) (string, bool) {
	text := strings.TrimSpace(line)
	if !strings.HasPrefix(text, "→") {
		return "", false
	}
	text = strings.TrimSpace(strings.TrimPrefix(text, "→"))
	if gap := strings.Index(text, "    "); gap >= 0 {
		text = strings.TrimSpace(text[:gap])
	}
	return text, true
}

// cursorCLIBusy reads the running indicator: Cursor's "ctrl+c to stop" hint
// beside the prompt, or the spinner row above it ("⠞ Working", "⠜ Running
// 71 tokens", "⠜ Reconnecting (attempt 1, 0s)"). The hint alone missed the
// reconnecting state, which the turn passes through before it ends, so a
// finished-looking pane announced completion and then finished again.
func cursorCLIBusy(lines []string) bool {
	start := max(0, len(lines)-12)
	for _, line := range lines[start:] {
		if strings.Contains(strings.ToLower(line), "ctrl+c to stop") {
			return true
		}
	}
	for _, line := range lines[start:] {
		if cursorCLISpinner(line) {
			return true
		}
	}
	return false
}

// cursorCLISpinner matches a row that starts with braille spinner glyphs and
// then a capitalised word.
func cursorCLISpinner(line string) bool {
	text := strings.TrimSpace(line)
	runes := []rune(text)
	i := 0
	for i < len(runes) && runes[i] >= 0x2800 && runes[i] <= 0x28FF {
		i++
	}
	if i == 0 || i >= len(runes)-1 || runes[i] != ' ' {
		return false
	}
	return unicode.IsUpper(runes[i+1])
}

func cursorCLIOpenPicker(lines []string) string {
	start := max(0, len(lines)-4)
	for _, line := range lines[start:] {
		for _, footer := range cursorCLIPickerFooters {
			if strings.Contains(line, footer) {
				return footer
			}
		}
	}
	return ""
}

// cursorCLITrustQuestion recognises the workspace trust box, but only while
// its footer is still the last thing in the pane: a fragment in scrollback
// must never become answerable.
func cursorCLITrustQuestion(lines []string) *protocol.Question {
	footer := -1
	for i := len(lines) - 1; i >= 0 && i >= len(lines)-5; i-- {
		if strings.Contains(lines[i], "Use arrow keys to navigate") {
			footer = i
			break
		}
	}
	if footer < 0 {
		return nil
	}
	for _, raw := range lines[footer+1:] {
		// Cursor frames a blank row with vertical borders between the
		// footer and bottom edge. It is not newer terminal content.
		line := strings.TrimSpace(strings.Trim(raw, " │"))
		if line != "" && !strings.HasPrefix(line, "╰") {
			return nil
		}
	}
	trust, quit := false, false
	for i := footer - 1; i >= 0 && i >= footer-12; i-- {
		trust = trust || strings.Contains(lines[i], "[a] Trust this workspace")
		quit = quit || strings.Contains(lines[i], "[q] Quit")
	}
	if !trust || !quit {
		return nil
	}
	q := &protocol.Question{
		Title: "Workspace trust", Prompt: "Trust this workspace?",
		Options: []protocol.QuestionOption{
			{Key: "a", Label: "Trust this workspace"},
			{Key: "q", Label: "Quit"},
		},
	}
	q.ID = terminalQuestionID(q)
	return q
}

type cursorCLIMenuOption struct {
	label string
	keys  []string
}

// letter is the single-letter key that chooses an option, or "" when only
// a named key (tab, shift+tab, esc) does.
func (o cursorCLIMenuOption) letter() string {
	for _, key := range o.keys {
		if len(key) == 1 && key[0] >= 'a' && key[0] <= 'z' {
			return key
		}
	}
	return ""
}

// proposes reports the options that open a free-text box rather than
// deciding on their own: "Skip & tell the agent what to do instead",
// "Reject & propose changes", "No, propose changes".
func (o cursorCLIMenuOption) proposes() bool {
	label := strings.ToLower(o.label)
	return strings.Contains(label, "propose changes") || strings.Contains(label, "tell the agent")
}

func parseCursorCLIOption(line string) (cursorCLIMenuOption, bool) {
	match := cursorCLIOption.FindStringSubmatch(cursorCLIBoxText(line))
	if match == nil {
		return cursorCLIMenuOption{}, false
	}
	var keys []string
	for _, key := range strings.Split(match[2], " or ") {
		key = strings.ToLower(strings.TrimSpace(key))
		if key == "" || strings.ContainsAny(key, " ,") && key != "shift+tab" {
			return cursorCLIMenuOption{}, false
		}
		keys = append(keys, key)
	}
	return cursorCLIMenuOption{label: strings.TrimSpace(match[1]), keys: keys}, true
}

// cursorCLIMenuQuestion reads an approval block or the plan box, returning
// the question and the key that opens its free-text box.
func cursorCLIMenuQuestion(lines []string) (*protocol.Question, string) {
	promptAt, title := -1, ""
	for i := len(lines) - 1; i >= 0 && i >= len(lines)-40; i-- {
		text := cursorCLIBoxText(lines[i])
		if t, ok := cursorCLIApprovals[text]; ok {
			promptAt, title = i, t
			break
		}
		if text == "Ready to build?" || text == "Ready to proceed?" {
			promptAt, title = i, "Cursor plan"
			break
		}
	}
	if promptAt < 0 {
		return nil, ""
	}
	var options []cursorCLIMenuOption
	var reasons []string
	last := -1
	for i := promptAt + 1; i < len(lines); i++ {
		text := cursorCLIBoxText(lines[i])
		if option, ok := parseCursorCLIOption(lines[i]); ok {
			options = append(options, option)
			last = i
			continue
		}
		if len(options) > 0 {
			break
		}
		if text != "" && !cursorCLIRule(lines[i]) {
			reasons = append(reasons, text)
		}
	}
	if len(options) == 0 || !cursorCLIMenuIsCurrent(lines, last) {
		return nil, ""
	}
	detail, ok := cursorCLIMenuDetail(lines, promptAt, title)
	if !ok {
		// A remote approval must show what it approves. If Cursor's pane
		// does not expose it, the decision stays on the Mac.
		return nil, ""
	}
	if len(reasons) > 0 && title != "Cursor plan" {
		detail = strings.TrimSpace(detail + "\n\n" + strings.Join(reasons, "\n"))
	}
	if len(detail) > 16*1024 {
		return nil, ""
	}
	q := &protocol.Question{Title: title, Prompt: cursorCLIBoxText(lines[promptAt]), Detail: detail}
	custom := ""
	for _, option := range options {
		key := option.letter()
		if option.proposes() {
			// Chosen with text from the phone: the key opens Cursor's box and
			// the text goes in it. A bare choice would leave Cursor waiting
			// in that box for an Enter only a named-key send can give.
			if key != "" && custom == "" {
				custom = key
				q.Custom = true
			}
			continue
		}
		if key == "" {
			// Tab and shift+Tab options (allowlist, Run Everything) need a
			// named-key send; they are left on the Mac until there is one.
			continue
		}
		q.Options = append(q.Options, protocol.QuestionOption{Key: key, Label: option.label})
	}
	if len(q.Options) == 0 && !q.Custom {
		return nil, ""
	}
	q.ID = terminalQuestionID(q)
	return q, custom
}

// cursorCLIMenuIsCurrent requires the menu to be the live bottom of the pane.
// After an answer Cursor erases the menu and redraws its prompt, so a prompt
// row below the menu means the menu is history. Below a live menu there may
// only be the box's bottom edge, a spinner, a hint row, and an error Cursor
// printed under its UI ("Error: You've hit your usage limit …").
func cursorCLIMenuIsCurrent(lines []string, last int) bool {
	extra := 0
	for _, raw := range lines[last+1:] {
		text := strings.TrimSpace(raw)
		switch {
		case text == "":
		case strings.HasPrefix(text, "Error:"):
			return true
		case cursorCLIBarLine(raw), strings.HasPrefix(text, "→"):
			return false
		case cursorCLIRule(raw), strings.HasPrefix(text, "└"), strings.HasPrefix(text, "╰"),
			cursorCLISpinner(raw), cursorCLIBoxText(raw) == "":
		default:
			extra++
		}
	}
	return extra <= 2
}

// cursorCLIMenuDetail collects what a menu is about: the rows between the
// rule (or box edge) above it and its question line. Shell approvals must
// show their command ("$  ls -la in ."); the plan box shows the plan.
func cursorCLIMenuDetail(lines []string, promptAt int, title string) (string, bool) {
	if title == "Cursor plan" {
		return cursorCLIPlanDetail(lines, promptAt)
	}
	var parts []string
	for i := promptAt - 1; i >= 0 && i >= promptAt-20; i-- {
		if cursorCLIRule(lines[i]) || cursorCLIBarLine(lines[i]) {
			break
		}
		if text := cursorCLIBoxText(lines[i]); text != "" {
			parts = append([]string{text}, parts...)
		}
	}
	if title == "Shell command" {
		for len(parts) > 0 && !strings.HasPrefix(parts[0], "$") {
			parts = parts[1:]
		}
		if len(parts) == 0 {
			return "", false
		}
		parts[0] = strings.TrimSpace(strings.TrimPrefix(parts[0], "$"))
	}
	detail := strings.Join(parts, "\n")
	return detail, strings.TrimSpace(detail) != ""
}

// cursorCLIPlanDetail is the plan box's body: its name and text, up to the
// rule above "Ready to build?".
func cursorCLIPlanDetail(lines []string, promptAt int) (string, bool) {
	top := -1
	for i := promptAt - 1; i >= 0 && i >= promptAt-200; i-- {
		text := cursorCLIBoxText(lines[i])
		if text == "Suggested Plan" || text == "Plan" {
			top = i
			break
		}
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "┌") || strings.HasPrefix(strings.TrimSpace(lines[i]), "╭") {
			top = i
			break
		}
	}
	if top < 0 {
		return "", false
	}
	var parts []string
	blank := false
	for i := top + 1; i < promptAt; i++ {
		if cursorCLIRule(lines[i]) {
			break
		}
		text := cursorCLIBoxText(lines[i])
		if text == "" {
			blank = len(parts) > 0
			continue
		}
		if blank {
			parts = append(parts, "")
			blank = false
		}
		parts = append(parts, text)
	}
	detail := strings.Join(parts, "\n")
	return detail, strings.TrimSpace(detail) != ""
}

// cursorCLITextBoxDetail shows what a free-text box is about: the command a
// skip refers to, which Cursor keeps boxed above the prompt.
func cursorCLITextBoxDetail(lines []string) string {
	for i := len(lines) - 1; i >= 0 && i >= len(lines)-15; i-- {
		text := cursorCLIBoxText(lines[i])
		if strings.HasPrefix(text, "$") {
			return strings.TrimSpace(strings.TrimPrefix(text, "$"))
		}
	}
	return ""
}

func (s *CursorCLISource) paneScreen(ctx context.Context, pane string) (cursorCLIScreen, error) {
	text, err := s.capturePane(ctx, pane)
	if err != nil {
		return cursorCLIScreen{}, err
	}
	return parseCursorCLIScreen(text), nil
}

// cursorCLIPaneState reads one pane for discovery. A decision the phone
// cannot answer still reports waiting_input, with no question, so the user
// hears that Cursor is blocked rather than seeing it idle.
func (s *CursorCLISource) cursorCLIPaneState(ctx context.Context, paneName string) (protocol.State, *protocol.Question) {
	screen, err := s.paneScreen(ctx, paneName)
	switch {
	case err != nil:
		return protocol.StateIdle, nil
	case screen.question != nil:
		return protocol.StateWaitingInput, screen.question
	case screen.needsUser:
		return protocol.StateWaitingInput, nil
	case screen.busy:
		return protocol.StateBusy, nil
	}
	return protocol.StateIdle, nil
}

func (s *CursorCLISource) CurrentQuestion(ctx context.Context, sessionID string) (*protocol.Question, error) {
	s.mu.RLock()
	session, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("source: unknown Cursor CLI session %q", sessionID)
	}
	if session.pane == "" {
		return nil, nil
	}
	screen, err := s.paneScreen(ctx, session.pane)
	if err != nil {
		return nil, err
	}
	return screen.question, nil
}

// refuseCursorCLISend fails closed unless the pane is at its prompt.
func (s *CursorCLISource) refuseCursorCLISend(ctx context.Context, pane string) error {
	screen, err := s.paneScreen(ctx, pane)
	if err != nil {
		return fmt.Errorf("source: could not inspect Cursor CLI before sending: %w", err)
	}
	if screen.question != nil {
		return errors.New("source: answer the pending Cursor CLI question before sending a message")
	}
	if screen.blocked != "" {
		return errors.New(screen.blocked)
	}
	return nil
}

// cursorCLITextBoxWait bounds how long an answer waits for Cursor to swap a
// menu for its free-text box before typing.
const cursorCLITextBoxWait = 2 * time.Second

func (s *CursorCLISource) Answer(ctx context.Context, sessionID string, answer protocol.QuestionAnswer) error {
	s.mu.RLock()
	session, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("source: unknown Cursor CLI session %q", sessionID)
	}
	if session.pane == "" {
		return errors.New("source: this Cursor CLI chat cannot receive answers remotely")
	}
	if session.meta.Question == nil || answer.QuestionID == "" ||
		answer.QuestionID != session.meta.Question.ID {
		return errors.New("source: that question is no longer current; refresh the session")
	}
	key := answer.OptionKey
	if len(answer.Options) > 1 || (len(answer.Options) == 1 && key != "" && answer.Options[0] != key) {
		return errors.New("source: this Cursor question accepts one listed option")
	}
	if key == "" && len(answer.Options) == 1 {
		key = answer.Options[0]
	}
	text := strings.TrimSpace(answer.Text)
	if (key == "") == (text == "") {
		return errors.New("source: choose one listed option or write an answer")
	}
	screen, err := s.paneScreen(ctx, session.pane)
	if err != nil || !sameQuestion(session.meta.Question, screen.question) {
		return errors.New("source: that question is no longer on screen; refresh the session")
	}
	if key != "" {
		if !questionHasOption(screen.question, key) {
			return errors.New("source: that option is no longer current; refresh the session")
		}
		return s.typeKey(ctx, session.pane, key)
	}
	if !screen.question.Custom {
		return errors.New("source: this Cursor question only accepts a listed option")
	}
	if containsControl(text) {
		return errors.New("source: an answer cannot contain control characters")
	}
	if !screen.textBox {
		if err := s.typeKey(ctx, session.pane, screen.customKey); err != nil {
			return err
		}
		// Cursor swaps the menu for its box on the next frame. Typing before
		// it has would put the text into the menu, where each letter is a
		// shortcut.
		deadline := time.Now().Add(cursorCLITextBoxWait)
		for {
			next, err := s.paneScreen(ctx, session.pane)
			if err == nil && next.textBox {
				break
			}
			if time.Now().After(deadline) {
				return errors.New("source: Cursor did not open its answer box; check the session on the Mac")
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(80 * time.Millisecond):
			}
		}
	}
	return s.typeText(ctx, session.pane, text)
}

// containsControl rejects terminal control characters other than newlines,
// which the multi-line paste carries as text.
func containsControl(text string) bool {
	for _, r := range text {
		if r == '\n' || r == '\t' {
			continue
		}
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

func (s *CursorCLISource) typeKey(ctx context.Context, pane, key string) error {
	if s.sendKey != nil {
		return s.sendKey(ctx, pane, key)
	}
	return tmux.Answer(ctx, pane, key)
}

func (s *CursorCLISource) typeText(ctx context.Context, pane, text string) error {
	if s.sendText != nil {
		return s.sendText(ctx, pane, text)
	}
	return tmux.Send(ctx, pane, text)
}
