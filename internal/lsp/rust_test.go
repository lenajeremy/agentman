package lsp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// rustupAt is a fake rustup in a folder, with cargo as rustup's proxies
// are: rustup under another name. `rustup component add rust-analyzer`
// puts rust-analyzer beside it, or fails when fail is set.
func rustupAt(t *testing.T, fail bool) (*Tools, string) {
	add := `[ "$1 $2 $3" = "component add rust-analyzer" ] || exit 2
printf '#!/bin/sh\necho rust-analyzer 1.93.0\n' > "$(dirname "$0")/rust-analyzer"; chmod +x "$(dirname "$0")/rust-analyzer"
`
	if fail {
		add = "exit 1\n"
	}
	tools, dir := fakeTools(t, map[string]string{"rustup": `echo "$@" >> "$(dirname "$0")/rustup.args"` + "\n" + add})
	if err := os.Symlink("rustup", filepath.Join(dir, "cargo")); err != nil {
		t.Fatal(err)
	}
	return tools, dir
}

func rustServer(release map[string]Asset) *Server {
	server := *ServerByID("rust-analyzer")
	install := *server.Install
	install.Release = release
	server.Install = &install
	return &server
}

// A rust-analyzer built for a newer toolchain than the Mac's says it doesn't
// support it, and works only in part. The toolchain's own goes with it.
func TestRustAnalyzerComesFromTheMacsRustToolchain(t *testing.T) {
	tools, dir := rustupAt(t, false)
	in := &Installer{Dir: t.TempDir(), Tools: tools}
	var said []string
	launch, err := in.Install(context.Background(), rustServer(nil), func(text string) { said = append(said, text) })
	if err != nil {
		t.Fatal(err)
	}
	if launch.Path != filepath.Join(dir, "rust-analyzer") {
		t.Errorf("launch = %+v", launch)
	}
	if !slices.Contains(said, "Adding rust-analyzer to your Rust toolchain…") {
		t.Errorf("said %q", said)
	}
	if args, _ := os.ReadFile(filepath.Join(dir, "rustup.args")); strings.TrimSpace(string(args)) != "component add rust-analyzer" {
		t.Errorf("rustup was run with %q", args)
	}
}

// Without rustup, or when rustup can't add it, the download.
func TestRustAnalyzerIsDownloadedWhenRustupCantGiveIt(t *testing.T) {
	program := []byte("#!/bin/sh\necho downloaded\n")
	packed := gz(program)
	url, hits := release(t, packed, false)
	tools, _ := rustupAt(t, true)
	in := &Installer{Dir: t.TempDir(), Tools: tools}
	launch, err := in.Install(context.Background(), rustServer(map[string]Asset{platform(): {URL: url, SHA256: sum(packed)}}), func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 1 || !strings.HasPrefix(launch.Path, in.Dir) {
		t.Errorf("launch = %+v, %d downloads", launch, hits.Load())
	}
}

// One downloaded before rustup could give it waits for rustup's.
func TestADownloadedRustAnalyzerGivesWayToTheToolchainsOwn(t *testing.T) {
	dir := t.TempDir()
	installed := filepath.Join(dir, "rust-analyzer", "2026-10-12")
	os.MkdirAll(installed, 0o755)
	os.WriteFile(filepath.Join(installed, "rust-analyzer"), []byte("#!/bin/sh\n"), 0o755)
	os.WriteFile(filepath.Join(installed, okMarker), []byte("2026-10-12\n"), 0o644)
	_, bin := rustupAt(t, false)
	server := rustServer(nil)
	for _, noDownloads := range []bool{false, true} {
		h := newHarness(t, Options{Servers: []*Server{server}, Find: func(*Server) string { return "" }, Dir: dir, NoDownloads: noDownloads})
		h.host.tools.use([]string{bin})
		h.host.installer.Tools = &h.host.tools
		launch, ok := h.host.locate(server)
		if noDownloads {
			// Then am adds nothing, and what is here runs.
			if !ok || !strings.HasPrefix(launch.Path, installed) {
				t.Errorf("without downloads: %+v %v", launch, ok)
			}
		} else if ok {
			t.Errorf("the download ran: %+v", launch)
		}
	}
}

// Homebrew's rust puts its own cargo in /opt/homebrew/bin, and links rustup
// there too: rust-analyzer gets the folder where cargo is rustup's.
func TestRustupsCargoComesFirst(t *testing.T) {
	brew := t.TempDir()
	os.WriteFile(filepath.Join(brew, "cargo"), []byte("#!/bin/sh\necho cargo 1.91.1 (Homebrew)\n"), 0o755)
	os.WriteFile(filepath.Join(brew, "rustup"), []byte("#!/bin/sh\n"), 0o755)
	_, proxies := rustupAt(t, false)
	tools := &Tools{}
	tools.use([]string{"/usr/bin", brew, proxies})
	if dirs := rustupDirs(tools); !slices.Equal(dirs, []string{proxies}) {
		t.Errorf("rustupDirs = %q, want %q", dirs, proxies)
	}
	tools.use([]string{"/usr/bin", brew})
	if dirs := rustupDirs(tools); dirs != nil {
		t.Errorf("without rustup's proxies: %q", dirs)
	}
}

// A Cargo workspace's crates share one rust-analyzer, which loads them all,
// rather than one each loading the whole workspace again.
func TestACargoWorkspaceHasOneServer(t *testing.T) {
	ws, _ := filepath.EvalSymlinks(t.TempDir())
	files := map[string]string{
		"Cargo.toml":              "[workspace]\nmembers = [\"app\", \"util\"]\n",
		"app/Cargo.toml":          "[package]\nname = \"app\"\n",
		"util/Cargo.toml":         "[package]\nname = \"util\"\n",
		"tools/lone/Cargo.toml":   "[package]\nname = \"lone\"\n",
		"shared/Cargo.toml":       "[workspace.dependencies]\nserde = \"1\"\n",
		"shared/inner/Cargo.toml": "[package]\nname = \"inner\"\n",
	}
	for name, text := range files {
		os.MkdirAll(filepath.Dir(filepath.Join(ws, name)), 0o755)
		os.WriteFile(filepath.Join(ws, name), []byte(text), 0o644)
	}
	server := ServerByID("rust-analyzer")
	for file, want := range map[string]string{
		"app/src/main.rs":         ws,
		"util/src/lib.rs":         ws,
		"src/lib.rs":              ws,
		"shared/inner/src/lib.rs": filepath.Join(ws, "shared"),
	} {
		if got := RootFor(server, filepath.Join(ws, file), ws); got != want {
			t.Errorf("%s: root %s, want %s", file, got, want)
		}
	}
	// Outside any workspace, the crate's own folder, as before.
	os.Remove(filepath.Join(ws, "Cargo.toml"))
	if got := RootFor(server, filepath.Join(ws, "tools/lone/src/main.rs"), ws); got != filepath.Join(ws, "tools/lone") {
		t.Errorf("a lone crate's root is %s", got)
	}
}

// The status bar says what the server is doing, a report at a time, and
// what it warned of when it is doing nothing.
func TestTheStatusSaysWhatTheServerIsDoingAndWarnedOf(t *testing.T) {
	h := newHarness(t, Options{})
	s := newSession(h.host, fakeServerSpec, t.TempDir())
	s.state = StateReady
	tell := func(method, params string) string {
		s.fromServer(nil, message{JSONRPC: "2.0", Method: method, Params: json.RawMessage(params)})
		return s.Status().Detail
	}
	progress := func(value string) string { return tell("$/progress", `{"token":"t","value":`+value+`}`) }

	for _, step := range []struct{ got, want string }{
		{progress(`{"kind":"begin","title":"Indexing","percentage":0}`), "Indexing 0%"},
		{progress(`{"kind":"report","percentage":50}`), "Indexing 50%"},
		{progress(`{"kind":"report","message":"core","percentage":66}`), "Indexing (core) 66%"},
		{progress(`{"kind":"report","percentage":90}`), "Indexing (core) 90%"},
		{progress(`{"kind":"end"}`), ""},
		{tell("window/showMessage", `{"type":2,"message":"Workspace uses an outdated toolchain\nConsider the rustup component"}`), "Workspace uses an outdated toolchain"},
		{progress(`{"kind":"begin","title":"cargo check"}`), "cargo check"},
		{progress(`{"kind":"end"}`), "Workspace uses an outdated toolchain"},
		{tell("window/showMessage", `{"type":3,"message":"Just so you know"}`), "Workspace uses an outdated toolchain"},
		{tell("experimental/serverStatus", `{"health":"ok","quiescent":true}`), ""},
		{tell("experimental/serverStatus", `{"health":"error","quiescent":true,"message":"Failed to load workspaces"}`), "Failed to load workspaces"},
	} {
		if step.got != step.want {
			t.Errorf("status %q, want %q", step.got, step.want)
		}
	}
}
