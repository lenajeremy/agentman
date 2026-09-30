package daemon

import (
	"encoding/base64"
	"errors"
	"net/http"
	"os"
	"strconv"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// A preview is a copy made to fit: at most 2048 pixels on its longest edge and
// 2 MiB, because it travels as one message through the relay. That is the
// right thing to look at on a phone and the wrong thing to keep — a mockup or
// a generated image saved from it is a smaller, re-encoded picture.
//
// So the file itself can be fetched as well, in pieces. Each piece is its own
// request and its own reply, which makes the phone the one setting the pace:
// nothing is sent that was not asked for, so a slow connection cannot back the
// relay's queue up the way a single 30 MiB push would. Nothing is held open
// between requests either. Every piece is authorised and opened from scratch,
// exactly as a preview is, so a download grants no reach a preview did not.
const (
	// originalChunkBytes is one piece before base64, which makes it 1.4 MiB on
	// the wire: half the largest preview already sent down this path, and well
	// inside the 4 MiB frame the relay accepts from a daemon.
	originalChunkBytes = 1 << 20
)

var errOriginalChanged = errors.New("the file changed while it was being read")

// readImageChunk reads one piece of an image that is already open and already
// authorised, and fills in result as a "chunk".
//
// Only what can be previewed can be downloaded: the type is sniffed from the
// file on every request rather than remembered from the first, so a path that
// stopped being an image between two pieces stops being served.
func readImageChunk(file *os.File, offset int64, result *protocol.WorkspaceResult) error {
	info, err := file.Stat()
	if err != nil {
		return errors.New("that file could not be read")
	}
	if !info.Mode().IsRegular() {
		return errors.New("only regular files can be read")
	}
	var head [512]byte
	n, _ := file.ReadAt(head[:], 0)
	mime := http.DetectContentType(head[:n])
	if _, ok := readableImages[mime]; !ok {
		return errors.New("only images can be downloaded this way")
	}
	size := info.Size()
	if size > maxImageInput {
		return errPreviewTooLarge
	}
	if offset < 0 || offset >= size {
		return errors.New("that is past the end of the file")
	}

	piece := make([]byte, min(int64(originalChunkBytes), size-offset))
	// Short only if the file shrank after it was measured.
	if read, _ := file.ReadAt(piece, offset); read != len(piece) {
		return errOriginalChanged
	}

	result.Kind = "chunk"
	result.MIME = mime
	result.Data = base64.StdEncoding.EncodeToString(piece)
	result.Offset = offset
	result.Size = size
	result.Version = fileVersion(info)
	return nil
}

// fileVersion names one state of a file. Length and modification time are what
// a rewrite changes; a digest would say the same thing at the cost of reading
// the whole file for every piece.
func fileVersion(info os.FileInfo) string {
	return strconv.FormatInt(info.Size(), 36) + "-" + strconv.FormatInt(info.ModTime().UnixNano(), 36)
}

// readWorkspaceChunk reads a piece of an image inside a session's directory.
func readWorkspaceChunk(root *os.Root, rel string, offset int64, result *protocol.WorkspaceResult) error {
	file, err := root.Open(rel)
	if err != nil {
		return err
	}
	defer file.Close()
	return readImageChunk(file, offset, result)
}

// readSeenFileChunk reads a piece of an image the session's agent opened.
func (d *Daemon) readSeenFileChunk(req protocol.Request) protocol.Event {
	file, _, refusal := d.openSeenFile(req)
	if refusal != "" {
		return workspaceError(req, refusal)
	}
	defer file.Close()
	result := &protocol.WorkspaceResult{SessionID: req.SessionID, Path: req.Path}
	if err := readImageChunk(file, req.Offset, result); err != nil {
		return workspaceError(req, err.Error())
	}
	return protocol.Event{Type: protocol.EvtWorkspace, Workspace: result}
}
