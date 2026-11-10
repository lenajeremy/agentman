package question

import (
	"path"
	"regexp"
	"strings"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// MacOSNotice says what macOS will ask the user to allow if this is agreed
// to, or nothing. An agent asking to run one of these commands is about to put
// a dialog on the user's screen, naming whatever started the agent; said
// beforehand, it isn't a surprise. Only a command being run counts, so the
// text is read as a shell would, a program at a time: mentioning one, or
// searching for its name, is nothing.
func MacOSNotice(q *protocol.Question) string {
	if q == nil {
		return ""
	}
	text := q.Detail
	// Claude Code's box is a description, a blank line, then the command.
	if strings.TrimSpace(q.Title) == "Bash command" {
		if _, command, ok := strings.Cut(text, "\n\n"); ok {
			text = command
		}
	}
	// Codex's command is in its prompt.
	if prompt := strings.TrimSpace(q.Prompt); strings.HasPrefix(prompt, "$ ") {
		text += "\n" + prompt
	}
	if what := dialogIn(text, 0); what != "" {
		return "If you agree, macOS will ask you to let this " + what + "."
	}
	return ""
}

// dialogIn is what the first program in these shell lines that brings up a
// dialog would ask for.
func dialogIn(text string, depth int) string {
	for _, command := range commands(text) {
		if what := dialogOf(words(command), depth); what != "" {
			return what
		}
	}
	return ""
}

var (
	assignment = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)
	shellFlag  = regexp.MustCompile(`^-[a-zA-Z]*c[a-zA-Z]*$`)
)

// leading are words that come before the program they apply to. "Shell" and
// "$" are how Kiro and Codex show a command.
var leading = map[string]bool{
	"if": true, "then": true, "elif": true, "else": true, "do": true, "while": true, "until": true, "!": true, "{": true,
	"Shell": true, "nohup": true, "time": true, "exec": true, "command": true, "builtin": true,
}

// wrappers run the program named later on their line, after flags of their own.
var wrappers = map[string]bool{"sudo": true, "env": true, "nice": true, "timeout": true, "caffeinate": true}

func dialogOf(args []string, depth int) string {
	for len(args) > 0 {
		word := strings.TrimLeft(args[0], "$(")
		name := path.Base(word)
		switch {
		case word == "":
			args = args[1:]
			continue
		case assignment.MatchString(word), leading[word]:
			args = args[1:]
			continue
		case wrappers[name]:
			args = afterWrapper(name, args[1:])
			continue
		}
		args[0] = word
		break
	}
	if len(args) == 0 {
		return ""
	}
	rest := args[1:]
	switch path.Base(args[0]) {
	case "crontab":
		if len(rest) == 1 && rest[0] == "-l" {
			return ""
		}
		return "administer your computer"
	case "osascript":
		if strings.Contains(strings.Join(rest, " "), "display notification") {
			return ""
		}
		return "control another app"
	case "screencapture":
		return "record your screen"
	case "sh", "bash", "zsh", "dash":
		if depth > 2 {
			return ""
		}
		for i, arg := range rest {
			if shellFlag.MatchString(arg) && i+1 < len(rest) {
				return dialogIn(rest[i+1], depth+1)
			}
		}
	}
	return ""
}

// afterWrapper is what is left once a wrapper's own flags and values are gone.
func afterWrapper(name string, args []string) []string {
	for len(args) > 0 {
		word := args[0]
		switch {
		case strings.HasPrefix(word, "-"):
			args = args[1:]
			// These take a value of their own.
			if (name == "sudo" && (word == "-u" || word == "-g")) || (name == "nice" && word == "-n") || (name == "timeout" && (word == "-k" || word == "-s")) {
				if len(args) > 0 {
					args = args[1:]
				}
			}
		case name == "timeout":
			return args[1:] // its duration
		case name == "env" && assignment.MatchString(word):
			args = args[1:]
		default:
			return args
		}
	}
	return nil
}

// commands splits shell text into its simple commands, leaving quoted text
// whole.
func commands(text string) []string {
	var out []string
	var current strings.Builder
	var quote rune
	flush := func() {
		if strings.TrimSpace(current.String()) != "" {
			out = append(out, current.String())
		}
		current.Reset()
	}
	for _, r := range text {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			}
			current.WriteRune(r)
		case r == '"' || r == '\'':
			quote = r
			current.WriteRune(r)
		case r == ';' || r == '&' || r == '|' || r == '\n':
			flush()
		default:
			current.WriteRune(r)
		}
	}
	flush()
	return out
}

// words splits one command the way a shell would, without its quotes.
func words(command string) []string {
	var out []string
	var current strings.Builder
	var quote rune
	started := false
	flush := func() {
		if started {
			out = append(out, current.String())
		}
		current.Reset()
		started = false
	}
	for _, r := range command {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				current.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote = r
			started = true
		case r == ' ' || r == '\t':
			flush()
		default:
			current.WriteRune(r)
			started = true
		}
	}
	flush()
	return out
}
