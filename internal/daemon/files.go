package daemon

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/lenajeremy/agentman/internal/attachments"
	"github.com/lenajeremy/agentman/internal/protocol"
)

const (
	// maxFilePartBytes bounds one part's base64: under the relay's 128 KiB
	// a request, with room for the envelope.
	maxFilePartBytes = 100 << 10
	// maxFileParts is enough parts for the largest file kept.
	maxFileParts = (attachments.MaxFileBytes*4/3)/(90<<10) + 2
	// fileUploadTTL is how long a file's parts wait for the rest.
	fileUploadTTL = 3 * time.Minute
	// maxFileUploads bounds files arriving at once.
	maxFileUploads = 8
)

// fileUploads joins the parts of files on their way from a phone.
type fileUploads struct {
	mu    sync.Mutex
	files map[string]*fileUpload
}

type fileUpload struct {
	name    string
	parts   []string
	got     int
	size    int
	started time.Time
}

// add keeps one part, and returns the whole file once every part is in.
func (f *fileUploads) add(part *protocol.FilePart, now time.Time) (name string, data []byte, done bool, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.files == nil {
		f.files = map[string]*fileUpload{}
	}
	for id, upload := range f.files {
		if now.Sub(upload.started) > fileUploadTTL {
			delete(f.files, id)
		}
	}
	upload := f.files[part.ID]
	if upload == nil {
		if len(f.files) >= maxFileUploads {
			return "", nil, false, errors.New("too many files are on their way at once")
		}
		upload = &fileUpload{name: part.Name, parts: make([]string, part.Parts), started: now}
		f.files[part.ID] = upload
	}
	if len(upload.parts) != part.Parts || upload.name != part.Name {
		delete(f.files, part.ID)
		return "", nil, false, errors.New("the parts of that file don't agree")
	}
	if upload.parts[part.Part] == "" {
		upload.got++
		upload.size += len(part.Data)
	}
	upload.parts[part.Part] = part.Data
	if upload.size > attachments.MaxFileBytes*4/3+4 {
		delete(f.files, part.ID)
		return "", nil, false, errors.New("that file is larger than 10 MB")
	}
	if upload.got < len(upload.parts) {
		return "", nil, false, nil
	}
	delete(f.files, part.ID)
	decoded, err := base64.StdEncoding.DecodeString(strings.Join(upload.parts, ""))
	if err != nil {
		return "", nil, false, errors.New("that file arrived damaged; send it again")
	}
	return upload.name, decoded, true, nil
}

// fileSaver is the part of the attachment store that keeps files.
type fileSaver interface {
	SaveFile(sessionID, name string, data []byte) (string, error)
}

// attachFile answers ReqAttachFile.
func (d *Daemon) attachFile(req protocol.Request) protocol.Event {
	d.mu.Lock()
	_, known := d.sessions[req.SessionID]
	store, _ := d.attachments.(fileSaver)
	d.mu.Unlock()
	if !known {
		return workspaceError(req, "that session is no longer available")
	}
	if store == nil {
		return workspaceError(req, "this Mac can't keep files right now")
	}
	name, data, done, err := d.files.add(req.File, time.Now())
	if err != nil {
		return workspaceError(req, err.Error())
	}
	answer := &protocol.AttachedFile{ID: req.File.ID, Done: done}
	if done {
		path, err := store.SaveFile(req.SessionID, name, data)
		if err != nil {
			return workspaceError(req, strings.TrimPrefix(err.Error(), "attachments: "))
		}
		answer.Path = path
	}
	return protocol.Event{Type: protocol.EvtAttached, SessionID: req.SessionID, Attached: answer}
}

func validFilePart(req protocol.Request) error {
	part := req.File
	if part == nil || part.Parts < 1 || part.Parts > maxFileParts || part.Part < 0 || part.Part >= part.Parts ||
		len(part.Data) == 0 || len(part.Data) > maxFilePartBytes || !validUploadName(part.ID) {
		return fmt.Errorf("daemon: that part of the file is malformed")
	}
	if part.Name == "" || len(part.Name) > 255 || containsTerminalControl(part.Name) || strings.ContainsAny(part.Name, "/\\\x00") {
		return fmt.Errorf("daemon: that file's name can't be kept")
	}
	return nil
}

// validUploadName is a part's file id: short, and only letters, digits and
// dashes, as the apps make them.
func validUploadName(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}
