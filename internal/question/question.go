// Package question detects the approval prompts agent CLIs show when they
// need a decision from the user.
//
// This is the state that matters most and the one hooks cannot see. Claude
// Code fires no Notification hook for a permission prompt, and its session
// registry still reports the session as "idle" while it sits blocked — from
// the outside, an agent waiting on a decision looks identical to one that has
// finished. The only place the truth exists is the terminal itself.
//
// So detection reads the pane. That has a real cost — it means only sessions
// launched through the tmux wrapper can be answered remotely — but it is the
// difference between a phone that tells you an agent is stuck and one that
// lets you unstick it.
package question

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Question is a pending decision an agent is blocked on.
type Question struct {
	// Prompt is the question itself, e.g. "Do you want to proceed?".
	Prompt string `json:"prompt"`
	// Title names what is being asked about, e.g. "Bash command".
	Title string `json:"title,omitempty"`
	// Detail is the substance — the command, the file, the diff summary.
	Detail string `json:"detail,omitempty"`
	// Options are the choices, in the order shown.
	Options []Option `json:"options"`
	// Multiple means the menu uses checkboxes and accepts several choices.
	Multiple bool `json:"multiple,omitempty"`
	// Custom means Claude drew its synthetic "Type something" choice. That
	// row is intentionally not exposed as a normal option; the app renders a
	// text box instead.
	Custom bool `json:"custom,omitempty"`

	// The remaining fields describe Claude's terminal layout. They are used to
	// complete a multi-select form atomically, but are not part of the wire
	// protocol sent to the app.
	CustomKey     string `json:"-"`
	CustomChecked bool   `json:"-"`
	ChoiceCount   int    `json:"-"`
	FocusIndex    int    `json:"-"`
	CustomIndex   int    `json:"-"`
	SubmitFocused bool   `json:"-"`
	// AdvanceWithTab means this question belongs to Claude's multi-question
	// tab strip. Selecting a single option records it but does not leave the
	// screen; Tab is the separate operation that advances to the next question.
	AdvanceWithTab bool `json:"-"`
	// PreviewLayout means numeric shortcuts are not accepted. The option must
	// be focused with arrows and selected with Enter before any tab advance.
	PreviewLayout bool `json:"-"`
}

// Option is one selectable answer.
type Option struct {
	// Key is what to send to choose this option — the digit shown beside it.
	Key string `json:"key"`
	// Label is the human text of the choice.
	Label string `json:"label"`
	// Description is the explanatory subtext rendered beneath or beside the
	// label. It is display-only and is never typed back into the terminal.
	Description string `json:"description,omitempty"`
	// Preview is the side panel Claude renders for the highlighted option.
	Preview string `json:"preview,omitempty"`
	// Selected marks the option the TUI currently has highlighted.
	Selected bool `json:"selected,omitempty"`
	// Checked is independent of keyboard focus in a multi-select menu.
	Checked bool `json:"checked,omitempty"`
}

// optionLine matches " ❯ 1. Yes", "   2. No", or Claude's compact
// multi-select form " ❯ 1.[ ]Choice", capturing the marker, digit, and label.
//
// The marker set is deliberately wide because each CLI picked a different
// glyph: Claude Code uses ❯ (U+276F) and Codex uses › (U+203A). Missing one
// is not a cosmetic failure — an unmatched marker line stops being an option
// and gets read as the question instead, which is exactly what happened to
// Codex's first choice before › was added here.
var optionLine = regexp.MustCompile(`^\s*([❯›>»▸▶→*]?)\s*(\d+)[.)]\s*(.*\S)\s*$`)

// checkboxLabel extracts Claude's multi-select prefix. Claude has used both
// tick and cross glyphs between releases, so any non-space mark means checked.
var checkboxLabel = regexp.MustCompile(`^\[\s*([^\]\s]?)\s*\]\s*(.*\S)\s*$`)

// formControl is the submit row below Claude's multi-select choices. It can
// receive keyboard focus, in which case no numbered row carries the marker.
var formControl = regexp.MustCompile(`(?i)^\s*([❯›>»▸▶→*]?)\s*(?:next|submit)\s*$`)

// footerLine matches the key-hint lines under a menu, which end the block.
//
// These hints are part of the protocol even though they look like decoration:
// they distinguish a live menu from a numbered list in ordinary transcript
// text. Claude's AskUserQuestion footer starts with "Enter to select", while
// Codex's multi-question form can start with Enter, arrows, Tab, Ctrl, or an
// option-position hint depending on its width and focus.
var footerLine = regexp.MustCompile(`(?i)^\s*(?:enter to|esc to|press |↑/↓|←/→|tab (?:to|or)|ctrl\s*\+|option \d+/\d+)`)

// taskChrome matches the rows Claude Code paints beneath a live control: its
// task list, and the tick/box glyphs of the entries under it.
//
// These sit below the footer, so they are chrome drawn around the menu rather
// than evidence the menu was answered and scrolled past. Only ever accepted
// once a footer has already established that this control is live, which is
// what keeps the guard against numbered prose intact.
var taskChrome = regexp.MustCompile(`^\s*(?:\d+\s+tasks?\b|[✔✓✗☐☒◻◼▪·]\s+\S)`)

// footerContinuation matches the second physical line of a wrapped hint row.
// It is deliberately only accepted after footerLine has established that this
// is a live control. Accepting one of these fragments by itself would weaken
// the guard that keeps numbered prose from becoming remotely answerable.
var footerContinuation = regexp.MustCompile(`(?i)(?:esc to cancel|ctrl\s*\+\S+\s+to|switch questions|\bin vim\b|n to add notes|tab/arrow keys)`)

// bareChatLine is Claude's conversation escape hatch in the side-by-side
// preview layout. Other layouts number the same row, so both shapes need to be
// excluded from answer options and from option descriptions.
var bareChatLine = regexp.MustCompile(`(?i)^\s*(?:[❯›>»▸▶→*]\s*)?chat about this\.?\s*$`)

// progressLine is UI chrome above a multi-question prompt. Treating it as a
// context boundary prevents the previous transcript from being folded into a
// newly displayed Codex question when the form advances from question 1 to 2.
var progressLine = regexp.MustCompile(`(?i)^\s*questions?\s+\d+\s*(?:/|of)\s*\d+(?:\s*\([^)]*\))?\s*$`)

// inputLine matches the TUI's own input box, which sits above the transcript
// and has nothing to do with the question. Without this the walk upward for
// context runs straight into whatever the user last typed and reports it as
// the question's heading.
var inputLine = regexp.MustCompile(`^\s*[❯›>]\s`)

// maxScan bounds how much of a pane is examined. A prompt is always at the
// bottom, so there is no reason to walk a long scrollback.
const maxScan = 40

// Detect finds a pending question in captured pane text, if there is one.
//
// Returns nil when the agent is not waiting on anything, which is the common
// case — this runs on every discovery sweep for every tmux-backed session.
func Detect(pane string) *Question {
	lines := strings.Split(strings.TrimRight(pane, "\n"), "\n")
	if len(lines) > maxScan {
		lines = lines[len(lines)-maxScan:]
	}
	previewColumn := findPreviewColumn(lines)
	advanceWithTab := strings.Contains(
		strings.ToLower(strings.Join(strings.Fields(strings.Join(lines, " ")), " ")),
		"tab to switch questions",
	)

	// Find the last run of numbered options, allowing wrapped row text between
	// them. Scanning from the bottom matters: a transcript can contain older
	// menus, and only the one at the end is still awaiting an answer.
	last := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if optionLine.MatchString(menuColumn(lines[i], previewColumn)) {
			last = i
			break
		}
	}
	if last == -1 {
		return nil
	}

	// Anything after the options other than wrapped option text, blanks, or a
	// key-hint footer means this menu is already resolved and scrolled past.
	// Requiring a footer is intentional: accepting an indented numbered list at
	// the end of an assistant response would let the phone type a digit into the
	// normal prompt.
	footerSeen := false
	for _, line := range lines[last+1:] {
		menuLine := menuColumn(line, previewColumn)
		trimmed := strings.TrimSpace(menuLine)
		if trimmed == "" || isRule(trimmed) {
			continue
		}
		if bareChatLine.MatchString(menuLine) {
			continue
		}
		if footerLine.MatchString(line) {
			footerSeen = true
			continue
		}
		if footerSeen && footerContinuation.MatchString(line) {
			continue
		}
		if footerSeen && taskChrome.MatchString(menuLine) {
			continue
		}
		if !footerSeen && (formControl.MatchString(menuLine) ||
			isOptionContinuation(menuLine, 2)) {
			continue
		}
		return nil
	}
	type paneOption struct {
		line        int
		key         string
		label       string
		description string
		focused     bool
		checkbox    bool
		checked     bool
		numberCol   int
	}

	lastMatch := optionLine.FindStringSubmatch(menuColumn(lines[last], previewColumn))
	expected, err := strconv.Atoi(lastMatch[2])
	if err != nil || expected < 1 {
		return nil
	}

	first := last
	var rawOptions []paneOption
	for i := last; i >= 0; i-- {
		menuLine := menuColumn(lines[i], previewColumn)
		match := optionLine.FindStringSubmatch(menuLine)
		if match == nil {
			// Codex renders descriptions in a second column and wraps them
			// onto indented lines in narrow panes. Claude can likewise wrap
			// long labels/descriptions. They belong to the option immediately
			// above them, not to the question context.
			continuationColumn := 2
			if len(rawOptions) > 0 {
				continuationColumn = rawOptions[0].numberCol
			}
			if formControl.MatchString(menuLine) ||
				isOptionContinuation(menuLine, continuationColumn) {
				continue
			}
			// AskUserQuestion draws a separator before its synthetic "Chat
			// about this" row. Cross separators only while an earlier numbered
			// key is still expected; consecutive keys prevent us from walking
			// into an unrelated menu above the rule.
			if isRule(strings.TrimSpace(menuLine)) && expected > 0 && len(rawOptions) > 0 {
				continue
			}
			break
		}
		keyNumber, err := strconv.Atoi(match[2])
		if err != nil || keyNumber != expected {
			break
		}
		expected--
		label, description, checkbox, checked := parseOptionLabel(match[3])
		first = i
		rawOptions = append([]paneOption{{
			line:        i,
			key:         match[2],
			label:       label,
			description: description,
			focused:     match[1] != "",
			checkbox:    checkbox,
			checked:     checked,
			numberCol:   optionNumberColumn(menuLine),
		}}, rawOptions...)
	}
	if expected != 0 {
		// The top of a long form fell outside the bounded capture. Returning a
		// partial menu would make its numeric keys unsafe to answer remotely.
		return nil
	}
	for i := range rawOptions {
		end := len(lines)
		if i+1 < len(rawOptions) {
			end = rawOptions[i+1].line
		}
		continuation := optionContinuationText(
			lines, rawOptions[i].line+1, end, previewColumn, rawOptions[i].numberCol,
		)
		if continuation != "" {
			if rawOptions[i].description != "" {
				rawOptions[i].description += " " + continuation
			} else {
				rawOptions[i].description = continuation
			}
		}
	}

	// Next/Submit can sit before a later numbered "Chat about this" row, so it
	// must be found across the whole menu rather than only below the last key.
	// Claude also retains an option marker when Next is focused; SubmitFocused
	// therefore takes precedence over that stale marker below.
	submitLine := -1
	submitFocused := false
	for i := first; i < len(lines); i++ {
		if footerLine.MatchString(lines[i]) {
			break
		}
		if match := formControl.FindStringSubmatch(menuColumn(lines[i], previewColumn)); match != nil {
			submitLine = i
			submitFocused = match[1] != ""
		}
	}

	// Once text has been entered, Claude replaces "Type something" with the
	// user's text. In the captured checkbox layout it remains identifiable as
	// the last checkbox before Next, followed by the numbered chat escape row.
	customLine := -1
	chatAfterSubmit := false
	for _, raw := range rawOptions {
		if isCustomOption(raw.label) {
			customLine = raw.line
		}
		if submitLine >= 0 && raw.line > submitLine && isChatOption(raw.label) {
			chatAfterSubmit = true
		}
	}
	if customLine < 0 && chatAfterSubmit {
		for _, raw := range rawOptions {
			if raw.checkbox && raw.line < submitLine && raw.line > customLine {
				customLine = raw.line
			}
		}
	}

	q := &Question{
		FocusIndex:     -1,
		CustomIndex:    -1,
		SubmitFocused:  submitFocused,
		AdvanceWithTab: advanceWithTab,
		PreviewLayout:  previewColumn >= 0,
	}
	visiblePreview := previewPanelText(lines, previewColumn)
	focusedOptions := 0
	for _, raw := range rawOptions {
		if isChatOption(raw.label) {
			// Claude's single-select form adds this action below a divider.
			// It opens a conversation; it is not an answer to the question.
			continue
		}
		if raw.focused {
			focusedOptions++
		}

		choiceIndex := q.ChoiceCount
		q.ChoiceCount++
		if raw.focused && !submitFocused {
			q.FocusIndex = choiceIndex
		}
		if raw.checkbox {
			q.Multiple = true
		}
		if raw.line == customLine {
			q.Custom = true
			q.CustomKey = raw.key
			q.CustomChecked = raw.checked
			q.CustomIndex = choiceIndex
			continue
		}
		optionPreview := ""
		if raw.focused {
			optionPreview = visiblePreview
		}
		q.Options = append(q.Options, Option{
			Key:         raw.key,
			Label:       raw.label,
			Description: raw.description,
			Preview:     optionPreview,
			Selected:    raw.focused && !submitFocused,
			Checked:     raw.checked,
		})
	}

	// A single numbered line is far more likely to be prose ("1. install it")
	// than a menu. Requiring a real choice keeps false positives out.
	if len(q.Options) < 2 {
		return nil
	}
	// Both supported TUIs draw exactly one active selection. Requiring that
	// marker prevents an assistant response ending in an ordinary numbered
	// list from becoming a fake approval card whose digit is typed into the
	// next prompt.
	if submitFocused {
		// Claude 2.1.220 leaves one stale option marker drawn while also marking
		// Next. More than one option marker is still an invalid/ambiguous menu.
		if focusedOptions > 1 {
			return nil
		}
	} else if focusedOptions != 1 {
		return nil
	}

	// The question is the nearest non-empty line above the options; the title
	// and detail are what sits above that, up to a rule or a blank run.
	// Kept short on purpose: a menu's heading and detail sit directly above it,
	// so walking further only risks dragging in unrelated transcript.
	var context []string
	bounded := false
	for i := first - 1; i >= 0 && len(context) < 14; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if progressLine.MatchString(lines[i]) {
			break
		}
		if isRule(trimmed) {
			// A rule means the CLI drew a box around this question, so
			// everything gathered belongs to it.
			bounded = true
			break
		}
		if inputLine.MatchString(lines[i]) {
			break
		}
		if trimmed == "" {
			if len(context) > 0 {
				context = append(context, "")
			}
			continue
		}
		context = append(context, trimmed)
	}
	// context is bottom-up. The question is the complete contiguous text block
	// immediately above the options, not just its last physical terminal line.
	// Claude wraps long questions at the pane width and places a blank between
	// that block and both the option list and any title/detail above it.
	promptLines := context
	if !bounded && len(promptLines) > 1 {
		// Codex does not rule off its menus. Preserve its established behavior:
		// the nearest line is the prompt and adjacent usage/chrome text is detail.
		promptLines = promptLines[:1]
	} else if separator := slices.Index(context, ""); separator >= 0 {
		promptLines = context[:separator]
	}
	if len(promptLines) > 0 {
		prompt := append([]string(nil), promptLines...)
		slices.Reverse(prompt)
		q.Prompt = strings.Join(prompt, " ")
	}
	if len(context) > len(promptLines) {
		rest := context[len(promptLines):]
		// Trim leading/trailing blanks left by the walk.
		for len(rest) > 0 && rest[0] == "" {
			rest = rest[1:]
		}
		for len(rest) > 0 && rest[len(rest)-1] == "" {
			rest = rest[:len(rest)-1]
		}
		if len(rest) > 0 {
			// Copy before reversing: the original bottom-up context is also used
			// below to recognize Claude's footerless review confirmation.
			body := append([]string(nil), rest...)
			// Only claim a heading when the block was actually delimited.
			// Claude Code rules off its prompts, so the topmost line really is
			// a title ("Bash command"); Codex does not, so the same guess
			// there promotes an unrelated line of transcript into a heading.
			if bounded {
				q.Title = cleanTitle(rest[len(rest)-1])
				body = append([]string(nil), rest[:len(rest)-1]...)
			}
			// Restore top-down order for the detail.
			for i, j := 0, len(body)-1; i < j; i, j = i+1, j-1 {
				body[i], body[j] = body[j], body[i]
			}
			q.Detail = strings.TrimSpace(strings.Join(body, "\n"))
		}
	}
	if q.Prompt == "" {
		q.Prompt = "The agent needs a decision"
	}
	if isReviewOptions(q.Options) {
		populateReviewContext(q, lines, first)
	}
	// Claude's final AskUserQuestion review deliberately omits the normal
	// "Enter to select" hint. Its exact title, prompt, and two confirmation
	// labels form an equally strong live-control signature. Every other menu
	// still requires a footer so numbered prose cannot become remotely
	// answerable.
	if !footerSeen && !isReviewConfirmation(q) {
		return nil
	}
	return q
}

func isReviewOptions(options []Option) bool {
	return len(options) == 2 &&
		strings.EqualFold(strings.TrimSpace(options[0].Label), "Submit answers") &&
		strings.EqualFold(strings.TrimSpace(options[1].Label), "Cancel")
}

func populateReviewContext(q *Question, lines []string, first int) {
	// Reviews can contain four question/answer pairs and wrapped text, so do
	// not reuse the deliberately short context window used by ordinary menus.
	var context []string
	for i := first - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if progressLine.MatchString(lines[i]) || isRule(trimmed) {
			break
		}
		if trimmed == "" {
			if len(context) > 0 {
				context = append(context, "")
			}
			continue
		}
		context = append(context, trimmed)
	}
	if len(context) == 0 ||
		!strings.EqualFold(strings.TrimSpace(context[0]), "Ready to submit your answers?") {
		return
	}
	q.Prompt = context[0]

	titleIndex := -1
	for i, line := range context {
		if strings.EqualFold(cleanTitle(line), "Review your answers") {
			titleIndex = i
			break
		}
	}
	q.Title = "Review your answers"
	if titleIndex < 0 {
		titleIndex = len(context)
	}
	body := append([]string(nil), context[1:titleIndex]...)
	for len(body) > 0 && body[0] == "" {
		body = body[1:]
	}
	for len(body) > 0 && body[len(body)-1] == "" {
		body = body[:len(body)-1]
	}
	for left, right := 0, len(body)-1; left < right; left, right = left+1, right-1 {
		body[left], body[right] = body[right], body[left]
	}
	q.Detail = strings.TrimSpace(strings.Join(body, "\n"))
}

func isReviewConfirmation(q *Question) bool {
	return isReviewOptions(q.Options) && q.Title == "Review your answers" &&
		strings.EqualFold(strings.TrimSpace(q.Prompt), "Ready to submit your answers?")
}

// isOptionContinuation reports whether a line is wrapped content belonging to
// a menu row. minColumn is the digit column of the adjacent option: Claude's
// checkbox descriptions align at that column (two in the captured layout),
// while single-select and Codex descriptions sit farther right. Using the
// actual row keeps this correct when the pane width or selection marker moves.
func isOptionContinuation(line string, minColumn int) bool {
	if strings.TrimSpace(line) == "" || footerLine.MatchString(line) ||
		footerContinuation.MatchString(line) || bareChatLine.MatchString(line) {
		return false
	}
	return leadingColumn(line) >= minColumn
}

func leadingColumn(line string) int {
	column := 0
	for _, r := range line {
		switch r {
		case ' ':
			column++
		case '\t':
			column += 4
		default:
			return column
		}
	}
	return column
}

// optionNumberColumn returns the display column at which a row's numeric key
// begins. Selection glyphs are runes rather than bytes, so work in runes here.
func optionNumberColumn(line string) int {
	runes := []rune(line)
	column := 0
	for column < len(runes) && (runes[column] == ' ' || runes[column] == '\t') {
		if runes[column] == '\t' {
			column += 4
		} else {
			column++
		}
	}
	// Tabs are not present in either TUI's output. If one ever appears, the
	// visual column above can exceed the rune index, so fall back to the stable
	// two-column minimum rather than indexing beyond the string.
	index := 0
	for index < len(runes) && (runes[index] == ' ' || runes[index] == '\t') {
		index++
	}
	if index < len(runes) && strings.ContainsRune("❯›>»▸▶→*", runes[index]) {
		index++
		column++
		for index < len(runes) && (runes[index] == ' ' || runes[index] == '\t') {
			index++
			column++
		}
	}
	if column < 2 {
		return 2
	}
	return column
}

// findPreviewColumn locates Claude's side-by-side preview panel from its top
// border on a numbered option row. The panel can shift left as notes grow, but
// within any one capture every panel line begins at this same rune column.
func findPreviewColumn(lines []string) int {
	for _, line := range lines {
		byteIndex := strings.Index(line, "┌")
		if byteIndex < 0 || !strings.HasPrefix(line[byteIndex:], "┌─") {
			continue
		}
		column := len([]rune(line[:byteIndex]))
		if optionLine.MatchString(menuColumn(line, column)) {
			return column
		}
	}
	return -1
}

func menuColumn(line string, previewColumn int) string {
	if previewColumn < 0 {
		return line
	}
	runes := []rune(line)
	if len(runes) <= previewColumn {
		return line
	}
	return strings.TrimRight(string(runes[:previewColumn]), " \t")
}

func previewPanelText(lines []string, previewColumn int) string {
	if previewColumn < 0 {
		return ""
	}
	var content []string
	inside := false
	for _, line := range lines {
		runes := []rune(line)
		if len(runes) <= previewColumn {
			continue
		}
		panel := runes[previewColumn:]
		if !inside {
			if len(panel) > 0 && panel[0] == '┌' {
				inside = true
			}
			continue
		}
		if len(panel) == 0 {
			continue
		}
		if panel[0] == '└' {
			break
		}
		if panel[0] != '│' {
			continue
		}
		panel = panel[1:]
		if len(panel) > 0 && panel[len(panel)-1] == '│' {
			panel = panel[:len(panel)-1]
		}
		content = append(content, strings.TrimSpace(string(panel)))
	}
	for len(content) > 0 && content[0] == "" {
		content = content[1:]
	}
	for len(content) > 0 && content[len(content)-1] == "" {
		content = content[:len(content)-1]
	}
	return strings.Join(content, "\n")
}

// isRule reports whether a line is a box-drawing separator.
func isRule(trimmed string) bool {
	if trimmed == "" {
		return false
	}
	for _, r := range trimmed {
		if r != '─' && r != '━' && r != '-' && r != '═' && r != '·' && r != ' ' {
			return false
		}
	}
	return true
}

func parseOptionLabel(label string) (clean, description string, checkbox, checked bool) {
	label = strings.TrimSpace(label)
	if match := checkboxLabel.FindStringSubmatch(label); match != nil {
		clean, description = splitOptionText(match[2])
		return clean, description, true, strings.TrimSpace(match[1]) != ""
	}
	clean, description = splitOptionText(label)
	return clean, description, false, false
}

func optionContinuationText(
	lines []string,
	start, end, previewColumn, minColumn int,
) string {
	var parts []string
	for i := start; i < end; i++ {
		line := menuColumn(lines[i], previewColumn)
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || isRule(trimmed) || footerLine.MatchString(line) ||
			footerContinuation.MatchString(line) || bareChatLine.MatchString(line) ||
			formControl.MatchString(line) || optionLine.MatchString(line) {
			continue
		}
		if isOptionContinuation(line, minColumn) {
			parts = append(parts, strings.Join(strings.Fields(trimmed), " "))
		}
	}
	return strings.Join(parts, " ")
}

func isCustomOption(label string) bool {
	label = strings.TrimSpace(label)
	label = strings.TrimSuffix(label, ".")
	return strings.EqualFold(label, "Type something")
}

func isChatOption(label string) bool {
	label = strings.TrimSpace(label)
	label = strings.TrimSuffix(label, ".")
	return strings.EqualFold(label, "Chat about this")
}

func cleanTitle(title string) string {
	title = strings.TrimSpace(title)
	// The multi-question tab strip keeps a completed mark on a question while
	// the user is still editing it, and plain capture-pane output drops the
	// styling that identifies the active tab. Returning no title is preferable
	// to confidently labelling the current card with the next question's header.
	if strings.Contains(title, "←") && strings.Count(title, "☐")+
		strings.Count(title, "☒")+
		strings.Count(title, "☑") > 1 {
		return ""
	}
	// Multi-question AskUserQuestion renders a tab strip instead of a simple
	// heading. A single unchecked tab can still be cleaned safely.
	if index := strings.IndexRune(title, '☐'); index >= 0 {
		current := strings.TrimSpace(title[index+len("☐"):])
		if end := strings.IndexAny(current, "☐☑☒✔→"); end >= 0 {
			current = current[:end]
		}
		if current = strings.TrimSpace(current); current != "" {
			return current
		}
	}
	return strings.TrimSpace(strings.TrimLeft(title, "←☐☑☒ "))
}

// cleanLabel tidies a choice for display on a phone.
//
// Codex pads a trailing description onto the same line ("Keep current model
// (never show again)   Hide future rate limit reminders"). Collapsing all the
// whitespace would fuse the choice and its explanation into one run-on, so the
// run of spaces that separates them is treated as the boundary and only the
// choice itself is kept — the label has to be readable as a button. Two spaces
// is enough of a signal, since a real label never contains a double space.
func cleanLabel(label string) string {
	label, _ = splitOptionText(label)
	return label
}

func splitOptionText(text string) (label, description string) {
	text = strings.TrimSpace(text)
	if idx := strings.Index(text, "  "); idx > 0 {
		label = text[:idx]
		description = text[idx:]
	} else {
		label = text
	}
	// Curly quotes come straight from the TUI; keep them, they are the CLI's
	// own wording and rewriting it would misquote the choice being made.
	return strings.Join(strings.Fields(label), " "), strings.Join(strings.Fields(description), " ")
}
