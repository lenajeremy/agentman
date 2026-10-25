package hook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
)

func TestCodexConfigGainsAndLosesOnlyAgentmansTable(t *testing.T) {
	before := "model = \"gpt-5\"\n\n[mcp_servers.github]\ncommand = \"gh-mcp\"\n"
	added := CodexWithMCP(before, "/opt/homebrew/bin/am", false)
	if !strings.HasPrefix(added, before) || !strings.Contains(added, "[mcp_servers.agentman]\ncommand = \"/opt/homebrew/bin/am\"\nargs = [\"mcp\"]") {
		t.Fatalf("added =\n%s", added)
	}
	if again := CodexWithMCP(added, "/opt/homebrew/bin/am", false); again != added {
		t.Fatalf("adding twice =\n%s", again)
	}
	if command := codexMCPCommand(added); command != "/opt/homebrew/bin/am" {
		t.Fatalf("command = %q", command)
	}
	withEnv := added + "\n[mcp_servers.agentman.env]\nX = \"1\"\n\n[profiles.fast]\nmodel = \"o4\"\n"
	removed := CodexWithMCP(withEnv, "", true)
	if strings.Contains(removed, "agentman") || !strings.Contains(removed, "[mcp_servers.github]") || !strings.Contains(removed, "[profiles.fast]") {
		t.Fatalf("removed =\n%s", removed)
	}
	if CodexWithMCP(before, "", true) != before {
		t.Fatal("removing what wasn't there changed the file")
	}
}

// mcpHome is a home with the agents given, by the folders they keep.
func mcpHome(t *testing.T, dirs ...string) string {
	t.Helper()
	home := t.TempDir()
	for _, dir := range dirs {
		if err := os.MkdirAll(filepath.Join(home, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	// No claude command, so Claude Code is only planned when its folder is.
	t.Setenv("PATH", t.TempDir())
	return home
}

func plansByKind(t *testing.T, in Installer, remove bool) map[protocol.Kind]Plan {
	t.Helper()
	plans, err := in.MCPPlans(remove)
	if err != nil {
		t.Fatal(err)
	}
	byKind := map[protocol.Kind]Plan{}
	for _, plan := range plans {
		byKind[plan.Kind] = plan
	}
	return byKind
}

func applyMCP(t *testing.T, plans map[protocol.Kind]Plan) {
	t.Helper()
	for kind, plan := range plans {
		if err := plan.Apply(); err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
	}
}

// Every installed agent gets the server in its own format, the user's own
// servers stay, and a second run changes nothing.
func TestEveryInstalledAgentGetsAgentmansServer(t *testing.T) {
	home := mcpHome(t, ".cursor", ".config/opencode", ".kiro", ".gemini/config", ".codex")
	write := func(rel, text string) {
		if err := os.WriteFile(filepath.Join(home, rel), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(".cursor/mcp.json", `{"mcpServers":{"linear":{"url":"https://mcp.linear.app"}},"other":1}`)
	write(".config/opencode/opencode.json", `{"$schema":"https://opencode.ai/config.json","mcp":{"railway":{"type":"local","command":["railway","mcp"],"enabled":true}}}`)
	write(".gemini/config/mcp_config.json", "")
	in := Installer{Home: home, Binary: "/opt/homebrew/bin/am"}

	plans := plansByKind(t, in, false)
	for _, kind := range []protocol.Kind{protocol.KindCursor, protocol.KindOpenCode, protocol.KindKiro, protocol.KindAntigravity, protocol.KindCodex} {
		if plan, ok := plans[kind]; !ok || !plan.Changed || plan.Err != nil {
			t.Fatalf("%s: %+v", kind, plan)
		}
	}
	if _, ok := plans[protocol.KindClaude]; ok {
		t.Fatal("Claude Code was planned without being installed")
	}
	applyMCP(t, plans)

	read := func(rel string) map[string]any {
		raw, err := os.ReadFile(filepath.Join(home, rel))
		if err != nil {
			t.Fatal(err)
		}
		var config map[string]any
		if err := json.Unmarshal(raw, &config); err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		return config
	}
	server := func(config map[string]any, key string) map[string]any {
		servers, _ := config[key].(map[string]any)
		entry, _ := servers[MCPServerName].(map[string]any)
		return entry
	}
	cursor := read(".cursor/mcp.json")
	if entry := server(cursor, "mcpServers"); entry["command"] != "/opt/homebrew/bin/am" || cursor["other"] != float64(1) ||
		cursor["mcpServers"].(map[string]any)["linear"] == nil {
		t.Fatalf("cursor = %v", cursor)
	}
	opencode := read(".config/opencode/opencode.json")
	if entry := server(opencode, "mcp"); entry["type"] != "local" || entry["enabled"] != true ||
		strings.Join([]string{entry["command"].([]any)[0].(string), entry["command"].([]any)[1].(string)}, " ") != "/opt/homebrew/bin/am mcp" ||
		opencode["mcp"].(map[string]any)["railway"] == nil || opencode["$schema"] == nil {
		t.Fatalf("opencode = %v", opencode)
	}
	for _, rel := range []string{".kiro/settings/mcp.json", ".gemini/config/mcp_config.json"} {
		if entry := server(read(rel), "mcpServers"); entry["command"] != "/opt/homebrew/bin/am" || entry["args"].([]any)[0] != "mcp" {
			t.Fatalf("%s = %v", rel, entry)
		}
	}

	for kind, plan := range plansByKind(t, in, false) {
		if plan.Changed {
			t.Errorf("%s changed on a second run", kind)
		}
	}

	// Taking it out leaves each file as the user had it.
	applyMCP(t, plansByKind(t, in, true))
	if _, found := server(read(".cursor/mcp.json"), "mcpServers")["command"]; found {
		t.Error("cursor kept the server")
	}
	if opencode := read(".config/opencode/opencode.json"); server(opencode, "mcp") != nil || opencode["mcp"].(map[string]any)["railway"] == nil {
		t.Errorf("opencode = %v", opencode)
	}
}

// A config Agentman can't read is left alone, a linked one stays a link,
// and what the user set on Agentman's entry survives.
func TestMCPConfigsAreHandledWithCare(t *testing.T) {
	home := mcpHome(t, ".cursor", ".kiro/settings", ".config/opencode", "dotfiles")
	_ = os.WriteFile(filepath.Join(home, ".cursor", "mcp.json"), []byte("{broken"), 0o644)
	target := filepath.Join(home, "dotfiles", "kiro-mcp.json")
	_ = os.WriteFile(target, []byte(`{"mcpServers":{"agentman":{"command":"/gone/am","args":["mcp"],"env":{"X":"1"}}}}`), 0o644)
	if err := os.Symlink(target, filepath.Join(home, ".kiro", "settings", "mcp.json")); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(home, ".config", "opencode", "opencode.jsonc"), []byte("{\n  // mine\n  \"lsp\": {}\n}"), 0o644)

	plans := plansByKind(t, Installer{Home: home, Binary: "/usr/local/bin/am"}, false)
	if plans[protocol.KindCursor].Err == nil || plans[protocol.KindOpenCode].Err == nil {
		t.Fatalf("an unreadable config was planned over: %+v", plans)
	}
	kiro := plans[protocol.KindKiro]
	if kiro.MCPRegistered() || !kiro.Changed {
		t.Fatalf("a server pointing at a missing am counted as registered: %+v", kiro)
	}
	if err := kiro.Apply(); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Lstat(filepath.Join(home, ".kiro", "settings", "mcp.json")); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the linked config was replaced by a file")
	}
	raw, _ := os.ReadFile(target)
	if !strings.Contains(string(raw), `"/usr/local/bin/am"`) || !strings.Contains(string(raw), `"X": "1"`) {
		t.Fatalf("kiro = %s", raw)
	}
	if again := plansByKind(t, Installer{Home: home, Binary: "/usr/local/bin/am"}, false)[protocol.KindKiro]; again.Changed {
		t.Fatal("kiro changed again")
	}
}

// Claude Code is given the server through its own command, and only when
// it doesn't have it.
func TestClaudeCodeGetsTheServerThroughItsCommand(t *testing.T) {
	home := mcpHome(t, ".claude")
	bin := t.TempDir()
	log := filepath.Join(bin, "calls")
	script := "#!/bin/sh\necho \"$@\" >> " + log + "\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	_ = os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"mcpServers":{"railway":{"command":"railway"}}}`), 0o600)
	in := Installer{Home: home, Binary: "/opt/homebrew/bin/am"}

	plan := plansByKind(t, in, false)[protocol.KindClaude]
	if !plan.Changed || plan.Err != nil {
		t.Fatalf("plan = %+v", plan)
	}
	if err := plan.Apply(); err != nil {
		t.Fatal(err)
	}
	calls, _ := os.ReadFile(log)
	if !strings.Contains(string(calls), "mcp add --scope user agentman -- /opt/homebrew/bin/am mcp") {
		t.Fatalf("calls = %s", calls)
	}

	_ = os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"mcpServers":{"agentman":{"type":"stdio","command":"/opt/homebrew/bin/am","args":["mcp"],"env":{}}}}`), 0o600)
	if again := plansByKind(t, in, false)[protocol.KindClaude]; again.Changed {
		t.Fatal("Claude Code was given the server twice")
	}
	if gone := plansByKind(t, in, true)[protocol.KindClaude]; !gone.Changed {
		t.Fatal("removing found nothing to remove")
	}
}
