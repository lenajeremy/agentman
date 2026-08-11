package source

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The fixtures below are trimmed copies of real records. None of these shapes
// are documented, so a CLI update can move the field — which is exactly what
// these tests exist to catch.

const claudeAssistantLine = `{"parentUuid":"a1","isSidechain":false,"message":{"role":"assistant","model":"claude-opus-5","content":[{"type":"text","text":"hi"}]},"type":"assistant","uuid":"u1","timestamp":"2026-08-11T12:00:00.000Z"}`

// Claude Code writes this for messages it generated itself.
const claudeSyntheticLine = `{"message":{"role":"assistant","model":"<synthetic>","content":[]},"type":"assistant","uuid":"u2"}`

const codexTurnContextLine = `{"timestamp":"2026-08-10T18:40:30.075Z","type":"turn_context","payload":{"turn_id":"t1","cwd":"/tmp","model":"gpt-5.6-sol","effort":"medium"}}`

const codexWorldStateLine = `{"timestamp":"2026-08-10T18:40:30.074Z","type":"world_state","payload":{"full":true,"state":{"collaboration_mode":{"mode":"default","model":"gpt-5.6-sol"}}}}`

func TestClaudeModelExtraction(t *testing.T) {
	if got := claudeModelOf([]byte(claudeAssistantLine)); got != "claude-opus-5" {
		t.Errorf("got %q, want claude-opus-5", got)
	}
	// "<synthetic>" is Claude Code's own bookkeeping, and showing it as the
	// model would be worse than showing nothing.
	if got := claudeModelOf([]byte(claudeSyntheticLine)); got != "" {
		t.Errorf("got %q, want empty for a synthetic message", got)
	}
	// A user line names no model, and must not be mistaken for one.
	if got := claudeModelOf([]byte(`{"type":"user","message":{"role":"user","content":"hi"}}`)); got != "" {
		t.Errorf("got %q, want empty for a user line", got)
	}
	if got := claudeModelOf([]byte("not json")); got != "" {
		t.Errorf("got %q, want empty for an unparseable line", got)
	}
}

func TestCodexModelExtraction(t *testing.T) {
	if got := codexModelOf([]byte(codexTurnContextLine)); got != "gpt-5.6-sol" {
		t.Errorf("turn_context: got %q, want gpt-5.6-sol", got)
	}
	// The same value is nested differently in world_state; both are real.
	if got := codexModelOf([]byte(codexWorldStateLine)); got != "gpt-5.6-sol" {
		t.Errorf("world_state: got %q, want gpt-5.6-sol", got)
	}
	if got := codexModelOf([]byte(`{"type":"event_msg","payload":{"type":"task_started"}}`)); got != "" {
		t.Errorf("got %q, want empty for a line with no model", got)
	}
}

func TestModelFromTranscriptTakesTheLatest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "t.jsonl")
	// A model change mid-session must win: the user ran /model and everything
	// after that point is the new one.
	lines := []string{
		claudeAssistantLine,
		`{"type":"user","message":{"role":"user","content":"switch"}}`,
		strings.Replace(claudeAssistantLine, "claude-opus-5", "claude-sonnet-5", 1),
		`{"type":"user","message":{"role":"user","content":"go on"}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := modelFromTranscript(path, claudeModelOf); got != "claude-sonnet-5" {
		t.Errorf("got %q, want the most recent model", got)
	}
}

func TestModelFromTranscriptReadsOnlyTheTail(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.jsonl")

	// A transcript far larger than the scan window, with the answer at the end.
	// The point is that a 300MB file must not be read to find this.
	var builder strings.Builder
	filler := `{"type":"user","message":{"role":"user","content":"` + strings.Repeat("x", 900) + `"}}`
	for builder.Len() < modelScanBytes*2 {
		builder.WriteString(filler)
		builder.WriteString("\n")
	}
	builder.WriteString(claudeAssistantLine)
	builder.WriteString("\n")
	if err := os.WriteFile(path, []byte(builder.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := modelFromTranscript(path, claudeModelOf); got != "claude-opus-5" {
		t.Errorf("got %q, want claude-opus-5 from the tail of a large file", got)
	}
}

func TestModelFromTranscriptSurvivesATruncatedFirstLine(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cut.jsonl")

	// The tail window will start mid-record on any real file. That partial line
	// must be skipped, not crash the scan or produce nonsense.
	var builder strings.Builder
	filler := `{"type":"user","message":{"role":"user","content":"` + strings.Repeat("y", 900) + `"}}`
	for builder.Len() < modelScanBytes+5_000 {
		builder.WriteString(filler)
		builder.WriteString("\n")
	}
	builder.WriteString(claudeAssistantLine)
	builder.WriteString("\n")
	if err := os.WriteFile(path, []byte(builder.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := modelFromTranscript(path, claudeModelOf); got != "claude-opus-5" {
		t.Errorf("got %q, want claude-opus-5", got)
	}
}

func TestModelFromTranscriptOnMissingFile(t *testing.T) {
	// A Codex session discovered from a tmux pane has no rollout at all, which
	// is normal and must report "unknown" rather than failing discovery.
	if got := modelFromTranscript(filepath.Join(t.TempDir(), "nope.jsonl"), claudeModelOf); got != "" {
		t.Errorf("got %q, want empty for a file that does not exist", got)
	}
}

func TestModelCacheRemembersMisses(t *testing.T) {
	cache := newModelCache()

	if _, ok := cache.get("claude:s1"); ok {
		t.Error("an empty cache reported a hit")
	}

	// Caching the miss is the point: a session that has not replied yet names
	// no model anywhere, and without this every sweep would re-scan its
	// transcript looking for something that is not there.
	cache.put("claude:s1", "")
	if _, ok := cache.get("claude:s1"); !ok {
		t.Error("a known-empty answer was not cached, so the tail would be re-read every second")
	}

	cache.put("claude:s2", "claude-opus-5")
	if model, ok := cache.get("claude:s2"); !ok || model != "claude-opus-5" {
		t.Errorf("got (%q, %v), want the cached model", model, ok)
	}
}

func TestModelCacheForgetsDeadSessions(t *testing.T) {
	cache := newModelCache()
	cache.put("claude:alive", "claude-opus-5")
	cache.put("claude:dead", "claude-opus-5")

	cache.forget(map[string]bool{"claude:alive": true})

	if _, ok := cache.get("claude:alive"); !ok {
		t.Error("a running session was forgotten")
	}
	if _, ok := cache.get("claude:dead"); ok {
		t.Error("a finished session stayed cached; on a machine that starts agents " +
			"all day this grows without bound")
	}
}
