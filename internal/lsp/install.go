package lsp

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Install is how am gets a server that isn't on the Mac, the first time a
// file of its language opens, into ~/.agentman/lsp/<id>/<version>. Versions
// are pinned here and move with am's releases; downloads are checked
// against pinned checksums, npm runs no install scripts, and nothing counts
// as installed until it is all there.
type Install struct {
	Version string
	// GoPackage is built with go install, as Zed gets gopls.
	GoPackage string
	// NPM is a package installed with npm, run with Node: its Bin names the
	// executable in its package.json.
	NPM string
	Bin string
	// Rustup is a component of the Mac's Rust toolchain, added with rustup
	// when there is one, before Release is downloaded.
	Rustup string
	// Release is a binary to download, by GOOS/GOARCH.
	Release map[string]Asset
	// Size is about how much it takes on disk, for am lsp status.
	Size string
}

// Asset is a file to download and the SHA-256 it must have. A .gz is
// unpacked.
type Asset struct {
	URL    string
	SHA256 string
}

// Node runs the servers installed with npm when the Mac has no Node of its
// own: Node 24 LTS, from nodejs.org.
var Node = Install{
	Version: "24.21.0",
	Release: map[string]Asset{
		"darwin/arm64": {"https://nodejs.org/dist/v24.21.0/node-v24.21.0-darwin-arm64.tar.gz", "bed7eea5325e1108f32ce5228ddd6a5f0f08a499ee42aa7442aea583702f6057"},
		"darwin/amd64": {"https://nodejs.org/dist/v24.21.0/node-v24.21.0-darwin-x64.tar.gz", "1462cb3b3046b815cf8ea436d3da450ec1a9f11dac7e5a46b0ada5305d7e8097"},
		"linux/arm64":  {"https://nodejs.org/dist/v24.21.0/node-v24.21.0-linux-arm64.tar.gz", "724282c3b43aec998aa9527380465b45d229e021b58035f5f4f63095eabfe5d5"},
		"linux/amd64":  {"https://nodejs.org/dist/v24.21.0/node-v24.21.0-linux-x64.tar.gz", "6e1db87ef58b8819e5d5402eff1536491b18edd8eb7bee5ef7897876e88dc5ff"},
	},
	Size: "about 190 MB, once for every server that needs it",
}

// Launch is how to run a server: its program and arguments, and folders
// its PATH needs.
type Launch struct {
	Path string
	Args []string
	Dirs []string
}

// Args0OrPath is what a launch runs: an npm server's script, else the
// program itself.
func (l Launch) Args0OrPath() string {
	if len(l.Args) > 0 && strings.HasSuffix(l.Args[0], ".js") {
		return l.Args[0]
	}
	return l.Path
}

// Installer gets servers into a folder.
type Installer struct {
	// Dir is ~/.agentman/lsp.
	Dir    string
	Tools  *Tools
	Client *http.Client

	mu    sync.Mutex
	locks map[string]*sync.Mutex
}

const okMarker = ".ok"

func platform() string { return runtime.GOOS + "/" + runtime.GOARCH }

func (in *Installer) folder(id, version string) string {
	return filepath.Join(in.Dir, id, version)
}

func (in *Installer) lock(id string) *sync.Mutex {
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.locks == nil {
		in.locks = map[string]*sync.Mutex{}
	}
	if in.locks[id] == nil {
		in.locks[id] = &sync.Mutex{}
	}
	return in.locks[id]
}

// Installed is the launch for a server's pinned version when it's here.
func (in *Installer) Installed(server *Server) (Launch, bool) {
	spec := server.Install
	if spec == nil {
		return Launch{}, false
	}
	dir := in.folder(server.ID, spec.Version)
	if _, err := os.Stat(filepath.Join(dir, okMarker)); err != nil {
		return Launch{}, false
	}
	switch {
	case spec.NPM != "":
		node, ok := in.node()
		if !ok {
			return Launch{}, false
		}
		entry, err := npmEntry(dir, spec.NPM, spec.Bin)
		if err != nil {
			return Launch{}, false
		}
		return Launch{Path: node.Path, Args: append([]string{entry}, server.Args...), Dirs: node.Dirs}, true
	case spec.GoPackage != "":
		return Launch{Path: filepath.Join(dir, "bin", path.Base(spec.GoPackage)), Args: server.Args}, true
	default:
		return Launch{Path: filepath.Join(dir, server.ID), Args: server.Args}, true
	}
}

// node is Node to run npm servers with: the Mac's own, or the one am got.
func (in *Installer) node() (Launch, bool) {
	if path := in.Tools.Look("node"); path != "" {
		return Launch{Path: path, Dirs: []string{filepath.Dir(path)}}, true
	}
	dir := in.folder("node", Node.Version)
	if _, err := os.Stat(filepath.Join(dir, okMarker)); err != nil {
		return Launch{}, false
	}
	bin := filepath.Join(dir, "bin")
	return Launch{Path: filepath.Join(bin, "node"), Dirs: []string{bin}}, true
}

// Install gets a server's pinned version, telling progress what it is
// doing; one install of a server runs at a time, however many ask.
func (in *Installer) Install(ctx context.Context, server *Server, progress func(string)) (Launch, error) {
	spec := server.Install
	if spec == nil {
		return Launch{}, errors.New(server.Missing)
	}
	lock := in.lock(server.ID)
	lock.Lock()
	defer lock.Unlock()
	if spec.Rustup != "" {
		if launch, ok := in.rustupComponent(ctx, server, progress); ok {
			return launch, nil
		}
	}
	if launch, ok := in.Installed(server); ok {
		return launch, nil
	}
	if err := os.MkdirAll(in.Dir, 0o755); err != nil {
		return Launch{}, err
	}
	var err error
	switch {
	case spec.NPM != "":
		err = in.installNPM(ctx, server, progress)
	case spec.GoPackage != "":
		err = in.installGo(ctx, server, progress)
	default:
		asset, ok := spec.Release[platform()]
		if !ok {
			return Launch{}, fmt.Errorf("%s isn't available for %s", server.Name, platform())
		}
		err = in.installed(server.ID, spec.Version, func(tmp string) error {
			return in.download(ctx, asset, filepath.Join(tmp, server.ID), server.Name, progress)
		})
	}
	if err != nil {
		return Launch{}, err
	}
	launch, ok := in.Installed(server)
	if !ok {
		return Launch{}, fmt.Errorf("%s installed, but can't be run", server.Name)
	}
	return launch, nil
}

// rustupComponent adds a server to the Mac's Rust toolchain, as rustup
// itself suggests, and runs it through rustup's proxy, which picks the
// toolchain each project asks for.
func (in *Installer) rustupComponent(ctx context.Context, server *Server, progress func(string)) (Launch, bool) {
	rustup := in.Tools.Look("rustup")
	if rustup == "" {
		return Launch{}, false
	}
	dir := filepath.Dir(rustup)
	proxy := filepath.Join(dir, server.ID)
	if !in.Tools.Runs(proxy, "--version") {
		progress(fmt.Sprintf("Adding %s to your Rust toolchain…", server.Name))
		if err := in.run(ctx, rustup, []string{"component", "add", server.Install.Rustup}, []string{dir}, nil); err != nil {
			return Launch{}, false
		}
		if !in.Tools.Runs(proxy, "--version") {
			return Launch{}, false
		}
	}
	return Launch{Path: proxy, Args: server.Args, Dirs: []string{dir}}, true
}

// installed fills a fresh folder and only then moves it into place, marked
// complete, removing the versions before it: a download cut short leaves
// nothing that looks installed.
func (in *Installer) installed(id, version string, fill func(tmp string) error) error {
	tmp, err := os.MkdirTemp(in.Dir, ".tmp-"+id+"-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if err := fill(tmp); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tmp, okMarker), []byte(version+"\n"), 0o644); err != nil {
		return err
	}
	final := in.folder(id, version)
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return err
	}
	_ = os.RemoveAll(final)
	if err := os.Rename(tmp, final); err != nil {
		return err
	}
	others, _ := os.ReadDir(filepath.Dir(final))
	for _, other := range others {
		if other.Name() != version {
			_ = os.RemoveAll(filepath.Join(filepath.Dir(final), other.Name()))
		}
	}
	return nil
}

func (in *Installer) installNPM(ctx context.Context, server *Server, progress func(string)) error {
	node, ok := in.node()
	if !ok {
		if err := in.installNode(ctx, server, progress); err != nil {
			return err
		}
		if node, ok = in.node(); !ok {
			return errors.New("Node.js installed, but can't be run")
		}
	}
	npm := in.npm(node)
	spec := server.Install
	progress(fmt.Sprintf("Installing %s…", server.Name))
	return in.installed(server.ID, spec.Version, func(tmp string) error {
		args := append(npm[1:], "install", "--prefix", tmp, "--no-save", "--no-package-lock", "--ignore-scripts",
			"--no-audit", "--no-fund", "--loglevel=error", spec.NPM+"@"+spec.Version)
		if err := in.run(ctx, npm[0], args, node.Dirs, nil); err != nil {
			return fmt.Errorf("npm couldn't install %s: %w", spec.NPM, err)
		}
		if _, err := npmEntry(tmp, spec.NPM, spec.Bin); err != nil {
			return err
		}
		return nil
	})
}

// npm is the npm that goes with node: beside it, or inside the Node am got.
func (in *Installer) npm(node Launch) []string {
	beside := filepath.Join(filepath.Dir(node.Path), "npm")
	if _, err := os.Stat(beside); err == nil {
		return []string{beside}
	}
	cli := filepath.Join(filepath.Dir(filepath.Dir(node.Path)), "lib", "node_modules", "npm", "bin", "npm-cli.js")
	return []string{node.Path, cli}
}

func (in *Installer) installNode(ctx context.Context, server *Server, progress func(string)) error {
	lock := in.lock("node")
	lock.Lock()
	defer lock.Unlock()
	if _, ok := in.node(); ok {
		return nil
	}
	asset, ok := Node.Release[platform()]
	if !ok {
		return fmt.Errorf("%s support needs Node.js: https://nodejs.org", server.Language)
	}
	return in.installed("node", Node.Version, func(tmp string) error {
		archive := filepath.Join(tmp, "node.tar.gz")
		if err := in.download(ctx, Asset{URL: asset.URL, SHA256: asset.SHA256}, archive, "Node.js", progress); err != nil {
			return err
		}
		progress("Unpacking Node.js…")
		defer os.Remove(archive)
		return untar(archive, tmp, 1)
	})
}

func (in *Installer) installGo(ctx context.Context, server *Server, progress func(string)) error {
	goBin := in.Tools.Look("go")
	if goBin == "" {
		return fmt.Errorf("%s support needs Go: https://go.dev/dl", server.Language)
	}
	spec := server.Install
	progress(fmt.Sprintf("Building %s…", server.Name))
	return in.installed(server.ID, spec.Version, func(tmp string) error {
		env := []string{"GOBIN=" + filepath.Join(tmp, "bin")}
		if err := in.run(ctx, goBin, []string{"install", spec.GoPackage + "@" + spec.Version}, []string{filepath.Dir(goBin)}, env); err != nil {
			return fmt.Errorf("go couldn't build %s: %w", server.Name, err)
		}
		return nil
	})
}

// run runs an installer's command; its output is kept for a failure.
func (in *Installer) run(ctx context.Context, name string, args, dirs, env []string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(withPath(in.Tools.Env(), dirs), env...)
	cmd.Dir = in.Dir
	var out tail
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%v%s", err, out.suffix())
	}
	return nil
}

// download fetches an asset to path, checking its checksum, unpacking a
// .gz, and reporting how far along it is.
func (in *Installer) download(ctx context.Context, asset Asset, to, name string, progress func(string)) error {
	client := in.Client
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Minute}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.URL, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("couldn't download %s: %w", name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("couldn't download %s: %s", name, resp.Status)
	}
	progress(fmt.Sprintf("Downloading %s…", name))
	part := to + ".part"
	file, err := os.OpenFile(part, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer os.Remove(part)
	sum := sha256.New()
	counted := &counter{r: resp.Body, total: resp.ContentLength, report: func(percent int) {
		progress(fmt.Sprintf("Downloading %s %d%%", name, percent))
	}}
	_, err = io.Copy(io.MultiWriter(file, sum), counted)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("couldn't download %s: %w", name, err)
	}
	if got := hex.EncodeToString(sum.Sum(nil)); !strings.EqualFold(got, asset.SHA256) {
		return fmt.Errorf("%s didn't download as published (checksum %s…), so it wasn't installed", name, got[:12])
	}
	if !strings.HasSuffix(asset.URL, ".gz") || strings.HasSuffix(asset.URL, ".tar.gz") {
		return os.Rename(part, to)
	}
	packed, err := os.Open(part)
	if err != nil {
		return err
	}
	defer packed.Close()
	unzipped, err := gzip.NewReader(packed)
	if err != nil {
		return err
	}
	out, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, unzipped)
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	return err
}

// counter reports a download's percentage, a few times a second.
type counter struct {
	r      io.Reader
	total  int64
	read   int64
	last   time.Time
	report func(int)
}

func (c *counter) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.read += int64(n)
	if c.total > 0 && time.Since(c.last) > 250*time.Millisecond {
		c.last = time.Now()
		c.report(int(c.read * 100 / c.total))
	}
	return n, err
}

// untar unpacks a .tar.gz into dir, dropping the first strip folders of
// each name, as tar --strip-components does. Nothing may land outside dir.
func untar(archive, dir string, strip int) error {
	file, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer file.Close()
	unzipped, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	reader := tar.NewReader(unzipped)
	root := filepath.Clean(dir) + string(os.PathSeparator)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		parts := strings.Split(strings.TrimPrefix(header.Name, "./"), "/")
		if len(parts) <= strip {
			continue
		}
		rest := filepath.Join(parts[strip:]...)
		if rest == "" || rest == "." {
			// The folder being stripped, itself.
			continue
		}
		target := filepath.Join(dir, rest)
		if !strings.HasPrefix(target, root) {
			return fmt.Errorf("archive entry %q is outside its folder", header.Name)
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode)&0o755|0o600)
			if err != nil {
				return err
			}
			_, err = io.Copy(out, reader)
			out.Close()
			if err != nil {
				return err
			}
		case tar.TypeSymlink:
			// npm and npx are links into lib; one pointing out is skipped.
			resolved := filepath.Join(filepath.Dir(target), header.Linkname)
			if filepath.IsAbs(header.Linkname) || !strings.HasPrefix(resolved, root) {
				continue
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			_ = os.Remove(target)
			if err := os.Symlink(header.Linkname, target); err != nil {
				return err
			}
		}
	}
}

// npmEntry is the script a package's bin names, inside dir/node_modules.
func npmEntry(dir, pkg, bin string) (string, error) {
	folder := filepath.Join(dir, "node_modules", filepath.FromSlash(pkg))
	data, err := os.ReadFile(filepath.Join(folder, "package.json"))
	if err != nil {
		return "", fmt.Errorf("%s isn't installed", pkg)
	}
	var manifest struct {
		Bin json.RawMessage `json:"bin"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		return "", err
	}
	var single string
	if json.Unmarshal(manifest.Bin, &single) == nil && single != "" {
		return filepath.Join(folder, filepath.FromSlash(single)), nil
	}
	var named map[string]string
	if json.Unmarshal(manifest.Bin, &named) == nil && named[bin] != "" {
		return filepath.Join(folder, filepath.FromSlash(named[bin])), nil
	}
	return "", fmt.Errorf("%s has no %s to run", pkg, bin)
}
