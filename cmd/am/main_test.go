package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/source"
	"github.com/lenajeremy/agentman/internal/update"
)

func TestVersionOutput(t *testing.T) {
	old := version
	version = "1.2.3-test"
	defer func() { version = old }()

	plain, err := formatVersion(false)
	if err != nil || plain != "agentman 1.2.3-test\n" {
		t.Fatalf("plain version = %q, %v", plain, err)
	}
	encoded, err := formatVersion(true)
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Version  string `json:"version"`
		APILevel int    `json:"apiLevel"`
		Protocol int    `json:"protocol"`
		Install  string `json:"install"`
		Managed  *bool  `json:"managed"`
		Path     string `json:"path"`
	}
	if err := json.Unmarshal([]byte(encoded), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Version != version {
		t.Fatalf("JSON version = %q, want %q", payload.Version, version)
	}
	if payload.APILevel != protocol.APILevel || payload.Protocol != protocol.Version ||
		payload.Install == "" || payload.Managed == nil || payload.Path == "" {
		t.Fatalf("JSON version = %s", encoded)
	}
}

// am version --json says, of any am the app may run, how it was installed
// and which app version it came with: the managed copy's from its marker,
// an app's own am's from the app.
func TestVersionReportSaysHowAmCame(t *testing.T) {
	old := version
	version = "0.39.0"
	defer func() { version = old }()

	home := t.TempDir()
	managed := update.ManagedPath(home)
	bundled := filepath.Join(home, "Agentman.app", "Contents", "MacOS", "am")
	for _, path := range []string{managed, bundled} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("am"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	plain := reportVersion(managed)
	if plain.Install != "download" || plain.Managed || plain.AppVersion != "" {
		t.Errorf("an am put at the managed path by hand: %+v", plain)
	}
	if err := update.WriteMarker(managed, update.Marker{AppVersion: "0.39.1"}); err != nil {
		t.Fatal(err)
	}
	if got := reportVersion(managed); got.Install != "managed" || !got.Managed || got.AppVersion != "0.39.1" ||
		got.Version != "0.39.0" || got.APILevel != protocol.APILevel || got.Path != managed {
		t.Errorf("the managed copy: %+v", got)
	}
	plist := "<plist><dict><key>CFBundleShortVersionString</key>\n\t<string>0.39.2</string></dict></plist>"
	if err := os.WriteFile(filepath.Join(home, "Agentman.app", "Contents", "Info.plist"), []byte(plist), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := reportVersion(bundled); got.Install != "app" || got.Managed || got.AppVersion != "0.39.2" {
		t.Errorf("the app's own am: %+v", got)
	}
	if got := reportVersion("/opt/homebrew/Caskroom/agentman/0.39.0/am"); got.Install != "homebrew" || got.Managed {
		t.Errorf("Homebrew's am: %+v", got)
	}
}

// Every session list tells the apps the daemon's API level and how its am
// was installed, before any release check has answered.
func TestTheDaemonSaysItsLevelAndRoute(t *testing.T) {
	info := daemonInfo(update.NewChecker("0.39.0"), update.UpgradeCommand, "managed")
	if info.APILevel != protocol.APILevel || info.Install != "managed" || info.Version == "" {
		t.Fatalf("daemonInfo = %+v", info)
	}
	encoded, err := json.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), fmt.Sprintf(`"apiLevel":%d`, protocol.APILevel)) ||
		!strings.Contains(string(encoded), `"install":"managed"`) {
		t.Errorf("on the wire: %s", encoded)
	}
}

func TestHistoryPageLimitBoundary(t *testing.T) {
	for _, limit := range []int{-1, 0, source.MaxPageMessages + 1, int(^uint(0) >> 1)} {
		if err := source.ValidatePageLimit(limit); err == nil {
			t.Errorf("limit %d was accepted", limit)
		}
	}
	for _, limit := range []int{1, 30, source.MaxPageMessages} {
		if err := source.ValidatePageLimit(limit); err != nil {
			t.Errorf("limit %d was rejected: %v", limit, err)
		}
	}
}

func TestRunHistoryRejectsUnsafeLimitsBeforeDiscovery(t *testing.T) {
	for _, limit := range []int{-1, 0, source.MaxPageMessages + 1, int(^uint(0) >> 1)} {
		err := runHistory(context.Background(), []string{
			"claude:any-session", "-limit", fmt.Sprint(limit),
		})
		if err == nil || !strings.Contains(err.Error(), "message limit must be between") {
			t.Errorf("limit %d returned %v", limit, err)
		}
	}
}
