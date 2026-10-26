// Package lsp runs language servers for the editor in Agentman for Mac.
//
// The editor's page (Monaco, in a web view) talks to `am serve` over one
// local WebSocket per window. For each file it asks which server and project
// root serve it; am starts that server on first use, speaks LSP to it over
// stdio, and passes messages between the server and every window using it.
// Servers nobody has used for a while are stopped, so memory doesn't pile up
// across the many folders agents work in.
package lsp

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// Server is a language server am knows how to run.
type Server struct {
	// ID names it on the wire and in ~/.agentman/lsp: "gopls".
	ID string
	// Name is what the editor shows while it starts.
	Name string
	// Language is what the editor calls the languages it serves, in words.
	Language string
	// Languages are the LSP language ids it serves.
	Languages []string
	// Markers are files whose folder is a project's root.
	Markers []string
	// Args follow the command.
	Args []string
	// Missing says what to do when the server isn't on the Mac and am
	// can't get it.
	Missing string
	// Install is how am gets it, or nil when only the Mac can provide it.
	Install *Install
	// find looks for a copy already on the Mac.
	find func(t *Tools) string
}

// catalog is every server am runs, by language.
var catalog = []*Server{
	{
		ID: "gopls", Name: "gopls", Language: "Go",
		Languages: []string{"go"},
		Markers:   []string{"go.work", "go.mod"},
		Missing:   "Go support needs gopls: go install golang.org/x/tools/gopls@latest",
		Install:   &Install{Version: "v0.24.0", GoPackage: "golang.org/x/tools/gopls", Size: "about 30 MB"},
		find:      func(t *Tools) string { return t.Look("gopls") },
	},
	{
		ID: "typescript", Name: "TypeScript", Language: "TypeScript",
		Languages: []string{"typescript", "typescriptreact", "javascript", "javascriptreact"},
		Markers:   []string{"tsconfig.json", "jsconfig.json", "package.json"},
		Args:      []string{"--stdio"},
		Missing:   "TypeScript support needs a language server: npm install -g @vtsls/language-server",
		Install:   &Install{Version: "0.3.0", NPM: "@vtsls/language-server", Bin: "vtsls", Size: "about 45 MB"},
		find: func(t *Tools) string {
			if path := t.Look("vtsls"); path != "" {
				return path
			}
			return t.Look("typescript-language-server")
		},
	},
	{
		ID: "pyright", Name: "Pyright", Language: "Python",
		Languages: []string{"python"},
		Markers:   []string{"pyproject.toml", "pyrightconfig.json", "setup.py", "setup.cfg", "requirements.txt"},
		Args:      []string{"--stdio"},
		Missing:   "Python support needs Pyright: npm install -g pyright",
		Install:   &Install{Version: "1.1.414", NPM: "pyright", Bin: "pyright-langserver", Size: "about 20 MB"},
		find:      func(t *Tools) string { return t.Look("pyright-langserver") },
	},
	{
		ID: "rust-analyzer", Name: "rust-analyzer", Language: "Rust",
		Languages: []string{"rust"},
		Markers:   []string{"Cargo.toml"},
		Missing:   "Rust support needs rust-analyzer: rustup component add rust-analyzer",
		Install: &Install{Version: "2026-10-12", Size: "about 40 MB", Release: map[string]Asset{
			"darwin/arm64": {"https://github.com/rust-lang/rust-analyzer/releases/download/2026-10-12/rust-analyzer-aarch64-apple-darwin.gz", "a2e45e828b37bfc4b507c2ca456e8b61096e0bdfbae225f3d33295546d1eeacf"},
			"darwin/amd64": {"https://github.com/rust-lang/rust-analyzer/releases/download/2026-10-12/rust-analyzer-x86_64-apple-darwin.gz", "56a5698a2e0d495c3ec28155416db310c879f21a70f6f38db549166cf157cfab"},
			"linux/arm64":  {"https://github.com/rust-lang/rust-analyzer/releases/download/2026-10-12/rust-analyzer-aarch64-unknown-linux-gnu.gz", "d7219d5d8f5537bb227b521157aebddfe7e4a90f4a85a74f31e53722f5691025"},
			"linux/amd64":  {"https://github.com/rust-lang/rust-analyzer/releases/download/2026-10-12/rust-analyzer-x86_64-unknown-linux-gnu.gz", "040cab4e2abc490fadd95c3f6cbc60b941a78761dbc3acf0ec66fd7a97f31db3"},
		}},
		find: func(t *Tools) string {
			// rustup puts a rust-analyzer on the PATH that only works once
			// the component is installed.
			if path := t.Look("rust-analyzer"); path != "" && t.Runs(path, "--version") {
				return path
			}
			return ""
		},
	},
	{
		ID: "sourcekit-lsp", Name: "SourceKit-LSP", Language: "Swift",
		Languages: []string{"swift"},
		Markers:   []string{"Package.swift"},
		Missing:   "Swift support needs Xcode or its command line tools: xcode-select --install",
		find:      func(t *Tools) string { return t.Xcrun("sourcekit-lsp") },
	},
	{
		ID: "clangd", Name: "clangd", Language: "C",
		Languages: []string{"c", "cpp", "objective-c", "objective-cpp"},
		Markers:   []string{"compile_commands.json", "compile_flags.txt", ".clangd"},
		Missing:   "C support needs clangd: xcode-select --install",
		find: func(t *Tools) string {
			if path := t.Xcrun("clangd"); path != "" {
				return path
			}
			return t.Look("clangd")
		},
	},
}

// ServerFor is the server for an LSP language id, or nil.
func ServerFor(language string) *Server {
	for _, server := range catalog {
		if slices.Contains(server.Languages, language) {
			return server
		}
	}
	return nil
}

// Root is the project root a file belongs to: the nearest folder above it
// with one of the server's markers, up to the home folder, or else the
// folder open in the editor.
func Root(file, folder string, markers []string) string {
	home, _ := os.UserHomeDir()
	for dir := filepath.Dir(file); ; dir = filepath.Dir(dir) {
		if dir == home || dir == "/" || dir == "." {
			break
		}
		for _, marker := range markers {
			if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
				return dir
			}
		}
		if parent := filepath.Dir(dir); parent == dir {
			break
		}
	}
	return folder
}

// Tools finds programs where developers' Macs keep them. am serve starts
// from a login shell or launchd, whose PATH misses most of them: nvm's node,
// Go's bin folder, cargo's.
type Tools struct {
	once  sync.Once
	dirs  []string
	mu    sync.Mutex
	xcrun map[string]string
}

// Dirs are the folders searched, in order: the PATH am serve has, then the
// usual places.
func (t *Tools) Dirs() []string {
	t.once.Do(func() {
		home, _ := os.UserHomeDir()
		var dirs []string
		dirs = append(dirs, filepath.SplitList(os.Getenv("PATH"))...)
		dirs = append(dirs,
			"/opt/homebrew/bin", "/usr/local/bin",
			filepath.Join(home, "go", "bin"),
			filepath.Join(home, ".cargo", "bin"),
			"/opt/homebrew/opt/rustup/bin",
			filepath.Join(home, ".volta", "bin"),
			filepath.Join(home, ".local", "share", "fnm", "aliases", "default", "bin"),
			filepath.Join(home, ".bun", "bin"),
			filepath.Join(home, ".local", "bin"),
		)
		dirs = append(dirs, newestNode(home)...)
		if gopath := goPath(dirs); gopath != "" {
			dirs = append(dirs, filepath.Join(gopath, "bin"))
		}
		t.dirs = unique(dirs)
	})
	return t.dirs
}

// use sets the folders searched, for tests.
func (t *Tools) use(dirs []string) {
	t.once.Do(func() {})
	t.dirs = dirs
}

// Look finds a program by name in Dirs.
func (t *Tools) Look(name string) string {
	for _, dir := range t.Dirs() {
		path := filepath.Join(dir, name)
		if info, err := os.Stat(path); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return path
		}
	}
	return ""
}

// Xcrun finds one of Xcode's tools, or the command line tools'.
func (t *Tools) Xcrun(name string) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if path, ok := t.xcrun[name]; ok {
		return path
	}
	if t.xcrun == nil {
		t.xcrun = map[string]string{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/bin/xcrun", "--find", name).Output()
	path := ""
	if err == nil {
		path = strings.TrimSpace(string(out))
	}
	t.xcrun[name] = path
	return path
}

// Runs says whether a program starts and exits cleanly with these arguments.
func (t *Tools) Runs(path string, args ...string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = t.Env()
	return cmd.Run() == nil
}

// Env is the environment servers run in: am's own, with Dirs as the PATH,
// so gopls finds go and the TypeScript server finds node.
func (t *Tools) Env() []string {
	env := []string{"PATH=" + strings.Join(t.Dirs(), string(os.PathListSeparator))}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "PATH=") {
			env = append(env, kv)
		}
	}
	return env
}

// newestNode is nvm's newest installed node's bin folder, if any.
func newestNode(home string) []string {
	versions, _ := filepath.Glob(filepath.Join(home, ".nvm", "versions", "node", "v*"))
	if len(versions) == 0 {
		return nil
	}
	sort.Slice(versions, func(i, j int) bool { return newerVersion(filepath.Base(versions[i]), filepath.Base(versions[j])) })
	return []string{filepath.Join(versions[0], "bin")}
}

// newerVersion compares "v24.11.1" and "v22.3.0" by their numbers.
func newerVersion(a, b string) bool {
	as := strings.Split(strings.TrimPrefix(a, "v"), ".")
	bs := strings.Split(strings.TrimPrefix(b, "v"), ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		var x, y int
		for _, c := range as[i] {
			x = x*10 + int(c-'0')
		}
		for _, c := range bs[i] {
			y = y*10 + int(c-'0')
		}
		if x != y {
			return x > y
		}
	}
	return len(as) > len(bs)
}

// goPath asks go where GOPATH is, when go is found.
func goPath(dirs []string) string {
	for _, dir := range dirs {
		goBin := filepath.Join(dir, "go")
		if info, err := os.Stat(goBin); err == nil && !info.IsDir() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			out, err := exec.CommandContext(ctx, goBin, "env", "GOPATH").Output()
			cancel()
			if err == nil {
				return strings.TrimSpace(string(out))
			}
			return ""
		}
	}
	return ""
}

func unique(dirs []string) []string {
	seen := map[string]bool{}
	out := dirs[:0]
	for _, dir := range dirs {
		if dir == "" || seen[dir] {
			continue
		}
		seen[dir] = true
		out = append(out, dir)
	}
	return out
}
