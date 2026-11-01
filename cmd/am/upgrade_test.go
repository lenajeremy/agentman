package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/lenajeremy/agentman/internal/update"
)

// The program read back from the job am service install writes, even with
// a character the plist escapes in its path.
func TestTheServicesProgramIsReadFromItsJob(t *testing.T) {
	binary := "/Users/me/Tools & Bits/am"
	plist := filepath.Join(t.TempDir(), "dev.agentman.serve.plist")
	content := renderServicePlist(servicePlist{Binary: binary, Log: "/tmp/serve.log", Home: "/Users/me", Path: "/usr/bin"})
	if err := os.WriteFile(plist, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := serviceBinary(plist)
	if err != nil || got != binary {
		t.Fatalf("serviceBinary = %q, %v", got, err)
	}
}

// A copy of an older release is brought up to date; a build of unreleased
// work, or one that doesn't run, is left as it is; a copy already on the new
// release, or newer, keeps what it has.
func TestWhatHappensToTheServicesOwnCopy(t *testing.T) {
	for _, c := range []struct {
		copied string
		err    error
		want   copyAction
	}{
		{"0.30.0", nil, copyReplace},
		{"0.29.1", nil, copyReplace},
		{"0.31.0", nil, copyKeep},
		{"0.32.0", nil, copyKeep},
		{"0.30.0-dev+history-tab", nil, copyLeave},
		{"", errors.New("exit status 137"), copyLeave},
	} {
		if got := serviceCopyAction(c.copied, c.err, "0.31.0"); got != c.want {
			t.Errorf("serviceCopyAction(%q, %v) = %v, want %v", c.copied, c.err, got, c.want)
		}
	}
}

// The new am is a new file renamed over the old, never written into it: on
// Apple silicon an am rewritten in place is killed at launch.
func TestTheNewAmIsANewFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "am")
	if err := os.WriteFile(path, []byte("old am"), 0o755); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := replaceBinary(path, []byte("new am")); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(before, after) || before.Sys().(*syscall.Stat_t).Ino == after.Sys().(*syscall.Stat_t).Ino {
		t.Error("the old file was written into")
	}
	if data, _ := os.ReadFile(path); string(data) != "new am" || after.Mode().Perm() != 0o755 {
		t.Errorf("new am = %q, mode %v", data, after.Mode().Perm())
	}
	if leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".am.new-*")); len(leftovers) != 0 {
		t.Errorf("left %v behind", leftovers)
	}
}

func TestABinarysVersionIsWhatItSays(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "am")
	script := "#!/bin/sh\n[ \"$1 $2\" = \"version --json\" ] && echo '{\"version\":\"v0.31.0\"}'\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := binaryVersion(context.Background(), fake); err != nil || got != "0.31.0" {
		t.Fatalf("binaryVersion = %q, %v", got, err)
	}
	if _, err := binaryVersion(context.Background(), filepath.Join(dir, "missing")); err == nil {
		t.Error("a missing am reported a version")
	}
}

// No route puts a file inside an app bundle: not at a path in one, and not
// through a folder that is a link into one. A link at the path itself that
// points into an app is replaced, and the app's am is left as it was.
func TestNothingIsWrittenIntoAnApp(t *testing.T) {
	dir := t.TempDir()
	macOS := filepath.Join(dir, "Agentman.app", "Contents", "MacOS")
	if err := os.MkdirAll(macOS, 0o755); err != nil {
		t.Fatal(err)
	}
	appAm := filepath.Join(macOS, "am")
	if err := os.WriteFile(appAm, []byte("signed am"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := replaceBinary(appAm, []byte("new am")); !errors.Is(err, errInsideApp) {
		t.Errorf("replacing the app's am: %v", err)
	}
	linkedDir := filepath.Join(dir, "bin")
	if err := os.Symlink(macOS, linkedDir); err != nil {
		t.Fatal(err)
	}
	if err := replaceBinary(filepath.Join(linkedDir, "am"), []byte("new am")); !errors.Is(err, errInsideApp) {
		t.Errorf("replacing through a linked folder: %v", err)
	}

	link := filepath.Join(dir, "am")
	if err := os.Symlink(appAm, link); err != nil {
		t.Fatal(err)
	}
	if err := replaceBinary(link, []byte("new am")); err != nil {
		t.Fatalf("replacing a link into the app: %v", err)
	}
	if info, err := os.Lstat(link); err != nil || !info.Mode().IsRegular() {
		t.Errorf("the link should now be a file of its own: %v %v", info, err)
	}
	if data, _ := os.ReadFile(appAm); string(data) != "signed am" {
		t.Errorf("the app's am was changed to %q", data)
	}
}

// The service's am is left to whatever keeps it current when that isn't
// am upgrade: the app it is inside, Homebrew or npm. A copy nothing else
// manages, the kind am upgrade replaces, has no keeper.
func TestWhatKeepsTheServicesAmCurrent(t *testing.T) {
	dir := t.TempDir()
	appAm := filepath.Join(dir, "Agentman.app", "Contents", "Resources", "am")
	if err := os.MkdirAll(filepath.Dir(appAm), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(appAm, []byte("am"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "am")
	if err := os.Symlink(appAm, link); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{
		link:  "Agentman.app",
		appAm: "Agentman.app",
		"/opt/homebrew/Caskroom/agentman/0.31.0/am":   "Homebrew",
		"/usr/local/lib/node_modules/agentman/bin/am": "npm",
		filepath.Join(dir, "plain", "am"):             "",
	} {
		if got := keptBy(path); got != want {
			t.Errorf("keptBy(%q) = %q, want %q", path, got, want)
		}
	}
}

// Run from the app's am, am upgrade hands over to the copy the app manages,
// never to an am put at that path by hand, and says to update the app when
// there is no copy to hand over to.
func TestAnAppsAmUpgradesTheDaemonsCopy(t *testing.T) {
	home := t.TempDir()
	self := filepath.Join(home, "Agentman.app", "Contents", "MacOS", "am")
	managed := update.ManagedPath(home)
	for _, path := range []string{self, managed} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("am"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := daemonCopyFor(self, home); ok {
		t.Error("handed over to an am with no marker")
	}
	err := upgradeFromApp(nil, home, self, false)
	if err == nil || !strings.Contains(err.Error(), "update Agentman.app") {
		t.Errorf("with no managed copy: %v", err)
	}
	if err := upgradeFromApp([]string{"-check"}, home, self, true); err != nil {
		t.Errorf("-check with no managed copy: %v", err)
	}
	if err := update.WriteMarker(managed, update.Marker{AppVersion: "0.39.0"}); err != nil {
		t.Fatal(err)
	}
	if got, ok := daemonCopyFor(self, home); !ok || got != managed {
		t.Errorf("daemonCopyFor = %q, %v", got, ok)
	}
	if _, ok := daemonCopyFor(managed, home); ok {
		t.Error("the managed copy handed over to itself")
	}
}
