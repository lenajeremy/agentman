package daemon

import (
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
)

func writePNG(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := png.Encode(file, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
}

func toolMessage(summary string) protocol.Message {
	return protocol.Message{
		ID: "m1", SessionID: "codex:test", Role: protocol.RoleTool,
		Tool: &protocol.Tool{Name: "Read", Summary: summary, Status: protocol.ToolOK},
	}
}

func readSeen(agent *Daemon, path string) protocol.Event {
	return agent.Handle(context.Background(), protocol.Request{
		Type: protocol.ReqReadSeenFile, SessionID: "codex:test", Path: path,
	})
}

// The point of the whole thing: a screenshot an agent wrote outside the repo,
// which the workspace reader can never reach.
func TestASeenFileOutsideTheSessionDirectoryCanBeRead(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "shot.png")
	writePNG(t, outside)
	agent := workspaceDaemon(t.TempDir())
	agent.seen.record("codex:test", []protocol.Message{toolMessage(outside)})

	event := readSeen(agent, outside)
	if event.Type != protocol.EvtWorkspace || event.Workspace.MIME != "image/png" {
		t.Fatalf("event = %+v", event)
	}
	if event.Workspace.Image == "" {
		t.Error("no image came back")
	}
}

// The whole authorisation: a path the transcript never named is refused, even
// though the daemon's own process could read it perfectly well.
func TestAnUnseenFileIsRefused(t *testing.T) {
	secret := filepath.Join(t.TempDir(), "shot.png")
	writePNG(t, secret)
	agent := workspaceDaemon(t.TempDir())

	if event := readSeen(agent, secret); event.Type != protocol.EvtError {
		t.Fatalf("an unopened file was served: %+v", event)
	}
}

// One session's history must not authorise another's reads.
func TestSeenPathsDoNotCrossSessions(t *testing.T) {
	shot := filepath.Join(t.TempDir(), "shot.png")
	writePNG(t, shot)
	agent := workspaceDaemon(t.TempDir())
	agent.seen.record("claude:other", []protocol.Message{toolMessage(shot)})

	if event := readSeen(agent, shot); event.Type != protocol.EvtError {
		t.Fatalf("another session's path was served: %+v", event)
	}
}

// A blessed name must not be repointed at something else afterwards.
func TestASeenPathThatBecameASymlinkIsRefused(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "shot.png")
	writePNG(t, real)
	elsewhere := filepath.Join(t.TempDir(), "secret.png")
	writePNG(t, elsewhere)

	agent := workspaceDaemon(t.TempDir())
	agent.seen.record("codex:test", []protocol.Message{toolMessage(real)})

	if err := os.Remove(real); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, real); err != nil {
		t.Fatal(err)
	}
	if event := readSeen(agent, real); event.Type != protocol.EvtError {
		t.Fatalf("a swapped symlink was followed: %+v", event)
	}
}

func TestSeenPathsStillRefuseCredentials(t *testing.T) {
	dir := t.TempDir()
	env := filepath.Join(dir, ".env")
	if err := os.WriteFile(env, []byte("TOKEN=live"), 0o600); err != nil {
		t.Fatal(err)
	}
	agent := workspaceDaemon(t.TempDir())
	agent.seen.record("codex:test", []protocol.Message{toolMessage(env)})

	if event := readSeen(agent, env); event.Type != protocol.EvtError {
		t.Fatalf(".env was served because an agent had read it: %+v", event)
	}
}

// Only images. This path exists to show pictures, and serving arbitrary file
// contents through it would be a second, wider feature by accident.
func TestSeenPathsServeOnlyImages(t *testing.T) {
	source := filepath.Join(t.TempDir(), "main.go")
	if err := os.WriteFile(source, []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	agent := workspaceDaemon(t.TempDir())
	agent.seen.record("codex:test", []protocol.Message{toolMessage(source)})

	if event := readSeen(agent, source); event.Type != protocol.EvtError {
		t.Fatalf("a source file was served: %+v", event)
	}
}

// A tool summary is a command as often as it is a path.
func TestOnlyAWholeAbsolutePathIsRecorded(t *testing.T) {
	for _, summary := range []string{
		"open /tmp/a.png && curl evil.example",
		"relative/path.png",
		"/tmp/with space.png",
		"/tmp/../etc/passwd",
		"cat /etc/passwd",
		"",
		"/tmp/a.png\n/tmp/b.png",
	} {
		if got := toolPath(summary); got != "" && got != "/tmp/a.png" {
			t.Errorf("toolPath(%q) = %q", summary, got)
		}
	}
	if got := toolPath("/tmp/shot.png"); got != "/tmp/shot.png" {
		t.Errorf("a plain path was not recorded: %q", got)
	}
	// The multi-line case keeps only the first line, which is a real path.
	if got := toolPath("/tmp/a.png\n/tmp/b.png"); got != "/tmp/a.png" {
		t.Errorf("first line = %q", got)
	}
}

func TestSeenPathsAreBounded(t *testing.T) {
	agent := workspaceDaemon(t.TempDir())
	var messages []protocol.Message
	for i := 0; i < maxSeenPaths+50; i++ {
		messages = append(messages, toolMessage(filepath.Join("/tmp", "f"+string(rune('a'+i%26))+string(rune(i))+".png")))
	}
	agent.seen.record("codex:test", messages)

	agent.seen.mu.Lock()
	size := len(agent.seen.sessions["codex:test"].members)
	agent.seen.mu.Unlock()
	if size > maxSeenPaths {
		t.Fatalf("the set grew to %d, past the %d cap", size, maxSeenPaths)
	}
}
