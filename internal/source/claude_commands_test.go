package source

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/tmux"
)

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func commandsByName(commands []protocol.Command) map[string]protocol.Command {
	byName := map[string]protocol.Command{}
	for _, c := range commands {
		byName[c.Name] = c
	}
	return byName
}

func TestClaudeCommandsListWhatClaudeWouldOffer(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	cwd := filepath.Join(repo, "apps", "web")
	writeFile(t, filepath.Join(repo, ".git", "HEAD"), "ref: refs/heads/main\n")

	writeFile(t, filepath.Join(home, ".claude", "settings.json"), `{
		"skillOverrides": {"hidden-by-override": "off"},
		"enabledPlugins": {"tools@market": true, "off@market": false}
	}`)
	// Personal skills: one renamed by its frontmatter, one only Claude may
	// run, one with a folded description, and one with none.
	writeFile(t, filepath.Join(home, ".claude", "skills", "deploy-staging", "SKILL.md"),
		"---\nname: deploy\ndescription: \"Deploy to staging\"\nargument-hint: [env]\n---\nDeploy.\n")
	writeFile(t, filepath.Join(home, ".claude", "skills", "claude-only", "SKILL.md"),
		"---\ndescription: Not for the menu\nuser-invocable: no\n---\nx\n")
	writeFile(t, filepath.Join(home, ".claude", "skills", "folded", "SKILL.md"),
		"---\ndescription: >\n  Folds over\n  two lines\n---\nbody\n")
	writeFile(t, filepath.Join(home, ".claude", "skills", "plain", "SKILL.md"), "# Says what it does\n\nMore.\n")
	writeFile(t, filepath.Join(home, ".claude", "skills", "hidden-by-override", "SKILL.md"), "---\ndescription: x\n---\n")
	// Older command files, one in a subfolder.
	writeFile(t, filepath.Join(home, ".claude", "commands", "standup.md"), "---\ndescription: Write my standup\n---\n")
	writeFile(t, filepath.Join(home, ".claude", "commands", "frontend", "component.md"), "Make a component\n")
	// Project skills from the folder Claude started in and the repo root; the
	// personal "deploy" wins over the project's.
	writeFile(t, filepath.Join(cwd, ".claude", "skills", "web-only", "SKILL.md"), "---\ndescription: Web app only\n---\n")
	writeFile(t, filepath.Join(repo, ".claude", "skills", "deploy", "SKILL.md"), "---\ndescription: The project's deploy\n---\n")
	writeFile(t, filepath.Join(repo, ".claude", "commands", "release.md"), "---\ndescription: Cut a release\nargument-hint: <version>\n---\n")
	// An enabled plugin and a disabled one.
	plugin := filepath.Join(home, ".claude", "plugins", "cache", "market", "tools", "1.0.0")
	off := filepath.Join(home, ".claude", "plugins", "cache", "market", "off", "1.0.0")
	writeFile(t, filepath.Join(home, ".claude", "plugins", "installed_plugins.json"), `{"version": 2, "plugins": {
		"tools@market": [{"scope": "user", "installPath": "`+plugin+`"}],
		"off@market": [{"scope": "user", "installPath": "`+off+`"}]
	}}`)
	writeFile(t, filepath.Join(plugin, "skills", "lint", "SKILL.md"), "---\ndescription: Lint the code\n---\n")
	writeFile(t, filepath.Join(plugin, "commands", "fmt.md"), "---\ndescription: Format\n---\n")
	writeFile(t, filepath.Join(off, "skills", "nope", "SKILL.md"), "---\ndescription: x\n---\n")
	// Synced claude.ai skills, for the organization signed in.
	writeFile(t, filepath.Join(home, ".claude.json"), `{"oauthAccount": {"organizationUuid": "org1"}}`)
	writeFile(t, filepath.Join(home, ".claude", "skills", "synced", "org1_acct", "pdf", "SKILL.md"), "---\nname: pdf\ndescription: PDFs\n---\n")
	writeFile(t, filepath.Join(home, ".claude", "skills", "synced", "org2_acct", "other", "SKILL.md"), "---\nname: other\ndescription: x\n---\n")
	// Claude's own list, as a session's first turn writes it.
	transcript := filepath.Join(t.TempDir(), "t.jsonl")
	writeFile(t, transcript, `{"type":"user","message":{"role":"user","content":"hi"}}`+"\n"+
		`{"type":"attachment","attachment":{"type":"skill_listing","names":["dataviz","deploy","tools:lint"],"content":"- dataviz: Charts and graphs\n- deploy: Deploy to staging\n- tools:lint: Lint the code"}}`+"\n")

	got := commandsByName(claudeCommands(home, cwd, transcript))
	want := map[string]protocol.Command{
		"compact":              {Name: "compact", ArgumentHint: "[instructions]", Source: "builtin"},
		"usage":                {Name: "usage", Source: "builtin", Panel: true},
		"deploy":               {Name: "deploy", Description: "Deploy to staging", ArgumentHint: "[env]", Source: "personal"},
		"folded":               {Name: "folded", Description: "Folds over two lines", Source: "personal"},
		"plain":                {Name: "plain", Description: "Says what it does", Source: "personal"},
		"standup":              {Name: "standup", Description: "Write my standup", Source: "personal"},
		"frontend:component":   {Name: "frontend:component", Description: "Make a component", Source: "personal"},
		"web-only":             {Name: "web-only", Description: "Web app only", Source: "project"},
		"release":              {Name: "release", Description: "Cut a release", ArgumentHint: "<version>", Source: "project"},
		"tools:lint":           {Name: "tools:lint", Description: "Lint the code", Source: "plugin"},
		"tools:fmt":            {Name: "tools:fmt", Description: "Format", Source: "plugin"},
		"anthropic-skills:pdf": {Name: "anthropic-skills:pdf", Description: "PDFs", Source: "personal"},
		"code-review":          {Name: "code-review", ArgumentHint: "[level] [target]", Source: "bundled"},
		"dataviz":              {Name: "dataviz", Description: "Charts and graphs", Source: "bundled"},
	}
	for name, w := range want {
		c, ok := got[name]
		if !ok {
			t.Errorf("/%s missing", name)
			continue
		}
		if w.Description != "" && c.Description != w.Description {
			t.Errorf("/%s description = %q, want %q", name, c.Description, w.Description)
		}
		if c.ArgumentHint != w.ArgumentHint || c.Source != w.Source || c.Panel != w.Panel {
			t.Errorf("/%s = %+v, want %+v", name, c, w)
		}
	}
	for _, name := range []string{"claude-only", "hidden-by-override", "off:nope", "anthropic-skills:other", "deploy-staging",
		"theme", "permissions", "exit"} {
		if _, ok := got[name]; ok {
			t.Errorf("/%s should not be offered", name)
		}
	}
}

func TestClaudePanelTextIsThePanelAlone(t *testing.T) {
	cases := map[string][]string{
		"usage":  {"Session", "Current week (all models)", "68% used", "Usage credits are off · /usage-credits to turn them on"},
		"status": {"Version:           2.1.311", "Email:             me@example.com"},
		"mcp":    {"Manage MCP servers", "✔ screenshotter            30 tools"},
		"tasks":  {"Background", "No tasks currently running"},
		"help":   {"Shortcuts"},
	}
	for name, wants := range cases {
		text, open := claudePanelText(readPaneFixture(t, "testdata", "claude-panel-"+name+".txt"))
		if !open {
			t.Errorf("%s: no panel read", name)
			continue
		}
		for _, want := range wants {
			if !strings.Contains(text, want) {
				t.Errorf("%s: missing %q in:\n%s", name, want, text)
			}
		}
		for _, leftOver := range []string{"▔", "Esc to ", "Settings  Status", "Help  General"} {
			if strings.Contains(text, leftOver) {
				t.Errorf("%s: kept %q", name, leftOver)
			}
		}
		if first, _, _ := strings.Cut(text, "\n"); strings.HasPrefix(first, " ") {
			t.Errorf("%s: margin kept: %q", name, first)
		}
	}
	if _, open := claudePanelText(readPaneFixture(t, "testdata", "claude-pane-vim-insert.txt")); open {
		t.Error("the prompt is not a panel")
	}
}

// scriptedPane plays a terminal: the prompt until the command is typed, the
// panel until Esc, then the prompt again.
type scriptedPane struct {
	mu      sync.Mutex
	typed   bool
	escapes int
	prompt  string
	panel   string
	opens   bool
}

func (p *scriptedPane) capture(context.Context, string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.typed && p.opens && p.escapes == 0 {
		return p.panel, nil
	}
	return p.prompt, nil
}

func (p *scriptedPane) escape(context.Context, string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.escapes++
	return nil
}

func TestClaudeCommandPanelReadsThenCloses(t *testing.T) {
	if !tmux.Available() {
		t.Skip("tmux not installed")
	}
	ctx := context.Background()
	name := "agentman-claude-panel"
	out := filepath.Join(t.TempDir(), "typed")
	if err := tmux.Launch(ctx, name, t.TempDir(), []string{"sh", "-c", "stty raw -echo; cat > " + out}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tmux.Kill(context.Background(), name) })
	time.Sleep(300 * time.Millisecond)
	width, height, _, err := tmux.Size(ctx, name)
	if err != nil {
		t.Fatal(err)
	}

	pane := &scriptedPane{
		prompt: readPaneFixture(t, "testdata", "claude-pane-vim-insert.txt"),
		panel:  readPaneFixture(t, "testdata", "claude-panel-usage.txt"),
		opens:  true,
	}
	s := &ClaudeSource{
		home:        t.TempDir(),
		closeDialog: pane.escape,
		sessions:    map[string]claudeSession{"claude:s1": {tmuxName: name, meta: protocol.Session{State: protocol.StateIdle}}},
	}
	s.capturePane = func(ctx context.Context, n string) (string, error) {
		if data, _ := os.ReadFile(out); strings.Contains(string(data), "/usage\r") {
			pane.mu.Lock()
			pane.typed = true
			pane.mu.Unlock()
		}
		return pane.capture(ctx, n)
	}

	panel, err := s.CommandPanel(ctx, "claude:s1", "/usage")
	if err != nil {
		t.Fatal(err)
	}
	if panel.Command != "/usage" || !strings.Contains(panel.Text, "Current week (all models)") {
		t.Fatalf("panel = %+v", panel)
	}
	if pane.escapes != 1 {
		t.Errorf("pressed Esc %d times, want once", pane.escapes)
	}
	// The pane is back to the size it had, and sized by tmux again.
	if w, h, _, err := tmux.Size(ctx, name); err != nil || w != width || h != height {
		t.Errorf("size after = %dx%d (%v), want %dx%d", w, h, err, width, height)
	}

	// A command that is not a safe panel is refused before anything is typed.
	if _, err := s.CommandPanel(ctx, "claude:s1", "/config"); err == nil {
		t.Error("/config was run as a panel")
	}
	// /help waits for a turn to end, so it is refused while Claude works.
	busy := &ClaudeSource{capturePane: pane.capture, sessions: map[string]claudeSession{
		"claude:s1": {tmuxName: name, meta: protocol.Session{State: protocol.StateBusy}}}}
	if _, err := busy.CommandPanel(ctx, "claude:s1", "/help"); err != errPanelBusy {
		t.Errorf("busy /help: %v", err)
	}
}

func TestClaudeCommandPanelNeverPressesEscWhenNothingOpened(t *testing.T) {
	if !tmux.Available() {
		t.Skip("tmux not installed")
	}
	ctx := context.Background()
	name := "agentman-claude-panel-none"
	if err := tmux.Launch(ctx, name, t.TempDir(), []string{"sh", "-c", "stty raw -echo; cat > /dev/null"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tmux.Kill(context.Background(), name) })
	time.Sleep(300 * time.Millisecond)

	pane := &scriptedPane{prompt: readPaneFixture(t, "testdata", "claude-pane-vim-insert.txt"), typed: true}
	s := &ClaudeSource{
		home:        t.TempDir(),
		capturePane: pane.capture,
		closeDialog: pane.escape,
		sessions:    map[string]claudeSession{"claude:s1": {tmuxName: name}},
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if _, err := s.CommandPanel(ctx, "claude:s1", "/status"); err == nil {
		t.Fatal("a panel that never opened was read")
	}
	// Esc with nothing open would stop a turn, or leave a Vim prompt in
	// normal mode.
	if pane.escapes != 0 {
		t.Errorf("pressed Esc %d times", pane.escapes)
	}
}

// /clear starts a new conversation in the same Claude: its registry names a
// new session for the same process. The new one names the old, so the apps
// can follow from the old thread, as Claude's own app does.
func TestClaudeClearNamesTheConversationItTookOver(t *testing.T) {
	home := t.TempDir()
	registry := filepath.Join(home, ".claude", "sessions", "4242.json")
	write := func(id string) {
		writeFile(t, registry, `{"pid": `+strconv.Itoa(os.Getpid())+`, "sessionId": "`+id+`", "cwd": "/tmp/proj",
			"startedAt": 1, "updatedAt": 2, "status": "idle"}`)
	}
	source, err := NewClaudeSource(home)
	if err != nil {
		t.Fatal(err)
	}
	source.listPanes = func(context.Context) ([]tmux.Session, error) { return nil, nil }
	discover := func() protocol.Session {
		t.Helper()
		sessions, err := source.Discover(context.Background())
		if err != nil || len(sessions) != 1 {
			t.Fatalf("sessions = %+v, %v", sessions, err)
		}
		return sessions[0]
	}

	write("11111111-1111-4111-8111-111111111111")
	if first := discover(); first.Previous != "" {
		t.Fatalf("a first conversation took over nothing: %q", first.Previous)
	}
	write("22222222-2222-4222-8222-222222222222")
	for range 2 {
		next := discover()
		if next.ID != "claude:22222222-2222-4222-8222-222222222222" ||
			next.Previous != "claude:11111111-1111-4111-8111-111111111111" {
			t.Fatalf("after /clear: %+v", next)
		}
	}
}

// /usage, /status, /help, /mcp and /tasks only show something; the prompt,
// a dialog that asks something, a permission prompt, a question and the
// model picker are not panels to close.
func TestClaudeInfoPanels(t *testing.T) {
	for _, name := range []string{"usage", "status", "help", "mcp", "tasks"} {
		if !claudeInfoPanel(readPaneFixture(t, "testdata", "claude-panel-"+name+".txt")) {
			t.Errorf("/%s is not an info panel", name)
		}
	}
	for _, path := range [][]string{
		{"testdata", "claude-pane-vim-insert.txt"},
		{"..", "question", "testdata", "claude_auto_mode_setup_dialog_real_pane.txt"},
		{"..", "question", "testdata", "claude_permission_real_pane.txt"},
		{"..", "question", "testdata", "claude_single_real_pane.txt"},
		{"..", "question", "testdata", "claude_model_picker_real_pane.txt"},
	} {
		if claudeInfoPanel(readPaneFixture(t, path...)) {
			t.Errorf("%s read as an info panel", path[len(path)-1])
		}
	}
}

// /usage left open at the Mac used to refuse every message from the phone.
// It is closed, with one Esc, and the message goes; a dialog that asks
// something is still refused, and no Esc is pressed on it.
func TestClaudeSendClosesAnInfoPanelFirst(t *testing.T) {
	if !tmux.Available() {
		t.Skip("tmux not installed")
	}
	ctx := context.Background()
	name := "agentman-claude-info-panel"
	out := filepath.Join(t.TempDir(), "typed")
	if err := tmux.Launch(ctx, name, t.TempDir(), []string{"sh", "-c", "stty raw -echo; cat > " + out}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tmux.Kill(context.Background(), name) })
	time.Sleep(300 * time.Millisecond)

	pane := &scriptedPane{
		prompt: readPaneFixture(t, "testdata", "claude-pane-vim-insert.txt"),
		panel:  readPaneFixture(t, "testdata", "claude-panel-usage.txt"),
		typed:  true, opens: true,
	}
	s := &ClaudeSource{home: t.TempDir(), capturePane: pane.capture, closeDialog: pane.escape,
		sessions: map[string]claudeSession{"claude:s1": {tmuxName: name}}}
	mode, err := s.Inject(ctx, "claude:s1", "hello from the phone")
	if err != nil || mode != protocol.InjectTmux {
		t.Fatalf("Inject = %v, %v", mode, err)
	}
	if pane.escapes != 1 {
		t.Errorf("pressed Esc %d times, want once", pane.escapes)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		data, _ := os.ReadFile(out)
		if strings.Contains(string(data), "hello from the phone") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("typed %q", data)
		}
		time.Sleep(50 * time.Millisecond)
	}

	dialog := &scriptedPane{prompt: readPaneFixture(t, "..", "question", "testdata", "claude_auto_mode_setup_dialog_real_pane.txt")}
	asking := &ClaudeSource{home: t.TempDir(), capturePane: dialog.capture, closeDialog: dialog.escape,
		sessions: map[string]claudeSession{"claude:s1": {tmuxName: name}}}
	if _, err := asking.Inject(ctx, "claude:s1", "typed into a dialog"); err == nil {
		t.Error("a message was sent into a dialog that asks something")
	}
	if dialog.escapes != 0 {
		t.Errorf("pressed Esc %d times on a dialog that asks something", dialog.escapes)
	}
}

// An open /usage is not the agent waiting on the user: no question, and the
// registry's "waiting" for it reads as idle.
func TestClaudeInfoPanelIsNotAQuestion(t *testing.T) {
	usage := readPaneFixture(t, "testdata", "claude-panel-usage.txt")
	s := &ClaudeSource{home: t.TempDir(), questionSpecs: map[string]claudeQuestionSpecCache{},
		capturePane: func(context.Context, string) (string, error) { return usage, nil }}
	meta := protocol.Session{ID: "claude:s1", State: protocol.StateWaitingInput}
	s.applyPane(&meta, "", usage)
	if meta.Question != nil || meta.State != protocol.StateIdle {
		t.Errorf("question %+v, state %s", meta.Question, meta.State)
	}
	s.sessions = map[string]claudeSession{"claude:s1": {tmuxName: "agentman-claude-unused", meta: meta}}
	if current, err := s.CurrentQuestion(context.Background(), "claude:s1"); current != nil || err != nil {
		t.Errorf("CurrentQuestion = %+v, %v", current, err)
	}
}
