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
