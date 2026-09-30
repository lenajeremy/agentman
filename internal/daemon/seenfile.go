package daemon

import (
	"encoding/base64"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// readSeenFile serves one file the session's agent already opened.
//
// Membership in that session's set is the whole authorisation: the phone can
// only ever name a path the transcript already named, so this grants no
// reach the app did not already have. What it adds is the bytes — a
// screenshot an agent wrote to a temp directory is a file the phone can now
// look at rather than only read the name of.
func (d *Daemon) readSeenFile(req protocol.Request) protocol.Event {
	file, info, refusal := d.openSeenFile(req)
	if refusal != "" {
		return workspaceError(req, refusal)
	}
	defer file.Close()

	result := &protocol.WorkspaceResult{SessionID: req.SessionID, Path: req.Path, Kind: "file"}
	var head [512]byte
	n, _ := file.Read(head[:])
	mime := http.DetectContentType(head[:n])
	if _, ok := readableImages[mime]; !ok {
		return workspaceError(req, "only images can be opened this way")
	}
	if info.Size() > maxImageInput {
		return workspaceError(req, "that "+errPreviewTooLarge.Error())
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return workspaceError(req, "that file could not be read")
	}
	// Resized to fit a phone rather than refused. The screenshots agents take
	// are exactly the images that land here, and at full Retina resolution
	// they were over the limit more often than not.
	data, previewMime, source, err := previewImage(file, mime)
	if err != nil {
		return workspaceError(req, "that "+err.Error())
	}
	result.Image = base64.StdEncoding.EncodeToString(data)
	result.MIME = previewMime
	result.Source = &source
	return protocol.Event{Type: protocol.EvtWorkspace, Workspace: result}
}

// openSeenFile opens one file the session's agent already opened, or says why
// it will not. The preview and the download of the file itself both start
// here, so neither can be reached by a path the other would refuse.
func (d *Daemon) openSeenFile(req protocol.Request) (*os.File, os.FileInfo, string) {
	path := filepath.Clean(req.Path)
	if path != req.Path || !filepath.IsAbs(path) {
		return nil, nil, "that is not a path this session opened"
	}
	if !d.seen.allows(req.SessionID, path) {
		return nil, nil, "that is not a path this session opened"
	}
	// Even so: a name the agent opened is not a promise about what is there
	// now, and the same credential rules apply as anywhere else.
	if privatePart(filepath.Base(path)) {
		return nil, nil, "that file is not available in Agentman"
	}

	// Lstat, not Stat: a symlink is followed by the agent but must not be
	// followed here, or a name the transcript blessed could be repointed at
	// something else between then and now.
	info, err := os.Lstat(path)
	if err != nil {
		return nil, nil, "that file is no longer there"
	}
	if !info.Mode().IsRegular() {
		return nil, nil, "only regular files can be read"
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, nil, "that file could not be read"
	}
	return file, info, ""
}
