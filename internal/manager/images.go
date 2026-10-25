package manager

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Images the user sends Agentman: each is checked, kept beside the
// conversation in images/, so the apps can show it again after a restart,
// and goes to the model as an image block with the message.

const (
	maxImagesPerMessage = 4
	// maxImageBytes is the API's own limit for one image.
	maxImageBytes = 5 << 20
	// maxImageReply keeps a kept image within one frame to an app.
	maxImageReply = 10 << 20
)

var imageExtensions = map[string]string{
	"image/png": "png", "image/jpeg": "jpg", "image/gif": "gif", "image/webp": "webp",
}

var validImageName = regexp.MustCompile(`^m-[0-9]+-[0-9a-f]{6}-[0-9]\.(png|jpg|gif|webp)$`)

// keepImages checks a message's images, keeps them, and returns their names
// and the blocks the model gets.
func (m *Manager) keepImages(entryID string, images []protocol.InlineImage) ([]string, []anthropic.BetaContentBlockParamUnion, error) {
	if len(images) == 0 {
		return nil, nil, nil
	}
	if len(images) > maxImagesPerMessage {
		return nil, nil, fmt.Errorf("send at most %d images at a time", maxImagesPerMessage)
	}
	type checked struct {
		data []byte
		mime string
	}
	var ok []checked
	for _, image := range images {
		data, err := base64.StdEncoding.DecodeString(image.Data)
		if err != nil || len(data) == 0 {
			return nil, nil, errors.New("an image didn't arrive whole")
		}
		if len(data) > maxImageBytes {
			return nil, nil, fmt.Errorf("an image is larger than %d MB", maxImageBytes>>20)
		}
		// What the bytes are, not what the app said they are.
		mime := http.DetectContentType(data)
		if _, known := imageExtensions[mime]; !known {
			return nil, nil, errors.New("send PNG, JPEG, GIF or WebP images")
		}
		ok = append(ok, checked{data, mime})
	}
	dir := filepath.Join(m.dir, "images")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, nil, err
	}
	names := make([]string, 0, len(ok))
	blocks := make([]anthropic.BetaContentBlockParamUnion, 0, len(ok))
	for i, image := range ok {
		name := fmt.Sprintf("%s-%d.%s", entryID, i, imageExtensions[image.mime])
		if err := os.WriteFile(filepath.Join(dir, name), image.data, 0o600); err != nil {
			return nil, nil, err
		}
		names = append(names, name)
		blocks = append(blocks, anthropic.BetaContentBlockParamUnion{OfImage: &anthropic.BetaImageBlockParam{
			Source: anthropic.BetaImageBlockParamSourceUnion{OfBase64: &anthropic.BetaBase64ImageSourceParam{
				Data: base64.StdEncoding.EncodeToString(image.data), MediaType: anthropic.BetaBase64ImageSourceMediaType(image.mime),
			}},
		}})
	}
	return names, blocks, nil
}

// readImage is a kept image, for an app to show.
func (m *Manager) readImage(name string) protocol.Event {
	if !validImageName.MatchString(name) {
		return protocol.Event{Type: protocol.EvtError, Error: "that isn't an image of this conversation"}
	}
	data, err := os.ReadFile(filepath.Join(m.dir, "images", name))
	if err != nil {
		return protocol.Event{Type: protocol.EvtError, Error: "that image is no longer kept"}
	}
	if len(data) > maxImageReply {
		return protocol.Event{Type: protocol.EvtError, Error: "that image is too large to show"}
	}
	return protocol.Event{Type: protocol.EvtWorkspace, Workspace: &protocol.WorkspaceResult{
		Kind: "file", Path: name, Image: base64.StdEncoding.EncodeToString(data), MIME: http.DetectContentType(data),
	}}
}
