package source

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// rewriteInPlace swaps text for same-length text and puts the modification
// time back, so the file looks unchanged to anything that only stats it. A
// listing that still shows the old text did not read the file again.
func rewriteInPlace(t *testing.T, path, old, replacement string) {
	t.Helper()
	if len(old) != len(replacement) {
		t.Fatal("the replacement must keep the size")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, bytes.ReplaceAll(body, []byte(old), []byte(replacement)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
}

func touchLater(t *testing.T, path string) {
	t.Helper()
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
}

type pastLister interface {
	Past(ctx context.Context, dir string, limit int) ([]protocol.Session, error)
}

func onlyPast(t *testing.T, s pastLister, dir string) protocol.Session {
	t.Helper()
	past, err := s.Past(context.Background(), dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(past) != 1 {
		t.Fatalf("listed %d sessions, want 1", len(past))
	}
	return past[0]
}

// Every discovery sweep, once a second, drops what it remembers about
// sessions that are not running. History listings shared that memory, so
// every listing of a folder read each ended transcript's head and tail again.
// What a listing reads from a file is now kept until the file changes.
func TestClaudeHistoryIsNotReadAgainAfterASweep(t *testing.T) {
	home := t.TempDir()
	work := filepath.Join(home, "code", "api")
	path := writeClaudeTranscript(t, home, "11111111-1111-4111-8111-111111111111", work,
		claudeUserLine(work, "fix the retry backoff"),
		`{"type":"assistant","message":{"role":"assistant","model":"claude-opus-4-1","content":[]}}`)
	s := hermeticClaude(t, home)

	first := onlyPast(t, s, work)
	if first.Name != "fix the retry backoff" || first.Model != "claude-opus-4-1" {
		t.Fatalf("first listing = %q on %q", first.Name, first.Model)
	}
	s.models.forget(nil) // what a sweep does when none of this is running

	rewriteInPlace(t, path, "fix the retry backoff", "fix the retry timeout")
	rewriteInPlace(t, path, "claude-opus-4-1", "claude-opus-4-2")
	again := onlyPast(t, s, work)
	if again.Name != first.Name || again.Model != first.Model {
		t.Errorf("an unchanged transcript was read again: %q on %q", again.Name, again.Model)
	}

	touchLater(t, path)
	changed := onlyPast(t, s, work)
	if changed.Name != "fix the retry timeout" || changed.Model != "claude-opus-4-2" {
		t.Errorf("a changed transcript was not read again: %q on %q", changed.Name, changed.Model)
	}
}

func TestCodexHistoryIsNotReadAgainAfterASweep(t *testing.T) {
	home := t.TempDir()
	work := filepath.Join(home, "code", "api")
	path := writeCodexHistoryRollout(t, home, "019cc033-f3f2-7c32-9215-a860efb9f9fd", work, `"cli"`,
		`{"type":"turn_context","payload":{"model":"gpt-5.5-codex"}}`,
		codexUserTurn("why is the relay flaky?"))
	s, err := NewCodexSource(home)
	if err != nil {
		t.Fatal(err)
	}
	headers := 0
	s.readMeta = func(path string) (codexMeta, error) {
		headers++
		return readCodexMeta(path)
	}
	ctx := context.Background()

	first := onlyPast(t, s, work)
	if first.Name != "why is the relay flaky?" || first.Model != "gpt-5.5-codex" {
		t.Fatalf("first listing = %q on %q", first.Name, first.Model)
	}
	// What a sweep does when none of this is running.
	s.models.forget(nil)
	s.forgetCodexRollouts(nil)

	rewriteInPlace(t, path, "why is the relay flaky?", "why is the relay broken")
	rewriteInPlace(t, path, "gpt-5.5-codex", "gpt-5.6-codex")
	again := onlyPast(t, s, work)
	if _, err := s.Directories(ctx); err != nil {
		t.Fatal(err)
	}
	if again.Name != first.Name || again.Model != first.Model {
		t.Errorf("an unchanged rollout was read again: %q on %q", again.Name, again.Model)
	}
	if headers != 1 {
		t.Errorf("the rollout's header was read %d times", headers)
	}

	touchLater(t, path)
	changed := onlyPast(t, s, work)
	if changed.Name != "why is the relay broken" || changed.Model != "gpt-5.6-codex" {
		t.Errorf("a changed rollout was not read again: %q on %q", changed.Name, changed.Model)
	}
}

// The memory is bounded, least recently used first.
func TestPastFactsAreBounded(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "t.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	var facts pastFacts[int]
	for i := range maxPastFacts + 100 {
		facts.put(filepath.Join(dir, string(rune('a'+i%26)), time.Duration(i).String()), info, i)
	}
	if got := len(facts.entries); got > maxPastFacts {
		t.Errorf("%d entries kept, want at most %d", got, maxPastFacts)
	}
	facts.put(path, info, 7)
	for i := range maxPastFacts - 1 {
		facts.put(filepath.Join(dir, "later", time.Duration(i).String()), info, i)
	}
	if got, ok := facts.get(path, info); !ok || got != 7 {
		t.Error("an entry was evicted before older ones")
	}
}
