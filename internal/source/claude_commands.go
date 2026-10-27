package source

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/tmux"
)

// The "/" menu of a Claude session: the commands Claude Code would offer at
// its own prompt. Claude Code keeps that list to itself, so it is put
// together the way Claude Code does: its built-in commands, the skills it
// ships, and skills and commands from the same places on disk, honouring
// the same settings. A session that has taken a turn also has Claude's own
// list of skills in its transcript, which adds any this misses.
//
// Built-in commands are the ones that do something useful sent from another
// device. Those that only run a picker or editor in the terminal (/theme,
// /permissions, /resume) are left out, and so are those that would leave the
// terminal, such as /exit and /background. Settings editors (/config,
// /advisor, /autocompact) need their argument: with none they open an editor
// in the terminal that a stray key can change things in.

var claudeBuiltinCommands = []protocol.Command{
	{Name: "compact", ArgumentHint: "[instructions]", Description: "Summarize the conversation so far to free up context"},
	{Name: "clear", ArgumentHint: "[name]", Description: "Start a new conversation with empty context"},
	{Name: "context", ArgumentHint: "[all]", Description: "Show how full the context window is"},
	{Name: "usage", Description: "Show your plan's usage limits and this session's cost", Panel: true},
	{Name: "status", Description: "Show the version, model, account and connections", Panel: true},
	{Name: "model", ArgumentHint: "<model>", Description: "Switch the model, and make it the default for new sessions"},
	{Name: "rename", ArgumentHint: "[name]", Description: "Rename this session"},
	{Name: "color", ArgumentHint: "[color|default]", Description: "Set this session's prompt bar colour"},
	{Name: "goal", ArgumentHint: "[condition|clear]", Description: "Keep working until a condition is met"},
	{Name: "plan", ArgumentHint: "[description]", Description: "Switch to plan mode, optionally with a task"},
	{Name: "recap", Description: "Summarize this session in one line"},
	{Name: "subtask", ArgumentHint: "<task>", Description: "Hand a side task to a subagent that reports back here"},
	{Name: "tasks", Description: "Show background work in this session", Panel: true},
	{Name: "mcp", ArgumentHint: "[reconnect|enable|disable <server>]", Description: "Show MCP servers, or reconnect, enable or disable one", Panel: true},
	{Name: "config", ArgumentHint: "<key=value>", Description: "Change a setting, such as theme=dark"},
	{Name: "output-style", ArgumentHint: "[style]", Description: "List output styles or switch to one"},
	{Name: "advisor", ArgumentHint: "<model|off>", Description: "Turn the advisor on or off for this session"},
	{Name: "autocompact", ArgumentHint: "<auto|tokens>", Description: "Set how full the context gets before it is compacted"},
	{Name: "reload-skills", Description: "Pick up skills added or changed on disk"},
	{Name: "reload-plugins", Description: "Reload plugins to apply changes"},
	{Name: "init", Description: "Write a CLAUDE.md guide for this project"},
	{Name: "security-review", Description: "Review this branch's changes for security problems"},
	{Name: "help", Description: "Show help and keyboard shortcuts", Panel: true},
}

// claudeBundledSkills are skills Claude Code ships. Which are on depends on
// the account and the session; Claude's own list, when the transcript has
// one, adds the rest.
var claudeBundledSkills = []protocol.Command{
	{Name: "code-review", ArgumentHint: "[level] [target]", Description: "Review the current diff for bugs"},
	{Name: "simplify", ArgumentHint: "[target]", Description: "Review changed code for cleanups and apply them"},
	{Name: "loop", ArgumentHint: "[interval] [prompt]", Description: "Run a prompt repeatedly while the session stays open"},
	{Name: "schedule", ArgumentHint: "[description]", Description: "Create or manage scheduled cloud tasks"},
	{Name: "verify", Description: "Check a change works by running the app"},
	{Name: "run", Description: "Launch the app to see a change working"},
	{Name: "debug", ArgumentHint: "[description]", Description: "Turn on debug logging and look into a problem"},
	{Name: "batch", ArgumentHint: "<instruction>", Description: "Make a large change in parallel across the codebase"},
	{Name: "doctor", Description: "Check your Claude Code setup and fix problems"},
	{Name: "update-config", ArgumentHint: "[request]", Description: "Change Claude Code's settings by describing the change"},
	{Name: "fewer-permission-prompts", Description: "Allow common read-only commands so you are asked less"},
}

// claudePanels are the commands read from a panel: they change nothing, and
// Esc closes them. The value says whether Claude opens it mid-turn; the rest
// it holds until the turn ends.
var claudePanels = map[string]bool{"usage": true, "status": true, "tasks": true, "mcp": false, "help": false}

// claudeManagedDir is where an organization deploys skills to a Mac.
var claudeManagedDir = "/Library/Application Support/ClaudeCode"

const (
	maxClaudeCommands           = 600
	maxCommandDescription       = 200
	maxCommandHint              = 80
	maxSkillFileBytes           = 64 << 10
	maxSkillListingScan   int64 = 16 << 20
)

// Commands lists a session's "/" menu.
func (s *ClaudeSource) Commands(ctx context.Context, sessionID string) ([]protocol.Command, error) {
	s.mu.RLock()
	session, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("source: unknown claude session %q", sessionID)
	}
	return claudeCommands(s.home, session.meta.Cwd, session.transcript), nil
}

func claudeCommands(home, cwd, transcript string) []protocol.Command {
	root := repoRoot(cwd)
	settings := readClaudeMenuSettings(home, root)
	seen := map[string]bool{}
	out := make([]protocol.Command, 0, 128)
	add := func(c protocol.Command) {
		key := strings.ToLower(c.Name)
		if !validCommandName(c.Name) || seen[key] || len(out) == maxClaudeCommands {
			return
		}
		seen[key] = true
		c.Description = clipCommandText(c.Description, maxCommandDescription)
		c.ArgumentHint = clipCommandText(c.ArgumentHint, maxCommandHint)
		out = append(out, c)
	}
	addAll := func(commands []protocol.Command) {
		for _, c := range commands {
			if c.Source != "plugin" && settings.overrides[c.Name] == "off" {
				continue
			}
			add(c)
		}
	}

	for _, c := range claudeBuiltinCommands {
		c.Source = "builtin"
		add(c)
	}
	// Enterprise over personal over project, and a skill over a command file
	// of the same name.
	managed := filepath.Join(claudeManagedDir, ".claude")
	personal := filepath.Join(home, ".claude")
	projects := projectClaudeDirs(cwd, root)
	addAll(scanSkillDir(filepath.Join(managed, "skills"), "managed", ""))
	addAll(scanSkillDir(filepath.Join(personal, "skills"), "personal", ""))
	for _, dir := range projects {
		addAll(scanSkillDir(filepath.Join(dir, "skills"), "project", ""))
	}
	addAll(scanCommandDir(filepath.Join(managed, "commands"), "managed", ""))
	addAll(scanCommandDir(filepath.Join(personal, "commands"), "personal", ""))
	for _, dir := range projects {
		addAll(scanCommandDir(filepath.Join(dir, "commands"), "project", ""))
	}
	addAll(syncedSkills(home))
	for _, c := range claudePluginCommands(home, cwd, root, settings.plugins) {
		add(c)
	}
	for _, c := range claudeBundledSkills {
		c.Source = "bundled"
		addAll([]protocol.Command{c})
	}
	for _, c := range readSkillListing(transcript) {
		addAll([]protocol.Command{c})
	}
	return out
}

// repoRoot is the git repository a folder is in, or the folder itself.
func repoRoot(cwd string) string {
	if cwd == "" {
		return ""
	}
	for dir := filepath.Clean(cwd); ; {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return filepath.Clean(cwd)
		}
		dir = parent
	}
}

// projectClaudeDirs are the .claude folders Claude Code reads project skills
// from: the folder it started in and each one above it, up to the
// repository's root, nearest first.
func projectClaudeDirs(cwd, root string) []string {
	if cwd == "" {
		return nil
	}
	var dirs []string
	for dir := filepath.Clean(cwd); ; dir = filepath.Dir(dir) {
		dirs = append(dirs, filepath.Join(dir, ".claude"))
		if dir == root || filepath.Dir(dir) == dir || !strings.HasPrefix(dir, root) {
			return dirs
		}
	}
}

// claudeMenuSettings is what the settings files say about the menu.
type claudeMenuSettings struct {
	// overrides is skillOverrides: "off" hides a skill.
	overrides map[string]string
	// plugins is enabledPlugins, "name@marketplace" to on or off.
	plugins map[string]bool
}

// readClaudeMenuSettings merges the user's settings and the project's, the
// project's winning.
func readClaudeMenuSettings(home, root string) claudeMenuSettings {
	merged := claudeMenuSettings{overrides: map[string]string{}, plugins: map[string]bool{}}
	paths := []string{filepath.Join(home, ".claude", "settings.json")}
	if root != "" {
		paths = append(paths,
			filepath.Join(root, ".claude", "settings.json"),
			filepath.Join(root, ".claude", "settings.local.json"))
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var file struct {
			SkillOverrides map[string]string `json:"skillOverrides"`
			EnabledPlugins map[string]any    `json:"enabledPlugins"`
		}
		if json.Unmarshal(data, &file) != nil {
			continue
		}
		for name, value := range file.SkillOverrides {
			merged.overrides[name] = value
		}
		for name, value := range file.EnabledPlugins {
			on, _ := value.(bool)
			merged.plugins[name] = on
		}
	}
	return merged
}

// scanSkillDir reads <dir>/<skill>/SKILL.md. A plugin's skills are named
// "plugin:skill".
func scanSkillDir(dir, source, plugin string) []protocol.Command {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []protocol.Command
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") || (plugin == "" && entry.Name() == "synced") {
			continue
		}
		file, ok := readSkillFile(filepath.Join(dir, entry.Name(), "SKILL.md"))
		if !ok || !file.userInvocable {
			continue
		}
		name := file.name
		if name == "" {
			name = entry.Name()
		}
		out = append(out, file.command(withPlugin(plugin, name), source))
	}
	return out
}

// scanCommandDir reads the older command files: <dir>/deploy.md is /deploy,
// and <dir>/frontend/component.md is /frontend:component.
func scanCommandDir(dir, source, plugin string) []protocol.Command {
	var out []protocol.Command
	_ = filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if entry.IsDir() {
			if path != dir && strings.HasPrefix(entry.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.EqualFold(filepath.Ext(path), ".md") || len(out) == maxClaudeCommands {
			return nil
		}
		rel, err := filepath.Rel(dir, strings.TrimSuffix(path, filepath.Ext(path)))
		if err != nil {
			return nil
		}
		file, ok := readSkillFile(path)
		if !ok || !file.userInvocable {
			return nil
		}
		name := strings.ReplaceAll(filepath.ToSlash(rel), "/", ":")
		out = append(out, file.command(withPlugin(plugin, name), source))
		return nil
	})
	return out
}

// syncedSkills are the claude.ai skills synced for the account signed in,
// named "anthropic-skills:<name>".
func syncedSkills(home string) []protocol.Command {
	root := filepath.Join(home, ".claude", "skills", "synced")
	buckets, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	org := claudeOrganization(home)
	var chosen string
	var newest time.Time
	for _, bucket := range buckets {
		if !bucket.IsDir() || strings.HasPrefix(bucket.Name(), ".") {
			continue
		}
		if org != "" && strings.HasPrefix(bucket.Name(), org+"_") {
			chosen = bucket.Name()
			break
		}
		if info, err := bucket.Info(); err == nil && info.ModTime().After(newest) {
			chosen, newest = bucket.Name(), info.ModTime()
		}
	}
	if chosen == "" {
		return nil
	}
	return scanSkillDir(filepath.Join(root, chosen), "personal", "anthropic-skills")
}

// claudeOrganization is the organization Claude Code is signed in to.
func claudeOrganization(home string) string {
	data, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		return ""
	}
	var config struct {
		OAuthAccount struct {
			OrganizationUUID string `json:"organizationUuid"`
		} `json:"oauthAccount"`
	}
	if json.Unmarshal(data, &config) != nil {
		return ""
	}
	return config.OAuthAccount.OrganizationUUID
}

// claudePluginCommands lists the skills and commands of enabled plugins.
func claudePluginCommands(home, cwd, root string, enabled map[string]bool) []protocol.Command {
	data, err := os.ReadFile(filepath.Join(home, ".claude", "plugins", "installed_plugins.json"))
	if err != nil {
		return nil
	}
	var installed struct {
		Plugins map[string][]struct {
			Scope       string `json:"scope"`
			InstallPath string `json:"installPath"`
			ProjectPath string `json:"projectPath"`
		} `json:"plugins"`
	}
	if json.Unmarshal(data, &installed) != nil {
		return nil
	}
	keys := make([]string, 0, len(installed.Plugins))
	for key := range installed.Plugins {
		if enabled[key] {
			keys = append(keys, key)
		}
	}
	slices.Sort(keys)
	var out []protocol.Command
	for _, key := range keys {
		install := ""
		for _, entry := range installed.Plugins[key] {
			switch {
			case entry.Scope == "user" && install == "":
				install = entry.InstallPath
			case entry.Scope != "user" && entry.ProjectPath != "" &&
				(filepath.Clean(entry.ProjectPath) == root || filepath.Clean(entry.ProjectPath) == filepath.Clean(cwd)):
				install = entry.InstallPath
			}
		}
		if install == "" {
			continue
		}
		name, _, _ := strings.Cut(key, "@")
		var manifest struct {
			Name     string `json:"name"`
			Commands any    `json:"commands"`
			Skills   any    `json:"skills"`
		}
		if data, err := os.ReadFile(filepath.Join(install, ".claude-plugin", "plugin.json")); err == nil {
			_ = json.Unmarshal(data, &manifest)
		}
		if manifest.Name != "" {
			name = manifest.Name
		}
		if file, ok := readSkillFile(filepath.Join(install, "SKILL.md")); ok && file.userInvocable {
			own := file.name
			if own == "" {
				own = filepath.Base(install)
			}
			out = append(out, file.command(withPlugin(name, own), "plugin"))
		}
		for _, dir := range pluginDirs(install, "skills", manifest.Skills) {
			out = append(out, scanSkillDir(dir, "plugin", name)...)
		}
		for _, dir := range pluginDirs(install, "commands", manifest.Commands) {
			out = append(out, scanCommandDir(dir, "plugin", name)...)
		}
	}
	return out
}

// pluginDirs is a plugin's default folder and any its manifest adds, kept
// inside the plugin.
func pluginDirs(install, fallback string, declared any) []string {
	dirs := []string{filepath.Join(install, fallback)}
	var extra []string
	switch value := declared.(type) {
	case string:
		extra = []string{value}
	case []any:
		for _, item := range value {
			if path, ok := item.(string); ok {
				extra = append(extra, path)
			}
		}
	}
	for _, path := range extra {
		dir := filepath.Join(install, path)
		if rel, err := filepath.Rel(install, dir); err != nil || rel == ".." || strings.HasPrefix(rel, "../") {
			continue
		}
		if !slices.Contains(dirs, dir) {
			dirs = append(dirs, dir)
		}
	}
	return dirs
}

func withPlugin(plugin, name string) string {
	if plugin == "" || strings.HasPrefix(name, plugin+":") {
		return name
	}
	return plugin + ":" + name
}

// skillFile is what the menu needs from a skill or command file.
type skillFile struct {
	name, description, hint string
	userInvocable           bool
}

func (f skillFile) command(name, source string) protocol.Command {
	return protocol.Command{Name: name, Description: f.description, ArgumentHint: f.hint, Source: source}
}

// readSkillFile reads a skill's frontmatter: name, description and
// argument-hint, and whether the person may run it. A file without a
// description is described by its first line.
func readSkillFile(path string) (skillFile, bool) {
	f, err := os.Open(path)
	if err != nil {
		return skillFile{}, false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxSkillFileBytes))
	if err != nil {
		return skillFile{}, false
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	file := skillFile{userInvocable: true}
	body := text
	if rest, ok := strings.CutPrefix(text, "---\n"); ok {
		if end := strings.Index(rest, "\n---"); end >= 0 {
			fields := frontmatterFields(rest[:end])
			body = rest[end+4:]
			file.name = strings.TrimSpace(fields["name"])
			file.description = fields["description"]
			file.hint = fields["argument-hint"]
			if value, ok := fields["user-invocable"]; ok {
				file.userInvocable = yamlTrue(value, true)
			}
		}
	}
	if strings.TrimSpace(file.description) == "" {
		for line := range strings.SplitSeq(body, "\n") {
			if line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#")); line != "" {
				file.description = line
				break
			}
		}
	}
	return file, true
}

// frontmatterFields reads the top-level "key: value" lines of YAML
// frontmatter, quoted values and folded or literal blocks included. Lists and
// nested maps are not needed for the menu and come back as their raw text.
func frontmatterFields(header string) map[string]string {
	fields := map[string]string{}
	lines := strings.Split(header, "\n")
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		if line == "" || line[0] == ' ' || line[0] == '\t' || line[0] == '#' {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if block := strings.TrimRight(value, "+-"); block == "|" || block == ">" {
			var parts []string
			for i+1 < len(lines) && (lines[i+1] == "" || lines[i+1][0] == ' ' || lines[i+1][0] == '\t') {
				i++
				parts = append(parts, strings.TrimSpace(lines[i]))
			}
			sep := " "
			if block == "|" {
				sep = "\n"
			}
			fields[key] = strings.TrimSpace(strings.Join(parts, sep))
			continue
		}
		fields[key] = unquoteYAML(value)
	}
	return fields
}

func unquoteYAML(value string) string {
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		if unquoted, err := strconv.Unquote(value); err == nil {
			return unquoted
		}
		return value[1 : len(value)-1]
	}
	if len(value) >= 2 && value[0] == '\'' && value[len(value)-1] == '\'' {
		return strings.ReplaceAll(value[1:len(value)-1], "''", "'")
	}
	return value
}

// yamlTrue reads a boolean as Claude Code does: true, yes, on and 1 in any
// letter case, and their opposites.
func yamlTrue(value string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(unquoteYAML(value))) {
	case "true", "yes", "on", "1":
		return true
	case "false", "no", "off", "0":
		return false
	}
	return fallback
}

// readSkillListing reads the newest list of skills Claude wrote into a
// transcript ("skill_listing"), from the start of it, where a session
// writes it with its first turn.
func readSkillListing(transcript string) []protocol.Command {
	if transcript == "" {
		return nil
	}
	f, err := os.Open(transcript)
	if err != nil {
		return nil
	}
	defer f.Close()
	reader := bufio.NewReaderSize(io.LimitReader(f, maxSkillListingScan), 256<<10)
	var listing struct {
		Attachment struct {
			Type    string   `json:"type"`
			Names   []string `json:"names"`
			Content string   `json:"content"`
		} `json:"attachment"`
	}
	var names []string
	var content string
	for {
		line, err := reader.ReadBytes('\n')
		if bytes.Contains(line, []byte(`"skill_listing"`)) && json.Unmarshal(line, &listing) == nil &&
			listing.Attachment.Type == "skill_listing" && len(listing.Attachment.Names) > 0 {
			names, content = listing.Attachment.Names, listing.Attachment.Content
		}
		if err != nil {
			break
		}
	}
	described := map[string]string{}
	for line := range strings.SplitSeq(content, "\n") {
		if entry, ok := strings.CutPrefix(line, "- "); ok {
			name, about, _ := strings.Cut(entry, ": ")
			described[strings.TrimSpace(name)] = strings.TrimSpace(about)
		}
	}
	out := make([]protocol.Command, 0, len(names))
	for _, name := range names {
		source := "bundled"
		if strings.Contains(name, ":") {
			source = "plugin"
		}
		out = append(out, protocol.Command{Name: name, Description: described[name], Source: source})
	}
	return out
}

// validCommandName is a name Claude Code would accept, and one that types
// as itself: no spaces, controls, or angle brackets.
func validCommandName(name string) bool {
	if name == "" || len(name) > 128 || strings.HasPrefix(name, "/") {
		return false
	}
	for _, r := range name {
		if unicode.IsSpace(r) || unicode.IsControl(r) || r == '<' || r == '>' ||
			unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Co, r) {
			return false
		}
	}
	return true
}

func clipCommandText(text string, limit int) string {
	text = strings.Join(strings.Fields(text), " ")
	if runes := []rune(text); len(runes) > limit {
		return string(runes[:limit-1]) + "…"
	}
	return text
}

// The panel a command opens, read off the terminal. A panel hangs from a row
// of "▔" under the conversation, in place of the prompt box, and ends with a
// line saying how to leave it ("Esc to cancel", "Esc to close"). The rows
// /usage needs run past a 24-row pane, so a pane no terminal is showing is
// made tall enough for a moment first.

const (
	panelRows        = 72
	panelColumns     = 110
	panelOpenTimeout = 10 * time.Second
	panelPoll        = 250 * time.Millisecond
	// panelLoadingGrace is how long a part still loading is waited for.
	panelLoadingGrace = 4 * time.Second
)

// errPanelBusy is CommandPanel's answer while Claude is working on a turn
// and the command is one it holds until the turn ends.
var errPanelBusy = errors.New("source: Claude is working; try again when it has finished")

// CommandPanel runs one of claudePanels and answers with what it showed.
func (s *ClaudeSource) CommandPanel(ctx context.Context, sessionID, command string) (protocol.CommandPanel, error) {
	name := strings.TrimPrefix(strings.TrimSpace(command), "/")
	midTurn, ok := claudePanels[name]
	if !ok {
		return protocol.CommandPanel{}, fmt.Errorf("source: /%s does not open a panel Agentman can read", name)
	}
	s.mu.RLock()
	session, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if !ok {
		return protocol.CommandPanel{}, fmt.Errorf("source: unknown claude session %q", sessionID)
	}
	if session.tmuxName == "" {
		return protocol.CommandPanel{}, fmt.Errorf("source: only sessions started with `am claude` can run commands")
	}
	if !midTurn && session.meta.State == protocol.StateBusy {
		return protocol.CommandPanel{}, errPanelBusy
	}
	lock := s.paneLock(session.tmuxName)
	lock.Lock()
	defer lock.Unlock()
	pane, err := s.capturePane(ctx, session.tmuxName)
	if err != nil {
		return protocol.CommandPanel{}, fmt.Errorf("source: could not read the terminal: %w", err)
	}
	if _, ok := claudePromptFooter(pane); !ok {
		return protocol.CommandPanel{}, fmt.Errorf("source: Claude has something open in its terminal; close it first")
	}
	if err := rejectSendIntoLiveQuestion(ctx, s.capturePane, session.tmuxName); err != nil {
		return protocol.CommandPanel{}, err
	}

	// Taller for a moment, unless someone is watching the terminal.
	if width, height, clients, err := tmux.Size(ctx, session.tmuxName); err == nil && clients == 0 && height < panelRows {
		if tmux.Resize(ctx, session.tmuxName, max(width, panelColumns), panelRows) == nil {
			defer func() {
				restore, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				_ = tmux.Unresize(restore, session.tmuxName, width, height)
			}()
		}
	}

	if err := s.typeInto(ctx, session.tmuxName, "/"+name); err != nil {
		return protocol.CommandPanel{}, err
	}
	text, err := s.readPanel(ctx, session.tmuxName)
	if err != nil {
		return protocol.CommandPanel{}, err
	}
	if err := s.closePanel(ctx, session.tmuxName); err != nil {
		return protocol.CommandPanel{}, err
	}
	return protocol.CommandPanel{Command: "/" + name, Text: text}, nil
}

// readPanel waits for the panel to open and settle (/usage scans for a
// moment before it says anything) and reads it.
func (s *ClaudeSource) readPanel(ctx context.Context, tmuxName string) (string, error) {
	deadline := time.Now().Add(panelOpenTimeout)
	var opened time.Time
	last := ""
	for {
		pane, err := s.capturePane(ctx, tmuxName)
		if err != nil {
			return "", err
		}
		text, open := claudePanelText(pane)
		if open && opened.IsZero() {
			opened = time.Now()
		}
		// Settled is the same twice running. A part still loading is given
		// a few seconds more, no longer: the rest is worth having now.
		loading := strings.Contains(text, "Scanning") || strings.Contains(text, "Loading")
		if open && text == last && (!loading || time.Since(opened) > panelLoadingGrace) {
			return text, nil
		}
		if open {
			last = text
		}
		if time.Now().After(deadline) {
			if open {
				// Still changing, but open: say what it says now.
				return text, nil
			}
			// Never opened: nothing to close, and Esc now would stop a turn.
			return "", fmt.Errorf("source: the panel did not open")
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(panelPoll):
		}
	}
}

// closePanel presses Esc until the prompt box is back, three times at most:
// anything typed while a panel is still open goes into the panel.
func (s *ClaudeSource) closePanel(ctx context.Context, tmuxName string) error {
	closeDialog := s.closeDialog
	if closeDialog == nil {
		closeDialog = tmux.Escape
	}
	for range 3 {
		if err := closeDialog(ctx, tmuxName); err != nil {
			return err
		}
		for range 8 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(panelPoll):
			}
			pane, err := s.capturePane(ctx, tmuxName)
			if err != nil {
				return err
			}
			if _, back := claudePromptFooter(pane); back {
				return nil
			}
		}
	}
	return fmt.Errorf("source: the panel did not close; press Esc in the terminal")
}

// claudePanelText is the panel on screen, and whether one is: its lines
// between the "▔" row and the line saying how to leave, the tabs along its
// top left out, and the margin they share taken off.
func claudePanelText(pane string) (string, bool) {
	if _, prompt := claudePromptFooter(pane); prompt {
		return "", false
	}
	lines := strings.Split(strings.TrimRight(pane, "\n"), "\n")
	top := -1
	for index := len(lines) - 1; index >= 0; index-- {
		if strings.Count(lines[index], "▔") >= 20 {
			top = index
			break
		}
	}
	if top < 0 {
		return "", false
	}
	end := -1
	for index := len(lines) - 1; index > top; index-- {
		if strings.Contains(lines[index], "Esc to ") {
			end = index
			break
		}
	}
	if end < 0 {
		return "", false
	}
	body := lines[top+1 : end]
	for len(body) > 0 && strings.TrimSpace(body[0]) == "" {
		body = body[1:]
	}
	if len(body) > 0 && isPanelTabs(body[0]) {
		body = body[1:]
	}
	return strings.Join(dedent(singleBlanks(blockLinesOf(body))), "\n"), true
}

// singleBlanks keeps one blank line where a panel leaves several.
func singleBlanks(lines []string) []string {
	out := lines[:0:0]
	for _, line := range lines {
		if line == "" && len(out) > 0 && out[len(out)-1] == "" {
			continue
		}
		out = append(out, line)
	}
	return out
}

// isPanelTabs is a row of a panel's tab names: "Settings  Status   Config".
func isPanelTabs(line string) bool {
	tabs := 0
	for part := range strings.SplitSeq(strings.TrimSpace(line), "  ") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if len(strings.Fields(part)) > 2 || !unicode.IsUpper([]rune(part)[0]) {
			return false
		}
		tabs++
	}
	return tabs >= 3
}

// blockLinesOf trims the blank lines at either end and each line's end.
func blockLinesOf(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, strings.TrimRight(line, " \t"))
	}
	for len(out) > 0 && out[0] == "" {
		out = out[1:]
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

// dedent takes off the indent every non-blank line shares.
func dedent(lines []string) []string {
	shared := -1
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if shared < 0 || indent < shared {
			shared = indent
		}
	}
	if shared <= 0 {
		return lines
	}
	out := make([]string, len(lines))
	for i, line := range lines {
		if len(line) >= shared {
			out[i] = line[shared:]
		} else {
			out[i] = strings.TrimLeft(line, " ")
		}
	}
	return out
}
