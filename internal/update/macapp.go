package update

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

// Agentman for Mac and am carry the same version, are released together
// under one tag, and stay matched both ways. Updating the app brings the
// daemon's am forward with it. Updating am, with am upgrade, asks whether
// the app should follow, and leaves the answer in a small file the app
// reads when it next connects. am never changes the app itself: the app
// updates itself, through Sparkle, and nothing is ever downgraded.

// MacAppID is a released Agentman for Mac's bundle identifier. A Debug
// build is dev.agentman.mac.debug, an app of its own that is never updated.
const MacAppID = "dev.agentman.mac"

// MacAppName is the app's folder in Applications.
const MacAppName = "Agentman.app"

// MacApp is an Agentman for Mac installed on this Mac.
type MacApp struct {
	// Path is the app bundle, such as /Applications/Agentman.app.
	Path string
	// Version is its CFBundleShortVersionString.
	Version string
}

// MacAppFolders are where the app is looked for, in order: the Mac's
// Applications folder, then the user's own.
func MacAppFolders(home string) []string {
	return []string{"/Applications", filepath.Join(home, "Applications")}
}

// FindMacApp finds Agentman for Mac in folders: the first Agentman.app
// there whose Info.plist names the released app's bundle identifier.
func FindMacApp(folders []string) (MacApp, bool) {
	for _, folder := range folders {
		bundle := filepath.Join(folder, MacAppName)
		if infoPlistString(bundle, "CFBundleIdentifier") != MacAppID {
			continue
		}
		return MacApp{Path: bundle, Version: AppVersion(bundle)}, true
	}
	return MacApp{}, false
}

// infoPlistString is the string an app bundle's Info.plist gives for key,
// or empty. Xcode leaves a Mac app's Info.plist as XML; one in binary form
// is read through plutil, which every Mac has.
func infoPlistString(bundle, key string) string {
	path := filepath.Join(bundle, "Contents", "Info.plist")
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	if strings.HasPrefix(string(raw), "bplist") {
		if runtime.GOOS != "darwin" {
			return ""
		}
		if raw, err = exec.Command("plutil", "-convert", "xml1", "-o", "-", path).Output(); err != nil {
			return ""
		}
	}
	pattern := regexp.MustCompile(`<key>` + regexp.QuoteMeta(key) + `</key>\s*<string>([^<]*)</string>`)
	if match := pattern.FindSubmatch(raw); match != nil {
		return strings.TrimSpace(string(match[1]))
	}
	return ""
}

// AppUpdateName is the file in ~/.agentman where am upgrade leaves what the
// user chose for the app.
const AppUpdateName = "app-update.json"

// AppChoice is what the user chose for Agentman for Mac when am upgrade
// took am to a release newer than the app.
type AppChoice string

const (
	// AppUpdate: update the app to match, without asking again.
	AppUpdate AppChoice = "update"
	// AppSkip: only am was updated; the app offers its update quietly.
	AppSkip AppChoice = "skip"
)

// AppIntent is app-update.json: the choice, for which version of am, and
// when it was made. The app reads it when it finds am serve newer than
// itself (desktop/Agentman/App/AppCatchUp.swift).
type AppIntent struct {
	Version string    `json:"version"`
	Choice  AppChoice `json:"choice"`
	// At is when, in milliseconds since 1970.
	At int64 `json:"at"`
}

// AppUpdatePath is app-update.json in the config folder dir (~/.agentman).
func AppUpdatePath(dir string) string {
	return filepath.Join(dir, AppUpdateName)
}

// WriteAppIntent records the choice in dir, stamped with now when it has no
// time of its own: a new file, readable only by this user, renamed into
// place so the app never reads half of it.
func WriteAppIntent(dir string, intent AppIntent) error {
	if intent.Choice != AppUpdate && intent.Choice != AppSkip {
		return fmt.Errorf("update: %q is no choice for the app", intent.Choice)
	}
	if intent.At == 0 {
		intent.At = time.Now().UnixMilli()
	}
	body, err := json.Marshal(intent)
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, "."+AppUpdateName+".new-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if _, err := temp.Write(append(body, '\n')); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), AppUpdatePath(dir))
}

// ReadAppIntent reads the choice recorded in dir, if there is one that
// makes sense.
func ReadAppIntent(dir string) (AppIntent, bool) {
	raw, err := os.ReadFile(AppUpdatePath(dir))
	if err != nil {
		return AppIntent{}, false
	}
	var intent AppIntent
	if json.Unmarshal(raw, &intent) != nil || !IsRelease(intent.Version) ||
		(intent.Choice != AppUpdate && intent.Choice != AppSkip) {
		return AppIntent{}, false
	}
	return intent, true
}
