package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/source"
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

// A folder's list must never arrive as EvtSessions.
//
// That type is the authoritative live snapshot: the app replaces the status
// board with it and drops the transcripts of everything missing from it. A
// folder's list is mostly sessions that have already ended, so sending it
// under that type put a month of finished agents on the board — and left them
// there after the filter was cleared, because by then they were the board.
func TestDirectorySessionsIsNotTheLiveSnapshot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	d := New(source.NewRegistry(), nil)

	event := d.HandleFrom(context.Background(), "app-1", protocol.Request{
		Type: protocol.ReqDirectorySessions,
		Path: "Projects/app",
	})
	if event.Type == protocol.EvtSessions {
		t.Fatal("a folder's list was sent as the live snapshot; it would replace the status board")
	}
	if event.Type != protocol.EvtDirectorySessions {
		t.Fatalf("type = %q, want %q", event.Type, protocol.EvtDirectorySessions)
	}

	// The live snapshot still uses its own type, so this did not simply
	// rename the thing the board listens for.
	live := d.HandleFrom(context.Background(), "app-1", protocol.Request{
		Type: protocol.ReqListSessions,
	})
	if live.Type != protocol.EvtSessions {
		t.Fatalf("list_sessions type = %q, want %q", live.Type, protocol.EvtSessions)
	}
}

// Creating a folder is held to exactly the rule launching in one is, because
// that is what it becomes moments later.
func TestCreateDirectoryStaysInsideHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, "Projects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(home, "escape")); err != nil {
		t.Fatal(err)
	}

	if _, err := createLaunchDirectory("Projects/fresh"); err != nil {
		t.Fatalf("Projects/fresh: %v", err)
	}
	info, err := os.Stat(filepath.Join(home, "Projects", "fresh"))
	if err != nil || !info.IsDir() {
		t.Fatalf("folder was not created: %v", err)
	}

	for _, raw := range []string{
		"../outside",       // above home
		"/tmp/anywhere",    // absolute
		"Projects/../.ssh", // traversal into a private folder
		".hidden",          // a dot-directory
		"escape/inside",    // through a symlink out of home
		"Projects/fresh",   // already exists
		"",                 // unnamed
	} {
		if _, err := createLaunchDirectory(raw); err == nil {
			t.Errorf("created an unsafe folder for %q", raw)
		}
	}
}

// A folder made for an agent to work in is not something the rest of the
// machine needs to read.
func TestCreatedDirectoryIsPrivate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if _, err := createLaunchDirectory("scratch"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(home, "scratch"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("mode = %o, want 700", perm)
	}
}
