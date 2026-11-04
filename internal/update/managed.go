package update

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Agentman for Mac carries an am inside its bundle, and puts a copy of it at
// ~/.agentman/bin/am for the login service to run. That copy is the daemon's
// one fixed place: it outlives the app being moved or updated, it is what
// agents' hooks and tools name, and am upgrade can bring it forward. A small
// file beside it says the app put it there, so am can tell it from a copy
// someone put in the same place by hand, which is left to work as before.

// MarkerName is the file beside the app-managed am that marks it as the
// app's.
const MarkerName = "am.managed.json"

// ManagingApp is the app that manages the copy, as the marker names it.
const ManagingApp = "Agentman for Mac"

// Marker is what the file beside the app-managed am says.
type Marker struct {
	// ManagedBy names the app that manages the copy.
	ManagedBy string `json:"managedBy"`
	// AppVersion is the version of the app that installed it, when known.
	AppVersion string `json:"appVersion,omitempty"`
	// InstalledAt is when it was installed.
	InstalledAt time.Time `json:"installedAt"`
}

// ManagedPath is where the app-managed am lives for home: home/.agentman/bin/am.
func ManagedPath(home string) string {
	return filepath.Join(home, ".agentman", "bin", "am")
}

// MarkerPath is where the marker for the am at binary is: beside it.
func MarkerPath(binary string) string {
	return filepath.Join(filepath.Dir(binary), MarkerName)
}

// ReadMarker reads the marker beside the am at binary, if there is one.
func ReadMarker(binary string) (Marker, bool) {
	raw, err := os.ReadFile(MarkerPath(binary))
	if err != nil {
		return Marker{}, false
	}
	var marker Marker
	if json.Unmarshal(raw, &marker) != nil {
		return Marker{}, false
	}
	return marker, true
}

// WriteMarker marks the am at binary as the app's, written to a new file
// and renamed into place so a reader never sees half of it.
func WriteMarker(binary string, marker Marker) error {
	if marker.ManagedBy == "" {
		marker.ManagedBy = ManagingApp
	}
	body, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return err
	}
	path := MarkerPath(binary)
	temp, err := os.CreateTemp(filepath.Dir(path), "."+MarkerName+".new-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if _, err := temp.Write(append(body, '\n')); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Chmod(0o644); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}

// IsManaged reports whether the am at binary is the app-managed copy: a file
// of its own, not a link, with the marker beside it, and not inside an app.
func IsManaged(binary string) bool {
	if InAppBundle(binary) {
		return false
	}
	info, err := os.Lstat(binary)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	_, ok := ReadMarker(binary)
	return ok
}

// InAppBundle reports whether path is inside a macOS app bundle: anywhere
// under an .app's Contents. An am there is the app's own, sealed by the
// app's code signature, and only the app's updates may replace it.
func InAppBundle(path string) bool {
	return strings.Contains(strings.ToLower(filepath.ToSlash(path)), ".app/contents/")
}

// BundleOf is the app bundle path is inside, the outermost if bundles
// nest, or empty when it is in none.
func BundleOf(path string) string {
	slashed := filepath.ToSlash(path)
	at := strings.Index(strings.ToLower(slashed), ".app/contents/")
	if at < 0 {
		return ""
	}
	return filepath.FromSlash(slashed[:at+len(".app")])
}

// AppVersion is the version an app bundle's Info.plist gives, its
// CFBundleShortVersionString; empty when it gives none.
func AppVersion(bundle string) string {
	return infoPlistString(bundle, "CFBundleShortVersionString")
}
