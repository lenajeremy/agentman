package lsp

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func gz(data []byte) []byte {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	w.Write(data)
	w.Close()
	return buf.Bytes()
}

func sum(data []byte) string {
	s := sha256.Sum256(data)
	return hex.EncodeToString(s[:])
}

// release serves a file and counts how many times it was fetched.
func release(t *testing.T, body []byte, cut bool) (string, *atomic.Int32) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if cut {
			w.Header().Set("Content-Length", fmt.Sprint(len(body)*2))
			w.Write(body)
			return
		}
		w.Write(body)
	}))
	t.Cleanup(server.Close)
	return server.URL + "/tool.gz", &hits
}

func releaseServer(url, checksum, version string) *Server {
	return &Server{ID: "tool", Name: "Tool", Language: "Tool", Install: &Install{Version: version, Release: map[string]Asset{platform(): {URL: url, SHA256: checksum}}}}
}

func installer(t *testing.T) *Installer {
	tools := &Tools{}
	tools.use([]string{"/usr/bin", "/bin"})
	return &Installer{Dir: t.TempDir(), Tools: tools}
}

func TestADownloadIsCheckedUnpackedAndFetchedOnce(t *testing.T) {
	program := []byte("#!/bin/sh\necho tool\n")
	packed := gz(program)
	url, hits := release(t, packed, false)
	in := installer(t)
	server := releaseServer(url, sum(packed), "1.0")
	var wg sync.WaitGroup
	launches := make([]Launch, 3)
	for i := range launches {
		wg.Go(func() {
			launch, err := in.Install(context.Background(), server, func(string) {})
			if err != nil {
				t.Error(err)
			}
			launches[i] = launch
		})
	}
	wg.Wait()
	if hits.Load() != 1 {
		t.Errorf("downloaded %d times", hits.Load())
	}
	got, err := os.ReadFile(launches[0].Path)
	if err != nil || !bytes.Equal(got, program) {
		t.Fatalf("installed %q, %v", got, err)
	}
	if info, _ := os.Stat(launches[0].Path); info.Mode()&0o111 == 0 {
		t.Error("the server can't be run")
	}
	if _, ok := in.Installed(server); !ok {
		t.Error("not installed after installing")
	}
}

func TestADownloadThatIsntAsPublishedIsRefused(t *testing.T) {
	url, _ := release(t, gz([]byte("tampered")), false)
	in := installer(t)
	server := releaseServer(url, sum([]byte("what was published")), "1.0")
	_, err := in.Install(context.Background(), server, func(string) {})
	if err == nil || !strings.Contains(err.Error(), "didn't download as published") {
		t.Fatalf("err = %v", err)
	}
	assertNothingInstalled(t, in, server)
}

func TestADownloadCutShortLeavesNothingInstalled(t *testing.T) {
	packed := gz([]byte("#!/bin/sh\n"))
	url, _ := release(t, packed, true)
	in := installer(t)
	server := releaseServer(url, sum(packed), "1.0")
	if _, err := in.Install(context.Background(), server, func(string) {}); err == nil {
		t.Fatal("a short download installed")
	}
	assertNothingInstalled(t, in, server)
}

func assertNothingInstalled(t *testing.T, in *Installer, server *Server) {
	t.Helper()
	if _, ok := in.Installed(server); ok {
		t.Error("counts as installed")
	}
	if _, err := os.Stat(in.folder(server.ID, server.Install.Version)); err == nil {
		t.Error("its folder is there")
	}
	leftovers, _ := filepath.Glob(filepath.Join(in.Dir, ".tmp-*"))
	if len(leftovers) > 0 {
		t.Errorf("left behind %v", leftovers)
	}
}

func TestANewVersionReplacesTheOne(t *testing.T) {
	packed := gz([]byte("#!/bin/sh\n"))
	url, _ := release(t, packed, false)
	in := installer(t)
	in.Install(context.Background(), releaseServer(url, sum(packed), "1.0"), func(string) {})
	if _, err := in.Install(context.Background(), releaseServer(url, sum(packed), "2.0"), func(string) {}); err != nil {
		t.Fatal(err)
	}
	versions, _ := os.ReadDir(filepath.Join(in.Dir, "tool"))
	if len(versions) != 1 || versions[0].Name() != "2.0" {
		t.Errorf("versions kept: %v", versions)
	}
}

func tarball(t *testing.T, entries ...*tar.Header) string {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	for _, h := range entries {
		if h.Typeflag == tar.TypeReg {
			h.Size = int64(len(h.Name))
		}
		tw.WriteHeader(h)
		if h.Typeflag == tar.TypeReg {
			tw.Write([]byte(h.Name))
		}
	}
	tw.Close()
	gw.Close()
	path := filepath.Join(t.TempDir(), "a.tar.gz")
	os.WriteFile(path, buf.Bytes(), 0o644)
	return path
}

func TestAnArchiveUnpacksInsideItsFolderOnly(t *testing.T) {
	archive := tarball(t,
		&tar.Header{Name: "node-v1/", Typeflag: tar.TypeDir, Mode: 0o755},
		&tar.Header{Name: "node-v1/bin/", Typeflag: tar.TypeDir, Mode: 0o755},
		&tar.Header{Name: "node-v1/bin/node", Typeflag: tar.TypeReg, Mode: 0o755},
		&tar.Header{Name: "node-v1/lib/npm/cli.js", Typeflag: tar.TypeReg, Mode: 0o644},
		&tar.Header{Name: "node-v1/bin/npm", Typeflag: tar.TypeSymlink, Linkname: "../lib/npm/cli.js"},
		&tar.Header{Name: "node-v1/bin/sneaky", Typeflag: tar.TypeSymlink, Linkname: "../../../../etc/passwd"},
	)
	dir := t.TempDir()
	if err := untar(archive, dir, 1); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "bin", "npm")); string(data) != "node-v1/lib/npm/cli.js" {
		t.Errorf("npm link reads %q", data)
	}
	if _, err := os.Lstat(filepath.Join(dir, "bin", "sneaky")); err == nil {
		t.Error("a link out of the folder was made")
	}
	escape := tarball(t, &tar.Header{Name: "x/../../evil", Typeflag: tar.TypeReg, Mode: 0o644})
	if err := untar(escape, t.TempDir(), 1); err == nil {
		t.Error("an entry outside the folder was written")
	}
}

// fakeTools is a folder of scripts standing in for node, npm and go.
func fakeTools(t *testing.T, scripts map[string]string) (*Tools, string) {
	dir := t.TempDir()
	for name, body := range scripts {
		os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body), 0o755)
	}
	tools := &Tools{}
	tools.use([]string{dir, "/usr/bin", "/bin"})
	return tools, dir
}

func TestServersFromNPMInstallWithoutScriptsAndRunOnNode(t *testing.T) {
	tools, bin := fakeTools(t, map[string]string{
		"node": "exit 0\n",
		// Writes the package as npm would, and what it was asked.
		"npm": `echo "$@" > "$(dirname "$0")/npm.args"
prefix=""; while [ $# -gt 1 ]; do [ "$1" = "--prefix" ] && prefix="$2"; shift; done
mkdir -p "$prefix/node_modules/@vtsls/language-server/bin"
echo '{"bin":{"vtsls":"bin/vtsls.js"}}' > "$prefix/node_modules/@vtsls/language-server/package.json"
`,
	})
	in := &Installer{Dir: t.TempDir(), Tools: tools}
	server := &Server{ID: "typescript", Name: "TypeScript", Language: "TypeScript", Args: []string{"--stdio"},
		Install: &Install{Version: "0.3.0", NPM: "@vtsls/language-server", Bin: "vtsls"}}
	launch, err := in.Install(context.Background(), server, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	if launch.Path != filepath.Join(bin, "node") || len(launch.Args) != 2 || !strings.HasSuffix(launch.Args[0], "bin/vtsls.js") || launch.Args[1] != "--stdio" {
		t.Errorf("launch = %+v", launch)
	}
	args, _ := os.ReadFile(filepath.Join(bin, "npm.args"))
	for _, want := range []string{"install", "--ignore-scripts", "@vtsls/language-server@0.3.0"} {
		if !strings.Contains(string(args), want) {
			t.Errorf("npm was run without %s: %s", want, args)
		}
	}
}

func TestGoplsIsBuiltWithGoInstall(t *testing.T) {
	tools, bin := fakeTools(t, map[string]string{
		"go": `echo "$@" > "$(dirname "$0")/go.args"; mkdir -p "$GOBIN"; printf '#!/bin/sh\n' > "$GOBIN/gopls"; chmod +x "$GOBIN/gopls"
`,
	})
	in := &Installer{Dir: t.TempDir(), Tools: tools}
	server := &Server{ID: "gopls", Name: "gopls", Language: "Go", Install: &Install{Version: "v0.24.0", GoPackage: "golang.org/x/tools/gopls"}}
	launch, err := in.Install(context.Background(), server, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(launch.Path) != "gopls" || !strings.Contains(launch.Path, filepath.Join("gopls", "v0.24.0", "bin")) {
		t.Errorf("launch = %+v", launch)
	}
	if args, _ := os.ReadFile(filepath.Join(bin, "go.args")); strings.TrimSpace(string(args)) != "install golang.org/x/tools/gopls@v0.24.0" {
		t.Errorf("go was run with %q", args)
	}
}

func TestGoSupportWithoutGoSaysSo(t *testing.T) {
	tools, _ := fakeTools(t, nil)
	in := &Installer{Dir: t.TempDir(), Tools: tools}
	server := &Server{ID: "gopls", Name: "gopls", Language: "Go", Install: &Install{Version: "v1", GoPackage: "golang.org/x/tools/gopls"}}
	if _, err := in.Install(context.Background(), server, func(string) {}); err == nil || !strings.Contains(err.Error(), "needs Go") {
		t.Errorf("err = %v", err)
	}
}

func TestTheFirstFileOfALanguageGetsItsServer(t *testing.T) {
	self, _ := os.Executable()
	program := []byte(fmt.Sprintf("#!/bin/sh\nexec %q \"$@\"\n", self))
	packed := gz(program)
	url, _ := release(t, packed, false)
	server := &Server{ID: "fake", Name: "Fake", Language: "Fake", Languages: []string{"fake"}, Markers: []string{"fake.root"},
		Install: &Install{Version: "1.0", Release: map[string]Asset{platform(): {URL: url, SHA256: sum(packed)}}}}
	h := newHarness(t, Options{Servers: []*Server{server}, Find: func(*Server) string { return "" }, Dir: t.TempDir()})
	root := project(t)
	w := h.window()
	session := w.resolve(1, filepath.Join(root, "a.fake"), root)
	f := w.until("installing", func(f frame) bool { return f.T == "status" && f.Status.State == StateInstalling })
	if !strings.Contains(f.Status.Detail, "Fake") {
		t.Errorf("detail = %q", f.Status.Detail)
	}
	w.ready(session)
}

func TestNoDownloadsMeansTheMacsOwnOnly(t *testing.T) {
	server := &Server{ID: "fake", Name: "Fake", Language: "Fake", Languages: []string{"fake"}, Markers: []string{"fake.root"},
		Missing: "Fake isn't installed", Install: &Install{Version: "1.0", Release: map[string]Asset{platform(): {URL: "http://127.0.0.1:1/x.gz", SHA256: "0"}}}}
	h := newHarness(t, Options{Servers: []*Server{server}, Find: func(*Server) string { return "" }, Dir: t.TempDir(), NoDownloads: true})
	root := project(t)
	w := h.window()
	w.resolve(1, filepath.Join(root, "a.fake"), root)
	f := w.until("failed", func(f frame) bool { return f.T == "status" && f.Status.State == StateFailed })
	if f.Status.Detail != "Fake isn't installed" {
		t.Errorf("detail = %q", f.Status.Detail)
	}
}

func TestATurnedOffServerDoesntRun(t *testing.T) {
	h := newHarness(t, Options{Disabled: map[string]bool{"fake": true}})
	root := project(t)
	w := h.window()
	w.send(frame{T: "resolve", ID: 1, File: filepath.Join(root, "a.fake"), Folder: root, Language: "fake"})
	if f := w.until("resolved", func(f frame) bool { return f.T == "resolved" }); f.Status != nil {
		t.Error("a turned-off server resolved")
	}
}

func TestTheUsersOwnProgramComesFirst(t *testing.T) {
	self, _ := os.Executable()
	h := newHarness(t, Options{Find: func(*Server) string { return "/nonexistent/fake" }, Paths: map[string]string{"fake": self}})
	root := project(t)
	w := h.window()
	w.ready(w.resolve(1, filepath.Join(root, "a.fake"), root))
}
