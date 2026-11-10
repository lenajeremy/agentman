package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSearchColumnsInUTF16(t *testing.T) {
	re, _ := searchExpression("needle", SearchOptions{})
	// The emoji takes two UTF-16 units, as Monaco counts.
	matches, more := searchMatches(re, "first\n  😀 a Needle and a needle\n", "a.txt", 100)
	if more || len(matches) != 1 {
		t.Fatalf("matches = %+v, more = %v", matches, more)
	}
	m := matches[0]
	if m.Line != 2 || m.Column != 8 || m.Length != 6 || m.Count != 2 {
		t.Fatalf("match = %+v", m)
	}
	if m.Preview != "😀 a Needle and a needle" {
		t.Fatalf("preview = %q", m.Preview)
	}
	want := []SearchRange{{Location: 5, Length: 6}, {Location: 18, Length: 6}}
	if len(m.Highlights) != 2 || m.Highlights[0] != want[0] || m.Highlights[1] != want[1] {
		t.Fatalf("highlights = %+v", m.Highlights)
	}
}

func TestSearchLongLineIsCut(t *testing.T) {
	line := strings.Repeat("x", 100) + "word" + strings.Repeat("y", 200)
	preview, highlight, shift := searchPreviewOf(line, 100, 4)
	if !strings.HasPrefix(preview, "…") || !strings.HasSuffix(preview, "…") {
		t.Fatalf("preview = %q", preview)
	}
	if got := string([]rune(preview)[highlight.Location : highlight.Location+highlight.Length]); got != "word" {
		t.Fatalf("highlight covers %q", got)
	}
	if shift != 59 {
		t.Fatalf("shift = %d", shift)
	}
}

func TestSearchStopsAtTheLimitAndReadsEdits(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "a.txt"), []byte(strings.Repeat("hit\n", 1500)), 0o644)
	os.WriteFile(filepath.Join(root, "b.txt"), []byte(strings.Repeat("hit\n", 1500)), 0o644)
	os.WriteFile(filepath.Join(root, "c.txt"), []byte("nothing"), 0o644)
	results := runSearch(root, "hit", SearchOptions{}, []string{"c.txt", "b.txt", "a.txt"}, map[string]string{"c.txt": "a hit"}, nil)
	if !results.Truncated || results.Total != searchMaxMatches {
		t.Fatalf("total = %d, truncated = %v", results.Total, results.Truncated)
	}
	if len(results.Files) != 2 || results.Files[0].Path != "a.txt" {
		t.Fatalf("files = %d, first %s", len(results.Files), results.Files[0].Path)
	}
	edited := runSearch(root, "hit", SearchOptions{}, []string{"c.txt"}, map[string]string{"c.txt": "a hit"}, nil)
	if edited.Total != 1 {
		t.Fatalf("unsaved edits not searched: %+v", edited)
	}
	bad := runSearch(root, "(", SearchOptions{Regex: true}, []string{"a.txt"}, nil, nil)
	if bad.Problem != "Not a regular expression" {
		t.Fatalf("problem = %q", bad.Problem)
	}
}

func TestFinderOrder(t *testing.T) {
	names := []string{"b", "File 10", "file 2", "A", "_x"}
	entries := []EditorEntry{}
	root := t.TempDir()
	for _, n := range names {
		os.WriteFile(filepath.Join(root, n), nil, 0o644)
	}
	os.Mkdir(filepath.Join(root, "zdir"), 0o755)
	os.WriteFile(filepath.Join(root, ".DS_Store"), nil, 0o644)
	entries = listFolder(root, "")
	got := []string{}
	for _, e := range entries {
		got = append(got, e.Name)
	}
	if strings.Join(got, ",") != "zdir,_x,A,b,file 2,File 10" {
		t.Fatalf("order = %v", got)
	}
}

func TestWriteKeepsPermissionsAndLinks(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real.sh")
	os.WriteFile(real, []byte("old"), 0o755)
	link := filepath.Join(root, "link.sh")
	os.Symlink(real, link)
	if _, err := (&EditorFiles{}).Write(link, "new"); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(real); string(data) != "new" {
		t.Fatalf("real file = %q", data)
	}
	if info, _ := os.Lstat(link); info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the link was written over")
	}
	if info, _ := os.Stat(real); info.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %v", info.Mode().Perm())
	}
}
