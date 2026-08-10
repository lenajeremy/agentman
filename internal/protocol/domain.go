// Package protocol defines the wire contract shared by the daemon, the relay,
// and the mobile app.
//
// The Go types here are the source of truth; the app consumes the same shapes
// as JSON. Everything an agent produces is normalized into Session and Message
// so the app never has to know which CLI it is looking at.
package protocol

// Kind identifies an agent CLI we know how to observe. Adding one means
// writing a source adapter — nothing outside the adapter should branch on this
// beyond presentation (glyph, colour).
type Kind string

const (
	KindClaude   Kind = "claude"
	KindCodex    Kind = "codex"
	KindOpenCode Kind = "opencode"
)

// State is a session's current disposition.
//
// StateWaitingInput means the agent is blocked on a permission prompt and is
// going nowhere until a human answers. It is the most actionable state, so the
// app sorts it above everything else.
type State string

const (
	StateBusy         State = "busy"
	StateIdle         State = "idle"
	StateWaitingInput State = "waiting_input"
	StateEnded        State = "ended"
)

// InjectMode is how (and whether) we can deliver a message into a running
// session. It is surfaced in the UI as a badge so the user knows what to
// expect before hitting send.
//
//	InjectAPI  — a real API on the agent itself. Instant, works mid-turn.
//	InjectTmux — session runs under our tmux wrapper; we type into it. Mid-turn.
//	InjectHook — no live channel. Queued, delivered when the current turn ends.
//	             Best-effort: the CLI can discard the injection.
//	InjectNone — read-only. The composer is disabled.
type InjectMode string

const (
	InjectAPI  InjectMode = "api"
	InjectTmux InjectMode = "tmux"
	InjectHook InjectMode = "hook"
	InjectNone InjectMode = "none"
)

// Session is one running agent, as the app sees it.
type Session struct {
	// ID is a stable composite, "<kind>:<nativeID>", unique across kinds.
	ID   string `json:"id"`
	Kind Kind   `json:"kind"`
	// NativeID is the agent's own session identifier, as it appears on disk
	// or in its API.
	NativeID string `json:"nativeId"`
	// Name is a human label. Claude supplies one; otherwise we derive it from
	// the working directory.
	Name           string     `json:"name"`
	Cwd            string     `json:"cwd"`
	State          State      `json:"state"`
	Inject         InjectMode `json:"inject"`
	StartedAt      int64      `json:"startedAt"`
	LastActivityAt int64      `json:"lastActivityAt"`
}

// Role is who produced a message.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
	RoleSystem    Role = "system"
)

// ToolStatus is the outcome of a tool invocation.
type ToolStatus string

const (
	ToolRunning ToolStatus = "running"
	ToolOK      ToolStatus = "ok"
	ToolError   ToolStatus = "error"
)

// Tool describes a tool call, reduced to what is readable at a glance.
type Tool struct {
	Name string `json:"name"`
	// Summary is the one detail worth showing inline: the command, the path,
	// the pattern.
	Summary string     `json:"summary,omitempty"`
	Status  ToolStatus `json:"status,omitempty"`
}

// Message is one normalized entry in a session's feed.
type Message struct {
	// ID is stable and idempotent across re-reads, which is what lets the app
	// dedupe when a live-tailed message also arrives inside a history page.
	// Derived from the transcript's own identifier where one exists, otherwise
	// from the record's byte offset.
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
	Role      Role   `json:"role"`
	Ts        int64  `json:"ts"`
	Text      string `json:"text,omitempty"`
	Tool      *Tool  `json:"tool,omitempty"`
	// IsSidechain marks subagent output, collapsed behind a chip in the UI
	// rather than inlined with the main conversation.
	IsSidechain bool `json:"isSidechain,omitempty"`
}

// Page is one screenful of scrollback.
//
// Cursors are opaque to the app, which only echoes them back. For file-backed
// agents a cursor encodes a byte offset; for OpenCode it wraps that API's own
// pagination. That indirection is what lets three very different agents page
// through a single call.
type Page struct {
	SessionID string `json:"sessionId"`
	// Messages are chronological (oldest first) regardless of read direction.
	Messages []Message `json:"messages"`
	// NextCursor is passed back as "before" to fetch further into the past.
	NextCursor string `json:"nextCursor,omitempty"`
	HasMore    bool   `json:"hasMore"`
}

// NewPage builds a page with a guaranteed non-nil message slice.
//
// Go marshals a nil slice as JSON `null` rather than `[]`, and a client that
// reasonably expects an array crashes on it. That is not hypothetical: an
// empty page — a session opened before it has written anything — took the
// mobile app down with "Cannot read properties of null". Normalizing here
// means no client has to defend against it.
func NewPage(sessionID string, messages []Message, nextCursor string, hasMore bool) Page {
	if messages == nil {
		messages = []Message{}
	}
	return Page{
		SessionID:  sessionID,
		Messages:   messages,
		NextCursor: nextCursor,
		HasMore:    hasMore,
	}
}
