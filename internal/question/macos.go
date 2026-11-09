package question

import (
	"regexp"
	"strings"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// The commands that bring up a macOS permission dialog of their own. An agent
// asking to run one is about to put a dialog on the user's screen, naming
// whatever started the agent; said beforehand, it isn't a surprise.
var macOSDialogs = []struct {
	command *regexp.Regexp
	what    string
}{
	{regexp.MustCompile(`(^|[\s;&|(/` + "`" + `])crontab\b(\s+-l\b)?`), "administer your computer"},
	{regexp.MustCompile(`(^|[\s;&|(/` + "`" + `])osascript\b`), "control another app"},
	{regexp.MustCompile(`(^|[\s;&|(/` + "`" + `])screencapture\b`), "record your screen"},
}

// MacOSNotice says what macOS will ask the user to allow if this is agreed
// to, or nothing. Only listing a crontab needs no permission.
func MacOSNotice(q *protocol.Question) string {
	if q == nil {
		return ""
	}
	text := strings.Join([]string{q.Title, q.Prompt, q.Detail}, "\n")
	for _, dialog := range macOSDialogs {
		for _, match := range dialog.command.FindAllString(text, -1) {
			if strings.HasSuffix(match, "-l") {
				continue
			}
			return "If you agree, macOS will ask you to let this " + dialog.what + "."
		}
	}
	return ""
}
