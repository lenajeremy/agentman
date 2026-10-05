package source

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// maxAntigravityAttachment bounds one copied image. The daemon's own store
// caps an upload at 4 MiB; this only guards against something else being
// handed in.
const maxAntigravityAttachment = 32 << 20

var _ AttachmentPlacer = (*AntigravitySource)(nil)

// PlaceAttachment implements AttachmentPlacer.
//
// A phone's image is saved under ~/.agentman, outside every workspace, and agy
// asks before it reads a file outside the workspace: "File access … Reason:
// outside workspace … Allow access to this file?", once for every image sent.
// The conversation's own .user_uploaded folder is where agy puts an image
// pasted at its prompt, and it reads files there without asking — verified on
// agy 1.2.17 by reading the same picture from both places. So the image is
// copied there and that path typed instead.
//
// Until agy has opened a conversation — a pane waiting for its first message —
// there is no folder to copy into, and the saved path is given unchanged.
func (s *AntigravitySource) PlaceAttachment(ctx context.Context, sessionID, path string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	session, err := s.session(sessionID)
	if err != nil {
		return "", err
	}
	conversation := session.meta.NativeID
	if !isUUID(conversation) {
		return path, nil
	}
	brain := filepath.Join(s.root(), "brain", conversation)
	if info, err := os.Lstat(brain); err != nil || !info.IsDir() {
		return path, nil
	}
	uploads := filepath.Join(brain, ".user_uploaded")
	if err := os.MkdirAll(uploads, 0o755); err != nil {
		return "", err
	}
	if info, err := os.Lstat(uploads); err != nil || !info.IsDir() {
		return "", errors.New("source: agy's upload folder is not a folder")
	}
	return copyAntigravityAttachment(path, uploads)
}

// copyAntigravityAttachment copies one regular file into dir under its own
// name, or a numbered variant of it, never replacing anything already there.
func copyAntigravityAttachment(path, dir string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("source: %s is not a regular file", path)
	}
	if info.Size() > maxAntigravityAttachment {
		return "", fmt.Errorf("source: %s is too large to attach", path)
	}
	source, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer source.Close()

	name := filepath.Base(path)
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for attempt := 0; attempt < 100; attempt++ {
		candidate := name
		if attempt > 0 {
			candidate = fmt.Sprintf("%s-%d%s", stem, attempt, ext)
		}
		target := filepath.Join(dir, candidate)
		// O_EXCL: an existing file, or a link planted where the copy is
		// going, is never written through.
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		_, copyErr := io.Copy(out, io.LimitReader(source, maxAntigravityAttachment))
		closeErr := out.Close()
		if copyErr != nil || closeErr != nil {
			os.Remove(target)
			return "", errors.Join(copyErr, closeErr)
		}
		return target, nil
	}
	return "", errors.New("source: could not name the attachment")
}
