package webapp

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

type entry struct {
	name, body string
	kind       byte
	link       string
}

// archive is a .tar.gz of entries, and its SHA-256.
func archive(t *testing.T, entries ...entry) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		kind := e.kind
		if kind == 0 {
			kind = tar.TypeReg
		}
		header := &tar.Header{Name: e.name, Typeflag: kind, Mode: 0o644, Size: int64(len(e.body)), Linkname: e.link}
		if kind != tar.TypeReg {
			header.Size = 0
		}
		if kind == tar.TypeDir {
			header.Mode = 0o755
		}
		if err := tw.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if kind == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(buf.Bytes())
	return buf.Bytes(), hex.EncodeToString(sum[:])
}

// release serves a file as a release would, counting downloads.
func release(t *testing.T, body []byte) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var count atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)
	return server, &count
}

func screens(t *testing.T) ([]byte, string) {
	return archive(t,
		entry{name: "./", kind: tar.TypeDir},
		entry{name: "./index.html", body: "<!doctype html><title>Agentman</title>"},
		entry{name: "./assets/", kind: tar.TypeDir},
		entry{name: "./assets/app.js", body: "console.log('hi')"},
		entry{name: "./editor/fonts/code.woff2", body: "font"},
	)
}

func leftovers(t *testing.T, dir string) []string {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// The first ask downloads this version's screens, checks them, unpacks
// them and serves them; later asks use them as they are. Older versions'
// folders, and a stopped attempt's, go.
func TestTheScreensAreFetchedOnceAndServed(t *testing.T) {
	body, sum := screens(t)
	server, downloads := release(t, body)
	dir := filepath.Join(t.TempDir(), "web")
	for _, old := range []string{"0.46.0", ".partial-123"} {
		if err := os.MkdirAll(filepath.Join(dir, old), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	store := NewStore(dir, "0.47.0", strings.ToUpper(sum), server.URL)
	if _, ok := store.Dir(); ok {
		t.Fatal("ready before fetching")
	}
	folder, fetched, err := store.Ensure(context.Background())
	if err != nil || !fetched || folder != filepath.Join(dir, "0.47.0") {
		t.Fatalf("ensure: %q %v %v", folder, fetched, err)
	}
	if got := leftovers(t, dir); len(got) != 1 || got[0] != "0.47.0" {
		t.Fatalf("left in the folder: %v", got)
	}
	data, err := os.ReadFile(filepath.Join(folder, "editor", "fonts", "code.woff2"))
	if err != nil || string(data) != "font" {
		t.Fatalf("unpacked: %q %v", data, err)
	}

	// A fresh store for the same folder (am serve restarted) finds them.
	again := NewStore(dir, "0.47.0", sum, server.URL)
	if _, fetched, err := again.Ensure(context.Background()); err != nil || fetched {
		t.Fatalf("second ensure: %v %v", fetched, err)
	}
	if downloads.Load() != 1 {
		t.Fatalf("%d downloads", downloads.Load())
	}
	if !Ready(again) {
		t.Fatal("not ready")
	}
	rec := httptest.NewRecorder()
	Handler(again).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/assets/app.js", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "console.log('hi')" {
		t.Fatalf("served: %d %q", rec.Code, rec.Body.String())
	}

	// Another version's am doesn't take this version's screens.
	newer := NewStore(dir, "0.48.0", sum, server.URL)
	if _, ok := newer.Dir(); ok {
		t.Fatal("0.47.0's screens served as 0.48.0's")
	}
}

// A file that isn't the release's is never used, and leaves nothing.
func TestADifferentFileIsRefused(t *testing.T) {
	body, _ := screens(t)
	server, _ := release(t, body)
	dir := filepath.Join(t.TempDir(), "web")
	store := NewStore(dir, "0.47.0", strings.Repeat("0", 64), server.URL)
	_, _, err := store.Ensure(context.Background())
	if err == nil || !strings.Contains(err.Error(), "SHA-256 differs") {
		t.Fatalf("err = %v", err)
	}
	if got := leftovers(t, dir); len(got) != 0 {
		t.Fatalf("left: %v", got)
	}
	rec := httptest.NewRecorder()
	Handler(store).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "am web") {
		t.Fatalf("served: %d %q", rec.Code, rec.Body.String())
	}
}

// Even the release's own file is unpacked only as folders and plain files
// inside the web folder.
func TestUnpackingStaysInsideTheFolder(t *testing.T) {
	for name, bad := range map[string]entry{
		"a name leaving it":  {name: "../evil.js", body: "x"},
		"an absolute name":   {name: "/tmp/evil.js", body: "x"},
		"a link":             {name: "link", kind: tar.TypeSymlink, link: "/etc/passwd"},
		"a hard link":        {name: "hard", kind: tar.TypeLink, link: "index.html"},
		"a backslashed name": {name: `..\evil.js`, body: "x"},
	} {
		t.Run(name, func(t *testing.T) {
			body, sum := archive(t, entry{name: "index.html", body: "ok"}, bad)
			server, _ := release(t, body)
			root := t.TempDir()
			dir := filepath.Join(root, "config", "web")
			store := NewStore(dir, "0.47.0", sum, server.URL)
			if _, _, err := store.Ensure(context.Background()); err == nil {
				t.Fatal("unpacked")
			}
			if _, err := os.Stat(filepath.Join(root, "config", "evil.js")); err == nil {
				t.Fatal("wrote outside")
			}
			if got := leftovers(t, dir); len(got) != 0 {
				t.Fatalf("left: %v", got)
			}
		})
	}
}

func TestADevelopmentBuildFetchesNothing(t *testing.T) {
	store := NewStore(t.TempDir(), "dev", "", "http://127.0.0.1:1/never")
	if _, _, err := store.Ensure(context.Background()); !errors.Is(err, ErrNotFetchable) {
		t.Fatalf("err = %v", err)
	}
	if store.Fetchable() || Ready(store) {
		t.Fatal("a development build has nothing to fetch")
	}
	var none *Store
	if none.Fetchable() {
		t.Fatal("no store")
	}
	if _, ok := none.Dir(); ok {
		t.Fatal("no store")
	}
}

// A release without the file (an older one, or before CI finished) says so.
func TestAMissingFileSaysWhere(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(server.Close)
	store := NewStore(t.TempDir(), "0.47.0", strings.Repeat("a", 64), server.URL+"/agentman-web_v0.47.0.tar.gz")
	_, _, err := store.Ensure(context.Background())
	if err == nil || !strings.Contains(err.Error(), "404") || !strings.Contains(err.Error(), "agentman-web_v0.47.0.tar.gz") {
		t.Fatalf("err = %v", err)
	}
}

// The release's address is by version.
func TestTheReleaseAddressIsTheVersions(t *testing.T) {
	t.Setenv("AGENTMAN_WEB_URL", "")
	store := ForThisBuild(t.TempDir(), "0.47.0")
	if store.url != "https://github.com/lenajeremy/agentman/releases/download/v0.47.0/agentman-web_v0.47.0.tar.gz" {
		t.Fatalf("url = %s", store.url)
	}
}
