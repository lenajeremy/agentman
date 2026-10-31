package daemon

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/source"
)

// deliver hands a message, and the images saved for it, to a session's agent.
//
// An agent that takes images as structured parts of a message gets them that
// way (source.AttachmentInjector). Every other agent is typed a path for each
// one, mapped first to wherever that agent reads images from when its adapter
// has a preference (source.AttachmentPlacer).
func (d *Daemon) deliver(ctx context.Context, sessionID, text string, paths []string) (protocol.InjectMode, error) {
	text, skills := d.namingSkills(ctx, sessionID, text)
	if len(paths) == 0 {
		if len(skills) > 0 {
			if mode, handled, err := d.registry.InjectWithSkills(ctx, sessionID, text, skills); handled {
				return mode, err
			}
		}
		return d.registry.Inject(ctx, sessionID, text)
	}
	if mode, handled, err := d.registry.InjectWithAttachments(ctx, sessionID, text, paths); handled {
		return mode, err
	}
	placed := make([]string, 0, len(paths))
	for _, path := range paths {
		placed = append(placed, d.placeAttachment(ctx, sessionID, path))
	}
	// The paths lead, the way a person types them before saying what to look
	// for. One line, so nothing submits early.
	return d.registry.Inject(ctx, sessionID, strings.TrimSpace(strings.Join(placed, " ")+" "+text))
}

// namingSkills is a message with the skills it names in the form its
// agent takes them (source.SkillText), and the ids of those an agent takes
// beside the message.
func (d *Daemon) namingSkills(ctx context.Context, sessionID, text string) (string, []string) {
	if !strings.ContainsAny(text, "/$") {
		return text, nil
	}
	d.mu.Lock()
	session, ok := d.sessions[sessionID]
	d.mu.Unlock()
	if !ok {
		return text, nil
	}
	commands, err := d.registry.Commands(ctx, sessionID)
	if err != nil || len(commands) == 0 {
		return text, nil
	}
	return source.SkillText(session.Kind, text, commands)
}

// placeAttachment returns the path to type for one saved image.
//
// Placing is a preference, never a condition: when the adapter fails, or
// answers with something that is not one absolute path on one line, the agent
// is given the original. A newline in particular would submit the message
// before its text was typed.
func (d *Daemon) placeAttachment(ctx context.Context, sessionID, path string) string {
	placed, err := d.registry.PlaceAttachment(ctx, sessionID, path)
	if err != nil || !filepath.IsAbs(placed) || strings.ContainsAny(placed, "\r\n\t") ||
		containsTerminalControl(placed) {
		return path
	}
	return placed
}

// saveInlineImages files the images the Mac app sent with a message and
// returns their paths, in order.
func (d *Daemon) saveInlineImages(sessionID string, images []protocol.InlineImage) ([]string, error) {
	d.mu.Lock()
	store := d.attachments
	d.mu.Unlock()
	if store == nil {
		return nil, errors.New("this daemon has nowhere to keep images")
	}
	paths := make([]string, 0, len(images))
	for _, image := range images {
		data, err := base64.StdEncoding.DecodeString(image.Data)
		if err != nil {
			return nil, errors.New("an image wasn't readable")
		}
		path, err := store.SaveData(sessionID, data)
		if err != nil {
			return nil, err
		}
		paths = append(paths, path)
	}
	return paths, nil
}

// uploadedImages collects a phone's images for a message to Agentman and
// returns their bytes. They are filed as an agent's are, read back, and the
// files removed: Agentman keeps its own copies with the conversation.
func (d *Daemon) uploadedImages(ctx context.Context, ids []string) ([]protocol.InlineImage, error) {
	if len(ids) > maxUploadIDs {
		return nil, fmt.Errorf("send at most %d images at a time", maxUploadIDs)
	}
	paths, err := d.saveAttachments(ctx, "agentman", ids)
	if err != nil {
		return nil, err
	}
	images := make([]protocol.InlineImage, 0, len(paths))
	for _, path := range paths {
		data, readErr := os.ReadFile(path)
		_ = os.Remove(path)
		if readErr != nil {
			return nil, errors.New("an image wasn't readable")
		}
		images = append(images, protocol.InlineImage{
			MIME: http.DetectContentType(data),
			Data: base64.StdEncoding.EncodeToString(data),
		})
	}
	return images, nil
}
