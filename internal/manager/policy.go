package manager

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Agentman's permission policy: which requests from an agent it may approve
// by itself, and which wait for the user.
//
// It is plain Go, deliberately, and not the model's judgement. Agent output
// is untrusted, and a model can be talked into things; this cannot. What the
// user chose as low-risk is approved: reading, searching, tests and builds,
// "continue?", and changes to files inside the agent's own folder. Deleting,
// installing, committing, pushing, deploying, networking, anything run as
// root, and anything this does not recognise wait for the user. When in
// doubt, it asks.
//
// It never picks an option that changes the agent's own settings ("Yes, and
// always allow…", "and don't ask again"): only the plain yes.

// decision is the policy's answer for one request.
type decision struct {
	// kind is what is asked: "command", "edit", "read", "network",
	// "continue" or "question". target is the command, path or URL.
	kind   string
	target string
	// auto means Agentman may approve it, with optionKey.
	auto      bool
	optionKey string
	// summary says what is being asked, for the conversation: "run
	// `npm test`", "edit notes.txt".
	summary string
	// reason says why it waits for the user, when it does.
	reason string
}

// classify decides one question an agent is showing, under the user's
// switches and rules.
func classify(session protocol.Session, q *protocol.Question, p policy) decision {
	if q == nil {
		return decision{reason: "nothing is being asked"}
	}
	kind, target := requestOf(q)
	return decide(session, kind, target, plainYes(q.Options), p)
}

// decide is the policy itself, for one request of kind on target; yes is
// the option that agrees, empty when there is none to choose.
func decide(session protocol.Session, kind, target, yes string, p policy) decision {
	d := decision{kind: kind, target: target}
	switch kind {
	case "command":
		d.summary = "run `" + clip(target, 80) + "`"
	case "edit":
		d.summary = "change " + filepath.Base(target)
	case "read":
		d.summary = "read " + filepath.Base(target)
	case "network":
		d.summary = "open " + clip(target, 80)
	case "continue":
		d.summary = "carry on"
	default:
		d.summary, d.reason = "answer a question", "it is a question for you"
		return d
	}

	// The user's "ask" rules win over everything, for a command over any of
	// its parts.
	targets := []string{target}
	if kind == "command" {
		targets = append(targets, splitCommand(target)...)
	}
	for _, each := range targets {
		if rule, ok := p.match("ask", kind, each, session.Cwd); ok {
			d.reason = "your rule says " + describe(rule)
			return d
		}
	}

	switch kind {
	case "command":
		if ok, why := commandAllowed(target, session.Cwd, p); !ok {
			d.reason = why
			return d
		}
		return approve(d, yes)
	case "edit", "read":
		if private(target) {
			d.reason = "it touches keys or credentials"
			return d
		}
		if _, ok := p.match("allow", kind, target, session.Cwd); ok {
			return approve(d, yes)
		}
		if kind == "edit" && !p.kinds.Edits {
			d.reason = "you approve changes to files yourself"
			return d
		}
		if kind == "read" && !p.kinds.Reads {
			d.reason = "you approve reading files yourself"
			return d
		}
		if !insideFolder(target, session.Cwd) {
			d.reason = "it is outside the agent's folder"
			return d
		}
		return approve(d, yes)
	case "network":
		if _, ok := p.match("allow", kind, target, session.Cwd); ok {
			return approve(d, yes)
		}
		d.reason = "it uses the network"
		return d
	}
	// A bare "continue?".
	if !p.kinds.Continue {
		d.reason = "you answer “continue?” yourself"
		return d
	}
	return approve(d, yes)
}

// isDenial reports whether the option with key says no.
func isDenial(options []protocol.QuestionOption, key string) bool {
	for _, option := range options {
		if option.Key == key {
			label := strings.ToLower(strings.TrimSpace(option.Label))
			return label == "no" || strings.HasPrefix(label, "no,") || strings.HasPrefix(label, "no ") ||
				strings.HasPrefix(label, "deny") || strings.HasPrefix(label, "reject")
		}
	}
	return false
}

func approve(d decision, yes string) decision {
	if yes == "" {
		d.reason = "there is no plain yes to choose"
		return d
	}
	d.auto, d.optionKey = true, yes
	return d
}

// plainYes is the key of the first option that agrees without changing the
// agent's settings.
func plainYes(options []protocol.QuestionOption) string {
	for _, option := range options {
		label := strings.ToLower(option.Label)
		if !strings.HasPrefix(label, "yes") {
			continue
		}
		if strings.Contains(label, "always") || strings.Contains(label, "don't ask") ||
			strings.Contains(label, "dont ask") || strings.Contains(label, "switch") ||
			strings.Contains(label, "auto") || strings.Contains(label, "tell ") ||
			strings.Contains(label, "allow all") || strings.Contains(label, "session") ||
			strings.Contains(label, "conversation") {
			continue
		}
		return option.Key
	}
	return ""
}

var (
	codexCommand   = regexp.MustCompile(`(?m)^\s*\$ (.+)$`)
	claudeEditTo   = regexp.MustCompile(`(?i)(?:make this edit to|create|write to|overwrite) (.+?)\?`)
	pathWithStats  = regexp.MustCompile(`^(\S+)\s{2,}[+-]`)
	continuePhrase = regexp.MustCompile(`(?i)^(do you want to |would you like to )?(continue|proceed|go ahead|keep going)\??$`)
)

// requestOf reads what a question is asking for, from the shapes the agent
// CLIs draw: a command, a file edit, a file read, a URL, a bare "continue?",
// or a question for the user.
func requestOf(q *protocol.Question) (kind, target string) {
	title := strings.TrimSpace(q.Title)
	prompt := strings.TrimSpace(q.Prompt)
	detail := strings.TrimSpace(q.Detail)
	lowerPrompt := strings.ToLower(prompt)

	switch {
	// Codex: "Would you like to run the following command?", then the
	// command after a "$ ", which the detector reports as the prompt.
	case strings.Contains(detail, "Would you like to run the following command?"):
		if command, ok := strings.CutPrefix(prompt, "$ "); ok {
			return "command", strings.TrimSpace(command)
		}
		if m := codexCommand.FindStringSubmatch(detail); m != nil {
			return "command", strings.TrimSpace(m[1])
		}
	// Claude Code: a "Bash command" box, its description, a blank line, then
	// the command.
	case title == "Bash command":
		if _, command, ok := strings.Cut(detail, "\n\n"); ok {
			return "command", strings.TrimSpace(command)
		}
		return "command", detail
	// Antigravity: "Requesting permission for:" with the command as detail.
	case strings.HasPrefix(title, "Requesting permission for") && strings.Contains(lowerPrompt, "run this command"):
		return "command", detail
	case strings.Contains(lowerPrompt, "file edit") || strings.Contains(lowerPrompt, "creation of this file"):
		if m := pathWithStats.FindStringSubmatch(title); m != nil {
			return "edit", m[1]
		}
		return "edit", title
	case strings.HasPrefix(title, "Read: "):
		return "read", strings.TrimPrefix(title, "Read: ")
	case strings.Contains(lowerPrompt, "url") || strings.HasPrefix(title, "http://") ||
		strings.HasPrefix(title, "https://") || title == "Fetch" || strings.Contains(strings.ToLower(title), "web search"):
		return "network", title
	case strings.HasPrefix(title, "Edit") || strings.HasPrefix(title, "Create") || strings.HasPrefix(title, "Write"):
		if m := claudeEditTo.FindStringSubmatch(prompt); m != nil {
			return "edit", strings.TrimSpace(m[1])
		}
	case title == "" && detail == "" && continuePhrase.MatchString(prompt) && len(q.Options) <= 3:
		return "continue", ""
	}
	return "question", ""
}

// insideFolder reports whether path is in the agent's folder. A relative
// path is taken from the folder, and one climbing out of it, or starting at
// someone's home, is not.
func insideFolder(path, folder string) bool {
	path = strings.TrimSpace(strings.Trim(path, `"'`))
	if path == "" || folder == "" || strings.HasPrefix(path, "~") {
		return false
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(folder, path)
	}
	path = filepath.Clean(path)
	folder = filepath.Clean(folder)
	return path == folder || strings.HasPrefix(path, folder+string(filepath.Separator))
}

// Commands, by what they do.
var (
	readOnlyCommands = []string{
		"ls", "cat", "head", "tail", "grep", "egrep", "fgrep", "rg", "pwd", "echo", "printf", "wc",
		"sort", "uniq", "cut", "tr", "diff", "cmp", "which", "whereis", "type", "file", "stat", "du",
		"df", "tree", "jq", "basename", "dirname", "realpath", "readlink", "date", "true", "false",
		"test", "[", "sleep", "seq", "nl", "column", "awk", "md5", "md5sum", "shasum", "sha256sum",
		"xxd", "od", "hexdump", "strings", "uname", "whoami", "hostname", "id", "ps",
	}
	checkCommands = []string{
		"tsc", "eslint", "vitest", "jest", "pytest", "mypy", "ruff", "golangci-lint", "swiftlint",
		"shellcheck", "gofmt", "biome",
	}
	// What waits for the user, and why, for commands worth naming.
	askReasons = map[string]string{
		"rm": "it deletes files", "rmdir": "it deletes files", "shred": "it deletes files",
		"curl": "it uses the network", "wget": "it uses the network", "ssh": "it uses the network",
		"scp": "it uses the network", "rsync": "it uses the network", "nc": "it uses the network",
		"sudo": "it runs as root", "su": "it runs as another user",
		"brew": "it installs software", "pip": "it installs software", "pip3": "it installs software",
		"gem": "it installs software", "apt": "it installs software", "apt-get": "it installs software",
		"kill": "it stops processes", "pkill": "it stops processes", "killall": "it stops processes",
		"docker": "it runs containers", "kubectl": "it changes a cluster",
		"vercel": "it deploys", "railway": "it deploys", "fly": "it deploys", "netlify": "it deploys",
		"heroku": "it deploys", "terraform": "it changes cloud resources", "gh": "it acts on GitHub",
		"open": "it opens other apps", "chmod": "it changes permissions", "chown": "it changes ownership",
	}
	readOnlyGit = []string{
		"status", "diff", "log", "show", "blame", "rev-parse", "ls-files", "describe", "shortlog",
		"reflog", "grep", "cat-file", "ls-tree", "show-ref", "merge-base", "add",
	}
	gitReasons = map[string]string{
		"push": "it pushes to a remote", "commit": "it makes a commit", "pull": "it uses the network",
		"fetch": "it uses the network", "clone": "it uses the network", "reset": "it rewrites history",
		"rebase": "it rewrites history", "merge": "it merges branches", "checkout": "it switches files",
		"switch": "it switches branches", "restore": "it discards changes", "clean": "it deletes files",
		"rm": "it deletes files", "cherry-pick": "it changes history", "revert": "it changes history",
	}
	packageScripts = []string{"test", "t", "lint", "build", "typecheck", "type-check", "check", "format:check", "tsc"}
)

// commandIsSafe decides a shell command by the built-in policy alone: every
// part of it must be read-only, a test or build, or a change to files inside
// the folder.
func commandIsSafe(command, folder string) (bool, string) {
	return commandAllowed(command, folder, policy{kinds: allKinds})
}

// commandAllowed decides a shell command under the user's switches and
// rules: each part must be allowed by a rule, or safe by the built-in policy
// with commands switched on. Nothing nested, nothing touching keys and
// nothing run as root, whatever the rules.
func commandAllowed(command, folder string, p policy) (bool, string) {
	command = strings.TrimSpace(command)
	if command == "" {
		return false, "the command is empty"
	}
	if strings.Contains(command, "`") || strings.Contains(command, "$(") ||
		strings.Contains(command, "<(") || strings.Contains(command, ">(") {
		return false, "it runs a nested command"
	}
	// A cd moves where the relative paths after it land, so each part is
	// checked from the folder the cds before it left the command in.
	_, wholeAllowed := p.match("allow", "command", command, folder)
	base := folder
	for _, part := range splitCommand(command) {
		if words := strings.Fields(part); len(words) > 0 && words[0] == "cd" {
			target := "~"
			if len(words) > 1 {
				target = words[1]
			}
			base = resolvePath(target, base)
			continue
		}
		if private(part) {
			return false, "it touches keys or credentials"
		}
		if runsAsRoot(part) {
			return false, "it runs as root"
		}
		if _, ok := p.match("allow", "command", part, folder); ok || wholeAllowed {
			continue
		}
		if !p.kinds.Commands {
			return false, "you approve commands yourself"
		}
		if ok, why := partIsSafe(part, base, folder); !ok {
			return false, why
		}
	}
	return true, ""
}

// resolvePath is path as an absolute path, taken from base when relative.
// It is empty when that cannot be known.
func resolvePath(path, base string) string {
	path = strings.Trim(strings.TrimSpace(path), `"'`)
	switch {
	case path == "~" || strings.HasPrefix(path, "~/"):
		home := homeDir()
		if home == "" {
			return ""
		}
		return filepath.Join(home, strings.TrimPrefix(path, "~"))
	case path == "" || path == "-" || strings.HasPrefix(path, "~") || strings.Contains(path, "$"):
		return ""
	case filepath.IsAbs(path):
		return filepath.Clean(path)
	case base == "":
		return ""
	}
	return filepath.Join(base, path)
}

// privatePlaces are where keys and credentials live. Even a read-only
// command that touches one waits for the user.
var privatePlaces = []string{".ssh", ".aws", ".gnupg", "id_rsa", "id_ed25519", "id_ecdsa", ".netrc", "Keychains", ".config/gh", ".docker/config.json", ".kube/config"}

var commandSeparators = regexp.MustCompile(`\s*(?:&&|\|\||;|\||\n)\s*`)

func splitCommand(command string) []string {
	var parts []string
	for _, part := range commandSeparators.Split(command, -1) {
		if part = strings.TrimSpace(part); part != "" {
			parts = append(parts, part)
		}
	}
	return parts
}

// harmlessRedirect is output thrown away or merged, which writes nothing.
var harmlessRedirect = regexp.MustCompile(`(?:^|\s)(?:[0-9]?>&[0-9]|&?[0-9]?>\s*/dev/null)`)

func private(text string) bool {
	for _, place := range privatePlaces {
		if strings.Contains(text, place) {
			return true
		}
	}
	return false
}

func partIsSafe(part, base, folder string) (bool, string) {
	if private(part) {
		return false, "it touches keys or credentials"
	}
	cleaned := harmlessRedirect.ReplaceAllString(part, " ")
	if strings.Contains(cleaned, ">") {
		return false, "it writes to a file"
	}
	if strings.Contains(cleaned, "<") {
		return false, "it reads from a redirect"
	}
	words := strings.Fields(cleaned)
	// Leading VAR=value assignments, and wrappers that only time or name the
	// command, say nothing about what it does.
	for len(words) > 0 && (strings.Contains(words[0], "=") && !strings.HasPrefix(words[0], "-") ||
		words[0] == "time" || words[0] == "nice" || words[0] == "command") {
		words = words[1:]
	}
	if len(words) == 0 {
		return true, ""
	}
	name := filepath.Base(words[0])
	args := words[1:]
	switch {
	case slices.Contains(readOnlyCommands, name), slices.Contains(checkCommands, name):
		return true, ""
	case name == "find":
		for _, arg := range args {
			if arg == "-delete" || strings.HasPrefix(arg, "-exec") || arg == "-ok" || strings.HasPrefix(arg, "-fprint") {
				return false, "it runs or deletes from a search"
			}
		}
		return true, ""
	case name == "sed":
		if slices.ContainsFunc(args, func(arg string) bool { return strings.HasPrefix(arg, "-i") }) {
			return false, "it edits files in place"
		}
		return true, ""
	case name == "prettier":
		return slices.Contains(args, "--check") || slices.Contains(args, "-c"), "it rewrites files"
	case name == "mkdir", name == "touch", name == "cp", name == "mv":
		for _, arg := range args {
			if strings.HasPrefix(arg, "-") {
				continue
			}
			if !insideFolder(resolvePath(arg, base), folder) {
				return false, "it changes files outside the agent's folder"
			}
		}
		return true, ""
	case name == "git":
		return gitIsSafe(args)
	case name == "go":
		return firstIn(args, "test", "build", "vet", "list", "version", "env", "doc", "fmt"), "it is a go command that changes more than the build"
	case name == "cargo":
		return firstIn(args, "test", "build", "check", "clippy", "fmt", "doc"), "it is a cargo command that changes more than the build"
	case name == "swift":
		return firstIn(args, "build", "test"), "it is a swift command that changes more than the build"
	case name == "make":
		return len(args) == 0 || firstIn(args, "test", "build", "check", "lint", "fmt", "format", "vet", "all"), "it runs a make target that is not a build or test"
	case name == "xcodebuild":
		for _, arg := range args {
			if arg == "archive" || arg == "install" || arg == "-exportArchive" {
				return false, "it archives or installs an app"
			}
		}
		return true, ""
	case name == "npm", name == "pnpm", name == "yarn", name == "bun":
		if len(args) == 0 {
			return false, "it installs packages"
		}
		if args[0] == "run" && len(args) > 1 && slices.Contains(packageScripts, args[1]) {
			return true, ""
		}
		if slices.Contains(packageScripts, args[0]) {
			return true, ""
		}
		if args[0] == "install" || args[0] == "i" || args[0] == "add" || args[0] == "ci" {
			return false, "it installs packages"
		}
		return false, "it runs a package script that is not a test or build"
	case name == "npx", name == "bunx":
		if len(args) > 0 && (slices.Contains(checkCommands, args[0]) ||
			(args[0] == "prettier" && (slices.Contains(args, "--check") || slices.Contains(args, "-c")))) {
			return true, ""
		}
		return false, "it runs a package that is not a known check"
	case name == "node":
		return len(args) > 0 && args[0] == "--test", "it runs a script"
	case name == "python", name == "python3":
		return len(args) > 1 && args[0] == "-m" && slices.Contains([]string{"pytest", "unittest", "py_compile"}, args[1]), "it runs a script"
	}
	if why, ok := askReasons[name]; ok {
		return false, why
	}
	return false, fmt.Sprintf("%s isn't on the list of safe commands", name)
}

func gitIsSafe(args []string) (bool, string) {
	// Skip git's own options before the subcommand: -C <dir>, -c <k=v>.
	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		if (args[0] == "-C" || args[0] == "-c") && len(args) > 1 {
			args = args[2:]
			continue
		}
		args = args[1:]
	}
	if len(args) == 0 {
		return true, ""
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "branch":
		for _, arg := range rest {
			if slices.Contains([]string{"-d", "-D", "--delete", "-m", "-M", "--move", "-c", "-C", "--copy", "-f", "--force"}, arg) {
				return false, "it changes branches"
			}
		}
		return true, ""
	case "tag":
		for _, arg := range rest {
			if !strings.HasPrefix(arg, "-l") && arg != "--list" {
				return false, "it makes or deletes a tag"
			}
		}
		return true, ""
	case "remote":
		return len(rest) == 0 || rest[0] == "-v" || rest[0] == "show" || rest[0] == "get-url", "it changes remotes"
	case "stash":
		return len(rest) > 0 && (rest[0] == "list" || rest[0] == "show"), "it stashes changes"
	case "config":
		return len(rest) > 0 && (rest[0] == "--get" || rest[0] == "--list" || rest[0] == "-l"), "it changes git config"
	}
	if slices.Contains(readOnlyGit, sub) {
		return true, ""
	}
	if why, ok := gitReasons[sub]; ok {
		return false, why
	}
	return false, "git " + sub + " isn't on the list of safe commands"
}

func firstIn(args []string, allowed ...string) bool {
	for _, arg := range args {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		return slices.Contains(allowed, arg)
	}
	return false
}
