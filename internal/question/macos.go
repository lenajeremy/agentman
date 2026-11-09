package question

import (
	"regexp"
	"strings"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// commandAt is where a program starts in a shell line: the line's start, after
// a separator, or inside bash -c "...", and after sudo or a folder.
const commandAt = "(?m)(?:^|[;&|(`]\\s*|\\$\\(\\s*|-c\\s+[\"'])\\s*(?:\\$\\s+)?(?:sudo\\s+)?(?:[^\\s;&|('\"]*/)?"

// The commands that bring up a macOS permission dialog of their own. An agent
// asking to run one is about to put a dialog on the user's screen, naming
// whatever started the agent; said beforehand, it isn't a surprise.
var macOSDialogs = []struct {
	command *regexp.Regexp
	what    string
	// harmless is what the command is doing when no dialog comes.
	harmless func(match string) bool
}{
	{regexp.MustCompile(commandAt + `crontab\b(?:\s+-l\b)?`), "administer your computer",
		func(match string) bool { return strings.HasSuffix(match, "-l") }},
	{regexp.MustCompile(commandAt + `osascript\b[^\n]*`), "control another app",
		func(match string) bool { return strings.Contains(match, "display notification") }},
	{regexp.MustCompile(commandAt + `screencapture\b`), "record your screen", nil},
}

// MacOSNotice says what macOS will ask the user to allow if this is agreed
// to, or nothing. Only a command being run counts, not one being mentioned.
func MacOSNotice(q *protocol.Question) string {
	if q == nil {
		return ""
	}
	text := q.Detail
	if strings.HasPrefix(strings.TrimSpace(q.Prompt), "$ ") {
		text += "\n" + strings.TrimSpace(q.Prompt)
	}
	for _, dialog := range macOSDialogs {
		for _, match := range dialog.command.FindAllString(text, -1) {
			if dialog.harmless != nil && dialog.harmless(match) {
				continue
			}
			return "If you agree, macOS will ask you to let this " + dialog.what + "."
		}
	}
	return ""
}
