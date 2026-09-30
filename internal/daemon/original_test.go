package daemon

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// download fetches a file the way the app does: one piece at a time, each
// asked for from where the last ended, until the pieces add up to the size the
// daemon reported.
func download(t *testing.T, agent *Daemon, kind protocol.RequestType, path string) (data []byte, pieces int) {
	t.Helper()
	version := ""
	for {
		event := agent.Handle(context.Background(), protocol.Request{
			Type: kind, SessionID: "codex:test", Path: path, Offset: int64(len(data)),
		})
		if event.Type != protocol.EvtWorkspace || event.Workspace.Kind != "chunk" {
			t.Fatalf("piece at %d: %+v", len(data), event)
		}
		chunk := event.Workspace
		if chunk.Offset != int64(len(data)) {
			t.Fatalf("asked for offset %d, got %d", len(data), chunk.Offset)
		}
		if version == "" {
			version = chunk.Version
		}
		if chunk.Version == "" || chunk.Version != version {
			t.Fatalf("version went from %q to %q on a file nothing touched", version, chunk.Version)
		}
		// What the phone and the relay will each accept in one frame.
		if encoded, _ := json.Marshal(event); len(encoded) > maxEventBytes {
			t.Fatalf("a piece is %d bytes on the wire, over the %d limit", len(encoded), maxEventBytes)
		}
		piece, err := base64.StdEncoding.DecodeString(chunk.Data)
		if err != nil || len(piece) == 0 {
			t.Fatalf("piece at %d did not decode: %v", len(data), err)
		}
		data = append(data, piece...)
		pieces++
		if int64(len(data)) >= chunk.Size {
			return data, pieces
		}
	}
}

// The case this exists for: the preview of a Retina screenshot is a smaller,
// re-encoded picture, and the file itself has to be reachable behind it.
func TestAReducedPreviewSaysSoAndTheFileDownloadsByteForByte(t *testing.T) {
	original := retinaScreenshot(t)
	if len(original) <= 2*originalChunkBytes {
		t.Fatalf("fixture is %d bytes; it must span several pieces to test anything", len(original))
	}

	t.Run("inside the session directory", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "shot.png"), original, 0o600); err != nil {
			t.Fatal(err)
		}
		agent := workspaceDaemon(dir)

		preview := agent.Handle(context.Background(), protocol.Request{
			Type: protocol.ReqReadFile, SessionID: "codex:test", Path: "shot.png",
		})
		if preview.Type != protocol.EvtWorkspace || preview.Workspace.Source == nil {
			t.Fatalf("preview carries no description of its file: %+v", preview)
		}
		source := preview.Workspace.Source
		if !source.Reduced {
			t.Error("a resized preview did not say it was reduced")
		}
		if source.Size != int64(len(original)) || source.MIME != "image/png" ||
			source.Width != 3024 || source.Height != 1964 {
			t.Errorf("source = %+v, want %d bytes of image/png at 3024x1964", *source, len(original))
		}
		shown, _ := base64.StdEncoding.DecodeString(preview.Workspace.Image)
		if bytes.Equal(shown, original) {
			t.Fatal("the preview is the original; this test needs one that was reduced")
		}

		got, pieces := download(t, agent, protocol.ReqReadFileChunk, "shot.png")
		if !bytes.Equal(got, original) {
			t.Fatalf("downloaded %d bytes that are not the %d-byte file", len(got), len(original))
		}
		if want := (len(original) + originalChunkBytes - 1) / originalChunkBytes; pieces != want {
			t.Errorf("took %d pieces, want %d", pieces, want)
		}
	})

	t.Run("a file the agent opened elsewhere", func(t *testing.T) {
		outside := filepath.Join(t.TempDir(), "shot.png")
		if err := os.WriteFile(outside, original, 0o600); err != nil {
			t.Fatal(err)
		}
		agent := workspaceDaemon(t.TempDir())
		agent.seen.record("codex:test", []protocol.Message{toolMessage(outside)})

		preview := readSeen(agent, outside)
		if preview.Type != protocol.EvtWorkspace || preview.Workspace.Source == nil ||
			!preview.Workspace.Source.Reduced || preview.Workspace.Source.Size != int64(len(original)) {
			t.Fatalf("preview = %+v", preview.Workspace)
		}
		got, _ := download(t, agent, protocol.ReqReadSeenFileChunk, outside)
		if !bytes.Equal(got, original) {
			t.Fatalf("downloaded %d bytes that are not the %d-byte file", len(got), len(original))
		}
	})
}

// When the preview already is the file there is nothing better to fetch, and
// the app needs to know that too, or it would download what it is holding.
func TestAnImageThatFitsIsNotReportedAsReduced(t *testing.T) {
	dir := t.TempDir()
	writePNG(t, filepath.Join(dir, "icon.png"))
	onDisk, err := os.ReadFile(filepath.Join(dir, "icon.png"))
	if err != nil {
		t.Fatal(err)
	}
	agent := workspaceDaemon(dir)

	event := agent.Handle(context.Background(), protocol.Request{
		Type: protocol.ReqReadFile, SessionID: "codex:test", Path: "icon.png",
	})
	if event.Type != protocol.EvtWorkspace || event.Workspace.Source == nil {
		t.Fatalf("event = %+v", event)
	}
	source := event.Workspace.Source
	if source.Reduced || source.Size != int64(len(onDisk)) || source.Width != 2 || source.Height != 2 {
		t.Errorf("source = %+v, want the %d-byte 2x2 file, not reduced", *source, len(onDisk))
	}
}

// A download is a second way to read a file, so it must refuse everything the
// preview refuses. Each of these is a path a preview already turns away.
func TestADownloadReachesNothingAPreviewCannot(t *testing.T) {
	dir := t.TempDir()
	writePNG(t, filepath.Join(dir, "ok.png"))
	writePNG(t, filepath.Join(dir, ".env.png"))
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("plain text"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.png")
	writePNG(t, outside)
	if err := os.Symlink(outside, filepath.Join(dir, "link.png")); err != nil {
		t.Fatal(err)
	}
	agent := workspaceDaemon(dir)

	chunk := func(kind protocol.RequestType, path string, offset int64) protocol.Event {
		request := protocol.Request{Type: kind, SessionID: "codex:test", Path: path, Offset: offset}
		if err := validateRequest(request); err != nil {
			return protocol.Event{Type: protocol.EvtError, Error: err.Error()}
		}
		return agent.Handle(context.Background(), request)
	}

	if event := chunk(protocol.ReqReadFileChunk, "ok.png", 0); event.Type != protocol.EvtWorkspace {
		t.Fatalf("an ordinary image was refused, so nothing below proves anything: %+v", event)
	}
	refused := []struct {
		name   string
		kind   protocol.RequestType
		path   string
		offset int64
	}{
		{"a file that is not an image", protocol.ReqReadFileChunk, "notes.txt", 0},
		{"a private name", protocol.ReqReadFileChunk, ".env.png", 0},
		{"a path out of the directory", protocol.ReqReadFileChunk, "../outside.png", 0},
		{"an absolute path as a workspace path", protocol.ReqReadFileChunk, outside, 0},
		{"a symlink out of the directory", protocol.ReqReadFileChunk, "link.png", 0},
		{"an offset past the end", protocol.ReqReadFileChunk, "ok.png", 1 << 20},
		{"a negative offset", protocol.ReqReadFileChunk, "ok.png", -1},
		{"a file the agent never opened", protocol.ReqReadSeenFileChunk, outside, 0},
		{"a relative path as a seen path", protocol.ReqReadSeenFileChunk, "ok.png", 0},
	}
	for _, c := range refused {
		if event := chunk(c.kind, c.path, c.offset); event.Type != protocol.EvtError {
			t.Errorf("%s was served: %+v", c.name, event.Workspace)
		}
	}

	// And a seen path stops being served when it stops being what was seen.
	agent.seen.record("codex:test", []protocol.Message{toolMessage(outside)})
	if event := chunk(protocol.ReqReadSeenFileChunk, outside, 0); event.Type != protocol.EvtWorkspace {
		t.Fatalf("a file the agent opened was refused: %+v", event)
	}
	if err := os.WriteFile(outside, []byte(strings.Repeat("not an image ", 100)), 0o600); err != nil {
		t.Fatal(err)
	}
	if event := chunk(protocol.ReqReadSeenFileChunk, outside, 0); event.Type != protocol.EvtError {
		t.Error("a path that stopped being an image was still served")
	}
}

// An agent can rewrite a screenshot between two pieces of a download. The
// version is how the app notices, instead of saving half of each picture.
func TestTheVersionChangesWhenTheFileIsRewritten(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shot.png")
	writePNG(t, path)
	agent := workspaceDaemon(dir)
	version := func() string {
		event := agent.Handle(context.Background(), protocol.Request{
			Type: protocol.ReqReadFileChunk, SessionID: "codex:test", Path: "shot.png",
		})
		if event.Type != protocol.EvtWorkspace {
			t.Fatalf("event = %+v", event)
		}
		return event.Workspace.Version
	}

	before := version()
	if again := version(); again != before {
		t.Fatalf("version moved from %q to %q with no write", before, again)
	}
	// Same length, later time: the rewrite a size check alone would miss.
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
	if after := version(); after == before {
		t.Error("a rewritten file kept its version")
	}
}
