package question

import (
	"regexp"
	"strings"
)

// kiroApproval is the header of Kiro's tool permission menu, e.g.
// "shell requires approval", or of its second level after choosing Trust,
// "shell requires approval · trust options". An approval raised inside a
// subagent is prefixed with its name: "read_notes > shell requires approval".
var kiroApproval = regexp.MustCompile(`^\s*(\S.*?)\s+requires approval(?:\s*·\s*(\S.*?))?\s*$`)

// kiroColumns splits a row drawn in two columns, as the trust options are:
// "Full command    ls -la". Two spaces or more separate them; one does not,
// since labels such as "Yes, single permission" contain single spaces.
var kiroColumns = regexp.MustCompile(`^(\S.*?\S)\s{2,}(\S.*)$`)

// kiroFooter is the key hint under Kiro's menus. Its wording follows the
// focused row — "↑↓ to navigate · ↵ to select" on a plain choice, "Enter to
// see more options" on one that opens a sub-menu — so only its fixed start is
// matched. The "requires approval" header is what anchors the menu.
var kiroFooter = regexp.MustCompile(`^\s*esc to close\s*·`)

// kiroOverlayFooter is the start of the hint under everything Kiro draws over
// its prompt: approval menus, the slash-command palette ("esc to cancel · ↵
// to select"), pickers (/model, /agent, /rewind), panels (/context, /usage,
// /tools) and the editor for a denial's reason.
var kiroOverlayFooter = regexp.MustCompile(`^\s*esc to (close|cancel)\b`)

// kiroEditHint is appended to the option that lets a denial carry a reason.
var kiroEditHint = regexp.MustCompile(`\s*\(Tab to edit\)\s*$`)

// KiroFeedbackLevel is the second half of the header Kiro shows after Tab on
// "No": an editor for the reason the call is refused.
const KiroFeedbackLevel = "Modify request"

// kiroDetailLines bounds how much of the call under review the question
// carries. The transcript holds all of it; this is what decides the answer.
const kiroDetailLines = 20

// DetectKiro returns the permission menu Kiro CLI is blocked on, or nil.
//
// The shape, as Kiro 2.27 draws it at 80 columns:
//
//	↓ Write /work/greeting.txt
//	  added 1 line in greeting.txt
//	     1+  hi there
//	──────────────
//	 write requires approval
//	 ❯ Yes, single permission
//	   Trust, always allow in this session
//	   No (Tab to edit)
//	──────────────
//	 esc to close · ↑↓ to navigate · ↵ to select · Tab to edit
//
// Tab on "No" opens an editor for the reason, which is a question too: the
// header gains "· Modify request", the options give way to a "›" input line,
// and the footer is a bare "esc to close".
func DetectKiro(pane string) *Question {
	lines := bottom(pane)
	footer := lastNonBlank(lines)
	if footer < 0 {
		return nil
	}
	if q := detectKiroFeedback(lines, footer); q != nil {
		return q
	}
	if !kiroFooter.MatchString(lines[footer]) {
		return nil
	}

	// Walk up from the footer: the menu's rule, its options, then its header.
	var options []Option
	header := -1
	for i := footer - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" {
			continue
		}
		if isRule(trimmed) {
			if len(options) == 0 {
				continue // the rule between the options and the footer
			}
			return nil // a rule inside the option block: not this menu
		}
		if match := kiroApproval.FindStringSubmatch(lines[i]); match != nil {
			header = i
			break
		}
		focused := strings.HasPrefix(trimmed, "❯")
		label := strings.TrimSpace(strings.TrimPrefix(trimmed, "❯"))
		label = kiroEditHint.ReplaceAllString(label, "")
		if label == "" {
			return nil
		}
		option := Option{Label: label, Selected: focused}
		if columns := kiroColumns.FindStringSubmatch(label); columns != nil {
			option.Label, option.Description = columns[1], columns[2]
		}
		options = append([]Option{option}, options...)
	}
	if header < 0 || len(options) < 2 {
		return nil
	}
	focus := keyByPosition(options)
	if focus < 0 {
		return nil
	}

	match := kiroApproval.FindStringSubmatch(lines[header])
	tool, level := match[1], match[2]
	prompt := tool + " requires approval"
	if level != "" {
		prompt += " · " + level
	}
	return &Question{
		Title:      kiroTitle(tool),
		Prompt:     prompt,
		Detail:     kiroDetail(lines, header),
		Options:    options,
		FocusIndex: focus,
	}
}

// detectKiroFeedback reads the editor Tab on "No" opens:
//
//	──────────────
//	 write requires approval · Modify request
//	 ›  add your feedback...
//	──────────────
//	 esc to close
//
// The call is still waiting on the user, so this is a question: its text box
// takes the reason to refuse with, and its one choice is Escape, back to the
// menu. What has been typed so far is not part of it — the question has to
// stay the same question while someone types at the Mac.
func detectKiroFeedback(lines []string, footer int) *Question {
	if strings.TrimSpace(lines[footer]) != "esc to close" {
		return nil
	}
	rule := footer - 1
	for rule >= 0 && strings.TrimSpace(lines[rule]) == "" {
		rule--
	}
	if rule < 0 || !isRule(strings.TrimSpace(lines[rule])) {
		return nil
	}
	// The input line sits between the header and the rule, and a long
	// reason wraps it onto several.
	header := -1
	for i := rule - 1; i >= 0 && i >= rule-8; i-- {
		match := kiroApproval.FindStringSubmatch(lines[i])
		if match == nil {
			continue
		}
		if match[2] != KiroFeedbackLevel {
			return nil
		}
		header = i
		break
	}
	if header < 0 || !strings.HasPrefix(strings.TrimSpace(lines[header+1]), "›") {
		return nil
	}
	tool := kiroApproval.FindStringSubmatch(lines[header])[1]
	return &Question{
		Title:   kiroTitle(tool),
		Prompt:  tool + " requires approval · " + KiroFeedbackLevel,
		Detail:  kiroDetail(lines, header),
		Options: []Option{{Key: "1", Label: "Back to the choices", Selected: true}},
		Custom:  true,
	}
}

// KiroOverlayOpen reports whether something Kiro draws over its prompt — a
// menu, picker or panel — has the keyboard. Typing then goes into that
// overlay rather than the prompt: into a picker's search, where Enter switches
// the model or the agent, or forks the session from /rewind.
func KiroOverlayOpen(pane string) bool {
	lines := bottom(pane)
	footer := lastNonBlank(lines)
	return footer >= 0 && kiroOverlayFooter.MatchString(lines[footer])
}

// kiroTitle names the tool a menu is about the way a person would: "Web
// fetch" for web_fetch, "Shell" for "read_notes > shell".
func kiroTitle(tool string) string {
	if cut := strings.LastIndex(tool, " > "); cut >= 0 {
		tool = tool[cut+len(" > "):]
	}
	if tool == "use_aws" {
		return "AWS"
	}
	return capitalize(strings.ReplaceAll(tool, "_", " "))
}

// kiroDetail reads the call under review: the "↓" row above the menu and the
// lines that belong to it, which for a write are its diff.
//
// It is not simply the row above the menu. A write puts its diff there, a call
// made alongside others has their rows ("• Grep …", "esc to cancel") in
// between, a parameter hangs beneath a call ("╰ search_terms=title"), and a
// running task list adds a status line. Reading only the line above the menu
// lost the command whenever any of those were on screen, and the phone asked
// "Shell requires approval" without saying what.
func kiroDetail(lines []string, header int) string {
	call := -1
	for i := header - 1; i >= 0; i-- {
		if kiroApproval.MatchString(lines[i]) {
			break // an earlier menu: the call for this one is not on screen
		}
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "↓") {
			call = i
			break
		}
	}
	if call < 0 {
		return ""
	}
	detail := []string{strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[call]), "↓"))}
	for _, line := range lines[call+1 : header] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || isRule(trimmed) || !strings.HasPrefix(line, " ") ||
			strings.HasPrefix(trimmed, "•") || strings.HasPrefix(trimmed, "↓") ||
			strings.HasPrefix(trimmed, "◐") || strings.HasPrefix(trimmed, "◇") {
			break
		}
		// A parameter repeats what the call says or names the working
		// directory, and "esc to cancel" belongs to the screen, not the call.
		if strings.HasPrefix(trimmed, "╰") || trimmed == "esc to cancel" {
			continue
		}
		if len(detail) == kiroDetailLines {
			break
		}
		detail = append(detail, trimmed)
	}
	return strings.Join(detail, "\n")
}
