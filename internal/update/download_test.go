package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func tarball(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zipped := gzip.NewWriter(&buf)
	archive := tar.NewWriter(zipped)
	for name, content := range files {
		if err := archive.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := archive.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zipped.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sumOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// A release served the way GitHub serves one: the archive for this system
// and checksums.txt beside it, under the version's tag.
func release(t *testing.T, archive []byte, listed string) *httptest.Server {
	t.Helper()
	name := Archive("0.31.0", "darwin", "arm64")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v0.31.0/checksums.txt":
			_, _ = w.Write([]byte(listed))
		case "/v0.31.0/" + name:
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

// The am in a release is taken out of its archive only once the archive
// matches the checksum the release published, as the install script does.
func TestAReleaseIsFetchedAndChecked(t *testing.T) {
	ctx := context.Background()
	name := Archive("0.31.0", "darwin", "arm64")
	good := tarball(t, map[string]string{"README.md": "read me", "LICENSE": "MIT", "am": "the new am"})
	sums := sumOf([]byte("other")) + "  agentman_v0.31.0_linux_amd64.tar.gz\n" + sumOf(good) + "  " + name + "\n"

	server := release(t, good, sums)
	binary, err := Fetch(ctx, server.Client(), server.URL, "0.31.0", "darwin", "arm64")
	if err != nil || string(binary) != "the new am" {
		t.Fatalf("Fetch = %q, %v", binary, err)
	}

	tampered := tarball(t, map[string]string{"am": "something else"})
	if _, err := Fetch(ctx, server.Client(), release(t, tampered, sums).URL, "0.31.0", "darwin", "arm64"); err == nil ||
		!strings.Contains(err.Error(), "does not match its published checksum") {
		t.Errorf("a tampered archive: %v", err)
	}

	empty := tarball(t, map[string]string{"README.md": "no binary", "bin/am": "not at the top"})
	if _, err := Fetch(ctx, server.Client(), release(t, empty, sumOf(empty)+"  "+name+"\n").URL, "0.31.0", "darwin", "arm64"); err == nil ||
		!strings.Contains(err.Error(), "holds no am") {
		t.Errorf("an archive without am: %v", err)
	}

	if _, err := Fetch(ctx, server.Client(), server.URL, "0.31.0", "freebsd", "riscv64"); err == nil ||
		!strings.Contains(err.Error(), "no checksum") {
		t.Errorf("a system with no build: %v", err)
	}
}
