package tmux

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// legacyServer stands a second private server in for the one terminals
// share, where agents started before they had their own, and returns its
// socket. Nothing here reaches the developer's own tmux.
func legacyServer(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "amlegacy")
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "sock")
	t.Setenv(LegacySocketEnv, socket)
	t.Cleanup(func() {
		out, _ := exec.Command("tmux", "-S", socket, "list-sessions", "-F", "#{session_name}").Output()
		for _, name := range strings.Fields(string(out)) {
			_ = exec.Command("tmux", "-S", socket, "kill-session", "-t", "="+name).Run()
		}
		_ = os.RemoveAll(dir)
	})
	return socket
}

// startOn starts `cat > out` in a session on a server, as an older am did.
func startOn(t *testing.T, socket, name string) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "out.txt")
	if err := exec.Command("tmux", "-S", socket, "new-session", "-d", "-s", name, "sh", "-c", "cat > "+out).Run(); err != nil {
		t.Fatalf("start on %s: %v", socket, err)
	}
	waitFor(t, func() bool { _, err := os.Stat(out); return err == nil }, "session did not start")
	return out
}

// Agents started before they had a server of their own are still found and
// still reached, alongside the new ones, until they end.
func TestAgentsOnTheSharedServerAreStillReached(t *testing.T) {
	requireTmux(t)
	socket := legacyServer(t)
	old := "agentman-claude-old-" + filepath.Base(t.TempDir())
	oldOut := startOn(t, socket, old)
	newer, newOut := newSink(t)

	sessions, err := List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, session := range sessions {
		names = append(names, session.Name)
	}
	if !slices.Contains(names, old) || !slices.Contains(names, newer) {
		t.Fatalf("listed %q; want both %q and %q", names, old, newer)
	}

	// Each message goes to the server its session runs on.
	if err := Send(context.Background(), old, "to the old one"); err != nil {
		t.Fatal(err)
	}
	if err := Send(context.Background(), newer, "to the new one"); err != nil {
		t.Fatal(err)
	}
	readSoon(t, oldOut, "to the old one")
	readSoon(t, newOut, "to the new one")

	// And a terminal opens on the right one.
	args := AttachArgs(context.Background(), old)
	if !slices.Contains(args, socket) || args[len(args)-1] != "="+old {
		t.Fatalf("attach for the old session: %q", args)
	}
	args = AttachArgs(context.Background(), newer)
	if !slices.Contains(args, os.Getenv(SocketEnv)) {
		t.Fatalf("attach for the new session: %q", args)
	}
}

// A sweep captures every pane in one run per server.
func TestCaptureReachesPanesOnBothServers(t *testing.T) {
	requireTmux(t)
	socket := legacyServer(t)
	old := "agentman-claude-shot-" + filepath.Base(t.TempDir())
	startOn(t, socket, old)
	newer, _ := newSink(t)
	ctx, done := WithSweep(context.Background())
	defer done()
	if _, err := List(ctx); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{old, newer} {
		if _, ok := sweepOf(ctx).capture(name); !ok {
			t.Errorf("the sweep didn't capture %s", name)
		}
	}
}

// The agents' server stays up with nothing on it and nobody attached, so the
// first agent never has to start it from a terminal.
func TestTheAgentsServerStaysUpEmpty(t *testing.T) {
	requireTmux(t)
	ctx := context.Background()
	if err := EnsureServer(ctx); err != nil {
		t.Fatal(err)
	}
	if ServerPID(ctx) == 0 {
		t.Fatal("no server after EnsureServer")
	}
	for option, want := range map[string]string{"exit-empty": "off", "exit-unattached": "off"} {
		out, err := runOn(ctx, ours(), "show-options", "-sv", option)
		if err != nil || strings.TrimSpace(out) != want {
			t.Errorf("%s = %q (%v), want %s", option, strings.TrimSpace(out), err, want)
		}
	}
	out, err := runOn(ctx, ours(), "show-options", "-gv", "destroy-unattached")
	if err != nil || strings.TrimSpace(out) != "off" {
		t.Errorf("destroy-unattached = %q (%v), want off", strings.TrimSpace(out), err)
	}
	// Twice is fine: the second finds it running.
	if err := EnsureServer(ctx); err != nil {
		t.Fatal(err)
	}
}

// An agent started from a terminal gets that terminal's environment, as a
// command typed there would.
func TestLaunchGivesTheAgentItsEnvironment(t *testing.T) {
	requireTmux(t)
	dir := t.TempDir()
	out := filepath.Join(dir, "env.txt")
	name := "agentman-test-env-" + filepath.Base(dir)
	if err := Launch(context.Background(), name, dir, []string{"sh", "-c", "printf %s \"$AGENTMAN_PROBE\" > " + out + "; sleep 30"},
		"AGENTMAN_PROBE=from the terminal"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = Kill(context.Background(), name) })
	readSoon(t, out, "from the terminal")
}

// The shared server is asked every sweep while agents still run there, and
// once it has none, only now and then: no agent starts there any more.
func TestTheSharedServerIsAskedRarelyOnceItHasNoAgents(t *testing.T) {
	requireTmux(t)
	socket := legacyServer(t)
	legacy := server{"-S", socket}
	listedNames := func() []string {
		sessions, err := List(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, session := range sessions {
			names = append(names, session.Name)
		}
		return names
	}

	// No server there at all is as quiet as one with no agents.
	listedNames()
	if dueForListing(legacy, time.Now()) {
		t.Fatal("a missing server is asked again on the next sweep")
	}
	old := "agentman-claude-quiet-" + filepath.Base(t.TempDir())
	startOn(t, socket, old)
	if slices.Contains(listedNames(), old) {
		t.Fatal("a quiet server was asked before its wait was out")
	}
	if !dueForListing(legacy, time.Now().Add(quietServerWait)) {
		t.Fatal("a quiet server is never asked again")
	}

	quiet.mu.Lock()
	quiet.next[legacy.key()] = time.Now().Add(-time.Second)
	quiet.mu.Unlock()
	if !slices.Contains(listedNames(), old) {
		t.Fatal("the agent on the shared server was not found once its wait was out")
	}
	if !dueForListing(legacy, time.Now()) || !slices.Contains(listedNames(), old) {
		t.Fatal("a server with an agent on it is not asked every sweep")
	}
	if !dueForListing(ours(), time.Now()) {
		t.Fatal("ours is not asked every sweep")
	}
}

// Only a definite "nothing here" quiets a server: a listing that failed for
// another reason may have missed running agents.
func TestOnlyANoServerErrorQuietsAServer(t *testing.T) {
	legacy := server{"-S", "/tmp/agentman-quiet-errors"}
	t.Cleanup(func() {
		quiet.mu.Lock()
		delete(quiet.next, legacy.key())
		quiet.mu.Unlock()
	})
	now := time.Now()
	noteListing(legacy, 0, errors.New("signal: killed"), now)
	if !dueForListing(legacy, now) {
		t.Fatal("a failed listing quieted the server")
	}
	noteListing(legacy, 0, errors.New("no server running on /tmp/agentman-quiet-errors"), now)
	if dueForListing(legacy, now) {
		t.Fatal("no server there did not quiet it")
	}
	noteListing(legacy, 1, nil, now)
	if !dueForListing(legacy, now) {
		t.Fatal("an agent found there left it quiet")
	}
}

func TestCommandsGoToTheServerOfTheSessionTheyName(t *testing.T) {
	t.Setenv(LegacySocketEnv, "/tmp/legacy-for-routing")
	rememberAll(map[string]server{"agentman-old": {"-S", "/tmp/legacy-for-routing"}})
	t.Cleanup(func() { rememberAll(map[string]server{}) })
	for _, args := range [][]string{
		{"send-keys", "-t", "agentman-old", "x"},
		{"capture-pane", "-t", "=agentman-old", "-p"},
		{"display-message", "-t", "agentman-old:0.0", "-p", "#{pid}"},
	} {
		if got := serverFor(args); !slices.Equal(got, server{"-S", "/tmp/legacy-for-routing"}) {
			t.Errorf("%q went to %q", args, got)
		}
	}
	if got := serverFor([]string{"send-keys", "-t", "agentman-new", "x"}); !slices.Equal(got, ours()) {
		t.Errorf("an unseen session went to %q, not ours", got)
	}
	if got := serverFor([]string{"list-sessions"}); !slices.Equal(got, ours()) {
		t.Errorf("an untargeted command went to %q, not ours", got)
	}
}
