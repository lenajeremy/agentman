package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/lenajeremy/agentman/internal/hook"
	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/shellenv"
	"github.com/lenajeremy/agentman/internal/update"
)

// am setup: everything Agentman needs on this computer, in one command that
// asks nothing and can be run again whenever, doing only what is missing.
// Agentman for Mac runs it on first launch, and after an update, with the
// am it carries; from a terminal it does what install-hooks, mcp install
// and service install do, one after another.
//
// The steps, each reported as it ends (with -json, as one JSON line, and a
// summary line last):
//
//   - config: ~/.agentman and its config, with the hook token, as am serve
//     makes them on first run.
//   - binary: with -from, or run from the am inside an app, the daemon's
//     copy of am put at ~/.agentman/bin/am and marked as the app's. A copy
//     already there gives way only to the same release or a newer one,
//     unless -force. Without either, the daemon runs this am.
//   - hooks and tools: agents' hooks, and Agentman's tools (am mcp), for
//     every agent found. Tools stay out if the user took them out.
//   - service: the login service on that am, taking over from any other
//     daemon, a terminal's am serve or a service running another am, so
//     only one ever runs. macOS only, as am service is.
//   - daemon: waits until am serve answers on its socket, running that am.

// The status of a step.
const (
	stepChanged   = "changed"
	stepUnchanged = "unchanged"
	stepSkipped   = "skipped"
	stepFailed    = "failed"
)

// setupStep is one step's line of am setup -json.
type setupStep struct {
	Step   string `json:"step"`
	Status string `json:"status"`
	// Detail says what happened, for a person.
	Detail string `json:"detail,omitempty"`
	// Path is the file the step is about: the config, the am, the job.
	Path string `json:"path,omitempty"`
	// Version is the am's, on the binary and daemon steps.
	Version string `json:"version,omitempty"`
	// APILevel is the running daemon's, on the daemon step.
	APILevel int `json:"apiLevel,omitempty"`
	// Agents are what the hooks and tools steps did for each agent.
	Agents []agentChange `json:"agents,omitempty"`
}

// setupSummary is am setup -json's last line.
type setupSummary struct {
	Step string `json:"step"`
	OK   bool   `json:"ok"`
	// Binary is the am the daemon runs, and Managed whether it is the copy
	// Agentman for Mac manages.
	Binary  string `json:"binary,omitempty"`
	Managed bool   `json:"managed"`
	// Version, APILevel and Install are what the running daemon reports;
	// empty and 0 when it didn't answer.
	Version  string `json:"version,omitempty"`
	APILevel int    `json:"apiLevel"`
	Install  string `json:"install,omitempty"`
	Socket   string `json:"socket"`
	Log      string `json:"log"`
	// Failed names the steps that failed.
	Failed []string `json:"failed,omitempty"`
}

// agentChange is what a step did for one agent.
type agentChange struct {
	Agent  string `json:"agent"`
	Status string `json:"status"`
	Path   string `json:"path,omitempty"`
	Error  string `json:"error,omitempty"`
}

// loginShellEnv reads the login shell's environment; tests replace it, so
// none runs the developer's own shell profile.
var loginShellEnv = shellenv.Load

type setupOptions struct {
	ConfigHome string
	// From is the am to install as the daemon's copy, with the version of
	// the app it came with; Force replaces a copy that is newer.
	From, AppVersion string
	Force            bool
	// Self is the am running, links followed.
	Self string
	// Wait bounds how long the daemon has to answer.
	Wait time.Duration
}

func runSetup(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "one JSON line per step, then a summary")
	from := fs.String("from", "", "install the daemon's copy of am, ~/.agentman/bin/am, from this am")
	appVersion := fs.String("app-version", "", "with -from: the version of the app it comes with")
	force := fs.Bool("force", false, "with -from: replace the copy even when it is newer or a development build")
	configHome := fs.String("config-home", "", "separate home for Agentman state")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("setup: unexpected argument %q", fs.Arg(0))
	}
	// Run by an app rather than from a terminal, am has the app's sparse
	// environment: without the login shell's PATH it wouldn't find the
	// agents' own commands (claude mcp add) or record a useful PATH in the
	// service's job.
	if !stdinIsTerminal() {
		if env, err := loginShellEnv(ctx); err == nil {
			shellenv.Apply(env)
		}
	}
	emit := printSetupStep
	if *asJSON {
		encoder := json.NewEncoder(os.Stdout)
		emit = func(step setupStep) { _ = encoder.Encode(step) }
	}
	summary := setUp(ctx, setupOptions{
		ConfigHome: *configHome, From: *from, AppVersion: *appVersion, Force: *force,
		Self: installedPath(), Wait: 30 * time.Second,
	}, emit)
	if *asJSON {
		_ = json.NewEncoder(os.Stdout).Encode(summary)
	} else if summary.OK {
		fmt.Println("\nAgentman is set up: am serve runs in the background from login, and your agents report to it.")
	}
	if !summary.OK {
		return fmt.Errorf("setup: %s didn't work, as said above", strings.Join(summary.Failed, ", "))
	}
	return nil
}

// setUp runs the steps, telling emit of each as it ends, and returns the
// summary. A step that leaves nothing for the next to stand on (no config,
// no am to run) ends it there.
func setUp(ctx context.Context, opts setupOptions, emit func(setupStep)) setupSummary {
	summary := setupSummary{Step: "summary"}
	report := func(step setupStep) {
		if step.Status == stepFailed {
			summary.Failed = append(summary.Failed, step.Step)
		}
		emit(step)
	}
	finish := func() setupSummary {
		summary.OK = len(summary.Failed) == 0
		return summary
	}

	config, cfg, err := setupConfig(opts.ConfigHome)
	report(config)
	if err != nil {
		return finish()
	}
	dir := filepath.Dir(config.Path)
	summary.Socket, summary.Log = filepath.Join(dir, "am.sock"), filepath.Join(dir, "serve.log")

	binary, changed := setupBinary(ctx, opts)
	report(binary)
	if binary.Status == stepFailed {
		return finish()
	}
	summary.Binary, summary.Managed = binary.Path, update.IsManaged(binary.Path)

	// Hooks and tools are the user's agents', and their hooks reach the
	// daemon on the default config; a separate config home's daemon, a
	// test one, leaves them alone.
	if opts.ConfigHome != "" {
		skip := "a separate config home: agents' hooks and tools stay with ~/.agentman"
		report(setupStep{Step: "hooks", Status: stepSkipped, Detail: skip})
		report(setupStep{Step: "tools", Status: stepSkipped, Detail: skip})
	} else {
		report(setupHooks(cfg.Token))
		report(setupTools(cfg))
	}

	service := setupService(ctx, binary.Path, binary.Version, opts.ConfigHome, summary.Socket, changed)
	report(service)
	if service.Status == stepFailed {
		return finish()
	}

	daemon, info := setupDaemon(ctx, summary.Socket, binary.Version, service.Status != stepSkipped, opts.Wait)
	if daemon.Status == stepFailed {
		daemon.Detail += "; see " + collapseHome(summary.Log)
	}
	report(daemon)
	if info != nil {
		summary.Version, summary.APILevel, summary.Install = info.Version, info.APILevel, info.Install
	}
	return finish()
}

// printSetupStep is a step's line for a person at a terminal.
func printSetupStep(step setupStep) {
	mark := "✓"
	switch step.Status {
	case stepSkipped:
		mark = "·"
	case stepFailed:
		mark = "✗"
	}
	fmt.Printf("  %s %-8s %s\n", mark, step.Step, step.Detail)
	for _, agent := range step.Agents {
		if agent.Error != "" {
			fmt.Printf("    %s %s\n", agent.Agent, dim(agent.Error))
		}
	}
}

func setupConfig(configHome string) (setupStep, hook.Config, error) {
	step := setupStep{Step: "config", Status: stepUnchanged}
	dir, err := hook.ConfigDir(configHome)
	if err != nil {
		step.Status, step.Detail = stepFailed, err.Error()
		return step, hook.Config{}, err
	}
	step.Path = filepath.Join(dir, "config.json")
	if _, err := os.Stat(step.Path); errors.Is(err, os.ErrNotExist) {
		step.Status = stepChanged
	}
	cfg, err := hook.LoadConfig(configHome)
	if err != nil {
		step.Status, step.Detail = stepFailed, err.Error()
		return step, cfg, err
	}
	step.Detail = collapseHome(step.Path)
	if step.Status == stepChanged {
		step.Detail += ", made with a new hook token"
	}
	return step, cfg, nil
}

// setupBinary settles which am the daemon runs: the daemon's copy, put in
// place from -from or from the app's own am, or, without either, a copy
// the app already manages, or this am. changed says a new file went in.
func setupBinary(ctx context.Context, opts setupOptions) (setupStep, bool) {
	step := setupStep{Step: "binary"}
	home := opts.ConfigHome
	if home == "" {
		var err error
		if home, err = os.UserHomeDir(); err != nil {
			step.Status, step.Detail = stepFailed, err.Error()
			return step, false
		}
	}
	managed := update.ManagedPath(home)
	source := opts.From
	if source == "" && update.InAppBundle(opts.Self) {
		source = opts.Self
	}
	if source == "" {
		step.Status = stepUnchanged
		detail := "the copy " + update.ManagingApp + " manages"
		if update.IsManaged(managed) {
			step.Path = managed
			step.Version, _ = binaryVersion(ctx, managed)
		} else {
			// This am, named as am service install names it: it is the
			// running program, so it says its version itself.
			var err error
			if step.Path, err = stableBinary(); err != nil {
				step.Status, step.Detail = stepFailed, err.Error()
				return step, false
			}
			step.Version, detail = strings.TrimPrefix(version, "v"), "this am"
		}
		step.Detail = fmt.Sprintf("%s, %s (%s)", collapseHome(step.Path), describeVersion(step.Version), detail)
		return step, false
	}
	result, err := installManaged(ctx, source, managed, opts.AppVersion, opts.Force)
	step.Path, step.Version = managed, result.Version
	switch {
	case err != nil:
		step.Status, step.Detail = stepFailed, err.Error()
	case result.Changed:
		step.Status = stepChanged
		step.Detail = fmt.Sprintf("%s, %s, installed from %s", collapseHome(managed), describeVersion(result.Version), collapseHome(source))
	default:
		step.Status = stepUnchanged
		step.Detail = fmt.Sprintf("%s, %s", collapseHome(managed), describeVersion(result.Version))
		if result.Kept != "" {
			step.Detail += ", kept: " + result.Kept
		}
	}
	return step, result.Changed
}

func describeVersion(v string) string {
	if v == "" {
		return "a version it doesn't say"
	}
	return "agentman " + v
}

// managedInstall is what putting the daemon's copy of am in place did.
type managedInstall struct {
	// Version is what the copy in place now says it is.
	Version string
	// Changed says a new file went in.
	Changed bool
	// Kept says why a copy already there was kept.
	Kept string
}

// installManaged puts the am at source at dest, the daemon's copy, and
// marks it as the app's. It is a new file renamed into place (see
// replaceBinary), so a daemon running the old one is never cut off, and
// links are not followed into dest: a link there is replaced by a copy of
// its own. A copy already at dest gives way when it doesn't run, or is an
// older or the same release, unless force says to replace it anyway.
func installManaged(ctx context.Context, source, dest, appVersion string, force bool) (managedInstall, error) {
	var result managedInstall
	real, err := filepath.EvalSymlinks(source)
	if err != nil {
		return result, fmt.Errorf("the am to install, %s: %w", collapseHome(source), err)
	}
	if info, err := os.Stat(real); err != nil || !info.Mode().IsRegular() {
		return result, fmt.Errorf("the am to install, %s, isn't a program", collapseHome(source))
	}
	incoming, err := binaryVersion(ctx, real)
	if err != nil {
		return result, fmt.Errorf("the am to install, %s, doesn't run: %w", collapseHome(source), err)
	}
	if appVersion == "" {
		if bundle := update.BundleOf(real); bundle != "" {
			appVersion = update.AppVersion(bundle)
		}
	}

	replace, kept := true, ""
	if info, err := os.Lstat(dest); err == nil && info.Mode().IsRegular() {
		data, readErr := os.ReadFile(dest)
		incomingData, incomingErr := os.ReadFile(real)
		identical := readErr == nil && incomingErr == nil && bytes.Equal(data, incomingData)
		existing, existingErr := binaryVersion(ctx, dest)
		replace, kept = managedCopyGivesWay(existing, existingErr, identical, incoming, force)
		if !replace {
			result.Version = existing
		}
	}
	if replace {
		data, err := os.ReadFile(real)
		if err != nil {
			return result, err
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return result, err
		}
		if err := replaceBinary(dest, data); err != nil {
			return result, fmt.Errorf("putting am at %s: %w", collapseHome(dest), err)
		}
		clearQuarantine(dest)
		if got, err := binaryVersion(ctx, dest); err != nil || got != incoming {
			return result, fmt.Errorf("the new am at %s doesn't run as %s", collapseHome(dest), incoming)
		}
		result.Version, result.Changed = incoming, true
	}
	result.Kept = kept
	if _, marked := update.ReadMarker(dest); result.Changed || !marked {
		if err := update.WriteMarker(dest, update.Marker{AppVersion: appVersion, InstalledAt: time.Now().UTC()}); err != nil {
			return result, fmt.Errorf("marking %s as %s's: %w", collapseHome(dest), update.ManagingApp, err)
		}
	}
	return result, nil
}

// managedCopyGivesWay decides whether the daemon's copy already in place
// is replaced by the am being installed, from what each says it is, and
// says why when it isn't. The same am stays; one that doesn't run goes; a
// release gives way to the same release or a newer one; a build of
// unreleased work, on either side, is kept unless forced, as am upgrade
// keeps one the service runs.
func managedCopyGivesWay(existing string, existingErr error, identical bool, incoming string, force bool) (bool, string) {
	switch {
	case identical:
		return false, ""
	case existingErr != nil || force:
		return true, ""
	case !update.IsRelease(existing):
		return false, fmt.Sprintf("it is %s, a development build (-force replaces it)", existing)
	case !update.IsRelease(incoming):
		return false, fmt.Sprintf("the am to install is %s, a development build (-force installs it)", incoming)
	case update.Before(incoming, existing):
		return false, fmt.Sprintf("it is %s, newer than %s", existing, incoming)
	default:
		return true, ""
	}
}

// clearQuarantine takes macOS's download quarantine off the copy, as the
// install script does, so the service isn't stopped by a Gatekeeper prompt
// nobody is there to answer.
func clearQuarantine(path string) {
	if runtime.GOOS == "darwin" {
		_ = exec.Command("xattr", "-d", "com.apple.quarantine", path).Run()
	}
}

func setupHooks(token string) setupStep {
	step := setupStep{Step: "hooks"}
	binary, err := hookBinary()
	if err != nil {
		step.Status, step.Detail = stepFailed, err.Error()
		return step
	}
	plans, err := hook.Installer{Binary: binary}.Plans(token, false)
	if err != nil {
		step.Status, step.Detail = stepFailed, err.Error()
		return step
	}
	step.Path = binary
	step.Agents = applyAgentPlans(plans)
	step.Status, step.Detail = summarizeAgents(step.Agents, "hooks call "+collapseHome(binary))
	return step
}

func setupTools(cfg hook.Config) setupStep {
	step := setupStep{Step: "tools"}
	if cfg.NoAgentTools {
		step.Status, step.Detail = stepSkipped, "you took Agentman's tools out of your agents; am mcp install puts them back"
		return step
	}
	binary, err := toolsBinary()
	if err != nil {
		step.Status, step.Detail = stepFailed, err.Error()
		return step
	}
	plans, err := hook.Installer{Binary: binary}.MCPPlans(false)
	if err != nil {
		step.Status, step.Detail = stepFailed, err.Error()
		return step
	}
	if len(plans) == 0 {
		step.Status, step.Detail = stepSkipped, "no agents found to give Agentman's tools to"
		return step
	}
	step.Path = binary
	step.Agents = applyAgentPlans(plans)
	step.Status, step.Detail = summarizeAgents(step.Agents, "am mcp from "+collapseHome(binary))
	return step
}

// applyAgentPlans applies each agent's plan and says what it did.
func applyAgentPlans(plans []hook.Plan) []agentChange {
	var changes []agentChange
	for _, plan := range plans {
		change := agentChange{Agent: string(plan.Kind), Path: plan.Path, Status: stepUnchanged}
		switch {
		case plan.Err != nil:
			change.Status, change.Error = stepFailed, plan.Err.Error()
		case !plan.Changed:
		default:
			if err := plan.Apply(); err != nil {
				change.Status, change.Error = stepFailed, err.Error()
			} else {
				change.Status = stepChanged
			}
		}
		changes = append(changes, change)
	}
	return changes
}

// summarizeAgents is a step's status and line from what it did for each
// agent: failed if any agent failed, changed if any changed.
func summarizeAgents(agents []agentChange, what string) (string, string) {
	status := stepUnchanged
	var changed, failed []string
	for _, agent := range agents {
		switch agent.Status {
		case stepFailed:
			failed = append(failed, agent.Agent)
		case stepChanged:
			changed = append(changed, agent.Agent)
		}
	}
	detail := what
	if len(changed) > 0 {
		status = stepChanged
		detail += "; set up for " + strings.Join(changed, ", ")
	}
	if len(failed) > 0 {
		status = stepFailed
		detail += "; couldn't set up " + strings.Join(failed, ", ")
	}
	if len(changed) == 0 && len(failed) == 0 {
		detail += "; already set up for every agent"
	}
	return status, detail
}

// setupService puts the login service on binary, unless it already runs
// it with nothing else serving: then it is left, or restarted when a new
// file went in at binary (a running am keeps the old one open) or the
// daemon answering on socket says it runs another version than wantVersion.
func setupService(ctx context.Context, binary, wantVersion, configHome, socket string, binaryChanged bool) setupStep {
	step := setupStep{Step: "service"}
	if runtime.GOOS != "darwin" {
		step.Status, step.Detail = stepSkipped, "no login service on this system yet; run am serve under systemd --user, or in a terminal"
		return step
	}
	plist, err := servicePlistPath()
	if err != nil {
		step.Status, step.Detail = stepFailed, err.Error()
		return step
	}
	step.Path = plist
	want := []string{binary, "serve"}
	if configHome != "" {
		want = append(want, "-config-home", configHome)
	}
	loaded := serviceLoaded()
	_, servicePID := serviceState()
	current, _ := serviceArguments(plist)
	terminal := findTerminalDaemon(configHome)
	if terminal == servicePID {
		terminal = 0
	}
	stale := false
	if info := daemonDetails(ctx, socket); info != nil && wantVersion != "" {
		stale = strings.TrimPrefix(info.Version, "v") != wantVersion
	}
	runs := "runs " + collapseHome(binary) + " at login"
	switch {
	case loaded && slices.Equal(current, want) && terminal == 0 && !binaryChanged && !stale:
		step.Status, step.Detail = stepUnchanged, runs
	case loaded && slices.Equal(current, want) && terminal == 0:
		if err := launchctl("kickstart", "-k", serviceTarget()); err != nil {
			step.Status, step.Detail = stepFailed, err.Error()
			return step
		}
		step.Status, step.Detail = stepChanged, runs+"; restarted to run "+describeVersion(wantVersion)+", agents keep running"
	default:
		var said []string
		if err := putServiceOn(binary, configHome, func(line string) { said = append(said, line) }); err != nil {
			step.Status, step.Detail = stepFailed, err.Error()
			return step
		}
		step.Status, step.Detail = stepChanged, runs
		if len(current) > 0 && current[0] != binary {
			step.Detail += "; took over from " + collapseHome(current[0])
		}
		if len(said) > 0 {
			step.Detail += "; " + strings.Join(said, "; ")
		}
	}
	return step
}

var (
	serviceArray = regexp.MustCompile(`(?s)<key>ProgramArguments</key>\s*<array>(.*?)</array>`)
	plistString  = regexp.MustCompile(`<string>([^<]*)</string>`)
)

// serviceArguments is what the service's job runs: the program and its
// arguments.
func serviceArguments(plist string) ([]string, error) {
	content, err := os.ReadFile(plist)
	if err != nil {
		return nil, err
	}
	array := serviceArray.FindSubmatch(content)
	if array == nil {
		return nil, fmt.Errorf("the service's job, %s, names no program", collapseHome(plist))
	}
	var args []string
	for _, match := range plistString.FindAllSubmatch(array[1], -1) {
		args = append(args, html.UnescapeString(string(match[1])))
	}
	return args, nil
}

// setupDaemon waits until am serve answers on socket running want, the
// version of the am the service was put on. Without a service (served
// false) it only reports what answers, if anything does.
func setupDaemon(ctx context.Context, socket, want string, served bool, wait time.Duration) (setupStep, *protocol.DaemonInfo) {
	step := setupStep{Step: "daemon", Path: socket}
	var info *protocol.DaemonInfo
	ask := func() bool {
		info = daemonDetails(ctx, socket)
		return info != nil && (!served || want == "" || strings.TrimPrefix(info.Version, "v") == want)
	}
	if !served {
		if !ask() {
			step.Status, step.Detail = stepSkipped, "am serve isn't running; start it with am serve"
			return step, nil
		}
	} else if !waitFor(wait, ask) {
		step.Status = stepFailed
		if info != nil {
			step.Version, step.APILevel = info.Version, info.APILevel
			step.Detail = fmt.Sprintf("am serve answers on %s but runs %s, not %s", collapseHome(socket), info.Version, want)
			return step, info
		}
		step.Detail = "am serve hasn't answered on " + collapseHome(socket)
		return step, nil
	}
	step.Status = stepUnchanged
	step.Version, step.APILevel = info.Version, info.APILevel
	step.Detail = fmt.Sprintf("am serve %s answers on %s", info.Version, collapseHome(socket))
	if !served && want != "" && strings.TrimPrefix(info.Version, "v") != want {
		step.Detail += fmt.Sprintf("; restart it to run %s", want)
	}
	return step, info
}

// daemonDetails is what am serve says about itself, if it answers: the
// details ride on the session list.
func daemonDetails(ctx context.Context, socket string) *protocol.DaemonInfo {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	event, err := socketCall(socket)(ctx, protocol.Request{Type: protocol.ReqListSessions})
	if err != nil {
		return nil
	}
	return event.Daemon
}
