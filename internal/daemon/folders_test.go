package daemon

import (
	"path/filepath"
	"testing"
)

// A folder named for reading history is held to a different rule than one
// named for launching a process in. Nothing is executed and the path is never
// opened — it is only compared against working directories the agents'
// own stores already recorded — so an absolute path and a dot-directory are
// both legitimate. A good deal of real history lives in exactly those: a
// scratch checkout under /private/tmp, a workspace under ~/.something.
func TestFolderPathsReachWhatTheLaunchBrowserCannot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	for _, raw := range []string{
		"Projects/app",
		"/private/tmp/scratch",
		filepath.Join(home, ".openclaw", "workspace"),
	} {
		if _, err := folderDirectory(raw); err != nil {
			t.Errorf("folderDirectory(%q) = %v, want it accepted", raw, err)
		}
	}
	// A relative path is still resolved against home, exactly as the New
	// session browser resolves one.
	got, err := folderDirectory("Projects/app")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "Projects", "app"); got != want {
		t.Errorf("folderDirectory = %q, want %q", got, want)
	}
	// ".." is collapsed rather than followed, so a path cannot be built to
	// mean something other than it reads.
	got, err = folderDirectory("Projects/../other")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "other"); got != want {
		t.Errorf("folderDirectory = %q, want %q", got, want)
	}
}

func TestFolderPathsRejectMalformedInput(t *testing.T) {
	for _, raw := range []string{
		"",
		"with\x00null",
		"back\\slash",
		string(make([]byte, maxFolderPathBytes+1)),
	} {
		if err := validateFolderPath(raw); err == nil {
			t.Errorf("validateFolderPath(%q) accepted a malformed folder", raw)
		}
	}
}
