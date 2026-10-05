package source

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// A 1×1 PNG, as the daemon's attachment store would have saved it.
var onePixelPNG, _ = base64.StdEncoding.DecodeString(
	"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGP4z8AAAAMBAQDJ/pLvAAAAAElFTkSuQmCC")

func writeImage(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "q7xk2m4pzt9w.png")
	if err := os.WriteFile(path, onePixelPNG, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The image goes to Cursor as an ACP image block, which its server attaches
// to the prompt; the transcript records the message exactly as typed, which
// is what the phone matches its own copy against.
func TestCursorACPPromptCarriesImagesAsBlocks(t *testing.T) {
	path := writeImage(t)
	blocks, shown := cursorACPPrompt(cursorACPTurn{Text: "What colour is this?", Images: []string{path}})
	if len(blocks) != 2 || blocks[0]["type"] != "image" || blocks[0]["mimeType"] != "image/png" ||
		blocks[0]["data"] != base64.StdEncoding.EncodeToString(onePixelPNG) ||
		blocks[1]["type"] != "text" || blocks[1]["text"] != "What colour is this?" {
		t.Fatalf("blocks = %+v", blocks)
	}
	if shown != "What colour is this?" {
		t.Fatalf("shown = %q", shown)
	}
	blocks, shown = cursorACPPrompt(cursorACPTurn{Images: []string{path}})
	if len(blocks) != 1 || shown != "[image]" {
		t.Fatalf("image-only turn = %+v %q", blocks, shown)
	}
	blocks, shown = cursorACPPrompt(cursorACPTurn{Text: "see attached", Images: []string{filepath.Join(t.TempDir(), "gone.png")}})
	if len(blocks) != 1 || !strings.Contains(shown, "no longer on the Mac") {
		t.Fatalf("missing image = %+v %q", blocks, shown)
	}
}

// While a turn runs the message waits in the record's queue, images and
// all, and a record written before images could be queued still loads.
func TestCursorACPQueuesAMessageWithItsImages(t *testing.T) {
	s, st, id, _ := cursorACPFake(t)
	path := writeImage(t)
	mode, err := s.InjectWithAttachments(context.Background(), id, "look", []string{path})
	if err != nil || mode != protocol.InjectAPI {
		t.Fatalf("inject = %s, %v", mode, err)
	}
	st.mu.Lock()
	queued := append([]cursorACPTurn(nil), st.record.Queued...)
	st.mu.Unlock()
	if len(queued) != 1 || queued[0].Text != "look" || len(queued[0].Images) != 1 || queued[0].Images[0] != path {
		t.Fatalf("queued = %+v", queued)
	}

	var record cursorACPRecord
	if err := json.Unmarshal([]byte(`{"nativeId":"x","queued":["older message"]}`), &record); err != nil ||
		len(record.Queued) != 1 || record.Queued[0].Text != "older message" {
		t.Fatalf("old queue = %+v, %v", record.Queued, err)
	}

	if _, err := s.InjectWithAttachments(context.Background(), id, "not a picture", []string{"/etc/hosts"}); err == nil {
		t.Fatal("a file that is not an image was accepted")
	}
}

// A terminal chat takes images as typed paths, which Cursor's TUI attaches
// on its own; the group delivers nothing and says so.
func TestCursorCLIGroupLeavesTerminalImagesToTheDaemon(t *testing.T) {
	group, _, _ := cursorGroupFixture(t)
	_, err := group.InjectWithAttachments(context.Background(), cursorCLIChatPrefix+"chat-1", "look", []string{writeImage(t)})
	if !errors.Is(err, ErrAttachmentsAsPaths) {
		t.Fatalf("err = %v, want ErrAttachmentsAsPaths", err)
	}
}
