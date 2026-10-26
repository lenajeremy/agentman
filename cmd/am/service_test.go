package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// The service's job file is one launchd reads: am serve at login, with the
// marker that makes it read the shell's environment, never taking its
// children (the agents' tmux server) down when it stops.
func TestServicePlistIsValidAndKeepsAgentsAlive(t *testing.T) {
	content := renderServicePlist(servicePlist{
		Binary: "/opt/homebrew/bin/am", Log: "/Users/me/.agentman/serve.log", Home: "/Users/me",
		Path: "/opt/homebrew/bin:/usr/bin & co",
	})
	for _, want := range []string{
		"<string>dev.agentman.serve</string>",
		"<string>/opt/homebrew/bin/am</string>\n\t\t<string>serve</string>",
		"<key>AGENTMAN_SERVICE</key>\n\t\t<string>1</string>",
		"<key>AbandonProcessGroup</key>\n\t<true/>",
		"<key>RunAtLoad</key>\n\t<true/>",
		"/opt/homebrew/bin:/usr/bin &amp; co",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("plist lacks %q", want)
		}
	}
	if strings.Contains(content, "-config-home") {
		t.Error("no config home was asked for")
	}
	if runtime.GOOS == "darwin" {
		path := filepath.Join(t.TempDir(), "job.plist")
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command("plutil", "-lint", path).CombinedOutput(); err != nil {
			t.Fatalf("plutil: %s", out)
		}
	}
	withHome := renderServicePlist(servicePlist{Binary: "/x/am", ConfigHome: "/tmp/test home", Log: "/l", Home: "/h", Path: "/p"})
	if !strings.Contains(withHome, "<string>-config-home</string>\n\t\t<string>/tmp/test home</string>") {
		t.Error("the config home should be passed to am serve")
	}
}

func TestLaunchStateIsReadFromLaunchctl(t *testing.T) {
	out := "gui/501/dev.agentman.serve = {\n\tactive count = 1\n\tpath = /Users/me/Library/LaunchAgents/dev.agentman.serve.plist\n\tstate = running\n\n\tprogram = /opt/homebrew/bin/am\n\tpid = 4242\n}\n"
	state, pid := parseLaunchState(out)
	if state != "running" || pid != 4242 {
		t.Fatalf("got %s %d", state, pid)
	}
	state, pid = parseLaunchState("gui/501/dev.agentman.serve = {\n\tstate = not running\n}\n")
	if state != "not" || pid != 0 {
		t.Fatalf("got %s %d", state, pid)
	}
}

// An agent started from a terminal keeps the shell's environment but not
// what describes the terminal: it runs in tmux, which sets those itself.
func TestAgentsKeepTheShellsEnvironmentButNotTheTerminals(t *testing.T) {
	env := agentEnv([]string{
		"PATH=/opt/homebrew/bin:/usr/bin", "ANTHROPIC_BASE_URL=https://example", "HOME=/Users/me",
		"TERM=xterm-ghostty", "TERM_PROGRAM=ghostty", "GHOSTTY_RESOURCES_DIR=/x", "TMUX=/tmp/s,1,0",
		"TMUX_PANE=%3", "PWD=/Users/me/app", "SHLVL=2", "__CFBundleIdentifier=com.mitchellh.ghostty", "broken",
	})
	want := []string{"PATH=/opt/homebrew/bin:/usr/bin", "ANTHROPIC_BASE_URL=https://example", "HOME=/Users/me"}
	if !slices.Equal(env, want) {
		t.Fatalf("got %q, want %q", env, want)
	}
}

func TestAttachPicksTheAgentMeant(t *testing.T) {
	agents := attachable([]protocol.Session{
		{ID: "aaa111", Name: "api-fix", Cwd: "/work/api", Inject: protocol.InjectTmux, State: protocol.StateBusy, LastActivityAt: 30},
		{ID: "bbb222", Name: "site", Cwd: "/work/site", Inject: protocol.InjectTmux, State: protocol.StateIdle, LastActivityAt: 20},
		{ID: "ccc333", Name: "watcher", Cwd: "/work/site", Inject: protocol.InjectNone, State: protocol.StateIdle, LastActivityAt: 40},
		{ID: "ddd444", Name: "old", Cwd: "/work/api", Inject: protocol.InjectTmux, State: protocol.StateEnded, LastActivityAt: 50},
	})
	if len(agents) != 2 || agents[0].ID != "aaa111" {
		t.Fatalf("attachable: %+v", agents)
	}
	pick := func(want, cwd string) string {
		agent, err := pickAgent(agents, want, cwd)
		if err != nil {
			return "error"
		}
		return agent.ID
	}
	for _, c := range []struct{ want, cwd, id string }{
		{"", "/work/site/src", "bbb222"}, // the only one in this folder
		{"", "/work", "error"},           // two here: which one?
		{"2", "/", "bbb222"},
		{"API-FIX", "/", "aaa111"},
		{"bbb", "/", "bbb222"},
		{"si", "/", "bbb222"},
		{"zzz", "/", "error"},
	} {
		if got := pick(c.want, c.cwd); got != c.id {
			t.Errorf("pick(%q in %s) = %s, want %s", c.want, c.cwd, got, c.id)
		}
	}
}
