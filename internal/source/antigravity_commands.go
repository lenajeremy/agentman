package source

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/question"
	"github.com/lenajeremy/agentman/internal/tmux"
)

// The "/" menu of an Antigravity session: what agy 1.4.8 offers at its own
// prompt that does something sent from another device, its workflows, and
// the skills it loaded.
//
// Left out are the commands that only open a picker or editor in the
// terminal (/config, /hooks, /keybindings, /permissions, /plugin, /resume,
// /rewind, /effort, /artifact, /agents, /credits, /skills; a bare /model is
// the app's own model menu), that leave the terminal or end the session
// (/exit, /logout), that act on the Mac rather than the session (/copy,
// /open, /voice, /title, /statusline, /remote-control), and those whose
// answer only shows over the prompt until it is dismissed (/btw,
// /codesearch).
var antigravityBuiltinCommands = []protocol.Command{
	{Name: "compact", Description: "Summarize the conversation so far to free up context"},
	{Name: "clear", Description: "Clear the conversation and start a new one"},
	{Name: "rename", ArgumentHint: "<name>", Description: "Rename this conversation"},
	{Name: "add-dir", ArgumentHint: "<path>", Description: "Add a folder to the workspace"},
	{Name: "usage", Description: "Show how much of each model's quota is left", Panel: true},
	{Name: "context", Description: "Show how full the context window is", Panel: true},
	{Name: "tasks", Description: "Show background tasks", Panel: true},
	{Name: "mcp", Description: "Show MCP servers and their tools", Panel: true},
	{Name: "diff", Description: "Show the changes in this folder, untracked files too", Panel: true},
}

// antigravityWorkflows are the workflows agy ships, offered in its menu
// beside the commands.
var antigravityWorkflows = []protocol.Command{
	{Name: "goal", ArgumentHint: "<goal>", Description: "Run until the goal is completely finished"},
	{Name: "plan", ArgumentHint: "<task>", Description: "Plan carefully before carrying out a task"},
	{Name: "schedule", ArgumentHint: "<instruction>", Description: "Run an instruction on a schedule, or once on a timer"},
	{Name: "browser", ArgumentHint: "<task>", Description: "Hand a web task to a browser agent"},
	{Name: "grill-me", Description: "Interview me to align on a plan"},
	{Name: "teamwork", ArgumentHint: "<project>", Description: "Have a team of agents take on a large project"},
	{Name: "learn", Description: "Capture what worked, or a correction, as a reusable skill or rule"},
	{Name: "boost", ArgumentHint: "<task>", Description: "Hand a complex task to the Boost multi-agent orchestrator"},
}

// antigravityMenuBuiltinSkills are the skills agy ships that its menu
// offers; the rest it uses on its own.
var antigravityMenuBuiltinSkills = map[string]bool{"agy-customizations": true, "antigravity-guide": true}

// antigravityPanels are the commands read back rather than sent: agy opens
// them in a panel under its prompt, and Esc closes it. /diff is the
// folder's changes, which git gives without agy's own viewer.
var antigravityPanels = map[string]bool{"usage": true, "context": true, "tasks": true, "mcp": true, "diff": true}

const (
	antigravityPanelTimeout = 8 * time.Second
	antigravityPanelPoll    = 150 * time.Millisecond
	// A viewer too long for the pane scrolls a line a key; at most this many
	// keys at a time, and this many times.
	antigravityScrollKeys  = 24
	antigravityScrollTimes = 40
	// agy cuts a viewer's lines off at the pane's edge rather than wrap them.
	antigravityPanelColumns = 200
)

// errAntigravityPanelBusy is CommandPanel's answer while agy is working.
var errAntigravityPanelBusy = errors.New("source: Antigravity is working; try again when it has finished")

// Commands lists a session's "/" menu.
func (s *AntigravitySource) Commands(ctx context.Context, sessionID string) ([]protocol.Command, error) {
	session, err := s.session(sessionID)
	if err != nil {
		return nil, err
	}
	return antigravityCommands(s.home, session.meta.Cwd), nil
}

func antigravityCommands(home, cwd string) []protocol.Command {
	seen := map[string]bool{}
	out := make([]protocol.Command, 0, 96)
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
	for _, c := range antigravityBuiltinCommands {
		c.Source = "builtin"
		add(c)
	}
	for _, c := range antigravityWorkflows {
		c.Source = "bundled"
		add(c)
	}
	// agy lists its skills by name, whichever folder each is in.
	skills := len(out)
	for _, c := range scanAntigravitySkills(home, cwd) {
		add(c)
	}
	slices.SortStableFunc(out[skills:], func(a, b protocol.Command) int {
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return out
}

// scanAntigravitySkills reads the skills agy loads, where its /skills says
// they are: the workspace's, the person's own (agy's and the ones shared
// with other agents), those it ships, and its plugins'. A name found twice
// is the first one's.
func scanAntigravitySkills(home, cwd string) []protocol.Command {
	gemini := filepath.Join(home, ".gemini")
	var out []protocol.Command
	if cwd != "" {
		out = append(out, scanSkillDir(filepath.Join(cwd, ".agents", "skills"), "project", "")...)
	}
	out = append(out, scanSkillDir(filepath.Join(gemini, "antigravity-cli", "skills"), "personal", "")...)
	out = append(out, scanSkillDir(filepath.Join(gemini, "skills"), "personal", "")...)
	for _, c := range scanSkillDir(filepath.Join(gemini, "antigravity-cli", "builtin", "skills"), "bundled", "") {
		if antigravityMenuBuiltinSkills[c.Name] {
			out = append(out, c)
		}
	}
	plugins := filepath.Join(gemini, "config", "plugins")
	entries, err := os.ReadDir(plugins)
	if err != nil {
		return out
	}
	off := antigravityPluginsOff(filepath.Join(gemini, "config", "config.json"))
	for _, entry := range entries {
		if !isDirEntry(plugins, entry) || strings.HasPrefix(entry.Name(), ".") || off[entry.Name()] {
			continue
		}
		out = append(out, scanSkillDir(filepath.Join(plugins, entry.Name(), "skills"), "plugin", entry.Name())...)
	}
	return out
}

// antigravityPluginsOff is the plugins turned off in agy's config:
//
//	{"plugins": {"chrome-devtools-plugin": {"enabled": false}}}
func antigravityPluginsOff(path string) map[string]bool {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var config struct {
		Plugins map[string]struct {
			Enabled *bool `json:"enabled"`
		} `json:"plugins"`
	}
	if json.Unmarshal(raw, &config) != nil {
		return nil
	}
	off := map[string]bool{}
	for name, plugin := range config.Plugins {
		if plugin.Enabled != nil && !*plugin.Enabled {
			off[name] = true
		}
	}
	return off
}

func (s *AntigravitySource) paneLock(tmuxName string) *sync.Mutex {
	lock, _ := s.panes.LoadOrStore(tmuxName, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

// CommandPanel runs one of antigravityPanels and answers with what it showed.
func (s *AntigravitySource) CommandPanel(ctx context.Context, sessionID, command string) (protocol.CommandPanel, error) {
	name := strings.TrimPrefix(strings.TrimSpace(command), "/")
	if !antigravityPanels[name] {
		return protocol.CommandPanel{}, fmt.Errorf("source: /%s is not a command Agentman can read back", name)
	}
	session, err := s.session(sessionID)
	if err != nil {
		return protocol.CommandPanel{}, err
	}
	if name == "diff" {
		text, err := folderDiff(ctx, session.meta.Cwd)
		if err != nil {
			return protocol.CommandPanel{}, err
		}
		return protocol.CommandPanel{Command: "/diff", Text: text}, nil
	}
	if session.tmuxName == "" || s.keys.send == nil {
		return protocol.CommandPanel{}, errors.New("source: only sessions started with `am antigravity` can run commands")
	}
	if session.meta.State == protocol.StateBusy {
		return protocol.CommandPanel{}, errAntigravityPanelBusy
	}
	lock := s.paneLock(session.tmuxName)
	lock.Lock()
	defer lock.Unlock()
	pane, err := s.capturePane(ctx, session.tmuxName)
	if err != nil {
		return protocol.CommandPanel{}, fmt.Errorf("source: could not read the terminal: %w", err)
	}
	if question.DetectAntigravity(pane) != nil {
		return protocol.CommandPanel{}, errors.New("source: answer the pending question first")
	}
	if question.AntigravityPanelOpen(pane) {
		return protocol.CommandPanel{}, errors.New("source: a panel is open in Antigravity on the Mac; close it there first")
	}
	if s.keys.taller != nil {
		defer s.keys.taller(ctx, session.tmuxName)()
	}

	if err := s.keys.typeText(ctx, session.tmuxName, "/"+name); err != nil {
		return protocol.CommandPanel{}, err
	}
	text, err := s.readAntigravityPanel(ctx, session.tmuxName, name)
	if closeErr := s.closeAnyPanel(ctx, session.tmuxName); closeErr != nil && err == nil {
		err = fmt.Errorf("source: /%s is still open on the Mac: %w", name, closeErr)
	}
	if err != nil {
		return protocol.CommandPanel{}, err
	}
	return protocol.CommandPanel{Command: "/" + name, Text: text}, nil
}

// readAntigravityPanel waits for the panel to open and settle and reads it,
// scrolling through a viewer too long for the pane.
func (s *AntigravitySource) readAntigravityPanel(ctx context.Context, tmuxName, name string) (string, error) {
	var view antigravityView
	opened, settled := false, false
	deadline := time.Now().Add(antigravityPanelTimeout)
	for !settled {
		if time.Now().After(deadline) {
			return "", fmt.Errorf("source: /%s did not open", name)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(antigravityPanelPoll):
		}
		pane, err := s.capturePane(ctx, tmuxName)
		if err != nil {
			return "", fmt.Errorf("source: could not read the terminal: %w", err)
		}
		next, ok := readAntigravityView(pane)
		// Settled is the same twice running.
		settled = ok && opened && next.equal(view)
		opened, view = ok, next
	}
	if view.total == 0 {
		return antigravityPanelText(name, view.head), nil
	}

	body := make([]string, view.total)
	copy(body[view.from-1:], view.body)
	shown := view.last()
	for times := 0; shown < view.total; times++ {
		if times == antigravityScrollTimes {
			return "", fmt.Errorf("source: /%s is too long to read", name)
		}
		// No further than what shows, or lines go by unread.
		keys := slices.Repeat([]string{"Down"}, min(antigravityScrollKeys, len(view.body), view.total-shown))
		if err := s.keys.send(ctx, tmuxName, keys...); err != nil {
			return "", err
		}
		moved := false
		for attempt := 0; attempt < 10 && !moved; attempt++ {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(antigravityKeyPause):
			}
			pane, err := s.capturePane(ctx, tmuxName)
			if err != nil {
				return "", fmt.Errorf("source: could not read the terminal: %w", err)
			}
			if next, ok := readAntigravityView(pane); ok && next.total == view.total && next.last() > shown {
				copy(body[next.from-1:], next.body)
				shown, moved = next.last(), true
			}
		}
		if !moved {
			return "", fmt.Errorf("source: /%s did not scroll", name)
		}
	}
	return antigravityPanelText(name, append(view.head, body...)), nil
}

// antigravityView is a panel open under agy's prompt. A viewer too long
// for the pane shows its heading, lines from..last() of its total, and
// says so under them: "(1–17 of 38 lines)".
type antigravityView struct {
	head        []string
	body        []string
	from, total int
}

func (v antigravityView) last() int { return v.from + len(v.body) - 1 }

func (v antigravityView) equal(other antigravityView) bool {
	return v.from == other.from && v.total == other.total &&
		slices.Equal(v.head, other.head) && slices.Equal(v.body, other.body)
}

var (
	// antigravityViewKeys is a panel's own key hints, its last line.
	antigravityViewKeys  = regexp.MustCompile(`^\s*Keyboard: |\besc (?:Close|Exit|Go Back|Cancel)\s*$`)
	antigravityViewRange = regexp.MustCompile(`^\s*\((\d+)–(\d+) of (\d+) lines\)\s*$`)
)

// readAntigravityView reads the panel between the prompt box's lower
// border and the panel's key hints.
func readAntigravityView(pane string) (antigravityView, bool) {
	lines := strings.Split(strings.TrimRight(pane, "\n"), "\n")
	end, seen := -1, 0
	for i := len(lines) - 1; i >= 0 && seen < 3 && end < 0; i-- {
		if strings.TrimSpace(lines[i]) == "" {
			continue
		}
		seen++
		if antigravityViewKeys.MatchString(lines[i]) {
			end = i
		}
	}
	start := -1
	for i := end - 1; i > 0 && start < 0; i-- {
		if isAntigravityRule(strings.TrimRight(lines[i], " ")) && strings.HasPrefix(strings.TrimSpace(lines[i-1]), ">") {
			start = i + 1
		}
	}
	if end < 0 || start < 0 {
		return antigravityView{}, false
	}
	view := lines[start:end]
	marker := len(view) - 1
	for marker >= 0 && strings.TrimSpace(view[marker]) == "" {
		marker--
	}
	if marker < 0 {
		return antigravityView{}, false
	}
	m := antigravityViewRange.FindStringSubmatch(view[marker])
	if m == nil {
		return antigravityView{head: view[:marker+1]}, true
	}
	from, _ := strconv.Atoi(m[1])
	to, _ := strconv.Atoi(m[2])
	total, _ := strconv.Atoi(m[3])
	shown := to - from + 1
	if from < 1 || shown < 1 || to > total || shown > marker {
		return antigravityView{}, false
	}
	return antigravityView{
		head: view[:marker-shown], body: view[marker-shown : marker],
		from: from, total: total,
	}, true
}

// antigravityContextGrid is the run of cells /context draws its usage as,
// beside the words that say the same.
var antigravityContextGrid = regexp.MustCompile(`^(?:[◉□⛁⛀▣■◎●○] ){5,}\s*`)

// antigravityPanelText is a panel's lines as the apps show them: the
// selected row unmarked, the heading's tree mark and /context's grid
// dropped.
func antigravityPanelText(name string, lines []string) string {
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if rest, ok := strings.CutPrefix(line, "> "); ok {
			line = "  " + rest
		}
		if name == "context" {
			if grid := antigravityContextGrid.FindString(line); grid != "" {
				line = strings.TrimPrefix(line, grid)
				if line == "" {
					continue
				}
			}
		}
		out = append(out, line)
	}
	out = dedent(singleBlanks(blockLinesOf(out)))
	if len(out) > 0 {
		out[0] = strings.TrimPrefix(out[0], "└ ")
	}
	return strings.Join(out, "\n")
}

// tallerForPanel makes a pane nobody is watching tall and wide enough for a
// long panel, and returns what puts it back.
func tallerForPanel(ctx context.Context, name string) func() {
	width, height, clients, err := tmux.Size(ctx, name)
	if err != nil || clients > 0 || (height >= panelRows && width >= antigravityPanelColumns) {
		return func() {}
	}
	if tmux.Resize(ctx, name, max(width, antigravityPanelColumns), max(height, panelRows)) != nil {
		return func() {}
	}
	// agy draws again at the new size before anything is typed.
	time.Sleep(300 * time.Millisecond)
	return func() {
		restore, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = tmux.Unresize(restore, name, width, height)
	}
}
