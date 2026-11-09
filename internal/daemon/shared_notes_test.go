package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/knowledge"
	"github.com/lenajeremy/agentman/internal/protocol"
)

type notesLab struct {
	notes *sharedNotes
	store *knowledge.Store
	home  string
	clock time.Time
}

func newNotesLab(t *testing.T) *notesLab {
	t.Helper()
	home := t.TempDir()
	for _, dir := range []string{".claude", ".config/muse"} {
		if err := os.MkdirAll(filepath.Join(home, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	store := knowledge.Open(t.TempDir())
	notes := newSharedNotes(store, knowledge.Homes{Home: home}, filepath.Join(t.TempDir(), "shared-notes.json"))
	lab := &notesLab{notes: notes, store: store, home: home, clock: time.UnixMilli(1_000_000)}
	notes.now = func() time.Time { return lab.clock }
	return lab
}

func (l *notesLab) tick(t *testing.T) {
	t.Helper()
	l.clock = l.clock.Add(time.Minute)
	l.notes.sync()
}

func (l *notesLab) write(t *testing.T, title, body string) knowledge.Note {
	t.Helper()
	note, err := l.store.Write(knowledge.Note{Tier: knowledge.Shared, Title: title, Body: body, Always: true})
	if err != nil {
		t.Fatal(err)
	}
	return note
}

func TestChangingANoteRewritesTheFilesByItself(t *testing.T) {
	lab := newNotesLab(t)
	lab.store.OnChange(lab.notes.kick)
	lab.write(t, "Use pnpm", "Always pnpm.")
	lab.notes.wg.Wait()
	raw, err := os.ReadFile(filepath.Join(lab.home, ".claude", "CLAUDE.md"))
	if err != nil || !strings.Contains(string(raw), "Always pnpm.") {
		t.Fatalf("got %q, %v", raw, err)
	}
}

func TestACursorFolderGetsItsCopyWhenSeen(t *testing.T) {
	lab := newNotesLab(t)
	lab.write(t, "Use pnpm", "Always pnpm.")
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	lab.notes.observe([]protocol.Session{{ID: "cursor-cli:a", Kind: protocol.KindCursorCLI, Cwd: dir}})
	lab.notes.wg.Wait()
	raw, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if err != nil || !strings.Contains(string(raw), "Always pnpm.") {
		t.Fatalf("got %q, %v", raw, err)
	}
}

func TestNotHomeForCursor(t *testing.T) {
	lab := newNotesLab(t)
	lab.notes.observe([]protocol.Session{{ID: "cursor-cli:a", Kind: protocol.KindCursorCLI, Cwd: lab.home}})
	lab.notes.wg.Wait()
	if len(lab.notes.state.Targets) != 0 {
		t.Fatalf("targets %v", lab.notes.state.Targets)
	}
}

func TestAFolderThatsGoneIsForgotten(t *testing.T) {
	lab := newNotesLab(t)
	lab.notes.state.Targets[cursorNotesPrefix+filepath.Join(lab.home, "deleted")] = map[string]noteOnDisk{}
	lab.tick(t)
	if len(lab.notes.state.Targets) != 1 { // muse's only
		t.Fatalf("targets %v", lab.notes.state.Targets)
	}
}

func TestWhatWentOnDiskIsRememberedAcrossARestart(t *testing.T) {
	lab := newNotesLab(t)
	note := lab.write(t, "Use pnpm", "Always pnpm.")
	lab.tick(t)
	again := newSharedNotes(lab.store, lab.notes.homes, lab.notes.path)
	got := again.state.Targets[museNotesTarget][note.Name]
	if got.Hash == "" || got.At == notOnDisk {
		t.Fatalf("got %+v", got)
	}
}

func TestANoteOnlyGetsANewDateWhenItChanges(t *testing.T) {
	lab := newNotesLab(t)
	note := lab.write(t, "Use pnpm", "Always pnpm.")
	lab.tick(t)
	first := lab.notes.state.Targets[museNotesTarget][note.Name].At
	lab.tick(t)
	if got := lab.notes.state.Targets[museNotesTarget][note.Name].At; got != first {
		t.Fatalf("moved from %d to %d", first, got)
	}
	lab.write(t, "Use pnpm", "Always pnpm, even here.")
	lab.tick(t)
	if got := lab.notes.state.Targets[museNotesTarget][note.Name].At; got == first {
		t.Fatal("didn't move")
	}
}
