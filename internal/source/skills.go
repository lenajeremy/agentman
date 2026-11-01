package source

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Skills for the agents whose own "/" menus Agentman doesn't list: Cursor's
// Agent CLI, Kiro and OpenCode. Each reads SKILL.md folders from the project
// and from home, in places of its own and ones it shares with other agents
// (checked on Cursor 2026.10.22, Kiro 2.37 and OpenCode 2.2). Their lists
// hold skills only: a skill can be named anywhere in a message, and the
// daemon sends it in the form each agent takes (see skillText).

// skillFolders are where an agent finds skills: folders inside the project,
// from its folder up to the repository's root, and folders in home.
type skillFolders struct {
	project  []string
	personal []string
	bundled  []string
	// byFolder names a skill by its folder rather than its frontmatter's
	// name, as Cursor does.
	byFolder bool
}

var (
	cursorSkillFolders = skillFolders{
		project:  []string{".cursor/skills", ".agents/skills", ".claude/skills", ".codex/skills", ".grok/skills"},
		personal: []string{".cursor/skills", ".agents/skills", ".claude/skills", ".codex/skills", ".grok/skills"},
		bundled:  []string{".cursor/skills-cursor"},
		byFolder: true,
	}
	kiroSkillFolders = skillFolders{
		project:  []string{".kiro/skills"},
		personal: []string{".kiro/skills"},
	}
	openCodeSkillFolders = skillFolders{
		project:  []string{".opencode/skills", ".opencode/skill", ".claude/skills", ".agents/skills"},
		personal: []string{".config/opencode/skills", ".claude/skills", ".agents/skills"},
	}
)

// listSkills lists the skills in an agent's folders, the project's first,
// each name once, by name.
func listSkills(home, cwd string, folders skillFolders) []protocol.Command {
	var found []protocol.Command
	scan := func(dir, source string) {
		for _, c := range scanSkillDir(dir, source, "") {
			if folders.byFolder {
				c.Name = filepath.Base(filepath.Dir(c.Path))
			}
			found = append(found, c)
		}
	}
	if cwd != "" {
		root := repoRoot(cwd)
		for dir := filepath.Clean(cwd); ; dir = filepath.Dir(dir) {
			for _, rel := range folders.project {
				scan(filepath.Join(dir, rel), "project")
			}
			if dir == root || dir == filepath.Dir(dir) {
				break
			}
		}
	}
	if home != "" {
		for _, rel := range folders.personal {
			scan(filepath.Join(home, rel), "personal")
		}
		for _, rel := range folders.bundled {
			scan(filepath.Join(home, rel), "bundled")
		}
	}
	seen := map[string]bool{}
	out := make([]protocol.Command, 0, len(found))
	for _, c := range found {
		key := strings.ToLower(c.Name)
		if !validCommandName(c.Name) || seen[key] || len(out) == maxClaudeCommands {
			continue
		}
		seen[key] = true
		c.Description = clipCommandText(c.Description, maxCommandDescription)
		c.ArgumentHint = clipCommandText(c.ArgumentHint, maxCommandHint)
		out = append(out, c)
	}
	slices.SortStableFunc(out, func(a, b protocol.Command) int {
		return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
	})
	return out
}

var errNoPanels = errors.New("source: this agent has no panel commands")

// Commands lists the skills a Cursor session can be told to use.
func (s *CursorCLISource) Commands(ctx context.Context, sessionID string) ([]protocol.Command, error) {
	s.mu.RLock()
	session, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("source: unknown cursor session %q", sessionID)
	}
	return listSkills(s.home, session.meta.Cwd, cursorSkillFolders), nil
}

// CommandPanel implements CommandSource: Cursor's list is skills only.
func (s *CursorCLISource) CommandPanel(context.Context, string, string) (protocol.CommandPanel, error) {
	return protocol.CommandPanel{}, errNoPanels
}

// Commands lists the skills a Kiro session can be told to use.
func (s *KiroSource) Commands(ctx context.Context, sessionID string) ([]protocol.Command, error) {
	session, err := s.session(sessionID)
	if err != nil {
		return nil, err
	}
	skills := listSkills(s.home, session.meta.Cwd, kiroSkillFolders)
	if st, owned := s.acpOwned(sessionID); owned {
		return append(s.acpCommands(st), skills...), nil
	}
	return skills, nil
}

// CommandPanel implements CommandSource: a session run through Kiro's ACP
// server answers its panel commands (/context, /usage...) as data; one in
// a terminal lists skills only.
func (s *KiroSource) CommandPanel(ctx context.Context, sessionID, command string) (protocol.CommandPanel, error) {
	if st, owned := s.acpOwned(sessionID); owned {
		return s.panelACP(ctx, st, command)
	}
	return protocol.CommandPanel{}, errNoPanels
}

// Commands lists what an OpenCode session can be told to do: OpenCode 2's
// own commands (its built-in /init and /review, and the user's), /compact,
// and the skills. An OpenCode 1 server lists skills only.
func (s *OpenCodeSource) Commands(ctx context.Context, sessionID string) ([]protocol.Command, error) {
	s.mu.RLock()
	session, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("source: unknown opencode session %q", sessionID)
	}
	home, _ := os.UserHomeDir()
	skills := listSkills(home, session.meta.Cwd, openCodeSkillFolders)
	commands, err := s.openCodeCommands(ctx, session)
	if err != nil {
		return skills, nil
	}
	return append(commands, skills...), nil
}

// CommandPanel implements CommandSource: OpenCode's list is skills only.
func (s *OpenCodeSource) CommandPanel(context.Context, string, string) (protocol.CommandPanel, error) {
	return protocol.CommandPanel{}, errNoPanels
}
