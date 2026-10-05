package daemon

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Bounds on what a session may offer to switch to. The app accepts the same
// counts and at least these lengths, so a list the daemon sends is never one
// the app refuses.
const (
	maxWireModes      = 16
	maxWireModels     = 64
	maxWireModelBytes = 128
)

var errSwitchDuringQuestion = errors.New("daemon: answer the pending question before switching")

// switchValue reports whether a mode or model name can go to the phone and
// come back as it was: not empty, bounded, on one line, with nothing a
// terminal would read and no whitespace at its ends.
func switchValue(value string, maxBytes int) bool {
	return value != "" && len(value) <= maxBytes && utf8.ValidString(value) &&
		value == strings.TrimSpace(value) && !strings.ContainsAny(value, "\n\t") &&
		!containsTerminalControl(value)
}

// switchList keeps the entries an adapter offered that the phone could send
// back, in order and once each.
//
// An entry that fails is dropped rather than repaired: the daemon accepts a
// switch only to a listed value, and a trimmed or cut one would be a name
// the adapter never offered. Always a new slice, so the adapter's own is
// never written to.
func switchList(values []string, maxEntries, maxBytes int) []string {
	var kept []string
	for _, value := range values {
		if len(kept) == maxEntries {
			break
		}
		if switchValue(value, maxBytes) && !slices.Contains(kept, value) {
			kept = append(kept, value)
		}
	}
	return kept
}

// switchSession answers ReqSetMode and ReqSetModel.
//
// It drives the agent's terminal like a send, so it is held to a send's
// rules: under the session's action lock, in the ordered mutation queue, and
// never while a question is pending, where a key meant for the mode cycle
// would land in the menu instead. Only a value the session advertised is
// accepted; the adapter listed exactly what it can switch to and verify.
func (d *Daemon) switchSession(ctx context.Context, req protocol.Request) protocol.Event {
	result := protocol.Event{
		Type: protocol.EvtSendResult, SessionID: req.SessionID,
		ClientID: req.ClientID, Status: protocol.StatusDelivered,
	}
	d.mu.Lock()
	session, known := d.sessions[req.SessionID]
	d.mu.Unlock()

	var err error
	switch {
	case !known:
		err = errors.New("daemon: that session is not running")
	case session.Question != nil:
		err = errSwitchDuringQuestion
	case req.Type == protocol.ReqSetMode && !slices.Contains(session.Modes, req.Text):
		err = fmt.Errorf("daemon: this session cannot switch to the mode %q", req.Text)
	case req.Type == protocol.ReqSetModel &&
		(session.ModelScope == "" || !slices.Contains(session.Models, req.Text)):
		err = fmt.Errorf("daemon: this session cannot switch to the model %q", req.Text)
	case req.Type == protocol.ReqSetMode:
		err = d.registry.SetMode(ctx, req.SessionID, req.Text)
	default:
		err = d.registry.SetModel(ctx, req.SessionID, req.Text)
	}
	if err != nil {
		result.Status = protocol.StatusFailed
		result.Error = err.Error()
	}
	return result
}
