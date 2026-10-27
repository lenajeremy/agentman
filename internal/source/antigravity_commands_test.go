package source

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/tmux"
)

func writeSkill(t *testing.T, dir, folder, frontmatter string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, folder), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "---\n" + frontmatter + "\n---\nDo it.\n"
	if err := os.WriteFile(filepath.Join(dir, folder, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The skills agy 1.4.8's /skills says it loads, from where it says: the
// workspace's, agy's own, the shared folder (links, here), the two it ships
// that its menu offers, and the plugins it has on.
func TestAntigravityMenuListsTheSkillsAgyLoads(t *testing.T) {
	home := t.TempDir()
	project := filepath.Join(t.TempDir(), "shop")
	gemini := filepath.Join(home, ".gemini")
	installed := filepath.Join(t.TempDir(), "installed")

	writeSkill(t, filepath.Join(project, ".agents", "skills"), "deploy", "name: deploy\ndescription: Deploy this project")
	writeSkill(t, filepath.Join(gemini, "antigravity-cli", "skills"), "mine", "name: mine\ndescription: My own skill")
	writeSkill(t, installed, "hyperframes", "name: hyperframes\ndescription: Make videos")
	if err := os.MkdirAll(filepath.Join(gemini, "skills"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(installed, "hyperframes"), filepath.Join(gemini, "skills", "hyperframes")); err != nil {
		t.Fatal(err)
	}
	builtin := filepath.Join(gemini, "antigravity-cli", "builtin", "skills")
	writeSkill(t, builtin, "antigravity_guide", "name: antigravity-guide\ndescription: A guide to Antigravity")
	writeSkill(t, builtin, "automation", "name: automation\ndescription: Used by /schedule")
	plugins := filepath.Join(gemini, "config", "plugins")
	writeSkill(t, filepath.Join(plugins, "chrome-devtools-plugin", "skills"), "a11y-debugging", "name: a11y-debugging\ndescription: Audit accessibility")
	writeSkill(t, filepath.Join(plugins, "off-plugin", "skills"), "hidden", "name: hidden\ndescription: From a plugin turned off")
	config := `{"plugins": {"chrome-devtools-plugin": {"enabled": true}, "off-plugin": {"enabled": false}}}`
	if err := os.WriteFile(filepath.Join(gemini, "config", "config.json"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}

	commands := antigravityCommands(home, project)
	want := map[string]string{
		"deploy": "project", "mine": "personal", "hyperframes": "personal",
		"antigravity-guide": "bundled", "chrome-devtools-plugin:a11y-debugging": "plugin",
		"usage": "builtin", "goal": "bundled",
	}
	for name, source := range want {
		if c, ok := commandNamed(commands, "", name); !ok || c.Source != source {
			t.Errorf("/%s = %+v (found %v), want source %s", name, c, ok, source)
		}
	}
	for _, name := range []string{"automation", "off-plugin:hidden", "exit", "config", "btw", "model"} {
		if _, ok := commandNamed(commands, "", name); ok {
			t.Errorf("/%s is offered", name)
		}
	}
	// Commands first, as agy lists them, then the skills by name.
	var skills []string
	for _, c := range commands[len(antigravityBuiltinCommands)+len(antigravityWorkflows):] {
		skills = append(skills, c.Name)
	}
	if got := strings.Join(skills, " "); got != "antigravity-guide chrome-devtools-plugin:a11y-debugging deploy hyperframes mine" {
		t.Errorf("skills in order: %s", got)
	}
}

func agyPanelText(t *testing.T, fixture, name string) string {
	t.Helper()
	view, ok := readAntigravityView(agyFixture(t, fixture))
	if !ok || view.total != 0 {
		t.Fatalf("%s: ok %v, view %+v", fixture, ok, view)
	}
	return antigravityPanelText(name, view.head)
}

func TestAntigravityPanelsReadAsText(t *testing.T) {
	usage := agyPanelText(t, "usage", "usage")
	if !strings.HasPrefix(usage, "Models & Quota\n\n  Account: me@example.com\n\nGEMINI MODELS") ||
		!strings.Contains(usage, "Refreshes in") || strings.Contains(usage, "esc Close") || strings.Contains(usage, "for shortcuts") {
		t.Errorf("/usage:\n%s", usage)
	}

	context := agyPanelText(t, "context", "context")
	if !strings.HasPrefix(context, "Context Usage\nClaude Opus 5.5 (High) · 18.0k/1M tokens\n(1.8%)") ||
		strings.Contains(context, "□ □") || !strings.Contains(context, "\n◉ User messages: 0 tokens (0.0%)\n") {
		t.Errorf("/context:\n%s", context)
	}

	if tasks := agyPanelText(t, "tasks", "tasks"); tasks != "Tasks\n  No background tasks." {
		t.Errorf("/tasks: %q", tasks)
	}
	mcp := agyPanelText(t, "mcp", "mcp")
	if !strings.HasPrefix(mcp, "MCP Servers") || !strings.Contains(mcp, "\n   ✓ agentman  Tools: search_notes") ||
		strings.Contains(mcp, "Keyboard:") {
		t.Errorf("/mcp:\n%s", mcp)
	}
}

// A viewer longer than the pane says which of its lines show, under them.
func TestAntigravityViewerSaysWhichLinesShow(t *testing.T) {
	top, ok := readAntigravityView(agyFixture(t, "usage-top"))
	if !ok || top.from != 1 || top.last() != 17 || top.total != 38 ||
		strings.Join(top.head, "|") != "└ Models & Quota||  Account: me@example.com|" ||
		strings.TrimSpace(top.body[0]) != "GEMINI MODELS" {
		t.Fatalf("top: ok %v %+v", ok, top)
	}
	scrolled, ok := readAntigravityView(agyFixture(t, "usage-scrolled"))
	if !ok || scrolled.from != 11 || scrolled.last() != 27 || len(scrolled.head) != len(top.head) {
		t.Fatalf("scrolled: ok %v %+v", ok, scrolled)
	}
	if _, ok := readAntigravityView(agyFixture(t, "idle-after-reply")); ok {
		t.Fatal("read a panel off the bare prompt")
	}
}

// agyViewer stands in for agy with a long viewer open under its prompt:
// Down scrolls it a line, Escape closes it.
type agyViewer struct {
	lines, shows int
	open         bool
	top          int
	events       []string
}

func (v *agyViewer) pane(context.Context, string) (string, error) {
	rule := strings.Repeat("─", 60)
	out := []string{"> earlier message", "  earlier reply", rule, ">", rule}
	if !v.open {
		return strings.Join(append(out, "? for shortcuts                     Model · high"), "\n"), nil
	}
	out = append(out, "└ Long Panel", "")
	for i := v.top; i < v.top+v.shows; i++ {
		out = append(out, fmt.Sprintf("  line %d", i+1))
	}
	out = append(out, fmt.Sprintf("  (%d–%d of %d lines)", v.top+1, v.top+v.shows, v.lines), "",
		"  ↑/↓ Scroll · pgup/pgdown Page · ctrl+end Bottom · ctrl+home Top · esc Close",
		"                                    Model · high")
	return strings.Join(out, "\n"), nil
}

func (v *agyViewer) keys() antigravityKeys {
	return antigravityKeys{
		typeText: func(_ context.Context, _, text string) error {
			v.events = append(v.events, "type "+text)
			v.open = true
			return nil
		},
		send: func(_ context.Context, _ string, keys ...string) error {
			v.events = append(v.events, fmt.Sprintf("%s×%d", keys[0], len(keys)))
			for _, key := range keys {
				switch key {
				case "Down":
					v.top = min(v.top+1, v.lines-v.shows)
				case "Escape":
					v.open = false
				}
			}
			return nil
		},
	}
}

func agyPanelSession(t *testing.T, capture func(context.Context, string) (string, error), keys antigravityKeys) (*AntigravitySource, string) {
	t.Helper()
	home := t.TempDir()
	writeAntigravityConversation(t, home, agConversation, agLinePrompt, agLineReply)
	pane := tmux.Session{Name: "agentman-antigravity-1-a", PanePID: 900, Cwd: "/work/ws1"}
	s := newTestAntigravity(t, home, 900, map[int]antigravityProcess{
		900: {cwd: "/work/ws1", conversations: []string{agConversation}},
	}, pane)
	idle := &agyKeys{panes: []string{agyFixture(t, "idle-after-reply")}}
	s.capturePane = idle.capture
	s.keys = idle.keys(false)
	discoverAntigravity(t, s)
	s.capturePane, s.keys = capture, keys
	return s, "antigravity:tmux-agentman-antigravity-1-a"
}

// Scrolled through to its end and read whole, then closed.
func TestAntigravityPanelScrollsThroughALongViewer(t *testing.T) {
	viewer := &agyViewer{lines: 60, shows: 10}
	s, id := agyPanelSession(t, viewer.pane, viewer.keys())
	panel, err := s.CommandPanel(context.Background(), id, "/usage")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Long Panel", ""}
	for i := 1; i <= 60; i++ {
		want = append(want, fmt.Sprintf("  line %d", i))
	}
	if panel.Command != "/usage" || panel.Text != strings.Join(want, "\n") {
		t.Errorf("panel %q:\n%s", panel.Command, panel.Text)
	}
	if viewer.open {
		t.Error("left the viewer open")
	}
	if got := strings.Join(viewer.events, " "); got != "type /usage Down×10 Down×10 Down×10 Down×10 Down×10 Escape×1" {
		t.Errorf("keys: %s", got)
	}
}

func TestAntigravityPanelRefusals(t *testing.T) {
	viewer := &agyViewer{lines: 5, shows: 5}
	s, id := agyPanelSession(t, viewer.pane, viewer.keys())
	if _, err := s.CommandPanel(context.Background(), id, "/exit"); err == nil {
		t.Error("ran a command that is not read back")
	}
	question := &agyKeys{panes: []string{agyFixture(t, "file-access")}}
	s.capturePane = question.capture
	if _, err := s.CommandPanel(context.Background(), id, "/usage"); err == nil || len(viewer.events) > 0 {
		t.Errorf("typed over a question: %v %q", err, viewer.events)
	}
	s.mu.Lock()
	session := s.sessions[id]
	session.meta.State = protocol.StateBusy
	s.sessions[id] = session
	s.mu.Unlock()
	if _, err := s.CommandPanel(context.Background(), id, "/usage"); err != errAntigravityPanelBusy {
		t.Errorf("busy: %v", err)
	}
}

// A call cancelled at its permission question gets no result (agy 1.4.8).
// Once agy is idle and the transcript quiet, the turn is over: the call shows
// interrupted, not running for ever.
func TestAntigravityUnansweredCallIsInterruptedOnceIdle(t *testing.T) {
	home := t.TempDir()
	path := writeAntigravityConversation(t, home, agConversation, agLinePrompt, agLineCall)
	s := newTestAntigravity(t, home, 900, map[int]antigravityProcess{
		900: {cwd: "/w", conversations: []string{agConversation}},
	})
	discoverAntigravity(t, s)
	id := "antigravity:" + agConversation
	setState := func(state protocol.State) {
		s.mu.Lock()
		session := s.sessions[id]
		session.meta.State = state
		s.sessions[id] = session
		s.mu.Unlock()
	}
	callStatus := func() (protocol.ToolStatus, string) {
		t.Helper()
		page, err := s.Page(context.Background(), id, "", 10)
		if err != nil || len(page.Messages) != 2 || page.Messages[1].Tool == nil {
			t.Fatalf("page = %+v, %v", page, err)
		}
		return page.Messages[1].Tool.Status, page.Messages[1].Text
	}

	setState(protocol.StateIdle)
	if status, _ := callStatus(); status != protocol.ToolRunning {
		t.Errorf("just written, the call is %s", status)
	}
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	setState(protocol.StateBusy)
	if status, _ := callStatus(); status != protocol.ToolRunning {
		t.Errorf("while agy works, the call is %s", status)
	}
	setState(protocol.StateIdle)
	if status, text := callStatus(); status != protocol.ToolError || text != "Interrupted" {
		t.Errorf("idle: %s %q", status, text)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out := make(chan []protocol.Message, 8)
	go func() { _ = s.Follow(ctx, id, out) }()
	select {
	case batch := <-out:
		if len(batch) != 1 || batch[0].Tool == nil || batch[0].Tool.Status != protocol.ToolError || batch[0].Text != "Interrupted" {
			t.Fatalf("follow: %+v", batch)
		}
	case <-ctx.Done():
		t.Fatal("follow left the call running")
	}
}
