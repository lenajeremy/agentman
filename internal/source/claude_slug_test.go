package source

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTranscriptUnder puts a transcript in a project folder named exactly as
// given, rather than by this package's own rule, so the rule itself is tested.
func writeTranscriptUnder(t *testing.T, home, folder, id, cwd string) string {
	t.Helper()
	dir := filepath.Join(home, ".claude", "projects", folder)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, id+".jsonl")
	if err := os.WriteFile(path, []byte(claudeUserLine(cwd, "plan the trip")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// Claude Code names a project folder by replacing every character that is not
// an ASCII letter or digit with "-" (2.1.289: replace(/[^a-zA-Z0-9]/g,"-")).
// The adapter replaced only "/" and ".", so a folder with a space, an
// underscore or an accent in its path showed "1 agent" and opened empty.
func TestClaudeHistoryFindsAFolderWithSpacesAndUnderscores(t *testing.T) {
	home := t.TempDir()
	const cwd = "/Users/me/Downloads/trip outside_naij (café)"
	const folder = "-Users-me-Downloads-trip-outside-naij--caf--"
	const id = "11111111-1111-4111-8111-111111111111"
	path := writeTranscriptUnder(t, home, folder, id, cwd)

	s := hermeticClaude(t, home)
	past, err := s.Past(context.Background(), cwd, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(past) != 1 || past[0].NativeID != id {
		t.Fatalf("Past(%q) = %+v, want the session in %s", cwd, past, folder)
	}
	if got := s.transcriptPath(cwd, id); got != path {
		t.Errorf("transcriptPath = %q, want %q", got, path)
	}
}

// Past 200 characters Claude cuts the name and appends a hash of the path.
// The hash need not be reproduced: the first 200 characters find the folder,
// and the cwd recorded in each transcript decides membership as it always has.
func TestClaudeHistoryFindsAFolderWithAVeryLongPath(t *testing.T) {
	home := t.TempDir()
	cwd := "/Users/me/" + strings.Repeat("deeply/nested/", 16) + "project"
	slug := strings.NewReplacer("/", "-").Replace(cwd)
	if len(slug) <= 200 {
		t.Fatalf("fixture slug is only %d characters", len(slug))
	}
	folder := slug[:200] + "-1x2y3z"
	const id = "22222222-2222-4222-8222-222222222222"
	writeTranscriptUnder(t, home, folder, id, cwd)

	past, err := hermeticClaude(t, home).Past(context.Background(), cwd, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(past) != 1 || past[0].NativeID != id {
		t.Fatalf("Past found %+v", past)
	}
}
