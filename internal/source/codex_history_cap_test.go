package source

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// History reads at most codexHistoryScanFiles rollouts. It walked the
// sessions tree oldest day first and stopped at the cap, so past it the
// newest sessions — the ones a folder most needs to show — were the ones
// left out.
func TestCodexHistoryPastItsCapKeepsTheNewest(t *testing.T) {
	home := t.TempDir()
	write := func(month, day string, i int) {
		dir := filepath.Join(home, ".codex", "sessions", "2026", month, day)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		name := fmt.Sprintf("rollout-2026-%s-%sT10-00-00-%05d.jsonl", month, day, i)
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for i := range codexHistoryScanFiles {
		write("01", "01", i)
	}
	write("10", "05", 0)

	src, err := NewCodexSource(home)
	if err != nil {
		t.Fatal(err)
	}
	all := src.allRollouts()
	if len(all) != codexHistoryScanFiles {
		t.Fatalf("kept %d rollouts, want the cap (%d)", len(all), codexHistoryScanFiles)
	}
	found := false
	for _, rollout := range all {
		if filepath.Base(rollout.path) == "rollout-2026-10-05T10-00-00-00000.jsonl" {
			found = true
		}
	}
	if !found {
		t.Error("today's rollout was dropped in favour of January's")
	}
}
