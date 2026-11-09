package knowledge

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func alwaysNote(t *testing.T, s *Store, title, body string) Note {
	t.Helper()
	note, err := s.Write(Note{Tier: Shared, Title: title, Body: body, Always: true})
	if err != nil {
		t.Fatal(err)
	}
	return note
}

func read(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func labHomes(t *testing.T, dirs ...string) Homes {
	t.Helper()
	home := t.TempDir()
	for _, dir := range dirs {
		if err := os.MkdirAll(filepath.Join(home, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return Homes{Home: home}
}

func TestATickedNoteReachesEveryInstalledAgentsOwnFile(t *testing.T) {
	store := Open(t.TempDir())
	alwaysNote(t, store, "Use pnpm", "Always pnpm.")
	homes := labHomes(t, ".claude", ".codex", ".config/opencode", ".gemini", ".kiro", ".config/muse")
	results, err := store.SyncPersonal(homes)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 6 {
		t.Fatalf("got %d results", len(results))
	}
	for _, result := range results {
		if !result.Installed || result.Err != nil {
			t.Fatalf("%+v", result)
		}
		if !strings.Contains(read(t, result.Path), "Always pnpm.") {
			t.Fatalf("%s has no note", result.Path)
		}
	}
}

func TestAnAgentThatIsntInstalledGetsNothing(t *testing.T) {
	store := Open(t.TempDir())
	alwaysNote(t, store, "Use pnpm", "Always pnpm.")
	homes := labHomes(t, ".claude")
	results, err := store.SyncPersonal(homes)
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range results {
		if result.Kind == "claude" {
			continue
		}
		if result.Installed {
			t.Fatalf("%s counted as installed", result.Kind)
		}
		if _, err := os.Stat(result.Path); err == nil {
			t.Fatalf("wrote %s", result.Path)
		}
	}
}

func TestTheUsersOwnInstructionsAreLeftAlone(t *testing.T) {
	store := Open(t.TempDir())
	note := alwaysNote(t, store, "Use pnpm", "Always pnpm.")
	homes := labHomes(t, ".claude")
	path := filepath.Join(homes.Home, ".claude", "CLAUDE.md")
	const mine = "# Mine\n\nDon't touch this.\n"
	if err := os.WriteFile(path, []byte(mine), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SyncPersonal(homes); err != nil {
		t.Fatal(err)
	}
	got := read(t, path)
	if !strings.HasPrefix(got, mine) || !strings.Contains(got, "Always pnpm.") {
		t.Fatalf("got %q", got)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatalf("mode became %v", info.Mode().Perm())
	}
	// Unticked, the file goes back to exactly what it was.
	note.Always = false
	if _, err := store.Write(note); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SyncPersonal(homes); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimRight(read(t, path), "\n"); got != strings.TrimRight(mine, "\n") {
		t.Fatalf("got %q", got)
	}
}

func TestSyncingTwiceChangesNothing(t *testing.T) {
	store := Open(t.TempDir())
	alwaysNote(t, store, "Use pnpm", "Always pnpm.")
	homes := labHomes(t, ".claude")
	path := filepath.Join(homes.Home, ".claude", "CLAUDE.md")
	if _, err := store.SyncPersonal(homes); err != nil {
		t.Fatal(err)
	}
	first := read(t, path)
	if _, err := store.SyncPersonal(homes); err != nil {
		t.Fatal(err)
	}
	if got := read(t, path); got != first {
		t.Fatalf("changed: %q", got)
	}
}

func TestALinkedInstructionFileStaysALink(t *testing.T) {
	store := Open(t.TempDir())
	alwaysNote(t, store, "Use pnpm", "Always pnpm.")
	homes := labHomes(t, ".claude")
	real := filepath.Join(t.TempDir(), "dotfiles-claude.md")
	if err := os.WriteFile(real, []byte("mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(homes.Home, ".claude", "CLAUDE.md")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SyncPersonal(homes); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Lstat(link); info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the link became a file")
	}
	if !strings.Contains(read(t, real), "Always pnpm.") {
		t.Fatal("the note didn't reach the linked file")
	}
}

func TestKirosFileGoesWhenTheLastNoteDoes(t *testing.T) {
	store := Open(t.TempDir())
	note := alwaysNote(t, store, "Use pnpm", "Always pnpm.")
	homes := labHomes(t, ".kiro")
	path := filepath.Join(homes.Home, ".kiro", "steering", "agentman-shared-notes.md")
	if _, err := store.SyncPersonal(homes); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read(t, path), "Always pnpm.") {
		t.Fatal("no note")
	}
	if err := store.Delete(Shared, note.Name); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SyncPersonal(homes); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(path)); err == nil {
		t.Fatal("left the steering folder behind")
	}
}

func repo(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	git(t, dir, "init", "-q")
	return dir
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", dir, "-c", "user.email=a@b", "-c", "user.name=a"}, args...)...)
	out, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return string(out)
}

func TestAProjectGetsTheNotesAndGitDoesntSeeThem(t *testing.T) {
	store := Open(t.TempDir())
	note := alwaysNote(t, store, "Use pnpm", "Always pnpm.")
	dir := repo(t)
	carried, err := store.SyncProject(dir)
	if err != nil || !carried {
		t.Fatalf("carried %v err %v", carried, err)
	}
	if !strings.Contains(read(t, filepath.Join(dir, "AGENTS.md")), "Always pnpm.") {
		t.Fatal("no note")
	}
	if status := git(t, dir, "status", "--porcelain"); status != "" {
		t.Fatalf("git sees %q", status)
	}
	// The last note gone, so is every trace of it.
	if err := store.Delete(Shared, note.Name); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SyncProject(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); err == nil {
		t.Fatal("left the file behind")
	}
	if strings.Contains(read(t, filepath.Join(dir, ".git", "info", "exclude")), excludeLine) {
		t.Fatal("left the exclude line behind")
	}
}

func TestATrackedAgentsFileIsNeverWritten(t *testing.T) {
	store := Open(t.TempDir())
	alwaysNote(t, store, "Use pnpm", "Always pnpm.")
	dir := repo(t)
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("team rules\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "AGENTS.md")
	git(t, dir, "commit", "-qm", "rules")
	carried, err := store.SyncProject(dir)
	if err != nil || carried {
		t.Fatalf("carried %v err %v", carried, err)
	}
	if got := read(t, filepath.Join(dir, "AGENTS.md")); got != "team rules\n" {
		t.Fatalf("got %q", got)
	}
}

func TestAnUntrackedAgentsFileOfTheUsersIsLeftAlone(t *testing.T) {
	store := Open(t.TempDir())
	alwaysNote(t, store, "Use pnpm", "Always pnpm.")
	dir := repo(t)
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("draft\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	carried, err := store.SyncProject(dir)
	if err != nil || carried {
		t.Fatalf("carried %v err %v", carried, err)
	}
	if status := git(t, dir, "status", "--porcelain"); !strings.Contains(status, "AGENTS.md") {
		t.Fatalf("hid the user's file from git: %q", status)
	}
}

func TestAFolderThatIsntARepositoryGetsNoFile(t *testing.T) {
	store := Open(t.TempDir())
	alwaysNote(t, store, "Use pnpm", "Always pnpm.")
	dir := t.TempDir()
	carried, err := store.SyncProject(dir)
	if err != nil || carried {
		t.Fatalf("carried %v err %v", carried, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); err == nil {
		t.Fatal("wrote a file")
	}
}

func TestChangingANoteSaysSo(t *testing.T) {
	store := Open(t.TempDir())
	calls := 0
	store.OnChange(func() { calls++ })
	note := alwaysNote(t, store, "Use pnpm", "Always pnpm.")
	if err := store.Delete(Shared, note.Name); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("called %d times", calls)
	}
}
