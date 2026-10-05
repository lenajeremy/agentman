package question

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// antigravityFooter is the key hint under Antigravity's arrow menus.
var antigravityFooter = regexp.MustCompile(`^\s*↑/↓ Navigate · enter Confirm\s*$`)

// antigravityModel is the model label Antigravity right-aligns on the bottom
// row, which can land on its own line beneath a menu's footer.
var antigravityModel = regexp.MustCompile(`^\s*\S.*·\s*(?:low|medium|high|max)\s*$`)

// AntigravityForm says which of agy's controls DetectAntigravityForm found,
// and what answering it takes beyond choosing an option.
type AntigravityForm struct {
	// Kind is "trust", "approval" (a tool permission), "question" (the
	// agent's ask_question form) or "subagent" (a subagent's tool permission,
	// which agy routes to the parent's screen).
	Kind string
	// Index and Count place an ask_question form among the questions it was
	// asked with: "Question 2/3" is Index 2, Count 3.
	Index, Count int
	// WriteInKey is the digit of the "Write-in..." row, which opens a text
	// box rather than choosing an answer.
	WriteInKey string
	// Amend means the approval offers "tab Amend": approving with a message
	// for the agent.
	Amend bool
	// Typing means a text box is open — the write-in answer or an amend
	// message. Keys pressed now are typed into it, not read as a choice.
	Typing bool
}

// DetectAntigravity returns the menu Antigravity CLI is blocked on, or nil.
func DetectAntigravity(pane string) *Question {
	q, _ := DetectAntigravityForm(pane)
	return q
}

var (
	// antigravityHint is the key-hint row under every control agy draws for
	// a decision, which is what tells it apart from a numbered list in a
	// reply. "enter Submit" alone is the hint while a text box is open.
	antigravityHint = regexp.MustCompile(`^\s*(?:↑/↓ Navigate\b|enter Submit\b)`)
	// antigravityStatus is the row beneath the hint: agy's footer, carrying
	// "esc to cancel" and the model on the right.
	antigravityStatus = regexp.MustCompile(`esc to cancel|\? for shortcuts|·\s*(?:low|medium|high|max)\b`)
	// antigravityCheckbox is a multi-select row's box: "[ ] apple", "[x] apple".
	antigravityCheckbox = regexp.MustCompile(`^\[([ xX])\]\s*(.*)$`)
	// antigravityQuestionHeading opens an ask_question form: "Question 2/3: …".
	antigravityQuestionHeading = regexp.MustCompile(`^Question (\d+)/(\d+):\s*(.*)$`)
	// antigravityAmendInput is the message box amend opens beneath an option.
	antigravityAmendInput = regexp.MustCompile(`^\s{4,}>`)
	// antigravitySubagent is the box agy draws over the prompt when a
	// subagent needs a permission: " ┃ research needs approval for Read".
	antigravitySubagent = regexp.MustCompile(`^\s*┃\s*(\S.*?) needs approval for (\S.*?)\s*$`)
)

// DetectAntigravityForm reads the control agy is blocked on, and what kind it
// is. Every shape below is copied from agy 1.2.17:
//
//	Command                                  Create file / Pending edit /
//	────────────────────────────────         File access / Read URL / Question
//	Requesting permission for:               ← what is being decided
//	   ls -la
//	Run this command?                        ← the question
//	> 1. Yes, run command
//	  2. No, cancel
//	  ↑/↓ Navigate · tab Amend · ctrl+g edit/expand command
//	esc to cancel                    Gemini 3.8 Flash · high
//
// The heading above the rule names the decision, which is what the phone shows
// as its title; everything between the rule and the question is its subject —
// a command, a diff, a path or a URL. The generic detector read none of that:
// it took the diff's first line for the title, folded a key hint into the
// question, and offered "Write-in..." as though it were an answer.
func DetectAntigravityForm(pane string) (*Question, AntigravityForm) {
	lines := joinAntigravityWraps(bottom(pane))
	if trust := detectAntigravityTrust(lines); trust != nil {
		return trust, AntigravityForm{Kind: "trust"}
	}
	if q, form := detectAntigravityControl(lines); q != nil {
		return q, form
	}
	if q := detectAntigravitySubagent(lines); q != nil {
		return q, AntigravityForm{Kind: "subagent"}
	}
	return nil, AntigravityForm{}
}

func detectAntigravityControl(lines []string) (*Question, AntigravityForm) {
	var form AntigravityForm
	// The hint row is the last thing above agy's own footer.
	end := lastNonBlank(lines)
	if end >= 0 && antigravityStatus.MatchString(lines[end]) && !antigravityHint.MatchString(lines[end]) {
		end = lastNonBlank(lines[:end])
	}
	if end < 0 || !antigravityHint.MatchString(lines[end]) {
		return nil, form
	}
	hint := lines[end]
	form.Amend = strings.Contains(hint, "tab Amend")

	// Walk up from the hint: the write-in box, the options, then the
	// question.
	var options []Option
	first := -1
	for i := end - 1; i >= 0; i-- {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		if match := optionLine.FindStringSubmatch(line); match != nil {
			label := match[3]
			option := Option{Key: match[2], Label: label, Selected: match[1] != ""}
			if box := antigravityCheckbox.FindStringSubmatch(label); box != nil {
				option.Label = strings.TrimSpace(box[2])
				option.Checked = box[1] != " "
				form.Kind = "question"
			}
			options = append([]Option{option}, options...)
			first = i
			continue
		}
		if len(options) == 0 {
			// Between the last option and the hint: blanks, or the write-in
			// box ("Your answer:" and whatever has been typed).
			if trimmed == "Your answer:" {
				form.Typing = true
			}
			continue
		}
		if trimmed == "" {
			continue
		}
		if antigravityAmendInput.MatchString(line) {
			form.Typing = true // amend's message box, open under option 1
			continue
		}
		break
	}
	if len(options) < 2 || first < 0 {
		return nil, form
	}

	// The question is the first line above the options; the heading is the
	// line above the rule above it.
	prompt := lastNonBlank(lines[:first])
	if prompt < 0 {
		return nil, form
	}
	rule := -1
	for i := prompt - 1; i >= 0; i-- {
		if isRule(strings.TrimSpace(lines[i])) && !strings.HasPrefix(lines[i], " ") {
			rule = i
			break
		}
	}
	if rule < 1 {
		return nil, form
	}
	title := strings.TrimSpace(lines[lastNonBlank(lines[:rule])])
	if title == "" || strings.HasPrefix(title, "─") {
		return nil, form
	}

	q := &Question{Title: title, Prompt: strings.TrimSpace(lines[prompt])}
	if heading := antigravityQuestionHeading.FindStringSubmatch(q.Prompt); heading != nil {
		form.Kind = "question"
		form.Index, _ = strconv.Atoi(heading[1])
		form.Count, _ = strconv.Atoi(heading[2])
		q.Prompt = heading[3]
		if form.Count > 1 {
			q.Title = fmt.Sprintf("%s %d/%d", title, form.Index, form.Count)
		}
	} else if form.Kind == "" {
		form.Kind = "approval"
	}

	var subject []string
	for _, line := range lines[rule+1 : prompt] {
		trimmed := strings.TrimSpace(line)
		// "shift+tab to auto-approve file edits" sits between an edit's diff
		// and its question; it is a key hint, not part of the change.
		// "Requesting permission for:" only labels the command beneath it.
		if trimmed == "" || strings.HasPrefix(trimmed, "shift+tab to ") ||
			trimmed == "Requesting permission for:" {
			continue
		}
		subject = append(subject, trimmed)
	}
	q.Detail = strings.Join(subject, "\n")

	for _, option := range options {
		if form.Kind == "question" && option.Label == "Write-in..." {
			// Not an answer: the row that opens a text box for one.
			form.WriteInKey = option.Key
			q.Custom = true
			continue
		}
		q.Options = append(q.Options, option)
	}
	q.Multiple = strings.Contains(hint, "space Toggle")
	for _, option := range q.Options {
		if option.Checked {
			q.Multiple = true
		}
	}
	focus := -1
	for i, option := range options {
		if option.Selected {
			if focus >= 0 {
				return nil, form // two cursors: not a state to act on
			}
			focus = i
		}
	}
	if focus < 0 {
		return nil, form
	}
	// FocusIndex counts the rows as drawn, write-in included, because that is
	// what arrow keys move through.
	q.FocusIndex = focus
	return q, form
}

// detectAntigravitySubagent reads the box agy draws above the prompt when one
// of its subagents needs a permission:
//
//	┃ research needs approval for Read
//	┃ ──────────────────────────────
//	┃
//	┃ ● Read(~/.zsh_history)
//	┃
//	┃ ctrl+k approve · alt+j manage
//
// The parent agent can be idle while this waits — its turn ended once the
// subagent was launched — so without reading the box the session showed idle
// with nothing to answer. The options are the ones agy's subagent panel
// (alt+j) offers for the same request.
func detectAntigravitySubagent(lines []string) *Question {
	for i := len(lines) - 1; i >= 0; i-- {
		if !strings.Contains(lines[i], "┃") || !strings.Contains(lines[i], "ctrl+k approve") {
			continue
		}
		// Walk up the box to its heading.
		var call string
		for j := i - 1; j >= 0 && strings.Contains(lines[j], "┃"); j-- {
			inner := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[j]), "┃"))
			if strings.HasPrefix(inner, "● ") && call == "" {
				call = strings.TrimSpace(strings.TrimPrefix(inner, "● "))
			}
			if match := antigravitySubagent.FindStringSubmatch(lines[j]); match != nil {
				return &Question{
					Title:  "Subagent " + match[1],
					Prompt: match[1] + " needs approval for " + match[2],
					Detail: call,
					Options: []Option{
						{Key: "1", Label: "Yes, approve"},
						{Key: "2", Label: "No, deny"},
					},
				}
			}
		}
		return nil
	}
	return nil
}

// joinAntigravityWraps puts agy's wrapped option labels back on one line.
//
// agy wraps a long label itself and continues it at column 0, not under the
// label, so tmux cannot rejoin it and the generic detector reads the
// unindented remainder as the end of the menu. At 80 columns — the size a
// phone-launched session starts at — that hid the permission prompt entirely,
// and the "esc to cancel" footer beneath it left the session reported as
// working until someone went to the Mac. Only lines that follow an option are
// joined, so the question and the text above the menu are untouched.
func joinAntigravityWraps(lines []string) []string {
	out := make([]string, 0, len(lines))
	afterOption := false
	for _, line := range lines {
		switch {
		case optionLine.MatchString(line):
			afterOption = true
		case afterOption && strings.TrimSpace(line) != "" && !strings.HasPrefix(line, " ") &&
			!footerLine.MatchString(line) && len(out) > 0:
			out[len(out)-1] = strings.TrimRight(out[len(out)-1], " ") + " " + strings.TrimSpace(line)
			continue
		default:
			afterOption = false
		}
		out = append(out, line)
	}
	return out
}

func detectAntigravityTrust(lines []string) *Question {
	end := lastNonBlank(lines)
	// The model label can sit beneath the footer on a line of its own.
	if end >= 0 && antigravityModel.MatchString(lines[end]) && !antigravityFooter.MatchString(lines[end]) {
		end = lastNonBlank(lines[:end])
	}
	if end < 0 || !antigravityFooter.MatchString(lines[end]) {
		return nil
	}
	joined := strings.Join(lines[:end], "\n")
	if !strings.Contains(joined, "Do you trust the contents of this project?") {
		return nil
	}

	var path string
	var options []Option
	for index, line := range lines[:end] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "Accessing workspace:" {
			// A path longer than the pane wraps onto following lines, broken
			// wherever the width fell, so the pieces join with nothing between.
			for next := index + 1; next < end; next++ {
				value := strings.TrimSpace(lines[next])
				if value == "" {
					if path != "" {
						break
					}
					continue
				}
				if strings.HasPrefix(value, "Do you trust") {
					break
				}
				path += value
			}
		}
		label := strings.TrimSpace(strings.TrimPrefix(trimmed, ">"))
		if label == "Yes, I trust this folder" || label == "No, exit" {
			options = append(options, Option{Label: label, Selected: strings.HasPrefix(trimmed, ">")})
		}
	}
	if len(options) != 2 || !strings.HasPrefix(path, "/") {
		return nil
	}
	focus := keyByPosition(options)
	if focus < 0 {
		return nil
	}
	return &Question{
		Title: "Workspace trust", Prompt: "Do you trust the contents of this project?",
		Detail: path, Options: options, FocusIndex: focus,
	}
}

// antigravityPanelHeading opens agy's subagent panel: "  main › research".
var antigravityPanelHeading = regexp.MustCompile(`^\s*main › (\S+)\s*$`)

// AntigravityPanel is agy's subagent panel, as alt+j opens it.
type AntigravityPanel struct {
	// Open is whether the panel is on screen at all.
	Open bool
	// Agent is the subagent it shows: "research" in "main › research".
	Agent string
	// Tool is the request awaiting approval ("Read", "Bash"), "" when none.
	Tool string
	// Detail is what the request would do, as the panel words it:
	// "Read: /Users/me/.zsh_history", "echo subagent-check".
	Detail string
	// Options are the choices offered for it. Only while the panel is
	// choosing: Escape leaves that and shows the request without them, and
	// a second Escape closes the panel with the request still waiting —
	// both checked against agy 1.2.17.
	Options []Option
}

// ReadAntigravitySubagentPanel reads the subagent panel agy opens on alt+j:
//
//	main › self
//	● running · 8s · 3 steps
//	…
//	⚠ Approval Required
//	  > Bash
//	    echo subagent-check
//	    > 1. Yes, approve
//	      2. No, deny
//	…
//	↑/↓ navigate · / select · enter confirm · esc cancel
func ReadAntigravitySubagentPanel(pane string) AntigravityPanel {
	var panel AntigravityPanel
	lines := strings.Split(strings.TrimRight(pane, "\n"), "\n")
	for _, line := range lines {
		if match := antigravityPanelHeading.FindStringSubmatch(line); match != nil {
			panel.Open, panel.Agent = true, match[1]
			break
		}
	}
	if !panel.Open {
		return panel
	}
	for i, line := range lines {
		if strings.TrimSpace(line) != "⚠ Approval Required" {
			continue
		}
		for _, next := range lines[i+1:] {
			trimmed := strings.TrimSpace(next)
			switch {
			case trimmed == "":
				if panel.Tool != "" {
					return panel
				}
			case optionLine.MatchString(next):
				match := optionLine.FindStringSubmatch(next)
				panel.Options = append(panel.Options, Option{Key: match[2], Label: match[3], Selected: match[1] != ""})
			case panel.Tool == "":
				panel.Tool = strings.TrimSpace(strings.TrimPrefix(trimmed, "> "))
			case panel.Detail == "" && len(panel.Options) == 0:
				panel.Detail = trimmed
			default:
				return panel
			}
		}
		break
	}
	return panel
}

// antigravityPanelHint is the key hint at the foot of one of agy's panels or
// pickers: /help, /model, /context, the artifact review panel, the subagent
// panel. Each ends in an esc that closes it, worded differently by each.
var antigravityPanelHint = regexp.MustCompile(`^\s*Keyboard: |\besc (?:Close|Done|back|cancel|Go Back|save & dismiss)\s*$`)

// AntigravityPanelOpen reports whether a panel or picker is open over agy's
// prompt. Text sent now would be typed into it — a picker's search box, a
// review panel's keys — instead of reaching the agent.
func AntigravityPanelOpen(pane string) bool {
	lines := bottom(pane)
	seen := 0
	for i := len(lines) - 1; i >= 0 && seen < 3; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" {
			continue
		}
		seen++
		if antigravityPanelHint.MatchString(lines[i]) {
			return true
		}
		if antigravityPanelHeading.MatchString(lines[i]) {
			return true
		}
	}
	return false
}
