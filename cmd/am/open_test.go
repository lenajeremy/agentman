package main

import (
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func editorQuery(t *testing.T, link string) url.Values {
	t.Helper()
	rest, ok := strings.CutPrefix(link, "agentman://edit?")
	if !ok {
		t.Fatalf("link %q", link)
	}
	query, err := url.ParseQuery(rest)
	if err != nil {
		t.Fatal(err)
	}
	return query
}

func TestOpenLinksAFolderWithItsAgent(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link, folder, err := editorLink(dir)
	query := editorQuery(t, link)
	if err != nil || folder != dir || query.Get("folder") != dir || query.Get("agent") != "here" || query.Has("file") {
		t.Fatalf("folder: %q %q %v", link, folder, err)
	}

	// A file opens in its folder, in a tab.
	link, folder, err = editorLink(filepath.Join(dir, "main.go"))
	query = editorQuery(t, link)
	if err != nil || folder != dir || query.Get("folder") != dir || query.Get("file") != "main.go" {
		t.Fatalf("file: %q %q %v", link, folder, err)
	}

	if _, _, err := editorLink(filepath.Join(dir, "missing")); err == nil || !strings.Contains(err.Error(), "no such folder or file") {
		t.Fatalf("missing: %v", err)
	}
}

// Agents report their folder by its real path; a folder reached through a
// link is named the same way, so its agent is found.
func TestOpenNamesAFolderByItsRealPath(t *testing.T) {
	real, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(t.TempDir(), "shop")
	if err := os.Symlink(real, linked); err != nil {
		t.Fatal(err)
	}
	if _, folder, err := editorLink(linked); err != nil || folder != real {
		t.Fatalf("folder %q, want %q (%v)", folder, real, err)
	}
}

func TestOpenWithNoFolderOpensTheCurrentOne(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("am open is for Agentman for Mac")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	var opened []string
	open := openLink
	openLink = func(link string) error { opened = append(opened, link); return nil }
	t.Cleanup(func() { openLink = open })

	if err := runOpen(nil); err != nil {
		t.Fatal(err)
	}
	if len(opened) != 1 || editorQuery(t, opened[0]).Get("folder") != dir {
		t.Fatalf("opened %q, want %s", opened, dir)
	}
	if err := runOpen([]string{"a", "b"}); err == nil {
		t.Fatal("took two folders")
	}
}
