package apphost

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A folder copied into itself, or into a folder inside it, is refused: it
// would find its own copy as it went and copy that, until the disk filled.
func TestAFolderIsNotCopiedIntoItself(t *testing.T) {
	root := t.TempDir()
	shop := filepath.Join(root, "shop")
	if err := os.MkdirAll(filepath.Join(shop, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(shop, "src", "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files := &EditorFiles{}
	for _, into := range []string{filepath.Join(shop, "shop copy"), filepath.Join(shop, "src", "shop copy")} {
		err := files.Copy(shop, into)
		if err == nil || !strings.Contains(err.Error(), "into itself") {
			t.Fatalf("copied into %s: %v", into, err)
		}
		if _, err := os.Lstat(into); err == nil {
			t.Fatalf("%s was made", into)
		}
	}
	// Beside it, and a folder whose name only begins the same, are fine.
	if err := files.Copy(shop, filepath.Join(root, "shop copy")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "shop copy", "src", "a.go")); err != nil {
		t.Fatal(err)
	}
	if err := files.Copy(filepath.Join(shop, "src"), filepath.Join(root, "shopping")); err != nil {
		t.Fatal(err)
	}
	for path, dir := range map[string]string{"/a/b": "/a", "/a": "/a", "/a/b/../c": "/a"} {
		if !within(path, dir) {
			t.Errorf("%s is within %s", path, dir)
		}
	}
	for path, dir := range map[string]string{"/ab": "/a", "/a": "/a/b", "/a/../b": "/a", "/..x/b": "/a"} {
		if within(path, dir) {
			t.Errorf("%s isn't within %s", path, dir)
		}
	}
}

// A picture opened in a tab of its own is sandboxed: an SVG's scripts don't
// run as the app.
func TestServedPicturesAreSandboxed(t *testing.T) {
	dir := t.TempDir()
	svg := filepath.Join(dir, "logo.svg")
	if err := os.WriteFile(svg, []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>fetch("/app/host/x")</script></svg>`), 0o644); err != nil {
		t.Fatal(err)
	}
	png := filepath.Join(dir, "shot.png")
	if err := os.WriteFile(png, []byte("\x89PNG\r\n\x1a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{svg, png} {
		rec := httptest.NewRecorder()
		ServeEditorFile(rec, httptest.NewRequest(http.MethodGet, "/__editor/file?path="+url.QueryEscape(path), nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d", path, rec.Code)
		}
		policy := rec.Header().Get("Content-Security-Policy")
		if !strings.HasPrefix(policy, "sandbox") || !strings.Contains(policy, "default-src 'none'") {
			t.Errorf("%s: policy %q", path, policy)
		}
		if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: sniffable", path)
		}
	}
	rec := httptest.NewRecorder()
	ServeEditorFile(rec, httptest.NewRequest(http.MethodGet, "/__editor/file?path="+url.QueryEscape(filepath.Join(dir, "notes.txt")), nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("a non-picture was served: %d", rec.Code)
	}
}
