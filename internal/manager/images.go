package manager

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Images the user sends Agentman: each is checked, kept beside the
// conversation in images/, so the apps can show it again after a restart,
// and goes to the model as an image block with the message, named so
// Agentman can forward it to an agent.

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

// forwardImages reads kept images back, by the names their message gave
// them, for send_to_agent to pass on.
func (m *Manager) forwardImages(names []string) ([]protocol.InlineImage, error) {
	if len(names) > maxImagesPerMessage {
		return nil, fmt.Errorf("send at most %d images at a time", maxImagesPerMessage)
	}
	images := make([]protocol.InlineImage, 0, len(names))
	for _, name := range names {
		if !validImageName.MatchString(name) {
			return nil, fmt.Errorf("%q isn't one of the user's images; use a name given with their message", name)
		}
		data, err := os.ReadFile(filepath.Join(m.dir, "images", name))
		if err != nil {
			return nil, fmt.Errorf("%s is no longer kept", name)
		}
		images = append(images, protocol.InlineImage{MIME: http.DetectContentType(data), Data: base64.StdEncoding.EncodeToString(data)})
	}
	return images, nil
}

// imageLabel names a message's images for the model, which sees only their
// pixels, so it can pass them on.
func imageLabel(names []string) string {
	return fmt.Sprintf("[The user's images with this message: %s. To pass them to an agent, name them in send_to_agent's images.]",
		strings.Join(names, ", "))
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
	// Agentman's own pictures belong to no agent, but the phone's 0.7.0 and
	// earlier drop a picture whose session is empty, leaving a blank box: so
	// this one names its owner.
	return protocol.Event{Type: protocol.EvtWorkspace, Workspace: &protocol.WorkspaceResult{
		Kind: "file", SessionID: "agentman", Path: name, Image: base64.StdEncoding.EncodeToString(data), MIME: http.DetectContentType(data),
	}}
}
