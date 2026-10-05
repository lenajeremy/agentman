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
	KindClaude      Kind = "claude"
	KindCodex       Kind = "codex"
	KindOpenCode    Kind = "opencode"
	KindCursor      Kind = "cursor"
	KindCursorCLI   Kind = "cursor-cli"
	KindKiro        Kind = "kiro"
	KindAntigravity Kind = "antigravity"
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
	// Mode is the agent's own name for the mode it is in — "plan",
	// "accept-edits", "ask" — as its footer or transcript spells it. Empty
	// means the agent's default, or an adapter that cannot tell.
	//
	// Read-only on the phone. Every CLI that has modes keeps the choice as a
	// global default, so switching one from here would change the laptop's
	// next session too.
	Mode string `json:"mode,omitempty"`
	// ContextPercent is how full the model's context window is, in whole
	// percent from 0 to 100. Zero is also "unknown": no adapter can tell
	// before the first reply.
	//
	// Whole percent rather than a fraction, so a long reply does not produce
	// a session update for every few tokens the window gains.
	ContextPercent int `json:"contextPercent,omitempty"`
	// Artifacts counts the documents this session's agent has written for
	// the user apart from the conversation — a plan, a task list, a
	// screenshot — which list_artifacts returns. ArtifactsToReview is how
	// many of them the agent is waiting on the user to approve.
	Artifacts         int `json:"artifacts,omitempty"`
	ArtifactsToReview int `json:"artifactsToReview,omitempty"`
	// Question is set when the agent is blocked on a decision. Its presence
	// is what makes StateWaitingInput actionable rather than merely visible:
	// the app renders the choices and the user taps one.
	Question *Question `json:"question,omitempty"`
	// Servers are the local web servers this agent has started, found by
	// watching which listening ports belong to its processes. The phone lists
	// them so the user can open one without touching the laptop.
	Servers []Server `json:"servers,omitempty"`

	// AgentPID is the agent's own process, when the adapter knows it. It
	// never leaves the machine: it is only how the daemon tells which
	// listening ports that agent's commands opened.
	AgentPID int `json:"-"`
}

// Server is a local web server an agent started.
type Server struct {
	Port int `json:"port"`
	// Command is the listening program ("node", "python3").
	Command string `json:"command,omitempty"`
	// Title is the page's <title>, when it serves HTML, so the list can say
	// "Vite + React" rather than only a number.
	Title string `json:"title,omitempty"`
	// Link is the public preview link while the server is being shared.
	Link string `json:"link,omitempty"`
}

// Artifact is a document an agent wrote for the user to read apart from the
// conversation: Antigravity's implementation plan, task list and walkthrough,
// a screenshot it took, a spec. Plans in particular are what people approve
// away from the desk, so the phone lists them, opens them, and answers a
// request for review.
type Artifact struct {
	// Name identifies the artifact within its session and is what
	// read_artifact and review_artifact take as Path. A plain file name,
	// never a path: no "/" and no "..".
	Name string `json:"name"`
	// Kind is what the artifact is for: "plan", "task", "walkthrough",
	// "spec", "image", "video" or "file". Open-ended, so the app shows a
	// kind it does not know with a generic icon rather than refusing the list.
	Kind string `json:"kind"`
	// Title is the agent's own heading for it, when it gave one.
	Title string `json:"title,omitempty"`
	// Summary is the agent's short description of what it holds.
	Summary   string `json:"summary,omitempty"`
	UpdatedAt int64  `json:"updatedAt"`
	Size      int64  `json:"size"`
	MIME      string `json:"mime,omitempty"`
	// Review means the agent asked the user to approve this artifact and has
	// had no answer since it last changed.
	Review bool `json:"review,omitempty"`
}

// Folder is one directory agents have run in.
//
// Counts are for the whole subtree, because that is what selecting a folder
// gives you: a repository's bin/ or mobile/ is the same project as its root,
// and a count that excluded them would not match the list it leads to.
type Folder struct {
	// Path is absolute. The phone only ever displays it, and hands it back as
	// an opaque token, so a folder outside the Mac user's home — a dot
	// directory, a temp checkout — stays reachable.
	Path string `json:"path"`
	// Agents is every session ever recorded under Path, running or long gone.
	Agents int `json:"agents"`
	// Running is how many of them are alive now, so the list can say which
	// folder is worth opening first.
	Running int `json:"running,omitempty"`
	// LastActivityAt orders the list: the folder you touched last is the one
	// you most likely want.
	LastActivityAt int64 `json:"lastActivityAt,omitempty"`
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
	// Field by field, because Servers makes the struct incomparable with !=.
	// TestSameAsCoversEveryField fails if a new field is left out here.
	return s.ID == other.ID && s.Kind == other.Kind && s.NativeID == other.NativeID &&
		s.Name == other.Name && s.Cwd == other.Cwd && s.State == other.State &&
		s.Inject == other.Inject && s.StartedAt == other.StartedAt &&
		s.LastActivityAt == other.LastActivityAt && s.Model == other.Model &&
		s.Mode == other.Mode && s.ContextPercent == other.ContextPercent &&
		s.Artifacts == other.Artifacts && s.ArtifactsToReview == other.ArtifactsToReview &&
		s.AgentPID == other.AgentPID &&
		s.Question.sameAs(other.Question) && SameServers(s.Servers, other.Servers)
}

// SameServers reports whether two server lists are identical, in order.
func SameServers(a, b []Server) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (q *Question) sameAs(other *Question) bool {
	if q == nil || other == nil {
		return q == other
	}
	if q.Prompt != other.Prompt || q.Title != other.Title || q.Detail != other.Detail ||
		q.ID != other.ID || q.Multiple != other.Multiple || q.Custom != other.Custom {
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
	// ID is an opaque revision for the exact pending decision. Returning it with
	// an answer lets the daemon reject a stale tap after another device has
	// already advanced the agent to a different question.
	ID      string           `json:"id,omitempty"`
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
	QuestionID string
	OptionKey  string
	Options    []string
	Text       string
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
