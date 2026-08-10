// Package source discovers running agent sessions and reads their output.
//
// Each supported CLI gets one adapter implementing Source. That interface is
// the extension point of the project: adding Gemini CLI, Grok CLI, or Amp
// means writing one file here and registering it, with nothing else in the
// daemon, relay, or app needing to change.
package source

import (
	"context"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Source observes one agent CLI.
//
// Implementations are expected to be cheap when the agent is not installed:
// Discover should return nothing rather than an error when the CLI's data
// directory is simply absent.
type Source interface {
	// Kind identifies which agent this adapter handles.
	Kind() protocol.Kind

	// Discover returns the sessions currently alive on this machine. It is
	// called on a timer and must be safe to call concurrently with Page.
	Discover(ctx context.Context) ([]protocol.Session, error)

	// Page returns scrollback for a session, ending before the given cursor.
	// An empty cursor means "the most recent messages". Cursors are opaque to
	// every caller; only the adapter that minted one interprets it.
	Page(ctx context.Context, sessionID, before string, limit int) (protocol.Page, error)

	// Follow streams messages appended to a session until ctx is cancelled.
	// It is started when the app opens a session and stopped when it leaves,
	// so an idle session costs nothing.
	Follow(ctx context.Context, sessionID string, out chan<- []protocol.Message) error
}

// Injector is implemented by adapters that can deliver a message into a
// running session. Adapters without a delivery channel simply omit it, and the
// app shows their sessions as read-only.
type Injector interface {
	// Inject delivers text to a session. The returned mode reports how it was
	// actually delivered, which may be weaker than the session's advertised
	// mode — a queued hook delivery in particular is not the same as a send,
	// and the UI says so.
	Inject(ctx context.Context, sessionID, text string) (protocol.InjectMode, error)
}
