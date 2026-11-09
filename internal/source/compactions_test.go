package source

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func writeUnder(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMuseCompactionsAreCounted(t *testing.T) {
	data := t.TempDir()
	writeUnder(t, filepath.Join(data, "sessions", "2026", "11", "09", "abc", "session.jsonl"),
		`{"payload_type":"context.compact.manual.target"}`+"\n"+
			`{"payload_type":"context.compact.manual.outcome","payload":{"terminal_outcome":"compacted"}}`+"\n"+
			`{"payload_type":"context.compaction.installed"}`+"\n")
	s := &MuseSource{home: data}
	got, ok := s.Compactions(context.Background(), "muse:abc")
	if !ok || got != 2 {
		t.Fatalf("got %d, %v", got, ok)
	}
	if _, ok := s.Compactions(context.Background(), "muse:missing"); ok {
		t.Fatal("found a session that isn't there")
	}
	if _, ok := s.Compactions(context.Background(), "muse:*"); ok {
		t.Fatal("a pattern matched as an id")
	}
}

func TestCursorCompactionsAreCounted(t *testing.T) {
	home := t.TempDir()
	writeUnder(t, filepath.Join(home, ".cursor", "projects", "p", "agent-transcripts", "abc", "abc.jsonl"),
		`{"role":"user","message":{"content":[{"type":"text","text":"<available_subagent_types>\nx"}]}}`+"\n")
	s := &CursorCLISource{home: home}
	got, ok := s.Compactions(context.Background(), "cursor-cli:abc")
	if !ok || got != 1 {
		t.Fatalf("got %d, %v", got, ok)
	}
}

// The registry holds the group, so that is where the count has to be found.
func TestCursorCompactionsReachTheRegistry(t *testing.T) {
	home := t.TempDir()
	writeUnder(t, filepath.Join(home, ".cursor", "projects", "p", "agent-transcripts", "abc", "abc.jsonl"),
		"<available_subagent_types>\n")
	registry := NewRegistry()
	registry.Add(&CursorCLIGroup{terminal: &CursorCLISource{home: home}})
	got, ok := registry.Compactions(context.Background(), "cursor-cli:abc")
	if !ok || got != 1 {
		t.Fatalf("got %d, %v", got, ok)
	}
}
