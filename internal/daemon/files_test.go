package daemon

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/attachments"
	"github.com/lenajeremy/agentman/internal/protocol"
)

func fileParts(id, name string, data []byte, size int) []protocol.FilePart {
	encoded := base64.StdEncoding.EncodeToString(data)
	var parts []protocol.FilePart
	for at := 0; at < len(encoded); at += size {
		parts = append(parts, protocol.FilePart{ID: id, Name: name, Data: encoded[at:min(at+size, len(encoded))]})
	}
	for i := range parts {
		parts[i].Part, parts[i].Parts = i, len(parts)
	}
	return parts
}

// A file sent from the phone arrives in parts and is kept under its own
// name, for the message to name it as @path.
func TestAFileSentInPartsIsKeptUnderItsName(t *testing.T) {
	d := workspaceDaemon(t.TempDir())
	d.SetAttachments(attachments.New(t.TempDir(), "", "token"))
	content := []byte(strings.Repeat("quarterly numbers\n", 400))
	parts := fileParts("f1", "Q3 report (final).pdf", content, 1000)
	if len(parts) < 3 {
		t.Fatalf("want several parts, got %d", len(parts))
	}
	var last protocol.Event
	for i := range parts {
		last = d.Handle(context.Background(), protocol.Request{Type: protocol.ReqAttachFile, SessionID: "codex:test", File: &parts[i]})
		if last.Type != protocol.EvtAttached {
			t.Fatalf("part %d: %+v", i, last)
		}
		if done := i == len(parts)-1; last.Attached.Done != done {
			t.Fatalf("part %d done = %v", i, last.Attached.Done)
		}
	}
	path := last.Attached.Path
	if filepath.Base(path) != "Q3-report-final.pdf" {
		t.Errorf("name = %q", filepath.Base(path))
	}
	if strings.ContainsAny(path, " ()") {
		t.Errorf("a path the agent can't read as one word: %q", path)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(content) {
		t.Fatalf("kept %d bytes, %v", len(got), err)
	}
}

func TestFilePartsThatCannotBeKeptAreRefused(t *testing.T) {
	d := workspaceDaemon(t.TempDir())
	d.SetAttachments(attachments.New(t.TempDir(), "", "token"))
	send := func(part protocol.FilePart) protocol.Event {
		return d.Handle(context.Background(), protocol.Request{Type: protocol.ReqAttachFile, SessionID: "codex:test", File: &part})
	}
	for name, part := range map[string]protocol.FilePart{
		"a path for a name": {ID: "f", Name: "../../etc/passwd", Part: 0, Parts: 1, Data: "eA=="},
		"no name":           {ID: "f", Part: 0, Parts: 1, Data: "eA=="},
		"bad id":            {ID: "f/1", Name: "a.txt", Part: 0, Parts: 1, Data: "eA=="},
		"part out of range": {ID: "f", Name: "a.txt", Part: 2, Parts: 2, Data: "eA=="},
		"too many parts":    {ID: "f", Name: "a.txt", Part: 0, Parts: 100_000, Data: "eA=="},
		"part too large":    {ID: "f", Name: "a.txt", Part: 0, Parts: 1, Data: strings.Repeat("A", 200<<10)},
	} {
		if event := send(part); event.Type != protocol.EvtError {
			t.Errorf("%s was accepted: %+v", name, event)
		}
	}
	// Parts that disagree about the file are dropped together.
	send(protocol.FilePart{ID: "g", Name: "a.txt", Part: 0, Parts: 2, Data: "eA=="})
	if event := send(protocol.FilePart{ID: "g", Name: "b.txt", Part: 1, Parts: 2, Data: "eA=="}); event.Type != protocol.EvtError {
		t.Errorf("a renamed part was taken: %+v", event)
	}
}
