package source

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Images from the phone reach a Cursor chat in the form its channel takes.
//
// A terminal chat needs nothing: Cursor's TUI attaches any image whose
// absolute path appears in a prompt (checked against the installed CLI), so
// the daemon's "paths, then the message" is already the right thing to type.
// The group says so with ErrAttachmentsAsPaths, having delivered nothing.
//
// An ACP chat got the same path as text, which Cursor's ACP server does not
// look at: the model saw a file name and had to decide to read it. ACP
// advertises promptCapabilities.image, and an image content block is what
// becomes an attached image there, so that is what is sent.

// cursorACPMaxImage bounds one image read into a prompt. The daemon's store
// holds nothing larger; this only stops a stray path from becoming a huge
// JSON line.
const cursorACPMaxImage = 8 << 20

var cursorACPImageTypes = map[string]bool{
	"image/png": true, "image/jpeg": true, "image/gif": true, "image/webp": true,
}

// InjectWithAttachments implements AttachmentInjector.
func (s *CursorCLIGroup) InjectWithAttachments(ctx context.Context, id, text string, paths []string) (protocol.InjectMode, error) {
	if !s.owned(id) {
		return protocol.InjectNone, ErrAttachmentsAsPaths
	}
	return s.acp.InjectWithAttachments(ctx, id, text, paths)
}

// InjectWithAttachments sends a message with images as ACP image blocks,
// queueing it like any other message while a turn runs.
func (s *CursorACPSource) InjectWithAttachments(ctx context.Context, id, text string, paths []string) (protocol.InjectMode, error) {
	st, err := s.get(id)
	if err != nil {
		return protocol.InjectNone, err
	}
	for _, path := range paths {
		if _, _, err := readCursorACPImage(path); err != nil {
			return protocol.InjectNone, err
		}
	}
	st.startMu.Lock()
	defer st.startMu.Unlock()
	return s.injectLocked(ctx, st, cursorACPTurn{Text: text, Images: append([]string(nil), paths...)})
}

// readCursorACPImage reads one saved image, refusing anything that is not a
// regular file holding a picture.
func readCursorACPImage(path string) ([]byte, string, error) {
	if !filepath.IsAbs(path) {
		return nil, "", errors.New("source: an image path must be absolute")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, "", fmt.Errorf("source: image is unavailable: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > cursorACPMaxImage {
		return nil, "", errors.New("source: that image cannot be sent")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, "", fmt.Errorf("source: image is unavailable: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, cursorACPMaxImage+1))
	if err != nil || len(data) > cursorACPMaxImage {
		return nil, "", errors.New("source: that image cannot be sent")
	}
	mime := http.DetectContentType(data)
	if !cursorACPImageTypes[mime] {
		return nil, "", errors.New("source: only PNG, JPEG, GIF and WebP images can be sent")
	}
	return data, mime, nil
}

// cursorACPPrompt builds a turn's prompt blocks, images first as a person
// would paste them, and the text the transcript shows for it. An image that
// has gone since the message was queued is left out rather than holding the
// queue up for ever.
func cursorACPPrompt(turn cursorACPTurn) ([]map[string]string, string) {
	var blocks []map[string]string
	missing, images := 0, 0
	for _, path := range turn.Images {
		data, mime, err := readCursorACPImage(path)
		if err != nil {
			missing++
			continue
		}
		blocks = append(blocks, map[string]string{
			"type": "image", "mimeType": mime, "data": base64.StdEncoding.EncodeToString(data),
		})
		images++
	}
	text := strings.TrimSpace(turn.Text)
	if text != "" || len(blocks) == 0 {
		blocks = append(blocks, map[string]string{"type": "text", "text": turn.Text})
	}
	// The phone keeps its own copy of what was sent until the transcript
	// shows the same text, so the text is recorded exactly as sent.
	shown := turn.Text
	if text == "" {
		shown = strings.TrimSpace(strings.Repeat("[image]\n", images))
	}
	if missing > 0 {
		shown = strings.TrimSpace(shown + fmt.Sprintf("\n(%d image(s) were no longer on the Mac)", missing))
	}
	return blocks, shown
}
