package source

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
)

func skill(name, description string) string {
	return "---\nname: " + name + "\ndescription: " + description + "\n---\nDo it.\n"
}

func names(commands []protocol.Command) string {
	var out []string
	for _, c := range commands {
		out = append(out, c.Name+"("+c.Source+")")
	}
	return strings.Join(out, " ")
}

// Cursor finds skills in its own folders and the ones it shares with other
// agents, in the project and in home, and names each by its folder.
func TestCursorListsSkillsByFolder(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(project, ".cursor/skills/tidy/SKILL.md"), skill("tidy-up", "Tidy the code"))
	writeFile(t, filepath.Join(project, ".claude/skills/review/SKILL.md"), skill("review", "Review it"))
	writeFile(t, filepath.Join(home, ".agents/skills/deploy/SKILL.md"), skill("deploy", "Ship it"))
	writeFile(t, filepath.Join(home, ".cursor/skills-cursor/canvas/SKILL.md"), skill("canvas", "Draw"))
	writeFile(t, filepath.Join(home, ".cursor/skills/review/SKILL.md"), skill("review", "The person's own"))
	got := listSkills(home, project, cursorSkillFolders)
	if names(got) != "canvas(bundled) deploy(personal) review(project) tidy(project)" {
		t.Fatalf("skills = %s", names(got))
	}
	for _, c := range got {
		if !c.Skill || !strings.HasSuffix(c.Path, filepath.Join(c.Name, "SKILL.md")) {
			t.Errorf("%s: skill %v at %q", c.Name, c.Skill, c.Path)
		}
	}
}

// Kiro reads .kiro/skills in the project and home; OpenCode its own
// folders, and Claude's and the shared ones.
func TestKiroAndOpenCodeListTheirSkills(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(project, ".kiro/skills/spec/SKILL.md"), skill("spec", "Write a spec"))
	writeFile(t, filepath.Join(home, ".kiro/skills/notes/SKILL.md"), skill("notes", "Take notes"))
	writeFile(t, filepath.Join(project, ".opencode/skill/lint/SKILL.md"), skill("lint", "Lint"))
	writeFile(t, filepath.Join(home, ".config/opencode/skills/plan/SKILL.md"), skill("plan", "Plan it"))
	writeFile(t, filepath.Join(home, ".claude/skills/review/SKILL.md"), skill("review", "Review it"))
	if got := names(listSkills(home, project, kiroSkillFolders)); got != "notes(personal) spec(project)" {
		t.Errorf("Kiro = %s", got)
	}
	if got := names(listSkills(home, project, openCodeSkillFolders)); got != "lint(project) plan(personal) review(personal)" {
		t.Errorf("OpenCode = %s", got)
	}
}

// Claude's and Codex's menus mark their skills, with the file each is in;
// commands aren't skills.
func TestSkillsAreMarkedWithTheirFiles(t *testing.T) {
	home, project := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(home, ".claude/skills/frontend-design/SKILL.md"), skill("frontend-design", "Design UI"))
	writeFile(t, filepath.Join(home, ".claude/commands/standup.md"), "Write my standup.\n")
	writeFile(t, filepath.Join(project, ".codex/skills/probe/SKILL.md"), skill("probe", "Probe"))
	byName := map[string]protocol.Command{}
	for _, c := range claudeCommands(home, project, "") {
		byName[c.Name] = c
	}
	if c := byName["frontend-design"]; !c.Skill || c.Path != filepath.Join(home, ".claude/skills/frontend-design/SKILL.md") {
		t.Errorf("frontend-design = %+v", c)
	}
	if c := byName["standup"]; c.Skill {
		t.Errorf("a command file read as a skill: %+v", c)
	}
	if c := byName["compact"]; c.Skill {
		t.Errorf("a built-in command read as a skill: %+v", c)
	}
	if c := byName["simplify"]; !c.Skill {
		t.Errorf("a bundled skill not marked: %+v", c)
	}
	var probe protocol.Command
	for _, c := range codexCommands(home, project, "") {
		if c.Name == "probe" {
			probe = c
		}
	}
	if !probe.Skill || probe.Prefix != "$" || !strings.HasSuffix(probe.Path, ".codex/skills/probe/SKILL.md") {
		t.Errorf("Codex's project skill = %+v", probe)
	}
	listed := codexSkillListing("### Skill roots\n- `r0` = `/x/skills`\n### Available skills\n- imagegen: Make images (file: r0/imagegen/SKILL.md)\n", project)
	if len(listed) != 1 || !listed[0].Skill || listed[0].Path != "/x/skills/imagegen/SKILL.md" {
		t.Errorf("Codex's listing = %+v", listed)
	}
}
