package question

import (
	"regexp"
	"strings"
)

// antigravityFooter is the key hint under Antigravity's arrow menus.
var antigravityFooter = regexp.MustCompile(`^\s*↑/↓ Navigate · enter Confirm\s*$`)

// antigravityModel is the model label Antigravity right-aligns on the bottom
// row, which can land on its own line beneath a menu's footer.
var antigravityModel = regexp.MustCompile(`^\s*\S.*·\s*(?:low|medium|high|max)\s*$`)

// DetectAntigravity returns the menu Antigravity CLI is blocked on, or nil.
//
// Its tool permission prompts are numbered and read correctly with the
// generic detector. Its first-run folder trust prompt is not:
//
//	Accessing workspace:
//	/path/to/project
//	Do you trust the contents of this project?
//	Antigravity CLI requires permission to read, edit, and execute files here.
//	> Yes, I trust this folder
//	  No, exit
//	  ↑/↓ Navigate · enter Confirm
func DetectAntigravity(pane string) *Question {
	lines := joinAntigravityWraps(bottom(pane))
	if trust := detectAntigravityTrust(lines); trust != nil {
		return trust
	}
	q := Detect(strings.Join(lines, "\n"))
	if q == nil {
		return nil
	}
	// The generic detector folds everything above the options into one line:
	// "Requesting permission for: ls -la Run this command?". The pane keeps
	// them on separate lines — the subject indented beneath the header, then
	// the question — so read them back from there rather than guessing where
	// a command ends and a sentence begins.
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "Requesting permission for:" {
			continue
		}
		var subject []string
		for _, line := range lines[i+1:] {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				continue
			}
			if strings.HasSuffix(trimmed, "?") && !strings.HasPrefix(line, " ") {
				q.Prompt = trimmed
				break
			}
			subject = append(subject, trimmed)
		}
		q.Detail = strings.Join(subject, "\n")
		if q.Title == "" {
			q.Title = "Permission"
		}
		break
	}
	return q
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
