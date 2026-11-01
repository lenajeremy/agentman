package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/hook"
	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/update"
)

// fakeAm writes a program at path that says it is agentman v, as am
// version --json does. note makes two of the same version differ.
func fakeAm(t *testing.T, path, v, note string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf("#!/bin/sh\n# %s\n[ \"$1 $2\" = \"version --json\" ] && echo '{\"version\":\"%s\"}'\n", note, v)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// fakeApp makes an Agentman.app under dir carrying an am of version v, and
// returns that am.
func fakeApp(t *testing.T, dir, v string) string {
	t.Helper()
	contents := filepath.Join(dir, "Agentman.app", "Contents")
	am := filepath.Join(contents, "MacOS", "am")
	fakeAm(t, am, v, "the app's")
	plist := "<plist><dict><key>CFBundleShortVersionString</key>\n\t<string>" + v + "</string></dict></plist>\n"
	if err := os.WriteFile(filepath.Join(contents, "Info.plist"), []byte(plist), 0o644); err != nil {
		t.Fatal(err)
	}
	return am
}

// fakeDaemon answers on the daemon's socket as am serve does, with the
// version it was last started on, and hangs up while it isn't running.
type fakeDaemon struct {
	mu      sync.Mutex
	version string
}

func startFakeDaemon(t *testing.T, socket string) *fakeDaemon {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	daemon := &fakeDaemon{}
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go daemon.answer(conn)
		}
	}()
	return daemon
}

func (d *fakeDaemon) answer(conn net.Conn) {
	defer conn.Close()
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return
	}
	var envelope protocol.Envelope
	if json.Unmarshal(line, &envelope) != nil {
		return
	}
	d.mu.Lock()
	running := d.version
	d.mu.Unlock()
	if running == "" {
		return
	}
	payload, _ := json.Marshal(protocol.Event{Type: protocol.EvtSessions, Daemon: &protocol.DaemonInfo{
		Version: running, APILevel: protocol.APILevel, Install: "managed",
	}})
	reply, _ := json.Marshal(protocol.Envelope{V: protocol.Version, ID: "reply", ReplyTo: envelope.ID, To: protocol.PeerApp, Payload: payload})
	_, _ = conn.Write(append(reply, '\n'))
}

func (d *fakeDaemon) run(v string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.version = v
}

// setupWorld is a Mac for am setup to set up: a HOME of its own with agents
// in it, a launchd that starts the fake daemon on whatever am the job names,
// and no terminal daemon unless a test adds one.
type setupWorld struct {
	home, plist string
	launchd     *fakeLaunchd
	daemon      *fakeDaemon
	stopped     *[]int
}

func newSetupWorld(t *testing.T, terminalPID int) *setupWorld {
	t.Helper()
	if runtime.GOOS != "darwin" {
		t.Skip("the login service is macOS's")
	}
	home := fakeAgentHome(t)
	w := &setupWorld{home: home, plist: filepath.Join(home, "Library", "LaunchAgents", "dev.agentman.serve.plist")}
	w.daemon = startFakeDaemon(t, filepath.Join(home, ".agentman", "am.sock"))
	w.launchd = &fakeLaunchd{pid: 100}
	w.launchd.onLoad = func() {
		args, err := serviceArguments(w.plist)
		if err != nil || len(args) == 0 {
			return
		}
		if sameFile(args[0], mustExecutable()) {
			// Never run the test binary itself: it would run the tests.
			w.daemon.run(strings.TrimPrefix(version, "v"))
			return
		}
		v, _ := binaryVersion(context.Background(), args[0])
		w.daemon.run(v)
	}
	w.launchd.use(t)
	w.stopped = fakeTerminalDaemon(t, terminalPID)
	return w
}

func (w *setupWorld) setUp(t *testing.T, opts setupOptions) (map[string]setupStep, setupSummary) {
	t.Helper()
	if opts.Wait == 0 {
		opts.Wait = 5 * time.Second
	}
	if opts.Self == "" {
		opts.Self = "/opt/homebrew/Caskroom/agentman/0.38.0/am"
	}
	steps := map[string]setupStep{}
	var order []string
	summary := setUp(context.Background(), opts, func(step setupStep) {
		steps[step.Step] = step
		order = append(order, step.Step)
	})
	if want := []string{"config", "binary", "hooks", "tools", "service", "daemon"}; summary.OK && !slices.Equal(order, want) {
		t.Errorf("steps came in the order %q, want %q", order, want)
	}
	return steps, summary
}

func statuses(steps map[string]setupStep) string {
	var out []string
	for _, name := range []string{"config", "binary", "hooks", "tools", "service", "daemon"} {
		out = append(out, name+"="+steps[name].Status)
	}
	return strings.Join(out, " ")
}

// The app's first run: am setup puts the app's am in place as the daemon's
// copy, marked as the app's, sets up every agent's hooks and tools on that
// copy, stops the am serve a terminal was running, and loads the service on
// the copy, waiting until the daemon answers on it. Run again, it finds
// nothing to do and touches nothing.
func TestSetupFromTheAppPutsEverythingInPlaceOnce(t *testing.T) {
	w := newSetupWorld(t, 4242)
	source := fakeApp(t, w.home, "0.39.0")
	managed := update.ManagedPath(w.home)

	steps, summary := w.setUp(t, setupOptions{From: source})
	if !summary.OK || statuses(steps) != "config=changed binary=changed hooks=changed tools=changed service=changed daemon=unchanged" {
		t.Fatalf("first run: %s\n%+v", statuses(steps), steps)
	}
	if summary.Binary != managed || !summary.Managed || summary.Version != "0.39.0" || summary.APILevel != protocol.APILevel ||
		summary.Socket != filepath.Join(w.home, ".agentman", "am.sock") || summary.Log != filepath.Join(w.home, ".agentman", "serve.log") {
		t.Errorf("summary = %+v", summary)
	}
	if marker, ok := update.ReadMarker(managed); !ok || marker.AppVersion != "0.39.0" || marker.ManagedBy != update.ManagingApp {
		t.Errorf("marker = %+v, %v", marker, ok)
	}
	if update.HowInstalled(managed) != update.AppManaged {
		t.Errorf("the copy is %v", update.HowInstalled(managed))
	}
	if got, _ := serviceBinary(w.plist); got != managed {
		t.Errorf("the service runs %q", got)
	}
	if !slices.Equal(*w.stopped, []int{4242}) {
		t.Errorf("the terminal's am serve: stopped %v", *w.stopped)
	}
	settings, _ := os.ReadFile(filepath.Join(w.home, ".claude", "settings.json"))
	if !strings.Contains(string(settings), `"command": "`+managed+`"`) {
		t.Errorf("Claude's hooks don't call the daemon's copy:\n%s", settings)
	}
	if calls, _ := os.ReadFile(filepath.Join(w.home, "claude-calls")); !strings.Contains(string(calls), "-- "+managed+" mcp") {
		t.Errorf("Claude wasn't given the tools on the daemon's copy: %s", calls)
	}
	loads := len(w.launchd.changes())

	steps, summary = w.setUp(t, setupOptions{From: source})
	if !summary.OK || statuses(steps) != "config=unchanged binary=unchanged hooks=unchanged tools=unchanged service=unchanged daemon=unchanged" {
		t.Fatalf("second run: %s\nhooks %+v\ntools %+v", statuses(steps), steps["hooks"], steps["tools"])
	}
	if got := w.launchd.changes(); len(got) != loads {
		t.Errorf("the second run changed launchd: %q", got[loads:])
	}
}

// After an app update the newer am goes in, and the service, already on
// the copy, is restarted onto it rather than loaded again.
func TestSetupRestartsTheServiceOnANewerCopy(t *testing.T) {
	w := newSetupWorld(t, 0)
	_, summary := w.setUp(t, setupOptions{From: fakeApp(t, w.home, "0.39.0")})
	if !summary.OK {
		t.Fatalf("first run: %+v", summary)
	}
	before := len(w.launchd.changes())
	steps, summary := w.setUp(t, setupOptions{From: fakeApp(t, filepath.Join(w.home, "new"), "0.40.0")})
	if !summary.OK || steps["binary"].Status != stepChanged || steps["service"].Status != stepChanged || summary.Version != "0.40.0" {
		t.Fatalf("after the update: %s %+v", statuses(steps), summary)
	}
	if got := w.launchd.changes()[before:]; !slices.Equal(got, []string{"kickstart -k " + serviceTarget()}) {
		t.Errorf("launchctl was asked %q, want only a restart", got)
	}
}

// A service running another am, Homebrew's, is taken over: the job is
// taken down and loaded again on the daemon's copy.
func TestSetupTakesTheServiceOverFromAnotherAm(t *testing.T) {
	w := newSetupWorld(t, 0)
	if err := os.MkdirAll(filepath.Dir(w.plist), 0o755); err != nil {
		t.Fatal(err)
	}
	old := renderServicePlist(servicePlist{Binary: "/opt/homebrew/bin/am", Log: "/l", Home: w.home, Path: "/usr/bin"})
	if err := os.WriteFile(w.plist, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	w.launchd.loaded = true
	w.daemon.run("0.38.0")

	steps, summary := w.setUp(t, setupOptions{From: fakeApp(t, w.home, "0.39.0")})
	if !summary.OK || summary.Version != "0.39.0" {
		t.Fatalf("%s %+v", statuses(steps), summary)
	}
	if !strings.Contains(steps["service"].Detail, "took over from /opt/homebrew/bin/am") {
		t.Errorf("service: %q", steps["service"].Detail)
	}
	target := fmt.Sprintf("gui/%d", os.Getuid())
	want := []string{"bootout " + serviceTarget(), "bootstrap " + target + " " + w.plist}
	if got := w.launchd.changes(); !slices.Equal(got, want) {
		t.Errorf("launchctl was asked %q, want %q", got, want)
	}
}

// From a terminal, with no app, am setup is install-hooks, mcp install and
// service install in one: the service and the hooks are on the am that ran
// it, as each of those commands would put them.
func TestSetupWithoutTheAppUsesThisAm(t *testing.T) {
	w := newSetupWorld(t, 0)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	steps, summary := w.setUp(t, setupOptions{Self: self})
	if !summary.OK || steps["binary"].Status != stepUnchanged || summary.Managed {
		t.Fatalf("%s %+v", statuses(steps), summary)
	}
	stable, _ := stableBinary()
	if got, _ := serviceBinary(w.plist); got != stable || summary.Binary != stable {
		t.Errorf("the service runs %q, want %q as am service install has it", got, stable)
	}
	settings, _ := os.ReadFile(filepath.Join(w.home, ".claude", "settings.json"))
	if !strings.Contains(string(settings), `"command": "`+stableBinaryPath(self)+`"`) {
		t.Errorf("Claude's hooks don't call this am as install-hooks records it:\n%s", settings)
	}
	if _, err := os.Stat(update.ManagedPath(w.home)); !errors.Is(err, os.ErrNotExist) {
		t.Error("a managed copy was made with no app")
	}
}

// Agentman's tools stay out when the user took them out, and a separate
// config home, a test daemon's, leaves the agents' hooks and tools alone.
func TestSetupLeavesAgentsAloneWhenItShould(t *testing.T) {
	w := newSetupWorld(t, 0)
	cfg, err := hook.LoadConfig("")
	if err != nil {
		t.Fatal(err)
	}
	cfg.NoAgentTools = true
	if err := hook.SaveConfig("", cfg); err != nil {
		t.Fatal(err)
	}
	steps, summary := w.setUp(t, setupOptions{From: fakeApp(t, w.home, "0.39.0")})
	if !summary.OK || steps["tools"].Status != stepSkipped || steps["hooks"].Status != stepChanged {
		t.Fatalf("with tools taken out: %s", statuses(steps))
	}
	if _, err := os.Stat(filepath.Join(w.home, "claude-calls")); err == nil {
		t.Error("Claude was given the tools the user took out")
	}

	other, err := os.MkdirTemp("", "amc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(other) })
	startFakeDaemon(t, filepath.Join(other, ".agentman", "am.sock")).run("0.39.0")
	steps, _ = w.setUp(t, setupOptions{ConfigHome: other, From: fakeApp(t, other, "0.39.0")})
	if steps["hooks"].Status != stepSkipped || steps["tools"].Status != stepSkipped {
		t.Errorf("with a separate config home: %s", statuses(steps))
	}
	if !update.IsManaged(update.ManagedPath(other)) || steps["binary"].Path != update.ManagedPath(other) {
		t.Errorf("the copy for a separate config home: %+v", steps["binary"])
	}
}

// A daemon that never answers fails the last step, and says where the log
// is; one that answers on another version than the copy also fails.
func TestSetupSaysWhenTheDaemonDoesntAnswer(t *testing.T) {
	w := newSetupWorld(t, 0)
	w.launchd.onLoad = nil
	steps, summary := w.setUp(t, setupOptions{From: fakeApp(t, w.home, "0.39.0"), Wait: 500 * time.Millisecond})
	if summary.OK || !slices.Equal(summary.Failed, []string{"daemon"}) || !strings.Contains(steps["daemon"].Detail, "serve.log") {
		t.Fatalf("%+v %+v", steps["daemon"], summary)
	}
	w.daemon.run("0.38.0")
	steps, summary = w.setUp(t, setupOptions{From: fakeApp(t, w.home, "0.39.0"), Wait: 500 * time.Millisecond})
	if summary.OK || !strings.Contains(steps["daemon"].Detail, "runs 0.38.0, not 0.39.0") {
		t.Fatalf("%+v %+v", steps["daemon"], summary)
	}
}

// Without a service (on Linux), the daemon step only says what answers.
func TestSetupWithoutAServiceOnlyReportsTheDaemon(t *testing.T) {
	dir, err := os.MkdirTemp("", "amd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "am.sock")
	daemon := startFakeDaemon(t, socket)
	if step, _ := setupDaemon(context.Background(), socket, "0.39.0", false, time.Second); step.Status != stepSkipped {
		t.Errorf("nothing running: %+v", step)
	}
	daemon.run("0.38.0")
	step, info := setupDaemon(context.Background(), socket, "0.39.0", false, time.Second)
	if step.Status != stepUnchanged || info == nil || !strings.Contains(step.Detail, "restart it to run 0.39.0") {
		t.Errorf("an older one running: %+v", step)
	}
}

// The daemon's copy gives way to the same release or a newer one, or when
// it doesn't run; it stays when it is the same am, a newer release, or a
// build of unreleased work on either side, unless forced.
func TestWhenTheDaemonsCopyGivesWay(t *testing.T) {
	broken := errors.New("exit status 137")
	for _, c := range []struct {
		existing  string
		err       error
		identical bool
		incoming  string
		force     bool
		want      bool
	}{
		{"0.38.0", nil, false, "0.39.0", false, true},
		{"0.39.0", nil, false, "0.39.0", false, true},
		{"0.39.0", nil, true, "0.39.0", false, false},
		{"0.40.0", nil, false, "0.39.0", false, false},
		{"0.40.0", nil, false, "0.39.0", true, true},
		{"", broken, false, "0.39.0", false, true},
		{"0.39.0-dev+abc", nil, false, "0.40.0", false, false},
		{"0.39.0-dev+abc", nil, false, "0.40.0", true, true},
		{"0.39.0", nil, false, "0.40.0-dev+abc", false, false},
	} {
		got, why := managedCopyGivesWay(c.existing, c.err, c.identical, c.incoming, c.force)
		if got != c.want || (!got && !c.identical && why == "") {
			t.Errorf("%+v: gives way %v (%q)", c, got, why)
		}
	}
}

// The copy is a file of its own, put in by rename: a link at its path is
// replaced, never written through, and a newer copy is kept and said so.
func TestTheDaemonsCopyIsAFileOfItsOwn(t *testing.T) {
	dir := t.TempDir()
	source := fakeApp(t, dir, "0.39.0")
	dest := update.ManagedPath(filepath.Join(dir, "home"))
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(source, dest); err != nil {
		t.Fatal(err)
	}
	result, err := installManaged(context.Background(), source, dest, "", false)
	if err != nil || !result.Changed || result.Version != "0.39.0" {
		t.Fatalf("installManaged = %+v, %v", result, err)
	}
	if info, err := os.Lstat(dest); err != nil || !info.Mode().IsRegular() || !update.IsManaged(dest) {
		t.Errorf("the copy is %v (%v)", info, err)
	}
	if marker, _ := update.ReadMarker(dest); marker.AppVersion != "0.39.0" {
		t.Errorf("the app's version wasn't read from the app: %+v", marker)
	}
	if data, _ := os.ReadFile(source); !strings.Contains(string(data), "the app's") {
		t.Error("the app's am was changed")
	}

	fakeAm(t, dest, "0.40.0", "upgraded")
	result, err = installManaged(context.Background(), source, dest, "", false)
	if err != nil || result.Changed || result.Version != "0.40.0" || !strings.Contains(result.Kept, "newer") {
		t.Errorf("over a newer copy: %+v, %v", result, err)
	}
	if _, err := installManaged(context.Background(), filepath.Join(dir, "missing"), dest, "", false); err == nil {
		t.Error("installed an am that isn't there")
	}
}

// am setup -json, what the app reads: one line per step, then the summary,
// each a JSON object of its own.
func TestSetupJSONIsALinePerStepThenASummary(t *testing.T) {
	w := newSetupWorld(t, 0)
	source := fakeApp(t, w.home, "0.39.0")
	var runErr error
	out := captureStdout(t, func() {
		runErr = runSetup(context.Background(), []string{"-json", "-from", source, "-app-version", "0.39.0"})
	})
	if runErr != nil {
		t.Fatalf("setup: %v\n%s", runErr, out)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	var names []string
	for _, line := range lines[:len(lines)-1] {
		var step map[string]any
		if err := json.Unmarshal([]byte(line), &step); err != nil {
			t.Fatalf("not JSON: %s", line)
		}
		names = append(names, step["step"].(string))
		if step["status"] == nil {
			t.Errorf("no status: %s", line)
		}
	}
	if want := []string{"config", "binary", "hooks", "tools", "service", "daemon"}; !slices.Equal(names, want) {
		t.Errorf("steps %q, want %q", names, want)
	}
	var summary setupSummary
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &summary); err != nil || summary.Step != "summary" || !summary.OK ||
		summary.APILevel != protocol.APILevel || !summary.Managed {
		t.Errorf("summary %s (%v)", lines[len(lines)-1], err)
	}
}
