package hook

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// realisticSettings mirrors the shape of an actual ~/.claude/settings.json,
// including keys agentman knows nothing about. Preserving these is the whole
// risk of this feature: this file is the user's real configuration.
const realisticSettings = `{
  "permissions": {
    "allow": [
      "Bash(brew install *)",
      "Bash(node *)"
    ],
    "defaultMode": "dontAsk"
  },
  "model": "claude-opus-5",
  "statusLine": {
    "type": "command",
    "command": "bash /Users/mac/.claude/statusline-command.sh"
  },
  "enabledPlugins": {
    "vercel@claude-plugins-official": true
  },
  "effortLevel": "xhigh",
  "theme": "dark"
}`

func claudeSettingsPath(home string) string {
	return filepath.Join(home, ".claude", "settings.json")
}

func codexConfigPath(home string) string {
	return filepath.Join(home, ".codex", "config.toml")
}

func writeSettings(t *testing.T, home, body string) string {
	t.Helper()
	path := claudeSettingsPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func applyAll(t *testing.T, in Installer, remove bool) []Plan {
	t.Helper()
	plans, err := in.Plans("tok", remove)
	if err != nil {
		t.Fatal(err)
	}
	for _, plan := range plans {
		if plan.Err != nil {
			continue
		}
		if err := plan.Apply(); err != nil {
			t.Fatalf("%s: %v", plan.Kind, err)
		}
	}
	return plans
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("%s is not valid JSON after write: %v", path, err)
	}
	return out
}

func TestInstallPreservesExistingSettings(t *testing.T) {
	home := t.TempDir()
	path := writeSettings(t, home, realisticSettings)

	applyAll(t, Installer{Home: home, Binary: "/usr/local/bin/am"}, false)

	got := readJSON(t, path)

	// Every pre-existing key must survive untouched.
	var before map[string]any
	if err := json.Unmarshal([]byte(realisticSettings), &before); err != nil {
		t.Fatal(err)
	}
	for key, want := range before {
		gotJSON, _ := json.Marshal(got[key])
		wantJSON, _ := json.Marshal(want)
		if string(gotJSON) != string(wantJSON) {
			t.Errorf("setting %q was altered:\n got %s\nwant %s", key, gotJSON, wantJSON)
		}
	}

	hooks, ok := got["hooks"].(map[string]any)
	if !ok {
		t.Fatal("hooks key missing after install")
	}
	for _, name := range Installed {
		if _, ok := hooks[string(name)]; !ok {
			t.Errorf("hook %q not registered", name)
		}
	}
}

func TestInstallKeepsUserHooksAndIsIdempotent(t *testing.T) {
	home := t.TempDir()
	// A user who already has their own Stop hook must keep it.
	path := writeSettings(t, home, `{
	  "hooks": {
	    "Stop": [
	      {"hooks": [{"type": "command", "command": "/usr/bin/say", "args": ["done"]}]}
	    ]
	  }
	}`)

	in := Installer{Home: home, Binary: "/usr/local/bin/am"}
	applyAll(t, in, false)

	countStop := func() (ours, theirs int) {
		hooks := readJSON(t, path)["hooks"].(map[string]any)
		for _, raw := range hooks["Stop"].([]any) {
			for _, c := range raw.(map[string]any)["hooks"].([]any) {
				if isOurCommand(c) {
					ours++
				} else {
					theirs++
				}
			}
		}
		return
	}

	ours, theirs := countStop()
	if theirs != 1 {
		t.Errorf("the user's own Stop hook was lost (found %d)", theirs)
	}
	if ours != 1 {
		t.Errorf("expected exactly 1 agentman hook, got %d", ours)
	}

	// Re-running must not accumulate duplicates.
	for range 3 {
		applyAll(t, in, false)
	}
	ours, theirs = countStop()
	if ours != 1 || theirs != 1 {
		t.Errorf("install is not idempotent: %d ours, %d theirs after 4 runs", ours, theirs)
	}

	// A second install reports no change, so `am install-hooks` is safe to
	// re-run and says so.
	plans, _ := in.Plans("tok", false)
	for _, p := range plans {
		if p.Kind == protocol.KindClaude && p.Changed {
			t.Error("a no-op install should report Changed=false")
		}
	}
}

func TestUninstallRemovesOnlyOurHooks(t *testing.T) {
	home := t.TempDir()
	path := writeSettings(t, home, `{
	  "model": "claude-opus-5",
	  "hooks": {
	    "Stop": [
	      {"hooks": [{"type": "command", "command": "/usr/bin/say", "args": ["done"]}]}
	    ]
	  }
	}`)

	in := Installer{Home: home, Binary: "/usr/local/bin/am"}
	applyAll(t, in, false)
	applyAll(t, in, true)

	got := readJSON(t, path)
	if got["model"] != "claude-opus-5" {
		t.Error("uninstall disturbed unrelated settings")
	}

	hooks, ok := got["hooks"].(map[string]any)
	if !ok {
		t.Fatal("uninstall removed the user's whole hooks block")
	}
	stop, ok := hooks["Stop"].([]any)
	if !ok || len(stop) != 1 {
		t.Fatalf("the user's Stop hook did not survive uninstall: %v", hooks["Stop"])
	}
	for _, c := range stop[0].(map[string]any)["hooks"].([]any) {
		if isOurCommand(c) {
			t.Error("an agentman hook survived uninstall")
		}
	}
	// Events where the user had nothing should be gone entirely, not left as
	// empty arrays cluttering their config.
	if _, exists := hooks["SessionStart"]; exists {
		t.Error("uninstall left an empty SessionStart key behind")
	}
}

func TestUninstallAfterBinaryMoved(t *testing.T) {
	home := t.TempDir()
	path := writeSettings(t, home, `{}`)

	// Installed from a build directory...
	applyAll(t, Installer{Home: home, Binary: "/Users/me/dev/agentman/bin/am"}, false)
	// ...then uninstalled after the binary moved to a brew prefix.
	applyAll(t, Installer{Home: home, Binary: "/opt/homebrew/bin/am"}, true)

	if hooks, exists := readJSON(t, path)["hooks"]; exists {
		t.Errorf("hooks from a previous install path were orphaned: %v", hooks)
	}
}

func TestUninstallDoesNotClaimSimilarlyNamedProgram(t *testing.T) {
	entry := map[string]any{
		"command": "/usr/local/bin/agentmanager",
		"args":    []any{"hook", "claude", "Stop"},
	}
	if isOurCommand(entry) {
		t.Fatal("similarly named user command was classified as agentman")
	}
}

func TestInstallRefusesToClobberUnparseableSettings(t *testing.T) {
	home := t.TempDir()
	// Half-written, or hand-edited with a trailing comma.
	const broken = `{"model": "claude-opus-5",}`
	path := writeSettings(t, home, broken)

	plans, err := Installer{Home: home, Binary: "/usr/local/bin/am"}.Plans("tok", false)
	if err != nil {
		t.Fatal(err)
	}
	var claudePlan Plan
	for _, p := range plans {
		if p.Kind == protocol.KindClaude {
			claudePlan = p
		}
	}
	if claudePlan.Err == nil {
		t.Fatal("expected a refusal rather than a guess at unreadable settings")
	}
	if err := claudePlan.Apply(); err == nil {
		t.Fatal("Apply should propagate the refusal")
	}

	raw, _ := os.ReadFile(path)
	if string(raw) != broken {
		t.Error("the unreadable file was modified; it must be left exactly as found")
	}
}

func TestInstallCreatesConfigWhenAbsent(t *testing.T) {
	home := t.TempDir()
	applyAll(t, Installer{Home: home, Binary: "/usr/local/bin/am"}, false)

	if _, ok := readJSON(t, claudeSettingsPath(home))["hooks"]; !ok {
		t.Error("hooks not written to a fresh settings file")
	}
	codex, err := os.ReadFile(codexConfigPath(home))
	if err != nil {
		t.Fatal(err)
	}
	if !contains(string(codex), `notify = ["/usr/local/bin/am", "hook", "codex", "Stop"]`) {
		t.Errorf("Codex's supported notify command was not installed:\n%s", codex)
	}
}

func TestInstallBacksUpPreviousConfig(t *testing.T) {
	home := t.TempDir()
	path := writeSettings(t, home, realisticSettings)

	applyAll(t, Installer{Home: home, Binary: "/usr/local/bin/am"}, false)

	backup, err := os.ReadFile(path + ".agentman.bak")
	if err != nil {
		t.Fatalf("no backup written: %v", err)
	}
	if string(backup) != realisticSettings {
		t.Error("backup does not match the original file")
	}
}

func TestClaudeInstallRefusesStalePlan(t *testing.T) {
	home := t.TempDir()
	path := writeSettings(t, home, `{"model":"alpha"}`)
	in := Installer{Home: home, Binary: "/usr/local/bin/am"}
	plans, err := in.Plans("tok", false)
	if err != nil {
		t.Fatal(err)
	}

	var plan Plan
	for _, candidate := range plans {
		if candidate.Kind == protocol.KindClaude {
			plan = candidate
			break
		}
	}
	if !plan.Changed || plan.Err != nil {
		t.Fatalf("unexpected Claude plan: %+v", plan)
	}

	// Keep the same byte length so a size-only or coarse-mtime fingerprint
	// would miss this edit.
	newer := []byte(`{"model":"bravo"}`)
	if err := os.WriteFile(path, newer, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(); err == nil ||
		!strings.Contains(err.Error(), "changed after it was inspected") ||
		!strings.Contains(err.Error(), "re-run") {
		t.Fatalf("Apply error = %v, want actionable stale-plan refusal", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, newer) {
		t.Fatalf("Claude's newer config was overwritten:\n got: %q\nwant: %q", got, newer)
	}
	if _, err := os.Stat(path + ".agentman.bak"); !os.IsNotExist(err) {
		t.Fatalf("stale plan unexpectedly wrote a backup: %v", err)
	}
}

func TestCodexInstallRefusesStalePlan(t *testing.T) {
	home := t.TempDir()
	path := codexConfigPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("model = \"alpha\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	in := Installer{Home: home, Binary: "/usr/local/bin/am"}
	plans, err := in.Plans("tok", false)
	if err != nil {
		t.Fatal(err)
	}

	var plan Plan
	for _, candidate := range plans {
		if candidate.Kind == protocol.KindCodex {
			plan = candidate
			break
		}
	}
	if !plan.Changed || plan.Err != nil {
		t.Fatalf("unexpected Codex plan: %+v", plan)
	}

	// Same-length replacement specifically exercises the content digest.
	newer := []byte("model = \"bravo\"\n")
	if err := os.WriteFile(path, newer, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(); err == nil ||
		!strings.Contains(err.Error(), "changed after it was inspected") ||
		!strings.Contains(err.Error(), "re-run") {
		t.Fatalf("Apply error = %v, want actionable stale-plan refusal", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, newer) {
		t.Fatalf("Codex's newer config was overwritten:\n got: %q\nwant: %q", got, newer)
	}
	if _, err := os.Stat(path + ".agentman.bak"); !os.IsNotExist(err) {
		t.Fatalf("stale plan unexpectedly wrote a backup: %v", err)
	}
}

func TestCodexNotifyPreservesTOMLAndIsIdempotent(t *testing.T) {
	home := t.TempDir()
	path := codexConfigPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	const original = "model = \"gpt-5.6-sol\"\n\n[tui]\nanimations = false\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	in := Installer{Home: home, Binary: "/usr/local/bin/am"}
	applyAll(t, in, false)
	first, _ := os.ReadFile(path)
	if !contains(string(first), original) {
		t.Fatalf("Codex config was not preserved:\n%s", first)
	}
	applyAll(t, in, false)
	second, _ := os.ReadFile(path)
	if string(second) != string(first) {
		t.Fatal("installing Codex notify twice changed the config")
	}
	applyAll(t, in, true)
	after, _ := os.ReadFile(path)
	if string(after) != original {
		t.Fatalf("uninstall did not restore the Codex config:\n%s", after)
	}
}

func TestCodexNotifyRefusesToReplaceUserCommand(t *testing.T) {
	home := t.TempDir()
	path := codexConfigPath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	const original = "notify = [\"terminal-notifier\"]\n"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	plans, err := (Installer{Home: home, Binary: "/usr/local/bin/am"}).Plans("tok", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, plan := range plans {
		if plan.Kind == protocol.KindCodex && plan.Err == nil {
			t.Fatal("installer would overwrite the user's existing Codex notifier")
		}
	}
}

func TestAppliedConfigsAndBackupsArePrivate(t *testing.T) {
	home := t.TempDir()
	path := writeSettings(t, home, realisticSettings)
	applyAll(t, Installer{Home: home, Binary: "/usr/local/bin/am"}, false)
	for _, candidate := range []string{path, path + ".agentman.bak"} {
		info, err := os.Stat(candidate)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Errorf("%s mode = %#o, want 0600", candidate, got)
		}
	}
}

func TestTokenIsNotPlacedInArgv(t *testing.T) {
	// argv is world-readable via ps; the token must come from the 0600 config
	// file instead.
	entry := ourCommand("/usr/local/bin/am", protocol.KindClaude, NameStop, "super-secret-token")
	rendered, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(rendered); contains(got, "super-secret-token") {
		t.Errorf("token leaked into the hook command line: %s", got)
	}
}

func contains(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) &&
		(func() bool {
			for i := 0; i+len(needle) <= len(haystack); i++ {
				if haystack[i:i+len(needle)] == needle {
					return true
				}
			}
			return false
		})()
}
