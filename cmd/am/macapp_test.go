package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/update"
)

// useMacApp makes this test's Mac one with Agentman for Mac at version in
// an Applications folder of its own, released (id dev.agentman.mac) unless
// id says otherwise, and returns the app.
func useMacApp(t *testing.T, version, id string) string {
	t.Helper()
	folder := t.TempDir()
	if version != "" {
		if id == "" {
			id = update.MacAppID
		}
		contents := filepath.Join(folder, update.MacAppName, "Contents")
		if err := os.MkdirAll(contents, 0o755); err != nil {
			t.Fatal(err)
		}
		plist := "<plist><dict>\n\t<key>CFBundleIdentifier</key>\n\t<string>" + id + "</string>\n" +
			"\t<key>CFBundleShortVersionString</key>\n\t<string>" + version + "</string>\n</dict></plist>\n"
		if err := os.WriteFile(filepath.Join(contents, "Info.plist"), []byte(plist), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	oldFolders, oldOS := macAppFolders, hostOS
	macAppFolders = func() []string { return []string{folder} }
	hostOS = "darwin"
	t.Cleanup(func() { macAppFolders, hostOS = oldFolders, oldOS })
	return filepath.Join(folder, update.MacAppName)
}

// plan runs planAppFollow for am going to 0.40.0, with typed for input.
func plan(opts appOptions, terminal bool, typed string) (appFollow, bool, string) {
	var out bytes.Buffer
	follow, ok := planAppFollow("0.40.0", opts, terminal, strings.NewReader(typed), &out)
	return follow, ok, out.String()
}

const appQuestion = "Agentman for Mac 0.39.0 is installed. am 0.40.0 needs the app at 0.40.0 too.\n" +
	"  1) Update both (recommended)\n" +
	"  2) Only update am (the app may not work properly until it's updated)\n" +
	"Choose [1]: "

// At a terminal, am upgrade asks about an older app, and both is the
// answer to Return, to 1, and to input that ends; 2 updates am alone, and
// anything else is asked again.
func TestUpgradeAsksAboutAnOlderApp(t *testing.T) {
	app := useMacApp(t, "0.39.0", "")
	for _, c := range []struct {
		typed string
		want  update.AppChoice
		said  string
	}{
		{"\n", update.AppUpdate, appQuestion},
		{"1\n", update.AppUpdate, appQuestion},
		{"", update.AppUpdate, appQuestion + "\n"},
		{"2\n", update.AppSkip, appQuestion},
		{" 2 \n", update.AppSkip, appQuestion},
		{"yes\n\n", update.AppUpdate, appQuestion + "Choose 1 or 2 [1]: "},
		{"3\n2\n", update.AppSkip, appQuestion + "Choose 1 or 2 [1]: "},
	} {
		follow, ok, said := plan(appOptions{}, true, c.typed)
		if !ok || follow.Choice != c.want || said != c.said {
			t.Errorf("typing %q: %+v, %v, said:\n%s", c.typed, follow, ok, said)
		}
		if follow.App != (update.MacApp{Path: app, Version: "0.39.0"}) || follow.Target != "0.40.0" {
			t.Errorf("typing %q: the plan is %+v", c.typed, follow)
		}
	}
}

// With nobody at a terminal, or -yes, both are updated without asking;
// -only-am leaves the app without asking. Each says what it chose.
func TestUpgradeChoosesWithoutAsking(t *testing.T) {
	useMacApp(t, "0.39.0", "")
	for _, c := range []struct {
		name     string
		opts     appOptions
		terminal bool
		want     update.AppChoice
		said     string
	}{
		{"no terminal", appOptions{}, false, update.AppUpdate, "Agentman for Mac 0.39.0 is installed. am 0.40.0 needs the app at 0.40.0 too. Updating both.\n"},
		{"-yes", appOptions{Yes: true}, true, update.AppUpdate, "Agentman for Mac 0.39.0 is installed. am 0.40.0 needs the app at 0.40.0 too. Updating both.\n"},
		{"-only-am", appOptions{OnlyAm: true}, true, update.AppSkip, "Agentman for Mac 0.39.0 is installed; updating only am, as -only-am says.\n"},
		{"-only-am, no terminal", appOptions{OnlyAm: true}, false, update.AppSkip, "Agentman for Mac 0.39.0 is installed; updating only am, as -only-am says.\n"},
	} {
		follow, ok, said := plan(c.opts, c.terminal, "2\n")
		if !ok || follow.Choice != c.want || said != c.said {
			t.Errorf("%s: %+v, %v, said %q", c.name, follow, ok, said)
		}
	}
}

// Nothing is asked, or said, without an app, with one already at the new
// release or newer, with a Debug build or a development version, or on
// Linux, which has no Agentman for Mac.
func TestUpgradeLeavesTheAppAloneWhenItShould(t *testing.T) {
	for _, c := range []struct {
		name, version, id string
		linux             bool
	}{
		{"no app", "", "", false},
		{"the app at the new release", "0.40.0", "", false},
		{"a newer app", "0.41.0", "", false},
		{"a Debug build", "0.39.0", "dev.agentman.mac.debug", false},
		{"an app of no release", "0.39.0-dev", "", false},
		{"Linux", "0.39.0", "", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			useMacApp(t, c.version, c.id)
			if c.linux {
				hostOS = "linux"
			}
			if follow, ok, said := plan(appOptions{}, true, "1\n"); ok || said != "" {
				t.Errorf("%+v, %v, said %q", follow, ok, said)
			}
			if follow, ok, said := plan(appOptions{Yes: true}, false, ""); ok || said != "" {
				t.Errorf("-yes: %+v, %v, said %q", follow, ok, said)
			}
		})
	}
}

// The choice is left in the config folder's app-update.json, for this user
// only, for the version am goes to.
func TestUpgradeLeavesTheChoiceForTheApp(t *testing.T) {
	home := t.TempDir()
	follow := appFollow{App: update.MacApp{Path: "/Applications/Agentman.app", Version: "0.39.0"}, Target: "0.40.0", Choice: update.AppSkip}
	if err := recordAppFollow(home, follow); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".agentman")
	intent, ok := update.ReadAppIntent(dir)
	if !ok || intent.Version != "0.40.0" || intent.Choice != update.AppSkip || intent.At == 0 {
		t.Errorf("app-update.json = %+v, %v", intent, ok)
	}
	if info, err := os.Stat(update.AppUpdatePath(dir)); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("app-update.json is %v (%v)", info, err)
	}
}

// After the upgrade, am says what the app does next: an open app connected
// to the new am serve is updating already; otherwise it updates when it
// next connects; one left as it is offers its update itself.
func TestUpgradeSaysWhatTheAppDoesNext(t *testing.T) {
	dir, err := os.MkdirTemp("", "amn")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "am.sock")
	daemon := startFakeDaemon(t, socket)
	running := false
	old := macAppRunning
	macAppRunning = func(bundle string) bool { return running && bundle == "/Applications/Agentman.app" }
	t.Cleanup(func() { macAppRunning = old })

	app := update.MacApp{Path: "/Applications/Agentman.app", Version: "0.39.0"}
	both := appFollow{App: app, Target: "0.40.0", Choice: update.AppUpdate}
	next := func(follow appFollow) string { return appFollowNext(context.Background(), follow, socket) }

	if got := next(both); got != "Agentman for Mac will update to 0.40.0 when it next connects." {
		t.Errorf("closed: %q", got)
	}
	running = true
	daemon.run("0.39.0")
	if got := next(both); got != "Agentman for Mac will update to 0.40.0 when it next connects." {
		t.Errorf("open, with am serve not restarted: %q", got)
	}
	daemon.run("0.40.0")
	if got := next(both); got != "Agentman for Mac is updating to match." {
		t.Errorf("open, on the new am serve: %q", got)
	}
	if got := next(appFollow{App: app, Target: "0.40.0", Choice: update.AppSkip}); got != "Agentman for Mac stays at 0.39.0 for now; it offers its update in its window." {
		t.Errorf("only am: %q", got)
	}
}

// am upgrade -check says which app is installed beside am.
func TestUpgradeCheckReportsTheApp(t *testing.T) {
	older := update.MacApp{Path: "/Applications/Agentman.app", Version: "0.39.0"}
	for _, c := range []struct {
		app       update.MacApp
		upgrading bool
		want      string
	}{
		{older, true, "Agentman for Mac 0.39.0 is installed; am upgrade offers to update it to 0.40.0 too."},
		{older, false, "Agentman for Mac 0.39.0 is installed, older than am; open it to update it to match."},
		{update.MacApp{Version: "0.40.0"}, true, "Agentman for Mac 0.40.0 is installed."},
		{update.MacApp{Version: "0.41.0"}, false, "Agentman for Mac 0.41.0 is installed."},
	} {
		if got := describeMacApp(c.app, "0.40.0", c.upgrading); got != c.want {
			t.Errorf("describeMacApp(%+v, %v) = %q", c.app, c.upgrading, got)
		}
	}
}

// -yes and -only-am contradict each other, which is said before anything
// is looked up.
func TestUpgradeRefusesBothAnswers(t *testing.T) {
	err := runUpgrade(context.Background(), []string{"-yes", "-only-am"})
	if err == nil || !strings.Contains(err.Error(), "choose one") {
		t.Errorf("runUpgrade(-yes -only-am) = %v", err)
	}
}
