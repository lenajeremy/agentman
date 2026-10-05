package question

import (
	"strconv"
	"strings"
)

// Kiro CLI and Antigravity CLI draw some of their menus without numbers: a
// cursor glyph on the focused row, arrow keys to move it, Enter to choose.
// Nothing about such a menu distinguishes it from a bulleted list in an
// agent's reply except its surroundings, so each detector here is anchored on
// wording and footers that only the live control draws. A detector that also
// matched prose would let the phone press Enter into a normal prompt.
//
// Options are keyed by position ("1", "2", …) because there is no digit to
// type; answering means moving focus from FocusIndex to the chosen row.

// keyByPosition numbers unnumbered options and returns the focused index, or
// -1 unless exactly one row carries the cursor. A menu with no focused row, or
// two, is not a state it is safe to navigate from.
func keyByPosition(options []Option) int {
	focus := -1
	for i := range options {
		options[i].Key = strconv.Itoa(i + 1)
		if options[i].Selected {
			if focus >= 0 {
				return -1
			}
			focus = i
		}
	}
	return focus
}

func bottom(pane string) []string {
	lines := strings.Split(strings.TrimRight(pane, "\n"), "\n")
	if len(lines) > maxScan {
		lines = lines[len(lines)-maxScan:]
	}
	return lines
}

func lastNonBlank(lines []string) int {
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			return i
		}
	}
	return -1
}

func capitalize(word string) string {
	if word == "" {
		return word
	}
	return strings.ToUpper(word[:1]) + word[1:]
}
