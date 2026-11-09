package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"html"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/lenajeremy/agentman/internal/hook"
	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/update"
)

// am serve as a login service: started by the system when the user logs in,
// restarted if it crashes, and owned by no terminal. The agents' tmux server
// is started by am serve (see tmux.EnsureServer), so it belongs to the
// service too, and quitting a terminal app can't take agents down with it.

// serviceLabel names the launchd job and the systemd unit.
const serviceLabel = "dev.agentman.serve"

// serviceAppBundle is the app macOS's Login Items list shows the service
// under, rather than a bare "am".
const serviceAppBundle = "dev.agentman.mac"

// serviceEnv marks am serve as started by the service, which then reads the
// login shell's environment it would have had from a terminal.
const serviceEnv = "AGENTMAN_SERVICE"

func inService() bool { return os.Getenv(serviceEnv) == "1" }

func runService(ctx context.Context, args []string) error {
	command := "status"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		command, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet("service "+command, flag.ContinueOnError)
	configHome := fs.String("config-home", "", "separate home for Agentman state")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if runtime.GOOS != "darwin" {
		return errors.New("service: only macOS so far; run am serve under systemd --user meanwhile")
	}
	switch command {
	case "install":
		return installService(ctx, *configHome)
	case "uninstall":
		return uninstallService()
	case "restart":
		if !serviceLoaded() {
			return errors.New("service: not installed; am service install sets it up")
		}
		if err := launchctl("kickstart", "-k", serviceTarget()); err != nil {
			return err
		}
		fmt.Println("Restarted am serve. Agents keep running.")
		return nil
	case "status":
		return printServiceStatus(ctx, *configHome)
	default:
		return fmt.Errorf("service: unknown command %q (install, uninstall, restart, status)", command)
	}
}

func installService(ctx context.Context, configHome string) error {
	binary, err := stableBinary()
	if err != nil {
		return err
	}
	dir, err := hook.ConfigDir(configHome)
	if err != nil {
		return err
	}
	// The am inside an app moves and changes with the app, so the service
	// runs the daemon's copy of it, ~/.agentman/bin/am, put in place first.
	if self := installedPath(); update.InAppBundle(self) {
		if binary, err = serviceCopyFromApp(ctx, self, configHome); err != nil {
			return err
		}
	}
	if err := putServiceOn(binary, configHome, func(line string) { fmt.Println(line) }); err != nil {
		return err
	}
	socket := filepath.Join(dir, "am.sock")
	if !waitFor(30*time.Second, func() bool { return socketAnswers(socket) }) {
		return fmt.Errorf("service: installed, but am serve hasn't answered yet; see %s", collapseHome(filepath.Join(dir, "serve.log")))
	}
	fmt.Println("Agentman now runs in the background: it starts when you log in, and agents keep running when you quit a terminal.")
	return printServiceStatus(ctx, configHome)
}

// serviceCopyFromApp puts the daemon's copy of am in place from the am
// inside an app at self, as am setup does, and returns where it is.
func serviceCopyFromApp(ctx context.Context, self, configHome string) (string, error) {
	home := configHome
	if home == "" {
		var err error
		if home, err = os.UserHomeDir(); err != nil {
			return "", err
		}
	}
	managed := update.ManagedPath(home)
	result, err := installManaged(ctx, self, managed, "", false)
	if err != nil {
		return "", fmt.Errorf("service: %w", err)
	}
	if result.Changed {
		fmt.Printf("Installed %s from %s, for the service to run: it stays put when the app moves.\n",
			collapseHome(managed), filepath.Base(update.BundleOf(self)))
	}
	return managed, nil
}

// putServiceOn makes the login service run the am at binary, taking over
// from any other daemon so only one ever runs: the job loaded now, which
// keeps the plist it was loaded with, is taken down first, and an am serve
// started from a terminal is stopped. The new job is then written and
// loaded. Whether it answers is the caller's to wait for. say is told what
// is happening, a line at a time.
func putServiceOn(binary, configHome string, say func(string)) error {
	dir, err := hook.ConfigDir(configHome)
	if err != nil {
		return err
	}
	plist, err := servicePlistPath()
	if err != nil {
		return err
	}
	if serviceLoaded() {
		_ = launchctl("bootout", serviceTarget())
		waitFor(5*time.Second, func() bool { return !serviceLoaded() })
	}
	if pid := findTerminalDaemon(configHome); pid > 0 {
		say(fmt.Sprintf("Stopping am serve (pid %d), started from a terminal…", pid))
		_ = stopProcess(pid)
		if !waitFor(15*time.Second, func() bool { return !processAlive(pid) }) {
			return fmt.Errorf("service: am serve (pid %d) didn't stop; stop it, then run this again", pid)
		}
	}
	if err := os.MkdirAll(filepath.Dir(plist), 0o755); err != nil {
		return err
	}
	content := renderServicePlist(servicePlist{
		Binary:     binary,
		ConfigHome: configHome,
		Log:        filepath.Join(dir, "serve.log"),
		Home:       os.Getenv("HOME"),
		Path:       os.Getenv("PATH"),
	})
	if err := os.WriteFile(plist, []byte(content), 0o644); err != nil {
		return err
	}
	return launchctl("bootstrap", fmt.Sprintf("gui/%d", os.Getuid()), plist)
}

// launchctlCommand runs launchctl and returns what it printed. launchd is
// the same whatever HOME says, so tests put a fake here: the real one would
// act on the user's own service.
var launchctlCommand = func(args ...string) ([]byte, error) {
	return exec.Command("launchctl", args...).CombinedOutput()
}

// findTerminalDaemon, stopProcess and processAlive find and stop an am
// serve started from a terminal; tests replace them so no real process is
// ever signalled.
var (
	findTerminalDaemon = terminalDaemon
	stopProcess        = func(pid int) error { return syscall.Kill(pid, syscall.SIGINT) }
	processAlive       = func(pid int) bool { return syscall.Kill(pid, 0) == nil }
)

func uninstallService() error {
	plist, err := servicePlistPath()
	if err != nil {
		return err
	}
	if serviceLoaded() {
		if err := launchctl("bootout", serviceTarget()); err != nil {
			return err
		}
	}
	if err := os.Remove(plist); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	fmt.Println("Removed the service. Agents already running keep running; start am serve yourself to reach them.")
	return nil
}

func printServiceStatus(ctx context.Context, configHome string) error {
	plist, _ := servicePlistPath()
	_, statErr := os.Stat(plist)
	switch {
	case serviceLoaded():
		state, pid := serviceState()
		line := "running"
		if state != "running" {
			line = state
		}
		if pid > 0 {
			line += fmt.Sprintf(" (pid %d)", pid)
		}
		fmt.Printf("service    %s, starts at login\n", line)
	case statErr == nil:
		fmt.Println("service    installed but not loaded; am service install loads it")
	default:
		fmt.Println("service    not installed; am service install keeps agents running when you quit a terminal")
	}
	dir, err := hook.ConfigDir(configHome)
	if err != nil {
		return err
	}
	call := socketCall(filepath.Join(dir, "am.sock"))
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	event, err := call(ctx, protocol.Request{Type: protocol.ReqAgentsTmux})
	switch {
	case err != nil:
		fmt.Println("am serve   not reachable")
	case event.Tmux == nil:
		fmt.Println("am serve   running, but older: it can't host agents on a server of their own yet")
	default:
		owner := "started from a terminal"
		if event.Tmux.Service {
			owner = "run by the service"
		}
		fmt.Printf("am serve   %s\n", owner)
		fmt.Printf("agents     tmux server %s (pid %d)\n", collapseHome(event.Tmux.Socket), event.Tmux.PID)
	}
	return nil
}

// servicePlist is what the launchd job runs and where.
type servicePlist struct {
	Binary, ConfigHome, Log, Home, Path string
}

// renderServicePlist writes the job: am serve at login, kept alive if it
// crashes but not after it is stopped on purpose, and never taking the
// processes it started down with it when it stops (AbandonProcessGroup):
// those include the agents' tmux server.
func renderServicePlist(p servicePlist) string {
	argument := func(value string) string { return "\t\t<string>" + html.EscapeString(value) + "</string>\n" }
	args := argument(p.Binary) + argument("serve")
	if p.ConfigHome != "" {
		args += argument("-config-home") + argument(p.ConfigHome)
	}
	env := func(name, value string) string {
		return "\t\t<key>" + name + "</key>\n\t\t<string>" + html.EscapeString(value) + "</string>\n"
	}
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + serviceLabel + `</string>
	<key>AssociatedBundleIdentifiers</key>
	<string>` + serviceAppBundle + `</string>
	<key>ProgramArguments</key>
	<array>
` + args + `	</array>
	<key>EnvironmentVariables</key>
	<dict>
` + env(serviceEnv, "1") + env("PATH", p.Path) + `	</dict>
	<key>WorkingDirectory</key>
	<string>` + html.EscapeString(p.Home) + `</string>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
	<key>ThrottleInterval</key>
	<integer>10</integer>
	<key>AbandonProcessGroup</key>
	<true/>
	<key>ProcessType</key>
	<string>Interactive</string>
	<key>StandardOutPath</key>
	<string>` + html.EscapeString(p.Log) + `</string>
	<key>StandardErrorPath</key>
	<string>` + html.EscapeString(p.Log) + `</string>
</dict>
</plist>
`
}

func servicePlistPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", serviceLabel+".plist"), nil
}

func serviceTarget() string { return fmt.Sprintf("gui/%d/%s", os.Getuid(), serviceLabel) }

func serviceLoaded() bool {
	_, err := launchctlCommand("print", serviceTarget())
	return err == nil
}

var (
	launchState = regexp.MustCompile(`(?m)^\s*state = (\S+)`)
	launchPID   = regexp.MustCompile(`(?m)^\s*pid = (\d+)`)
)

// serviceState is what launchd says of the job: its state and process.
func serviceState() (string, int) {
	out, err := launchctlCommand("print", serviceTarget())
	if err != nil {
		return "not loaded", 0
	}
	return parseLaunchState(string(out))
}

func parseLaunchState(out string) (string, int) {
	state := "unknown"
	if match := launchState.FindStringSubmatch(out); match != nil {
		state = match[1]
	}
	pid := 0
	if match := launchPID.FindStringSubmatch(out); match != nil {
		pid, _ = strconv.Atoi(match[1])
	}
	return state, pid
}

func launchctl(args ...string) error {
	out, err := launchctlCommand(args...)
	if err != nil {
		return fmt.Errorf("launchctl %s: %s", args[0], strings.TrimSpace(string(out)))
	}
	return nil
}

// terminalDaemon is the pid of an am serve not run by the service, found by
// the hook port it holds, or 0.
func terminalDaemon(configHome string) int {
	cfg, err := hook.LoadConfig(configHome)
	if err != nil {
		return 0
	}
	_, port, err := net.SplitHostPort(cfg.ListenAddr())
	if err != nil {
		return 0
	}
	out, err := exec.Command("lsof", "-nP", "-iTCP:"+port, "-sTCP:LISTEN", "-t").Output()
	if err != nil {
		return 0
	}
	for _, field := range strings.Fields(string(out)) {
		pid, err := strconv.Atoi(field)
		if err != nil {
			continue
		}
		command, _ := exec.Command("ps", "-o", "command=", "-p", field).Output()
		if strings.Contains(string(command), "am") && strings.Contains(string(command), "serve") {
			return pid
		}
	}
	return 0
}

// socketAnswers reports whether am serve answers on its socket.
func socketAnswers(path string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := socketCall(path)(ctx, protocol.Request{Type: protocol.ReqAgentsTmux})
	return err == nil
}

func waitFor(limit time.Duration, done func() bool) bool {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if done() {
			return true
		}
		time.Sleep(250 * time.Millisecond)
	}
	return done()
}

// refuseBesideService stops a second am serve, started by hand, while the
// service runs one: two daemons would fight over the same agents.
func refuseBesideService() error {
	if inService() || runtime.GOOS != "darwin" || !serviceLoaded() {
		return nil
	}
	if state, _ := serviceState(); state != "running" {
		return nil
	}
	return errors.New("Agentman already runs in the background (am service status). " +
		"am service restart restarts it; am service uninstall stops it so you can run am serve yourself")
}
