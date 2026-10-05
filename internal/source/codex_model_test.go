package source

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Codex names the model in its turn_context records, and the adapter looked
// only in the last 256 KiB. A turn whose command printed a lot pushes them
// out of that window: a third of the sessions on this Mac showed no model.
// The model of the first turn is a better answer than none.
func TestACodexModelIsFoundWhenTheTailHasNone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	lines := []string{
		`{"type":"session_meta","payload":{"id":"t1","cwd":"/work"}}`,
		`{"type":"turn_context","payload":{"model":"gpt-5.5-codex","cwd":"/work"}}`,
	}
	output := `{"type":"response_item","payload":{"type":"function_call_output","call_id":"c","output":"` +
		strings.Repeat("x", 64<<10) + `"}}`
	for range 6 {
		lines = append(lines, output)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := codexModel(path); got != "gpt-5.5-codex" {
		t.Errorf("model = %q", got)
	}
}

// The newest model still wins when the tail has one.
func TestACodexModelPrefersTheNewestTurn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	body := `{"type":"turn_context","payload":{"model":"old-model"}}` + "\n" +
		`{"type":"turn_context","payload":{"model":"new-model"}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := codexModel(path); got != "new-model" {
		t.Errorf("model = %q", got)
	}
}
