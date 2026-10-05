package main

import (
	"context"
	"io"
	"net"
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
