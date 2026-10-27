package source

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/tmux"
)

// The "/" menu of a Codex session: what Codex 0.162 offers at its own prompt
// that does something sent from another device, and the skills it loaded,
// which are typed with "$" rather than "/".
//
// Left out are the commands that only open a picker or editor in the
// terminal (/permissions, /resume, /theme; a bare /model is the app's own
// model menu), that leave the terminal or end the session (/exit, /logout,
// /app, /archive, /delete), and that act on the Mac rather than the session
// (/copy, /ide, /voice).
var codexBuiltinCommands = []protocol.Command{
	{Name: "compact", Description: "Summarize the conversation so far to free up context"},
	{Name: "review", ArgumentHint: "<instructions>", Description: "Review the current changes for issues, told what to look at"},
	{Name: "init", Description: "Write an AGENTS.md guide for this project"},
	{Name: "goal", ArgumentHint: "<objective>", Description: "Set a goal to keep working towards"},
	{Name: "plan", Description: "Switch to Plan mode"},
	{Name: "recap", Description: "Summarize the conversation now"},
	{Name: "rename", ArgumentHint: "<name>", Description: "Rename this conversation"},
	{Name: "new", Description: "Start a new conversation"},
	{Name: "model", Description: "Choose the model and how hard it thinks"},
	{Name: "status", Description: "Show the model, permissions, account and token usage", Panel: true},
	{Name: "diff", Description: "Show the changes in this folder, untracked files too", Panel: true},
	{Name: "mcp", Description: "Show MCP servers and their tools", Panel: true},
	{Name: "ps", Description: "Show the terminals running in the background", Panel: true},
	{Name: "stop", Description: "Stop every terminal running in the background"},
}

// codexPanels are the commands read back rather than sent. Codex prints
// /status, /mcp and /ps into its conversation under the command, and /diff
// is the folder's changes, which git gives without Codex's full-screen view.
var codexPanels = map[string]bool{"status": true, "mcp": true, "ps": true, "diff": true}

const (
	maxCodexSkillScan int64 = 8 << 20
	codexPanelTimeout       = 8 * time.Second
	// Lines of the pane's history searched for a command's output.
	codexPanelHistory = 400
	maxCodexDiffLines = 4000
	maxCodexDiffFiles = 50
)

// errCodexPanelBusy is CommandPanel's answer while Codex is working: what it
// prints then can land among the turn's own lines.
var errCodexPanelBusy = errors.New("source: Codex is working; try again when it has finished")

// Commands lists a session's "/" menu.
func (s *CodexSource) Commands(ctx context.Context, sessionID string) ([]protocol.Command, error) {
	s.mu.RLock()
	session, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("source: unknown codex session %q", sessionID)
	}
	return codexCommands(s.home, session.meta.Cwd, session.transcript), nil
}

func codexCommands(home, cwd, transcript string) []protocol.Command {
	seen := map[string]bool{}
	out := make([]protocol.Command, 0, 96)
	add := func(c protocol.Command) {
		key := c.Prefix + strings.ToLower(c.Name)
		if !validCommandName(c.Name) || seen[key] || len(out) == maxClaudeCommands {
			return
		}
		seen[key] = true
		c.Description = clipCommandText(c.Description, maxCommandDescription)
		c.ArgumentHint = clipCommandText(c.ArgumentHint, maxCommandHint)
		out = append(out, c)
	}
	for _, c := range codexBuiltinCommands {
		c.Source = "builtin"
		add(c)
	}
	// The skills Codex itself listed for the session are the ones it will
	// take; a session that has not started one yet has those on disk.
	skills := readCodexSkillListing(transcript, cwd)
	if len(skills) == 0 {
		skills = scanCodexSkills(home, cwd)
	}
	for _, c := range skills {
		c.Prefix = "$"
		add(c)
	}
	return out
}

// scanCodexSkills reads the skills Codex loads from disk: its own, the
// ones it ships, the person's under ~/.agents, and the project's.
func scanCodexSkills(home, cwd string) []protocol.Command {
	codexHome := filepath.Join(home, ".codex")
	var out []protocol.Command
	if cwd != "" {
		root := repoRoot(cwd)
		for dir := filepath.Clean(cwd); ; dir = filepath.Dir(dir) {
			out = append(out, scanSkillDir(filepath.Join(dir, ".agents", "skills"), "project", "")...)
			if dir == root || dir == filepath.Dir(dir) {
				break
			}
		}
	}
	out = append(out, scanSkillDir(filepath.Join(codexHome, "skills"), "personal", "")...)
	out = append(out, scanSkillDir(filepath.Join(home, ".agents", "skills"), "personal", "")...)
	out = append(out, scanSkillDir(filepath.Join(codexHome, "skills", ".system"), "bundled", "")...)
	return out
}

// Codex hands the model the skills it loaded at the start of a session, in
// a developer message: a table of the folders they are in, then a line a
// skill.
//
//	### Skill roots
//	- `r2` = `/Users/me/.codex/skills/.system`
//	### Available skills
//	- imagegen: Generate or edit raster images ... (file: r2/imagegen/SKILL.md)
var (
	codexSkillRoot  = regexp.MustCompile("^- `([^`]+)` = `([^`]+)`$")
	codexSkillEntry = regexp.MustCompile(`^- (\S+): (.*)$`)
	codexSkillFile  = regexp.MustCompile(`\s*\(file: ([^)]*)\)$`)
)

// readCodexSkillListing reads the skills a rollout says Codex loaded.
func readCodexSkillListing(transcript, cwd string) []protocol.Command {
	if transcript == "" {
		return nil
	}
	file, err := os.Open(transcript)
	if err != nil {
		return nil
	}
	defer file.Close()
	scanner := bufio.NewScanner(io.LimitReader(file, maxCodexSkillScan))
	scanner.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for scanner.Scan() {
		line := scanner.Bytes()
		if !strings.Contains(string(line), "### Available skills") {
			continue
		}
		var record struct {
			Type    string `json:"type"`
			Payload struct {
				Type    string `json:"type"`
				Role    string `json:"role"`
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &record) != nil || record.Type != "response_item" || record.Payload.Role != "developer" {
			continue
		}
		var text strings.Builder
		for _, block := range record.Payload.Content {
			text.WriteString(block.Text)
		}
		if skills := codexSkillListing(text.String(), cwd); len(skills) > 0 {
			return skills
		}
	}
	return nil
}

// codexSkillListing reads the skills section of Codex's instructions.
func codexSkillListing(text, cwd string) []protocol.Command {
	roots := map[string]string{}
	var out []protocol.Command
	listing := false
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimSpace(line)
		if m := codexSkillRoot.FindStringSubmatch(line); m != nil {
			roots[m[1]] = m[2]
			continue
		}
		if line == "### Available skills" {
			listing = true
			continue
		}
		if !listing {
			continue
		}
		m := codexSkillEntry.FindStringSubmatch(line)
		if m == nil {
			if len(out) > 0 {
				break
			}
			continue
		}
		description, path := m[2], ""
		if f := codexSkillFile.FindStringSubmatch(description); f != nil {
			description = strings.TrimSpace(description[:len(description)-len(f[0])])
			path = f[1]
			if alias, rest, ok := strings.Cut(path, "/"); ok && roots[alias] != "" {
				path = filepath.Join(roots[alias], rest)
			}
		}
		out = append(out, protocol.Command{Name: m[1], Description: description, Source: codexSkillSource(m[1], path, cwd)})
	}
	return out
}

// codexSkillSource says where a skill comes from, as the menu's other
// entries do.
func codexSkillSource(name, path, cwd string) string {
	switch {
	case strings.Contains(path, "/skills/.system/"):
		return "bundled"
	case strings.Contains(path, "/plugins/") || strings.Contains(name, ":"):
		return "plugin"
	case cwd != "" && strings.HasPrefix(path, repoRoot(cwd)+string(filepath.Separator)):
		return "project"
	}
	return "personal"
}

// paneLock serializes what is typed into a pane with reading it back.
func (s *CodexSource) paneLock(tmuxName string) *sync.Mutex {
	lock, _ := s.panes.LoadOrStore(tmuxName, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

// CommandPanel runs one of codexPanels and answers with what it showed.
func (s *CodexSource) CommandPanel(ctx context.Context, sessionID, command string) (protocol.CommandPanel, error) {
	name := strings.TrimPrefix(strings.TrimSpace(command), "/")
	if !codexPanels[name] {
		return protocol.CommandPanel{}, fmt.Errorf("source: /%s is not a command Agentman can read back", name)
	}
	s.mu.RLock()
	session, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if !ok {
		return protocol.CommandPanel{}, fmt.Errorf("source: unknown codex session %q", sessionID)
	}
	if name == "diff" {
		text, err := folderDiff(ctx, session.meta.Cwd)
		if err != nil {
			return protocol.CommandPanel{}, err
		}
		return protocol.CommandPanel{Command: "/diff", Text: text}, nil
	}
	if session.tmuxName == "" {
		return protocol.CommandPanel{}, fmt.Errorf("source: only sessions started with `am codex` can run commands")
	}
	if session.meta.State == protocol.StateBusy {
		return protocol.CommandPanel{}, errCodexPanelBusy
	}
	lock := s.paneLock(session.tmuxName)
	lock.Lock()
	defer lock.Unlock()
	if err := rejectSendIntoLiveQuestion(ctx, s.capturePane, session.tmuxName); err != nil {
		return protocol.CommandPanel{}, err
	}

	// Wider for a moment, unless someone is watching the terminal: Codex
	// wraps what it prints at the width it has.
	if width, height, clients, err := tmux.Size(ctx, session.tmuxName); err == nil && clients == 0 && width < panelColumns {
		if tmux.Resize(ctx, session.tmuxName, panelColumns, max(height, 40)) == nil {
			defer func() {
				restore, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				defer cancel()
				_ = tmux.Unresize(restore, session.tmuxName, width, height)
			}()
			// Codex draws again at the new size before anything is typed.
			time.Sleep(300 * time.Millisecond)
		}
	}

	before, err := tmux.CaptureScrollback(ctx, session.tmuxName, codexPanelHistory)
	if err != nil {
		return protocol.CommandPanel{}, fmt.Errorf("source: could not read the terminal: %w", err)
	}
	echo := "/" + name
	if err := tmux.Send(ctx, session.tmuxName, echo); err != nil {
		return protocol.CommandPanel{}, err
	}
	deadline := time.Now().Add(codexPanelTimeout)
	last := ""
	for {
		select {
		case <-ctx.Done():
			return protocol.CommandPanel{}, ctx.Err()
		case <-time.After(panelPoll):
		}
		after, err := tmux.CaptureScrollback(ctx, session.tmuxName, codexPanelHistory)
		if err != nil {
			return protocol.CommandPanel{}, fmt.Errorf("source: could not read the terminal: %w", err)
		}
		text, ok := codexPanelText(before, after, echo)
		// Settled is the same twice running.
		if ok && text != "" && text == last {
			return protocol.CommandPanel{Command: echo, Text: text}, nil
		}
		last = text
		if time.Now().After(deadline) {
			if ok && text != "" {
				return protocol.CommandPanel{Command: echo, Text: text}, nil
			}
			return protocol.CommandPanel{}, fmt.Errorf("source: Codex did not show %s", echo)
		}
	}
}

// codexPanelText is what a command printed, read off the pane: the lines
// under the last echo of the command, up to the composer, a turn's status or
// whatever Codex printed next. ok is false until a new echo is there.
func codexPanelText(before, after, echo string) (string, bool) {
	lines := strings.Split(strings.TrimRight(after, "\n"), "\n")
	echoes := func(pane []string) (last, count int) {
		last = -1
		for i, line := range pane {
			if strings.TrimRight(line, " ") == echo {
				last, count = i, count+1
			}
		}
		return last, count
	}
	at, count := echoes(lines)
	if _, earlier := echoes(strings.Split(before, "\n")); at < 0 || count <= earlier {
		return "", false
	}
	var out []string
	for _, line := range lines[at+1:] {
		if codexPanelEnds(line) {
			break
		}
		out = append(out, line)
	}
	return strings.Join(dedent(singleBlanks(blockLinesOf(out))), "\n"), true
}

// codexPanelEnds reports whether a line is past what a command printed: the
// composer, another entry of the conversation, or a turn's status. What the
// commands print is indented under their heading.
func codexPanelEnds(line string) bool {
	if strings.Contains(line, "esc to interrupt") {
		return true
	}
	for _, mark := range []string{"›", "•", "◦", "■", "/"} {
		if strings.HasPrefix(line, mark) {
			return true
		}
	}
	return false
}

// folderDiff is what Codex's and agy's /diff show: the folder's changes since
// its last commit, and the files git does not track yet as new files.
func folderDiff(ctx context.Context, cwd string) (string, error) {
	git := func(args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", cwd, "--no-pager", "-c", "core.quotepath=off"}, args...)...)
		out, err := cmd.Output()
		// --no-index exits 1 when the two sides differ, which is the point.
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 && len(out) > 0 {
			err = nil
		}
		return string(out), err
	}
	if cwd == "" {
		return "", errors.New("source: this session has no folder")
	}
	if _, err := git("rev-parse", "--is-inside-work-tree"); err != nil {
		return "", errors.New("source: this folder is not in a git repository")
	}
	tracked, err := git("diff", "HEAD", "--no-color", "--no-ext-diff")
	if err != nil {
		// A repository with no commit yet has nothing to compare to but the
		// index.
		if tracked, err = git("diff", "--no-color", "--no-ext-diff"); err != nil {
			return "", fmt.Errorf("source: git could not read the changes: %w", err)
		}
	}
	parts := []string{strings.TrimRight(tracked, "\n")}
	untracked, err := git("ls-files", "--others", "--exclude-standard", "-z")
	if err == nil {
		files := strings.Split(strings.TrimRight(untracked, "\x00"), "\x00")
		for i, file := range files {
			if file == "" {
				continue
			}
			if i == maxCodexDiffFiles {
				parts = append(parts, fmt.Sprintf("… %d more new files", len(files)-i))
				break
			}
			if diff, err := git("diff", "--no-index", "--no-color", "--no-ext-diff", "--", "/dev/null", file); err == nil {
				parts = append(parts, strings.TrimRight(diff, "\n"))
			}
		}
	}
	text := strings.TrimSpace(strings.Join(parts, "\n"))
	if text == "" {
		return "No changes.", nil
	}
	lines := strings.Split(text, "\n")
	if len(lines) > maxCodexDiffLines {
		text = strings.Join(lines[:maxCodexDiffLines], "\n") + fmt.Sprintf("\n… %d more lines", len(lines)-maxCodexDiffLines)
	}
	return text, nil
}
