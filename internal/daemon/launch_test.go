package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/source"
	"github.com/lenajeremy/agentman/internal/tmux"
)

type cursorLaunchStub struct {
	cwd, prompt string
	calls       int
}

func (*cursorLaunchStub) Kind() protocol.Kind                                  { return protocol.KindCursorCLI }
func (*cursorLaunchStub) Discover(context.Context) ([]protocol.Session, error) { return nil, nil }
func (*cursorLaunchStub) Page(_ context.Context, id, _ string, _ int) (protocol.Page, error) {
	return protocol.NewPage(id, nil, "", false), nil
}
func (*cursorLaunchStub) Follow(ctx context.Context, _ string, _ chan<- []protocol.Message) error {
	<-ctx.Done()
	return ctx.Err()
}
func (s *cursorLaunchStub) Launch(_ context.Context, cwd, prompt string) (string, error) {
	s.cwd, s.prompt = cwd, prompt
	s.calls++
	return "cursor-cli:acp:test-session", nil
}

func TestNewCursorSessionUsesStreamingLauncher(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, "project")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	stub := &cursorLaunchStub{}
	registry := source.NewRegistry()
	registry.Add(stub)
	d := New(registry, nil)
	req := protocol.Request{ClientID: "new-cursor", Kind: protocol.KindCursorCLI, Path: "project", Text: "Hello Cursor"}
	id, err := d.startLocalSession(context.Background(), req)
	if err != nil || id != "cursor-cli:acp:test-session" || stub.cwd != dir || stub.prompt != req.Text {
		t.Fatalf("launch = %s, %v, %+v", id, err, stub)
	}
	if _, err := d.startLocalSession(context.Background(), req); err != nil || stub.calls != 1 {
		t.Fatalf("launch retry was not idempotent: %v, %d", err, stub.calls)
	}
}

func TestLaunchDirectoriesStayInsideHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, "Projects", "app"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(home, ".ssh"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(home, "escape")); err != nil {
		t.Fatal(err)
	}
	names, err := listLaunchDirectories("")
	if err != nil || len(names) != 1 || names[0] != "Projects" {
		t.Fatalf("root listing = %q, %v", names, err)
	}
	for _, path := range []string{"../outside", "/tmp", "Projects/../.ssh", ".ssh", "escape", "Projects//app"} {
		if _, err := launchDirectory(path, false); err == nil {
			t.Errorf("accepted unsafe launch path %q", path)
		}
	}
	dir, err := launchDirectory("Projects/app", false)
	if err != nil || dir != filepath.Join(home, "Projects", "app") {
		t.Fatalf("launch directory = %q, %v", dir, err)
	}
}

func TestLaunchRequestRejectsUntrustedInputs(t *testing.T) {
	base := protocol.Request{Type: protocol.ReqStartSession, ClientID: "launch-1",
		Kind: protocol.KindClaude, Path: "Projects/app", Text: "Build it"}
	if err := validateRequest(base); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*protocol.Request){
		func(req *protocol.Request) { req.Kind = protocol.KindCursor },
		func(req *protocol.Request) { req.Path = "../private" },
		func(req *protocol.Request) { req.Text = "" },
		func(req *protocol.Request) { req.Text = "hello\x1b[2J" },
		func(req *protocol.Request) { req.ClientID = "" },
	} {
		req := base
		mutate(&req)
		if err := validateRequest(req); err == nil {
			t.Errorf("accepted invalid launch: %+v", req)
		}
	}
}

func TestStartTerminalSessionLaunchesInChosenDirectory(t *testing.T) {
	if !tmux.Available() {
		t.Skip("tmux not installed")
	}
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	dir := filepath.Join(root, "project")
	if err := os.MkdirAll(bin, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(root, "launched.json")
	script := "#!/bin/sh\nprintf '%s\\n' \"$PWD\" \"$1\" \"$2\" > \"" + output + "\"\nsleep 30\n"
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	id, err := startTerminalSession(context.Background(), protocol.KindCodex, dir, "Review this file")
	if err != nil {
		t.Fatal(err)
	}
	name := strings.TrimPrefix(id, "codex:tmux-")
	t.Cleanup(func() { _ = tmux.Kill(context.Background(), name) })
	var raw []byte
	for range 40 {
		raw, err = os.ReadFile(output)
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 3 || lines[0] != dir || lines[1] != "--" || lines[2] != "Review this file" {
		t.Fatalf("launched with %q", lines)
	}
}

func TestStartOpenCodeCreatesAndPrompts(t *testing.T) {
	dir := t.TempDir()
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path+"?"+r.URL.RawQuery)
		if r.URL.Query().Get("directory") != dir {
			t.Errorf("wrong directory %q", r.URL.Query().Get("directory"))
		}
		if r.URL.Path == "/session" {
			_, _ = w.Write([]byte(`{"id":"new-chat"}`))
			return
		}
		if r.URL.Path == "/session/new-chat/prompt_async" {
			var body struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if len(body.Parts) != 1 || body.Parts[0].Text != "Build it" {
				t.Errorf("body = %+v", body)
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	t.Setenv("AGENTMAN_OPENCODE_URL", server.URL)
	id, err := startOpenCodeSession(context.Background(), dir, "Build it")
	if err != nil || id != "opencode:new-chat" {
		t.Fatalf("start = %q, %v", id, err)
	}
	if len(paths) != 2 || !strings.HasPrefix(paths[0], "/session?") ||
		!strings.HasPrefix(paths[1], "/session/new-chat/prompt_async?") {
		t.Fatalf("paths = %q", paths)
	}
}

func TestManagedOpenCodePortOnlyAcceptsWatchedServerNames(t *testing.T) {
	if got := managedOpenCodePort("agentman-opencode-server-4096-123-abc"); got != 4096 {
		t.Fatalf("port = %d", got)
	}
	for _, name := range []string{
		"agentman-opencode-4096-abc", "agentman-opencode-server-4096abc-x",
		"agentman-opencode-server-9999-x", "agentman-opencode-server-4096",
	} {
		if got := managedOpenCodePort(name); got != 0 {
			t.Errorf("accepted %q as port %d", name, got)
		}
	}
}
