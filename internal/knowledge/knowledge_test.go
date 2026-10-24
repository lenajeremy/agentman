package knowledge

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNotesAreWrittenReadAndListed(t *testing.T) {
	s := Open(t.TempDir())
	saved, err := s.Write(Note{Tier: Shared, Title: "Commit style", Always: true, Body: "Imperative subject.\nNo trailers."})
	if err != nil {
		t.Fatal(err)
	}
	if saved.Name != "commit-style" || saved.Title != "Commit style" || !saved.Always || !strings.Contains(saved.Body, "No trailers.") {
		t.Fatalf("saved = %+v", saved)
	}
	if _, err := s.Write(Note{Tier: Private, Name: "clients", Title: "Clients", Body: "Acme pays late."}); err != nil {
		t.Fatal(err)
	}
	_ = s.Touch(Private, "clients", time.Now().Add(-time.Hour))

	all, _ := s.List("")
	if len(all) != 2 || all[0].Name != "commit-style" || all[0].Body != "" {
		t.Fatalf("list = %+v", all)
	}
	shared, _ := s.List(Shared)
	if len(shared) != 1 {
		t.Fatalf("shared = %+v", shared)
	}
	if note, err := s.Read("", "clients"); err != nil || note.Tier != Private {
		t.Fatalf("read without a tier = %+v, %v", note, err)
	}
}

// Moving a note to the other tier moves it; it isn't left in both.
func TestATierChangeMovesTheNote(t *testing.T) {
	s := Open(t.TempDir())
	_, _ = s.Write(Note{Tier: Shared, Name: "keys", Title: "Keys", Body: "x"})
	_, _ = s.Write(Note{Tier: Private, Name: "keys", Title: "Keys", Body: "x"})
	if _, err := s.Read(Shared, "keys"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("still shared: %v", err)
	}
}

func TestNamesCannotLeaveTheFolder(t *testing.T) {
	s := Open(t.TempDir())
	for _, name := range []string{"../escape", "a/b", "UPPER", ".hidden"} {
		if _, err := s.Write(Note{Tier: Shared, Name: name, Title: "x", Body: "x"}); err == nil {
			t.Errorf("wrote a note called %q", name)
		}
	}
	if _, err := s.Write(Note{Tier: Shared, Body: "no name and no title"}); err == nil {
		t.Error("wrote a note with neither a name nor a title")
	}
	if _, err := s.Write(Note{Tier: "public", Name: "a", Body: "x"}); err == nil {
		t.Error("wrote to a tier that doesn't exist")
	}
}

func TestSearchFindsTheBestNoteFirst(t *testing.T) {
	s := Open(t.TempDir())
	_, _ = s.Write(Note{Tier: Shared, Title: "Deploying", Body: "Deploy with vercel --prod after the tests pass.\nNever deploy on Fridays."})
	_, _ = s.Write(Note{Tier: Shared, Title: "Style", Body: "Short sentences. Mention deploy once."})
	_, _ = s.Write(Note{Tier: Private, Title: "Secrets", Body: "deploy token rotation"})

	hits, err := s.Search("deploy fridays", Shared, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 || hits[0].Name != "deploying" || !strings.Contains(hits[0].Snippet, "Fridays") {
		t.Fatalf("hits = %+v", hits)
	}
	for _, hit := range hits {
		if hit.Tier != Shared {
			t.Fatal("a shared search found a private note")
		}
	}
}

// The managed block goes in, is replaced in place, leaves the rest of the
// file alone, and comes out when nothing is marked "always".
func TestInstructionsKeepTheirOwnBlock(t *testing.T) {
	s := Open(t.TempDir())
	project := t.TempDir()
	path := filepath.Join(project, "CLAUDE.md")
	if err := os.WriteFile(path, []byte("# Project\n\nUse pnpm."), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _ = s.Write(Note{Tier: Shared, Title: "Commits", Always: true, Body: "Imperative subjects."})
	_, _ = s.Write(Note{Tier: Shared, Title: "Reference", Body: "Not always."})
	_, _ = s.Write(Note{Tier: Private, Title: "Private", Always: true, Body: "Never in a project."})

	file, count, err := s.SyncInstructions(project, "claude")
	if err != nil || file != path || count != 1 {
		t.Fatalf("sync = %s, %d, %v", file, count, err)
	}
	raw, _ := os.ReadFile(path)
	text := string(raw)
	if !strings.HasPrefix(text, "# Project\n\nUse pnpm.\n") || !strings.Contains(text, "### Commits") ||
		strings.Contains(text, "Not always") || strings.Contains(text, "Never in a project") {
		t.Fatalf("file =\n%s", text)
	}

	_, _ = s.Write(Note{Tier: Shared, Name: "commits", Title: "Commits", Always: true, Body: "Imperative, under 72 characters."})
	_, _, _ = s.SyncInstructions(project, "claude")
	raw, _ = os.ReadFile(path)
	if strings.Count(string(raw), beginMarker) != 1 || !strings.Contains(string(raw), "under 72") {
		t.Fatalf("after an update =\n%s", raw)
	}

	_ = s.Delete(Shared, "commits")
	_, count, _ = s.SyncInstructions(project, "claude")
	raw, _ = os.ReadFile(path)
	if count != 0 || strings.Contains(string(raw), beginMarker) || !strings.Contains(string(raw), "Use pnpm.") {
		t.Fatalf("after removing the note =\n%s", raw)
	}

	if file, _, _ := s.SyncInstructions(project, "codex"); filepath.Base(file) != "AGENTS.md" {
		t.Fatalf("codex reads %s", file)
	}
}
