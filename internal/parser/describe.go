package parser

import (
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

// DescribeTool says in plain words what one tool call did: "Ran the Go
// tests", "Edited tools.go", "Searched for “toolPath”". The raw command
// stays in the tool's summary for whoever wants it; this is what a person
// reads first.
//
// own is the agent's own description of the step, when it gave one —
// Claude Code writes one for every command — turned from an instruction
// ("Run the tests") into what happened ("Ran the tests").
func DescribeTool(name, summary, own string) string {
	if own = oneLine(own); own != "" {
		return clipWords(pastTense(own), 90)
	}
	summary = strings.TrimSpace(summary)
	first := oneLine(headLine(summary))
	switch toolKind(name) {
	case "read":
		name := fileName(first)
		if isImageName(name) {
			if generatedName(name) {
				return "Looked at an image"
			}
			return "Looked at " + name
		}
		return withTarget("Read", name)
	case "edit":
		return withTarget("Edited", fileName(first))
	case "write":
		return withTarget("Wrote", fileName(first))
	case "search":
		if first == "" {
			return "Searched the files"
		}
		return "Searched for “" + clipWords(first, 50) + "”"
	case "web":
		if u, err := url.Parse(first); err == nil && u.Host != "" {
			return "Fetched a page from " + strings.TrimPrefix(u.Host, "www.")
		}
		if first != "" {
			return "Searched the web for “" + clipWords(first, 50) + "”"
		}
		return "Looked something up online"
	case "task":
		return "Handed part of the work to a helper agent"
	case "plan":
		return "Updated its plan"
	case "command":
		return describeCommand(summary)
	}
	if name == "" {
		return ""
	}
	return "Used " + strings.TrimPrefix(name, "mcp__")
}

func toolKind(name string) string {
	switch strings.ToLower(name) {
	case "bash", "shell", "exec_command", "run_shell_command", "local_shell", "terminal", "bashoutput":
		return "command"
	case "read", "read_file", "view", "cat", "notebookread":
		return "read"
	case "edit", "multiedit", "apply_patch", "str_replace", "str_replace_editor", "notebookedit", "replace":
		return "edit"
	case "write", "create_file", "write_file":
		return "write"
	case "grep", "glob", "search", "find", "list", "ls", "codebase_search", "file_search":
		return "search"
	case "webfetch", "fetch", "websearch", "web_search", "web_fetch":
		return "web"
	case "task", "agent", "subagent":
		return "task"
	case "todowrite", "update_plan", "todo_write":
		return "plan"
	}
	return ""
}

var (
	// leading `cd somewhere &&`, and VAR=value assignments, say nothing about
	// what a command does.
	cdPrefix   = regexp.MustCompile(`^\s*cd\s+[^;&|]+?\s*(&&|;)\s*`)
	assignment = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=("[^"]*"|'[^']*'|\S*)\s+`)
	quoted     = regexp.MustCompile(`\s-[A-Za-z]*m\s+("([^"]*)"|'([^']*)')`)
	stepSplit  = regexp.MustCompile(`\s*(&&|\|\||;|\|)\s*`)
)

// describeCommand says what a shell command does, from its first step.
func describeCommand(command string) string {
	line := oneLine(headLine(command))
	for {
		trimmed := cdPrefix.ReplaceAllString(line, "")
		trimmed = assignment.ReplaceAllString(trimmed, "")
		trimmed = strings.TrimPrefix(trimmed, "env ")
		trimmed = strings.TrimPrefix(trimmed, "sudo ")
		trimmed = strings.TrimPrefix(trimmed, "timeout ")
		if trimmed == line {
			break
		}
		line = trimmed
	}
	steps := stepSplit.Split(line, -1)
	if len(steps) == 0 || strings.TrimSpace(steps[0]) == "" {
		return "Ran a command"
	}
	words := strings.Fields(steps[0])
	program := filepath.Base(words[0])
	arg := func(i int) string {
		if i < len(words) {
			return words[i]
		}
		return ""
	}
	// The first argument that is not a flag.
	target := func() string {
		for _, w := range words[1:] {
			if !strings.HasPrefix(w, "-") {
				return strings.Trim(w, `"'`)
			}
		}
		return ""
	}

	switch program {
	case "git":
		switch arg(1) {
		case "commit":
			if m := quoted.FindStringSubmatch(steps[0]); m != nil {
				msg := m[2] + m[3]
				return "Committed “" + clipWords(headLine(msg), 60) + "”"
			}
			return "Committed the changes"
		case "push":
			return "Pushed to GitHub"
		case "pull", "fetch":
			return "Fetched the latest from GitHub"
		case "status":
			return "Checked what has changed"
		case "diff":
			return "Looked at the changes"
		case "log", "show":
			return "Looked at the history"
		case "checkout", "switch":
			return "Switched branch"
		case "branch":
			return "Looked at the branches"
		case "add":
			return "Staged changes"
		case "stash":
			return "Set changes aside"
		case "merge", "rebase":
			return "Combined branches"
		case "clone":
			return "Downloaded a repository"
		}
		return "Ran git " + arg(1)
	case "go":
		switch arg(1) {
		case "test":
			return "Ran the Go tests"
		case "build", "install":
			return "Built the Go code"
		case "vet":
			return "Checked the Go code"
		case "run":
			return "Ran a Go program"
		case "mod", "get":
			return "Updated Go dependencies"
		}
	case "npm", "pnpm", "yarn", "bun", "npx":
		switch arg(1) {
		case "test":
			return "Ran the tests"
		case "install", "i", "ci", "add":
			return "Installed packages"
		case "run":
			return "Ran the " + arg(2) + " script"
		case "tsc":
			return "Checked the TypeScript"
		case "expo":
			return "Ran Expo " + arg(2)
		}
		if program == "npx" && arg(1) != "" {
			return "Ran " + arg(1)
		}
	case "xcodebuild":
		if strings.Contains(steps[0], " test") {
			return "Ran the app's tests"
		}
		return "Built the app"
	case "swift", "swiftc":
		return "Compiled a Swift helper"
	case "grep", "rg", "ag":
		return "Searched the code"
	case "find", "fd", "mdfind":
		return "Looked for files"
	case "ls":
		if t := target(); t != "" {
			return "Listed " + fileName(t)
		}
		return "Listed the folder"
	case "cat", "head", "tail", "less", "sed", "awk", "nl", "wc":
		if program == "sed" && !strings.Contains(steps[0], " -n") {
			return "Edited text with sed"
		}
		if t := lastPathArg(words); t != "" {
			return "Read " + fileName(t)
		}
		return "Read a file"
	case "rm":
		if t := target(); t != "" {
			return "Removed " + fileName(t)
		}
		return "Removed files"
	case "mkdir":
		return "Made a folder"
	case "cp", "rsync":
		return "Copied files"
	case "mv":
		return "Moved files"
	case "curl", "wget":
		for _, w := range words[1:] {
			if u, err := url.Parse(strings.Trim(w, `"'`)); err == nil && u.Host != "" {
				return "Called " + u.Host
			}
		}
		return "Made a web request"
	case "open":
		return "Opened " + fileName(target())
	case "kill", "pkill", "killall":
		return "Stopped a process"
	case "pgrep", "ps", "lsof", "lsappinfo":
		return "Checked what is running"
	case "sleep":
		return "Waited"
	case "python", "python3", "node", "ruby", "bash", "sh", "zsh":
		return "Ran a script"
	case "make":
		return "Ran make " + arg(1)
	case "cargo":
		return "Ran cargo " + arg(1)
	case "docker":
		return "Ran docker " + arg(1)
	case "gh":
		return "Used GitHub: " + strings.TrimSpace(arg(1)+" "+arg(2))
	case "brew":
		return "Ran Homebrew " + arg(1)
	case "echo", "printf":
		return "Printed a note"
	case "osascript":
		return "Ran an AppleScript"
	case "screencapture":
		return "Took a screenshot"
	case "tmux":
		return "Used tmux"
	case "claude", "codex", "opencode", "cursor-agent", "kiro-cli", "gemini":
		return "Ran " + program
	}
	return "Ran " + program
}

// pastTense turns an instruction into what was done: "Run the tests" →
// "Ran the tests". Only the first word changes, and only when it reads as a
// verb.
func pastTense(text string) string {
	word, rest, _ := strings.Cut(text, " ")
	lower := strings.ToLower(word)
	if lower == "" || !isWord(lower) || strings.HasSuffix(lower, "ed") || strings.HasSuffix(lower, "ing") {
		return text
	}
	past, ok := irregular[lower]
	if !ok {
		switch {
		case notVerbs[lower], strings.HasSuffix(lower, "ly") && !lyVerbs[lower]:
			return text
		case doubled[lower]:
			past = lower + lower[len(lower)-1:] + "ed"
		case strings.HasSuffix(lower, "e"):
			past = lower + "d"
		case strings.HasSuffix(lower, "y") && len(lower) > 1 && !strings.ContainsRune("aeiou", rune(lower[len(lower)-2])):
			past = lower[:len(lower)-1] + "ied"
		default:
			past = lower + "ed"
		}
	}
	if r := []rune(word); len(r) > 0 && unicode.IsUpper(r[0]) {
		past = strings.ToUpper(past[:1]) + past[1:]
	}
	if rest == "" {
		return past
	}
	return past + " " + rest
}

var irregular = map[string]string{
	"run": "ran", "rerun": "reran", "read": "read", "reread": "reread", "build": "built",
	"rebuild": "rebuilt", "find": "found", "make": "made", "write": "wrote", "rewrite": "rewrote",
	"get": "got", "show": "showed", "take": "took", "give": "gave", "put": "put", "set": "set",
	"reset": "reset", "send": "sent", "resend": "resent", "keep": "kept", "begin": "began",
	"see": "saw", "tell": "told", "choose": "chose", "leave": "left", "hold": "held",
	"bring": "brought", "catch": "caught", "cut": "cut", "do": "did", "redo": "redid",
	"undo": "undid", "go": "went", "have": "had", "know": "knew", "let": "let", "lose": "lost",
	"pay": "paid", "quit": "quit", "say": "said", "split": "split", "spend": "spent",
	"think": "thought", "understand": "understood", "feed": "fed", "lay": "laid",
	"rid": "rid", "shut": "shut", "fit": "fit", "dig": "dug", "spin": "spun", "win": "won",
	"draw": "drew", "redraw": "redrew", "throw": "threw", "grow": "grew", "fall": "fell",
	"stick": "stuck", "strike": "struck", "swing": "swung", "teach": "taught", "seek": "sought",
	"sell": "sold", "meet": "met", "sleep": "slept", "light": "lit", "lead": "led",
	"bind": "bound", "rebind": "rebound", "wind": "wound", "unwind": "unwound", "hide": "hid",
	"ring": "rang", "drive": "drove", "ride": "rode", "rise": "rose", "speak": "spoke",
	"steal": "stole", "wake": "woke", "wear": "wore", "tear": "tore", "forget": "forgot",
	"overwrite": "overwrote", "upload": "uploaded", "download": "downloaded",
}

// lyVerbs are the verbs that end like adverbs ("already", "finally").
var lyVerbs = map[string]bool{
	"apply": true, "reply": true, "supply": true, "rely": true, "multiply": true, "comply": true,
	"imply": true, "fly": true, "rally": true, "tally": true, "reapply": true,
}

var doubled = map[string]bool{
	"stop": true, "commit": true, "plan": true, "drop": true, "tag": true, "ship": true,
	"skip": true, "swap": true, "wrap": true, "unwrap": true, "trim": true, "step": true,
	"log": true, "map": true, "grab": true, "chop": true, "zip": true, "unzip": true,
	"scan": true, "pin": true, "unpin": true, "plug": true, "grep": true, "prep": true,
	"strip": true, "flag": true, "submit": true, "rerun": true, "admit": true, "omit": true,
	"emit": true, "permit": true, "drag": true, "snap": true, "tap": true, "nap": true,
	"jam": true, "clip": true, "flip": true, "slim": true, "stub": true, "tab": true,
}

// notVerbs are first words that are not instructions: a description that
// starts with one is left as it is.
var notVerbs = map[string]bool{
	"the": true, "a": true, "an": true, "this": true, "that": true, "these": true, "those": true,
	"my": true, "our": true, "your": true, "its": true, "all": true, "every": true, "each": true,
	"no": true, "one": true, "two": true, "both": true, "new": true, "old": true, "final": true,
	"quick": true, "full": true, "git": true, "go": false, "npm": true, "xcode": true, "swift": true,
	"is": true, "are": true, "was": true, "were": true, "why": true, "what": true, "how": true,
	"when": true, "where": true, "which": true, "if": true, "then": true, "now": true,
	"again": true, "also": true, "only": true, "just": true, "first": true, "second": true,
	"next": true, "last": true, "agentman": true, "claude": true, "codex": true,
	"already": true, "still": true, "here": true, "there": true, "back": true, "away": true,
	"done": true, "nothing": true, "everything": true, "something": true,
}

func isWord(s string) bool {
	for _, r := range s {
		if !unicode.IsLetter(r) && r != '-' {
			return false
		}
	}
	return true
}

func isImageName(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".heic", ".bmp", ".tif", ".tiff":
		return true
	}
	return false
}

// generatedName reports whether a file's name was made by a program rather
// than a person — "72v33m2m3pjbqp54.jpg" — and so tells a reader nothing.
func generatedName(name string) bool {
	stem := strings.TrimSuffix(name, filepath.Ext(name))
	if len(stem) < 8 || strings.ContainsAny(stem, " -_.") {
		return false
	}
	digits := 0
	for _, r := range stem {
		if unicode.IsDigit(r) {
			digits++
		}
	}
	return digits*4 >= len(stem)
}

func withTarget(verb, target string) string {
	if target == "" {
		return verb + " a file"
	}
	return verb + " " + target
}

func fileName(path string) string {
	path = strings.Trim(strings.TrimSpace(path), `"'`)
	if path == "" {
		return ""
	}
	return filepath.Base(path)
}

// lastPathArg is the last argument that looks like a file, the thing a
// reading command reads.
func lastPathArg(words []string) string {
	for i := len(words) - 1; i > 0; i-- {
		w := strings.Trim(words[i], `"'`)
		if strings.HasPrefix(w, "-") || strings.ContainsAny(w, "<>$`") {
			continue
		}
		if strings.Contains(w, "/") || strings.Contains(w, ".") {
			return w
		}
	}
	return ""
}

func headLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func clipWords(s string, limit int) string {
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	cut := string(r[:limit])
	if i := strings.LastIndex(cut, " "); i > limit/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,.;:") + "…"
}
