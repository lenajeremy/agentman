package update

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// putMacApp makes an app bundle called Agentman.app in folder whose
// Info.plist names id and version, as Xcode writes it.
func putMacApp(t *testing.T, folder, id, version string) string {
	t.Helper()
	bundle := filepath.Join(folder, MacAppName)
	if err := os.MkdirAll(filepath.Join(bundle, "Contents"), 0o755); err != nil {
		t.Fatal(err)
	}
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
	<key>CFBundleIdentifier</key>
	<string>` + id + `</string>
	<key>CFBundleShortVersionString</key>
	<string>` + version + `</string>
</dict>
</plist>
`
	if err := os.WriteFile(filepath.Join(bundle, "Contents", "Info.plist"), []byte(plist), 0o644); err != nil {
		t.Fatal(err)
	}
	return bundle
}

// The app is the released one in Applications or in the user's own
// Applications folder, the Mac's first; a Debug build, an app of its own,
// isn't it, and neither is a folder with no app in it.
func TestFindingAgentmanForMac(t *testing.T) {
	system, user, empty := t.TempDir(), t.TempDir(), t.TempDir()
	if _, ok := FindMacApp([]string{empty, filepath.Join(empty, "missing")}); ok {
		t.Fatal("found an app where there is none")
	}
	putMacApp(t, system, "dev.agentman.mac.debug", "0.40.0-dev")
	mine := putMacApp(t, user, MacAppID, "0.39.0")
	app, ok := FindMacApp([]string{system, user})
	if !ok || app != (MacApp{Path: mine, Version: "0.39.0"}) {
		t.Fatalf("FindMacApp = %+v, %v; want the released app in the user's folder", app, ok)
	}
	released := putMacApp(t, empty, MacAppID, "0.40.0")
	if app, _ := FindMacApp([]string{empty, user}); app.Path != released {
		t.Errorf("the first folder's app should win, got %+v", app)
	}
	if got := MacAppFolders("/Users/me"); len(got) != 2 || got[0] != "/Applications" || got[1] != "/Users/me/Applications" {
		t.Errorf("MacAppFolders = %q", got)
	}
}

// An Info.plist in binary form is read too, through plutil.
func TestABinaryInfoPlistIsRead(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("plutil is the Mac's")
	}
	bundle := putMacApp(t, t.TempDir(), MacAppID, "0.40.0")
	plist := filepath.Join(bundle, "Contents", "Info.plist")
	if out, err := exec.Command("plutil", "-convert", "binary1", plist).CombinedOutput(); err != nil {
		t.Fatalf("plutil: %v %s", err, out)
	}
	if raw, _ := os.ReadFile(plist); string(raw[:6]) != "bplist" {
		t.Fatal("the plist didn't become binary")
	}
	if got := AppVersion(bundle); got != "0.40.0" {
		t.Errorf("AppVersion = %q", got)
	}
	if _, ok := FindMacApp([]string{filepath.Dir(bundle)}); !ok {
		t.Error("the app wasn't found by its binary Info.plist")
	}
}

// The choice is written whole, for this user only, and read back; a file
// that says nothing sensible is no choice.
func TestTheChoiceForTheAppIsLeftWhole(t *testing.T) {
	dir := t.TempDir()
	if _, ok := ReadAppIntent(dir); ok {
		t.Fatal("read a choice that was never made")
	}
	before := time.Now().UnixMilli()
	if err := WriteAppIntent(dir, AppIntent{Version: "0.40.0", Choice: AppUpdate}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(AppUpdatePath(dir))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("app-update.json: %v, %v", info, err)
	}
	raw, _ := os.ReadFile(AppUpdatePath(dir))
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil || fields["version"] != "0.40.0" || fields["choice"] != "update" {
		t.Errorf("app-update.json says %s (%v)", raw, err)
	}
	intent, ok := ReadAppIntent(dir)
	if !ok || intent.Version != "0.40.0" || intent.Choice != AppUpdate || intent.At < before || intent.At > time.Now().UnixMilli() {
		t.Errorf("read back %+v, %v", intent, ok)
	}
	if err := WriteAppIntent(dir, AppIntent{Version: "0.41.0", Choice: AppSkip, At: 7}); err != nil {
		t.Fatal(err)
	}
	if intent, _ := ReadAppIntent(dir); intent != (AppIntent{Version: "0.41.0", Choice: AppSkip, At: 7}) {
		t.Errorf("the second choice: %+v", intent)
	}
	if leftovers, _ := filepath.Glob(filepath.Join(dir, ".*.new-*")); len(leftovers) != 0 {
		t.Errorf("left %v behind", leftovers)
	}

	if err := WriteAppIntent(dir, AppIntent{Version: "0.41.0", Choice: "later"}); err == nil {
		t.Error("wrote a choice that isn't one")
	}
	for _, body := range []string{`{"version":"0.41.0","choice":"later"}`, `{"version":"dev","choice":"update"}`, `not json`} {
		if err := os.WriteFile(AppUpdatePath(dir), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if intent, ok := ReadAppIntent(dir); ok {
			t.Errorf("%s read as %+v", body, intent)
		}
	}
}
