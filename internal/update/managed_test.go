package update

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The am inside an app is the app's, wherever the app is, even when
// Homebrew keeps the app in its Caskroom.
func TestAnAppsOwnAmIsTheApps(t *testing.T) {
	for path, want := range map[string]How{
		"/Applications/Agentman.app/Contents/MacOS/am":                                   AppBundle,
		"/Applications/Agentman.app/Contents/Resources/am":                               AppBundle,
		"/Users/me/Downloads/Agentman 2.app/Contents/Helpers/am":                         AppBundle,
		"/opt/homebrew/Caskroom/agentman/0.40.0/Agentman.app/Contents/Resources/am":      AppBundle,
		"/Applications/Agentman.APP/contents/MacOS/am":                                   AppBundle,
		"/Users/me/apps/Agentman.app.backup/am":                                          Downloaded,
		"/opt/homebrew/Caskroom/agentman/0.40.0/am":                                      Homebrew,
		"/usr/local/lib/node_modules/agentman/node_modules/agentman-darwin-arm64/bin/am": Npm,
	} {
		if got := HowInstalled(path); got != want {
			t.Errorf("HowInstalled(%q) = %v, want %v", path, got, want)
		}
	}
	if got := BundleOf("/Applications/Agentman.app/Contents/Resources/am"); got != "/Applications/Agentman.app" {
		t.Errorf("BundleOf = %q", got)
	}
	if got := BundleOf("/usr/local/bin/am"); got != "" {
		t.Errorf("BundleOf a plain am = %q", got)
	}
}

// The copy the app puts at ~/.agentman/bin/am is told from one put there by
// hand by the marker beside it, and only a file of its own counts: a link
// is not the copy the app wrote.
func TestTheAppsCopyIsToldByItsMarker(t *testing.T) {
	home := t.TempDir()
	binary := ManagedPath(home)
	if binary != filepath.Join(home, ".agentman", "bin", "am") {
		t.Fatalf("ManagedPath = %q", binary)
	}
	if err := os.MkdirAll(filepath.Dir(binary), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("am"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := HowInstalled(binary); got != Downloaded {
		t.Fatalf("an am put there by hand is %v, want %v", got, Downloaded)
	}

	at := time.Date(2026, 11, 1, 9, 30, 0, 0, time.UTC)
	if err := WriteMarker(binary, Marker{AppVersion: "0.39.0", InstalledAt: at}); err != nil {
		t.Fatal(err)
	}
	marker, ok := ReadMarker(binary)
	if !ok || marker.ManagedBy != ManagingApp || marker.AppVersion != "0.39.0" || !marker.InstalledAt.Equal(at) {
		t.Fatalf("marker = %+v, %v", marker, ok)
	}
	if got := HowInstalled(binary); got != AppManaged || !IsManaged(binary) {
		t.Fatalf("the app's copy is %v", got)
	}
	if leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(binary), ".*.new-*")); len(leftovers) != 0 {
		t.Errorf("left %v behind", leftovers)
	}

	if err := os.Remove(binary); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(home, "elsewhere-am")
	if err := os.WriteFile(target, []byte("am"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, binary); err != nil {
		t.Fatal(err)
	}
	if IsManaged(binary) {
		t.Error("a link at the managed path counted as the app's copy")
	}

	if err := os.WriteFile(MarkerPath(binary), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := ReadMarker(binary); ok {
		t.Error("a marker that isn't JSON was read")
	}
}

// A marker inside an app bundle never makes the bundle's am the managed
// copy: what is in the bundle is replaced only by the app.
func TestAMarkerInsideAnAppChangesNothing(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Agentman.app", "Contents", "MacOS")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "am")
	if err := os.WriteFile(binary, []byte("am"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := WriteMarker(binary, Marker{}); err != nil {
		t.Fatal(err)
	}
	if IsManaged(binary) || HowInstalled(binary) != AppBundle {
		t.Fatalf("the bundle's am is %v", HowInstalled(binary))
	}
}

func TestAnAppsVersionIsReadFromItsInfoPlist(t *testing.T) {
	bundle := filepath.Join(t.TempDir(), "Agentman.app")
	if err := os.MkdirAll(filepath.Join(bundle, "Contents"), 0o755); err != nil {
		t.Fatal(err)
	}
	plist := `<?xml version="1.0" encoding="UTF-8"?>
<plist version="1.0">
<dict>
	<key>CFBundleShortVersionString</key>
	<string>0.39.0</string>
	<key>CFBundleVersion</key>
	<string>44</string>
</dict>
</plist>
`
	if err := os.WriteFile(filepath.Join(bundle, "Contents", "Info.plist"), []byte(plist), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := AppVersion(bundle); got != "0.39.0" {
		t.Errorf("AppVersion = %q", got)
	}
	if got := AppVersion(filepath.Join(t.TempDir(), "Missing.app")); got != "" {
		t.Errorf("a missing app's version = %q", got)
	}
}

func TestRoutesHaveNames(t *testing.T) {
	for how, want := range map[How]string{
		Downloaded: "download", Homebrew: "homebrew", Npm: "npm", AppBundle: "app", AppManaged: "managed",
	} {
		if got := how.String(); got != want {
			t.Errorf("%d is named %q, want %q", how, got, want)
		}
	}
}
