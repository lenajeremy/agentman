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
	// Model is what the agent is actually running — "claude-opus-5",
	// "gpt-5.6-sol", "big-pickle". Empty until the agent has replied once,
	// because none of the three record it before then.
	//
	// Worth surfacing because Kind does not answer the question people ask.
	// "Codex" says which CLI is open, not which model is doing the work, and
	// those diverge constantly.
	Model string `json:"model,omitempty"`
	// Question is set when the agent is blocked on a decision. Its presence
	// is what makes StateWaitingInput actionable rather than merely visible:
	// the app renders the choices and the user taps one.
	Question *Question `json:"question,omitempty"`
}

// SameAs reports whether two snapshots of a session are equivalent.
//
// Deliberately not ==. Session holds a *Question, so the compiler compares
// pointer identity, and discovery builds a fresh Question from the pane on
// every sweep — two readings of one unchanged prompt therefore compare unequal
// forever. That made a session blocked on a permission prompt emit an update
// every second, which is the exact state where the user is most likely to be
// watching their phone on cell data.
func (s Session) SameAs(other Session) bool {
	question, otherQuestion := s.Question, other.Question
	s.Question, other.Question = nil, nil
	if s != other {
		return false
	}
	return question.sameAs(otherQuestion)
}

func (q *Question) sameAs(other *Question) bool {
	if q == nil || other == nil {
		return q == other
	}
	if q.Prompt != other.Prompt || q.Title != other.Title || q.Detail != other.Detail ||
		q.Multiple != other.Multiple || q.Custom != other.Custom {
		return false
	}
	if len(q.Options) != len(other.Options) {
		return false
	}
	for i := range q.Options {
		if q.Options[i] != other.Options[i] {
			return false
		}
	}
	return true
}

// Question is a pending decision an agent is waiting on.
type Question struct {
	Prompt  string           `json:"prompt"`
	Title   string           `json:"title,omitempty"`
	Detail  string           `json:"detail,omitempty"`
	Options []QuestionOption `json:"options"`
	// Multiple means the agent accepts more than one listed choice. Custom
	// means the user may supply their own text instead of a listed choice.
	Multiple bool `json:"multiple,omitempty"`
	Custom   bool `json:"custom,omitempty"`
}

// QuestionOption is one choice. Key is what gets sent to select it.
type QuestionOption struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Preview     string `json:"preview,omitempty"`
	Selected    bool   `json:"selected,omitempty"`
	Checked     bool   `json:"checked,omitempty"`
}

// QuestionAnswer is the complete user response to one displayed question.
// Terminal menus use OptionKey; API-backed agents may accept several choices
// and/or custom text.
type QuestionAnswer struct {
	OptionKey string
	Options   []string
	Text      string
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
	Name    string     `json:"name"`
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
