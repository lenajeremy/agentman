package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// fakeLaunchd stands in for launchctl: it keeps whether the job is loaded
// and every command it was given, and runs onLoad when a job is loaded or
// restarted, where a test starts its stand-in daemon.
type fakeLaunchd struct {
	mu     sync.Mutex
	loaded bool
	pid    int
	calls  []string
	onLoad func()
}

func (f *fakeLaunchd) use(t *testing.T) {
	t.Helper()
	old := launchctlCommand
	launchctlCommand = f.run
	t.Cleanup(func() { launchctlCommand = old })
}

func (f *fakeLaunchd) run(args ...string) ([]byte, error) {
	f.mu.Lock()
	f.calls = append(f.calls, strings.Join(args, " "))
	var onLoad func()
	switch args[0] {
	case "print":
		defer f.mu.Unlock()
		if !f.loaded {
			return []byte("Could not find service in domain"), errors.New("exit status 113")
		}
		return []byte(fmt.Sprintf("gui/501/dev.agentman.serve = {\n\tstate = running\n\tpid = %d\n}\n", f.pid)), nil
	case "bootout":
		f.loaded = false
	case "bootstrap", "kickstart":
		f.loaded = true
		onLoad = f.onLoad
	}
	f.mu.Unlock()
	if onLoad != nil {
		onLoad()
	}
	return nil, nil
}

// changes are the commands that would have changed launchd, without the
// questions asked of it.
func (f *fakeLaunchd) changes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, call := range f.calls {
		if !strings.HasPrefix(call, "print ") {
			out = append(out, call)
		}
	}
	return out
}

// fakeTerminalDaemon makes an am serve started from a terminal appear, as
// pid, until it is stopped.
func fakeTerminalDaemon(t *testing.T, pid int) *[]int {
	t.Helper()
	var mu sync.Mutex
	running := pid > 0
	var stopped []int
	oldFind, oldStop, oldAlive := findTerminalDaemon, stopProcess, processAlive
	findTerminalDaemon = func(string) int {
		mu.Lock()
		defer mu.Unlock()
		if running {
			return pid
		}
		return 0
	}
	stopProcess = func(p int) error {
		mu.Lock()
		defer mu.Unlock()
		stopped = append(stopped, p)
		running = false
		return nil
	}
	processAlive = func(int) bool {
		mu.Lock()
		defer mu.Unlock()
		return running
	}
	t.Cleanup(func() { findTerminalDaemon, stopProcess, processAlive = oldFind, oldStop, oldAlive })
	return &stopped
}

// am service install takes over from whatever daemon runs: the job loaded
// now is taken down, a terminal's am serve is stopped, and the new job is
// written under HOME and loaded. Nothing here reaches the real launchd.
func TestTheServiceTakesOverFromAnyOtherDaemon(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	launchd := &fakeLaunchd{loaded: true, pid: 100}
	launchd.use(t)
	stopped := fakeTerminalDaemon(t, 4242)

	var said []string
	binary := filepath.Join(home, ".agentman", "bin", "am")
	if err := putServiceOn(binary, "", func(line string) { said = append(said, line) }); err != nil {
		t.Fatal(err)
	}
	plist := filepath.Join(home, "Library", "LaunchAgents", "dev.agentman.serve.plist")
	target := fmt.Sprintf("gui/%d", os.Getuid())
	want := []string{"bootout " + target + "/dev.agentman.serve", "bootstrap " + target + " " + plist}
	if got := launchd.changes(); !slices.Equal(got, want) {
		t.Errorf("launchctl was asked %q, want %q", got, want)
	}
	if !slices.Equal(*stopped, []int{4242}) || len(said) != 1 || !strings.Contains(said[0], "4242") {
		t.Errorf("the terminal's am serve: stopped %v, said %q", *stopped, said)
	}
	if got, err := serviceBinary(plist); err != nil || got != binary {
		t.Errorf("the job runs %q (%v), want %q", got, err, binary)
	}
	content, _ := os.ReadFile(plist)
	if !strings.Contains(string(content), filepath.Join(home, ".agentman", "serve.log")) {
		t.Errorf("the log is no longer ~/.agentman/serve.log:\n%s", content)
	}
}

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

// From another agent's shell, its environment would make the new agent its
// child (no transcript, nothing for Agentman to follow): am serve's is used.
func TestAgentsStartedFromAnotherAgentDontInheritItsSession(t *testing.T) {
	env := agentEnv([]string{"PATH=/usr/bin", "CLAUDECODE=1", "CLAUDE_CODE_CHILD_SESSION=1", "CLAUDE_CODE_SESSION_ID=abc"})
	if env != nil {
		t.Fatalf("got %q, want none (am serve's environment)", env)
	}
	if env := agentEnv([]string{"PATH=/usr/bin", "CLAUDECODE="}); !slices.Equal(env, []string{"PATH=/usr/bin"}) {
		t.Fatalf("an empty CLAUDECODE isn't a session: got %q", env)
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
