package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/hook"
	"github.com/lenajeremy/agentman/internal/update"
)

// Regression: hooks were recorded at the symlink's target, which under
// Homebrew is /opt/homebrew/Caskroom/agentman/<version>/am. The next upgrade
// deleted that directory and every Claude hook failed with ENOENT, so sessions
// stopped reporting state with nothing in the daemon log to say why.
func TestStableBinaryPathKeepsTheSymlink(t *testing.T) {
	dir := t.TempDir()
	versioned := filepath.Join(dir, "Caskroom", "agentman", "0.6.0")
	if err := os.MkdirAll(versioned, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(versioned, "am")
	if err := os.WriteFile(target, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "am")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	if got := stableBinaryPath(link); got != link {
		t.Errorf("stableBinaryPath(%q) = %q, want the symlink itself so it survives an upgrade", link, got)
	}
}

func TestStableBinaryPathKeepsARealFile(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "am")
	if err := os.WriteFile(binary, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := stableBinaryPath(binary); got != binary {
		t.Errorf("stableBinaryPath(%q) = %q, want it unchanged", binary, got)
	}
}

// A dangling link cannot be run, so there is nothing to preserve; resolving is
// the only remaining chance of naming something real.
func TestStableBinaryPathFallsBackWhenNothingResolves(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "gone")
	if got := stableBinaryPath(missing); got != missing {
		t.Errorf("stableBinaryPath(%q) = %q, want the input back", missing, got)
	}
	if got := stableBinaryPath(""); got != "" {
		t.Errorf("stableBinaryPath(\"\") = %q, want empty", got)
	}
}

// Cursor counts a hook that prints nothing as a failed hook, so a Cursor
// hook answers "{}" when the daemon has nothing to hand back — including when
// no daemon is running at all.
func TestCursorHookAlwaysAnswersWithJSON(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// A listener that is closed at once: nothing answers on this port, and
	// the user's own daemon on the default port is never reached.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	listener.Close()
	if err := hook.SaveConfig(home, hook.Config{Token: "tok", HookAddr: addr}); err != nil {
		t.Fatal(err)
	}

	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldIn, oldOut := os.Stdin, os.Stdout
	os.Stdin, os.Stdout = stdinR, stdoutW
	defer func() { os.Stdin, os.Stdout = oldIn, oldOut }()
	_, _ = stdinW.WriteString(`{"session_id":"chat-1","hook_event_name":"stop","status":"completed"}`)
	stdinW.Close()

	if err := runHook(context.Background(), []string{"cursor-cli", "Stop"}); err != nil {
		t.Fatal(err)
	}
	stdoutW.Close()
	out, _ := io.ReadAll(stdoutR)
	if strings.TrimSpace(string(out)) != "{}" {
		t.Fatalf("cursor hook printed %q, want {}", out)
	}
}

// Kiro adds what a session-start or prompt hook prints to the model's
// context, so a Kiro hook prints nothing — even when the daemon answers.
func TestKiroHookPrintsNothing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"decision":"block","reason":"this must not reach Kiro"}`))
	}))
	defer daemon.Close()
	if err := hook.SaveConfig(home, hook.Config{Token: "tok", HookAddr: daemon.Listener.Addr().String()}); err != nil {
		t.Fatal(err)
	}
	for _, event := range []string{"SessionStart", "UserPromptSubmit", "Stop"} {
		stdinR, stdinW, _ := os.Pipe()
		stdoutR, stdoutW, _ := os.Pipe()
		oldIn, oldOut := os.Stdin, os.Stdout
		os.Stdin, os.Stdout = stdinR, stdoutW
		_, _ = stdinW.WriteString(`{"hook_event_name":"stop","cwd":"/work","session_id":"s","assistant_response":"done"}`)
		stdinW.Close()
		err := runHook(context.Background(), []string{"kiro", event})
		os.Stdin, os.Stdout = oldIn, oldOut
		stdoutW.Close()
		out, _ := io.ReadAll(stdoutR)
		if err != nil || len(out) != 0 {
			t.Errorf("%s: printed %q (err %v)", event, out, err)
		}
	}
}

// Uninstalling takes out everything Agentman installed, Kiro's opt-in agent
// included — and only when the agent is Agentman's.
func TestUninstallRemovesKirosOptInAgent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	plan := hook.Installer{Home: home, Binary: "/opt/homebrew/bin/am"}.PlanKiro(false, func() (map[string]any, error) {
		return map[string]any{"name": "base", "description": "Default agent", "tools": []any{"*"}}, nil
	})
	if err := plan.Apply(); err != nil {
		t.Fatal(err)
	}
	stdoutR, stdoutW, _ := os.Pipe()
	oldOut := os.Stdout
	os.Stdout = stdoutW
	err := runInstallHooks(context.Background(), nil, true)
	os.Stdout = oldOut
	stdoutW.Close()
	out, _ := io.ReadAll(stdoutR)
	if err != nil {
		t.Fatalf("uninstall: %v\n%s", err, out)
	}
	if _, err := os.Stat(hook.KiroAgentPath(home)); !os.IsNotExist(err) {
		t.Errorf("the Kiro agent survived an uninstall:\n%s", out)
	}
}

// captureStdout runs fn with what it prints kept from the test's output, and
// returns it.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = write
	done := make(chan []byte)
	go func() {
		out, _ := io.ReadAll(read)
		done <- out
	}()
	defer func() { os.Stdout = old }()
	fn()
	write.Close()
	return string(<-done)
}

// fakeAgentHome is a HOME of its own with a Claude Code and a Codex in it,
// and a PATH holding only a claude command that writes down what it was
// asked and keeps the server it was given in ~/.claude.json, as the real
// one does, so nothing here reaches the user's own agents. Its path is kept
// short: a unix socket's may not pass 104 bytes on macOS, and the daemon's
// is in it.
func fakeAgentHome(t *testing.T) string {
	t.Helper()
	home, err := os.MkdirTemp("", "amh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	t.Setenv("HOME", home)
	for _, dir := range []string{".claude", ".codex"} {
		if err := os.MkdirAll(filepath.Join(home, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	bin := filepath.Join(home, "path")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	claude := "#!/bin/sh\necho \"$@\" >> \"$HOME/claude-calls\"\n" +
		"[ \"$1 $2\" = \"mcp add\" ] && printf '{\"mcpServers\":{\"agentman\":{\"command\":\"%s\",\"args\":[\"mcp\"]}}}' \"$7\" > \"$HOME/.claude.json\"\n" +
		"exit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(claude), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	return home
}

// putManagedCopy puts an am at the managed path, marked as the app's when
// marked is set.
func putManagedCopy(t *testing.T, home string, marked bool) string {
	t.Helper()
	managed := update.ManagedPath(home)
	if err := os.MkdirAll(filepath.Dir(managed), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(managed, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if marked {
		if err := update.WriteMarker(managed, update.Marker{AppVersion: "0.39.0"}); err != nil {
			t.Fatal(err)
		}
	}
	return managed
}

// Without the app, hooks and tools name this am as they always did, even
// with an am put at ~/.agentman/bin/am by hand; with the app's copy there,
// they name that copy, which the daemon runs and which never moves.
func TestHooksAndToolsNameTheAppsCopyWhenThereIsOne(t *testing.T) {
	home := fakeAgentHome(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	stable, err := stableBinary()
	if err != nil {
		t.Fatal(err)
	}
	managed := putManagedCopy(t, home, false)
	if got, _ := hookBinary(); got != stableBinaryPath(self) {
		t.Errorf("hooks without the app name %q, want %q", got, stableBinaryPath(self))
	}
	if got, _ := toolsBinary(); got != stable {
		t.Errorf("tools without the app name %q, want %q", got, stable)
	}

	if err := update.WriteMarker(managed, update.Marker{AppVersion: "0.39.0"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := hookBinary(); got != managed {
		t.Errorf("hooks name %q, want the app's copy", got)
	}
	if got, _ := toolsBinary(); got != managed {
		t.Errorf("tools name %q, want the app's copy", got)
	}

	var installErr error
	out := captureStdout(t, func() { installErr = runInstallHooks(context.Background(), nil, false) })
	if installErr != nil {
		t.Fatalf("install-hooks: %v\n%s", installErr, out)
	}
	settings, _ := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	codex, _ := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	calls, _ := os.ReadFile(filepath.Join(home, "claude-calls"))
	for name, content := range map[string][]byte{"Claude's hooks": settings, "Codex's config": codex, "claude mcp add": calls} {
		if !strings.Contains(string(content), managed) {
			t.Errorf("%s don't name the app's copy:\n%s", name, content)
		}
		if strings.Contains(string(content), self) {
			t.Errorf("%s name the am that installed them:\n%s", name, content)
		}
	}
}

func TestKiroResumesKeepTheirAgent(t *testing.T) {
	for args, want := range map[string]bool{
		"":                      false,
		"fix the build":         false,
		"--resume":              true,
		"-r":                    true,
		"--resume-id abc":       true,
		"--resume-id=abc":       true,
		"--resume-picker":       true,
		"--model claude-sonnet": false,
	} {
		if got := kiroResumes(strings.Fields(args)); got != want {
			t.Errorf("kiroResumes(%q) = %v, want %v", args, got, want)
		}
	}
}
