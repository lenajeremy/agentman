package source

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// What Codex 0.162 hands the model at the start of a session, cut down.
const codexSkillsMessage = "## Skills\nA skill is a set of local instructions to follow.\n" +
	"### Skill roots\n" +
	"- `r0` = `/Users/me/.codex/skills`\n" +
	"- `r1` = `/Users/me/.agents/skills`\n" +
	"- `r2` = `/Users/me/.codex/skills/.system`\n" +
	"- `r3` = `/Users/me/.codex/plugins/cache/openai-curated-remote/notion/3.0.2/skills`\n" +
	"### Available skills\n" +
	"- imagegen: Generate or edit raster images. (file: r2/imagegen/SKILL.md)\n" +
	"- apple-design: Apple's approach to interface design: motion, depth. (file: r1/apple-design/SKILL.md)\n" +
	"- notion:notion-setup: Set up Notion for this workspace. (file: r3/notion-setup/SKILL.md)\n" +
	"- shop-deploy: Deploy the shop. (file: /Users/me/code/shop/.agents/skills/shop-deploy/SKILL.md)\n" +
	"- apple-design: Listed twice, as Codex sometimes does. (file: r1/apple-design/SKILL.md)\n" +
	"### How to use skills\n- Discovery: the list above is the skills available in this session.\n"

func writeCodexSkillRollout(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	line, err := json.Marshal(map[string]any{
		"timestamp": "2026-10-27T08:03:18.237Z", "type": "response_item",
		"payload": map[string]any{"type": "message", "role": "developer", "content": []any{
			map[string]any{"type": "input_text", "text": "<permissions instructions>…</permissions instructions>"},
			map[string]any{"type": "input_text", "text": codexSkillsMessage},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	meta := `{"timestamp":"2026-10-27T08:03:16.601Z","type":"session_meta","payload":{"id":"s1","cwd":"/Users/me/code/shop"}}`
	if err := os.WriteFile(path, []byte(meta+"\n"+string(line)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func commandNamed(commands []protocol.Command, prefix, name string) (protocol.Command, bool) {
	for _, c := range commands {
		if c.Prefix == prefix && c.Name == name {
			return c, true
		}
	}
	return protocol.Command{}, false
}

// The skills a session offers are the ones Codex says it loaded, taken with
// "$", and where each comes from.
func TestCodexMenuListsTheSkillsCodexLoaded(t *testing.T) {
	commands := codexCommands(t.TempDir(), "/Users/me/code/shop", writeCodexSkillRollout(t))
	if c, ok := commandNamed(commands, "", "compact"); !ok || c.Source != "builtin" {
		t.Fatalf("compact = %+v", c)
	}
	want := map[string]string{"imagegen": "bundled", "apple-design": "personal", "notion:notion-setup": "plugin", "shop-deploy": "project"}
	for name, source := range want {
		c, ok := commandNamed(commands, "$", name)
		if !ok || c.Source != source || strings.Contains(c.Description, "(file:") {
			t.Errorf("$%s = %+v (found %v), want source %s", name, c, ok, source)
		}
	}
	if c, _ := commandNamed(commands, "$", "apple-design"); c.Description != "Apple's approach to interface design: motion, depth." {
		t.Errorf("description = %q", c.Description)
	}
	seen := 0
	for _, c := range commands {
		if c.Name == "apple-design" {
			seen++
		}
	}
	if seen != 1 {
		t.Errorf("apple-design listed %d times", seen)
	}
	if _, ok := commandNamed(commands, "$", "Discovery"); ok {
		t.Error("read past the list")
	}
}

// Before its first turn a session has no list; the skills are read from
// where Codex loads them.
func TestCodexMenuReadsSkillsFromDiskBeforeAnyTurn(t *testing.T) {
	home := t.TempDir()
	project := filepath.Join(t.TempDir(), "shop")
	skill := func(dir, name, description string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(dir, name), 0o755); err != nil {
			t.Fatal(err)
		}
		body := "---\nname: " + name + "\ndescription: " + description + "\n---\nDo it.\n"
		if err := os.WriteFile(filepath.Join(dir, name, "SKILL.md"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	skill(filepath.Join(home, ".codex", "skills"), "mine", "My own skill")
	skill(filepath.Join(home, ".codex", "skills", ".system"), "imagegen", "Generate images")
	skill(filepath.Join(home, ".agents", "skills"), "shared", "A skill under ~/.agents")
	skill(filepath.Join(project, ".agents", "skills"), "deploy", "Deploy this project")
	if err := os.MkdirAll(filepath.Join(project, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	commands := codexCommands(home, project, "")
	want := map[string]string{"mine": "personal", "imagegen": "bundled", "shared": "personal", "deploy": "project"}
	for name, source := range want {
		if c, ok := commandNamed(commands, "$", name); !ok || c.Source != source {
			t.Errorf("$%s = %+v (found %v), want %s", name, c, ok, source)
		}
	}
}

func TestCodexPanelCommandsAreMarked(t *testing.T) {
	commands := codexCommands(t.TempDir(), "", "")
	for _, name := range []string{"status", "diff", "mcp", "ps"} {
		if c, ok := commandNamed(commands, "", name); !ok || !c.Panel {
			t.Errorf("/%s = %+v", name, c)
		}
	}
	for _, name := range []string{"exit", "logout", "permissions", "resume", "copy", "delete"} {
		if _, ok := commandNamed(commands, "", name); ok {
			t.Errorf("/%s is offered", name)
		}
	}
}

// Codex 0.162's /status, as it prints it into the conversation.
const codexStatusPrinted = `/status

  >_ OpenAI Codex (v0.162.1)

  Visit https://chatgpt.com/settings/usage for up-to-date
  information on rate limits and credits

  Model:               GPT-6.1-Sol (reasoning low)
  Directory:           /Users/me/code/shop
  Token usage:         0 total  (0 input + 0 output)
`

func TestCodexPanelTextIsWhatTheCommandPrinted(t *testing.T) {
	before := "› Ask Codex to do anything\n\n  GPT-6.1-Sol default · ~/code/shop\n"
	after := codexStatusPrinted + "\n› Ask Codex to do anything\n\n  GPT-6.1-Sol default · ~/code/shop\n"
	text, ok := codexPanelText(before, after, "/status")
	want := ">_ OpenAI Codex (v0.162.1)\n\nVisit https://chatgpt.com/settings/usage for up-to-date\n" +
		"information on rate limits and credits\n\nModel:               GPT-6.1-Sol (reasoning low)\n" +
		"Directory:           /Users/me/code/shop\nToken usage:         0 total  (0 input + 0 output)"
	if !ok || text != want {
		t.Fatalf("ok %v, text:\n%s", ok, text)
	}
}

// Run again, the last output is the new one, and not before it is there.
func TestCodexPanelTextWaitsForANewEcho(t *testing.T) {
	before := codexStatusPrinted + "\n› Ask Codex to do anything\n"
	if _, ok := codexPanelText(before, before, "/status"); ok {
		t.Fatal("the earlier output was taken for the new one")
	}
	after := strings.Replace(codexStatusPrinted, "0 total", "10 total", 1)
	after = codexStatusPrinted + "\n" + after + "\n• Explored\n  └ Read main.go\n› Ask Codex to do anything\n"
	text, ok := codexPanelText(before, after, "/status")
	if !ok || !strings.Contains(text, "10 total") || strings.Contains(text, "Explored") {
		t.Fatalf("ok %v, text:\n%s", ok, text)
	}
}

func TestCodexPanelTextStopsAtATurnsStatus(t *testing.T) {
	after := "/mcp\n\n🔌  MCP Tools\n\n  • codex_tui: connected (12 tools)\n\n" +
		"◦ Working (3s • esc to interrupt)\n› Ask Codex to do anything\n"
	text, ok := codexPanelText("", after, "/mcp")
	if !ok || text != "🔌  MCP Tools\n\n  • codex_tui: connected (12 tools)" {
		t.Fatalf("ok %v, text:\n%q", ok, text)
	}
}

func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=t@example.com", "-c", "user.name=t"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	run("init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-qm", "init")
	return dir
}

func TestCodexDiffShowsChangesAndNewFiles(t *testing.T) {
	dir := gitRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.md"), []byte("# Notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	text, err := folderDiff(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--- a/main.go", "+func main() {}", "+++ b/notes.md", "+# Notes"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
}

func TestCodexDiffOfAQuietFolder(t *testing.T) {
	if text, err := folderDiff(context.Background(), gitRepo(t)); err != nil || text != "No changes." {
		t.Fatalf("%q, %v", text, err)
	}
	if _, err := folderDiff(context.Background(), t.TempDir()); err == nil || !strings.Contains(err.Error(), "not in a git repository") {
		t.Fatalf("outside git: %v", err)
	}
}

func TestCodexPanelRefusedWhileWorking(t *testing.T) {
	s := &CodexSource{sessions: map[string]codexSession{"codex:s1": {
		tmuxName: "agentman-codex-x", meta: protocol.Session{ID: "codex:s1", State: protocol.StateBusy},
	}}}
	if _, err := s.CommandPanel(context.Background(), "codex:s1", "/status"); err != errCodexPanelBusy {
		t.Fatalf("err = %v", err)
	}
	if _, err := s.CommandPanel(context.Background(), "codex:s1", "/exit"); err == nil {
		t.Fatal("ran a command that is not read back")
	}
}
