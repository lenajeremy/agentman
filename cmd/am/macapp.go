package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/lenajeremy/agentman/internal/hook"
	"github.com/lenajeremy/agentman/internal/update"
)

// Agentman for Mac follows am. The two carry the same version and are
// released together, so when am upgrade takes am past the app's version it
// asks whether the app should update too, both being the answer unless the
// user says otherwise. The answer is left in ~/.agentman/app-update.json,
// written before the daemon restarts, so the app finds it when it connects
// to the new am serve and acts on it through its own updates. am never
// changes the app: only the app's updates replace it, and they never take
// it backwards. On Linux, and on a Mac without the app, am upgrade asks
// nothing, as before.

// macAppFolders are where am upgrade looks for Agentman for Mac; tests
// point it at folders of their own.
var macAppFolders = func() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return []string{"/Applications"}
	}
	return update.MacAppFolders(home)
}

// hostOS is the system am runs on; a variable so tests can be Linux.
var hostOS = runtime.GOOS

// macAppRunning reports whether the app at bundle is open: whether a
// process runs the program inside it. Tests replace it.
var macAppRunning = func(bundle string) bool {
	out, err := exec.Command("ps", "-axo", "comm=").Output()
	if err != nil {
		return false
	}
	inside := strings.TrimSuffix(bundle, "/") + "/Contents/MacOS/"
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), inside) {
			return true
		}
	}
	return false
}

// installedMacApp is Agentman for Mac, on a Mac that has it.
func installedMacApp() (update.MacApp, bool) {
	if hostOS != "darwin" {
		return update.MacApp{}, false
	}
	return update.FindMacApp(macAppFolders())
}

// appOptions are am upgrade's answers about the app, given ahead.
type appOptions struct {
	// Yes updates the app too, without asking.
	Yes bool
	// OnlyAm leaves the app as it is, without asking.
	OnlyAm bool
}

// appFollow is what happens to Agentman for Mac when am goes to Target.
type appFollow struct {
	App    update.MacApp
	Target string
	Choice update.AppChoice
}

// planAppFollow settles what happens to the app when am goes to target:
// nothing when there is no app, or it is at target already or newer, or
// either isn't a release. Otherwise the flags say, or the person at the
// terminal does; with no one to ask, both are updated.
func planAppFollow(target string, opts appOptions, terminal bool, in io.Reader, out io.Writer) (appFollow, bool) {
	app, ok := installedMacApp()
	if !ok || !update.Before(app.Version, target) {
		return appFollow{}, false
	}
	follow := appFollow{App: app, Target: target}
	situation := fmt.Sprintf("%s %s is installed. am %s needs the app at %s too.", update.ManagingApp, app.Version, target, target)
	switch {
	case opts.OnlyAm:
		follow.Choice = update.AppSkip
		fmt.Fprintf(out, "%s %s is installed; updating only am, as -only-am says.\n", update.ManagingApp, app.Version)
	case opts.Yes || !terminal:
		follow.Choice = update.AppUpdate
		fmt.Fprintf(out, "%s Updating both.\n", situation)
	default:
		follow.Choice = askAboutApp(situation, in, out)
	}
	return follow, true
}

// askAboutApp asks whether to update the app as well as am, both being the
// answer to an empty line, or to no answer at all.
func askAboutApp(situation string, in io.Reader, out io.Writer) update.AppChoice {
	fmt.Fprintf(out, "%s\n"+
		"  1) Update both (recommended)\n"+
		"  2) Only update am (the app may not work properly until it's updated)\n"+
		"Choose [1]: ", situation)
	reader := bufio.NewReader(in)
	for {
		line, err := reader.ReadString('\n')
		switch strings.TrimSpace(line) {
		case "2":
			return update.AppSkip
		case "1":
			return update.AppUpdate
		case "":
			if err == nil {
				return update.AppUpdate
			}
		}
		if err != nil {
			// Input ended without an answer: the default, on a line of
			// its own.
			fmt.Fprintln(out)
			return update.AppUpdate
		}
		fmt.Fprint(out, "Choose 1 or 2 [1]: ")
	}
}

// recordAppFollow leaves the choice in app-update.json for the app to read.
func recordAppFollow(configHome string, follow appFollow) error {
	dir, err := hook.ConfigDir(configHome)
	if err != nil {
		return err
	}
	return update.WriteAppIntent(dir, update.AppIntent{Version: follow.Target, Choice: follow.Choice})
}

// appFollowNext says what the app does next, once am is upgraded. An open
// app connected to am serve on the new version is updating already; else
// it updates when it next connects. One left as it is offers its update in
// its window.
func appFollowNext(ctx context.Context, follow appFollow, socket string) string {
	if follow.Choice == update.AppSkip {
		return fmt.Sprintf("%s stays at %s for now; it offers its update in its window.", update.ManagingApp, follow.App.Version)
	}
	if macAppRunning(follow.App.Path) {
		if running, ok := daemonVersion(ctx, socket); ok && running == follow.Target {
			return update.ManagingApp + " is updating to match."
		}
	}
	return fmt.Sprintf("%s will update to %s when it next connects.", update.ManagingApp, follow.Target)
}

// describeMacApp is am upgrade -check's line about the app, beside am at
// target: its version, and whether it is behind.
func describeMacApp(app update.MacApp, target string, upgrading bool) string {
	switch {
	case !update.Before(app.Version, target):
		return fmt.Sprintf("%s %s is installed.", update.ManagingApp, app.Version)
	case upgrading:
		return fmt.Sprintf("%s %s is installed; am upgrade offers to update it to %s too.", update.ManagingApp, app.Version, target)
	default:
		return fmt.Sprintf("%s %s is installed, older than am; open it to update it to match.", update.ManagingApp, app.Version)
	}
}
