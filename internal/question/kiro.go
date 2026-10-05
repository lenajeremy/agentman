package question

import (
	"regexp"
	"strings"
)

// kiroApproval is the header of Kiro's tool permission menu, e.g.
// "shell requires approval", or of its second level after choosing Trust,
// "shell requires approval · trust options".
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

// kiroEditHint is appended to the option that lets a denial carry a reason.
var kiroEditHint = regexp.MustCompile(`\s*\(Tab to edit\)\s*$`)

// DetectKiro returns the permission menu Kiro CLI is blocked on, or nil.
//
// The shape, as Kiro 2.24 draws it:
//
//	↓ Shell ls -la
//	──────────────
//	 shell requires approval
//	 ❯ Yes, single permission
//	   Trust, always allow in this session
//	   No (Tab to edit)
//	──────────────
//	 esc to close · ↑↓ to navigate · ↵ to select · Tab to edit
func DetectKiro(pane string) *Question {
	lines := bottom(pane)
	footer := lastNonBlank(lines)
	if footer < 0 || !kiroFooter.MatchString(lines[footer]) {
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
	q := &Question{
		Title:      capitalize(tool),
		Prompt:     prompt,
		Options:    options,
		FocusIndex: focus,
	}
	// The call being approved sits just above the menu's top rule, as
	// "↓ Shell ls -la". It is what the user actually needs to judge.
	for i := header - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || isRule(trimmed) {
			continue
		}
		if strings.HasPrefix(trimmed, "↓") {
			q.Detail = strings.TrimSpace(strings.TrimPrefix(trimmed, "↓"))
		}
		break
	}
	return q
}
