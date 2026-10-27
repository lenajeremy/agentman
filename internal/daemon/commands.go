package daemon

import (
	"context"
	"fmt"
	"strings"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// maxPanelCommandBytes bounds ReqCommandPanel's command: a name, no
// arguments.
const maxPanelCommandBytes = 64

// listCommands answers ReqListCommands.
func (d *Daemon) listCommands(ctx context.Context, req protocol.Request) protocol.Event {
	commands, err := d.registry.Commands(ctx, req.SessionID)
	if err != nil {
		return workspaceError(req, commandError(err))
	}
	if commands == nil {
		commands = []protocol.Command{}
	}
	return protocol.Event{Type: protocol.EvtCommands, SessionID: req.SessionID, Commands: commands}
}

// commandPanel answers ReqCommandPanel.
func (d *Daemon) commandPanel(ctx context.Context, req protocol.Request) protocol.Event {
	panel, err := d.registry.CommandPanel(ctx, req.SessionID, req.Text)
	if err != nil {
		return workspaceError(req, commandError(err))
	}
	return protocol.Event{Type: protocol.EvtCommandPanel, SessionID: req.SessionID, Panel: &panel}
}

func validCommandPanel(req protocol.Request) error {
	command := strings.TrimSpace(req.Text)
	if !strings.HasPrefix(command, "/") || len(command) > maxPanelCommandBytes ||
		strings.ContainsAny(command, " \t") || containsTerminalControl(command) {
		return fmt.Errorf("daemon: name one command, such as /usage")
	}
	return nil
}

// commandError is an error as the person reads it, without the package that
// raised it.
func commandError(err error) string {
	text := err.Error()
	for _, prefix := range []string{"source: ", "daemon: ", "tmux: "} {
		text = strings.TrimPrefix(text, prefix)
	}
	if text != "" {
		text = strings.ToUpper(text[:1]) + text[1:]
	}
	return text
}
