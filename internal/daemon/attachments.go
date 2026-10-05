package daemon

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// deliver hands a message, and the images saved for it, to a session's agent.
//
// An agent that takes images as structured parts of a message gets them that
// way (source.AttachmentInjector). Every other agent is typed a path for each
// one, mapped first to wherever that agent reads images from when its adapter
// has a preference (source.AttachmentPlacer).
func (d *Daemon) deliver(ctx context.Context, sessionID, text string, paths []string) (protocol.InjectMode, error) {
	if len(paths) == 0 {
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
