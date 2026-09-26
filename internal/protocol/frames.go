package protocol

import "encoding/json"

// Version is the wire protocol version. Bumped only on a breaking change, so
// an old app talking to a new relay fails loudly rather than subtly.
const Version = 2

// Peer names a routing destination.
type Peer string

const (
	PeerDaemon Peer = "daemon"
	PeerApp    Peer = "app"
	// PeerRelay marks a control frame the relay handles itself — pairing and
	// connection status. Everything else it forwards without looking inside.
	PeerRelay Peer = "relay"
)

// Envelope wraps every frame on the wire.
//
// The relay routes on this and does not interpret Payload for daemon/app
// traffic, but the bytes are not encrypted end to end: a relay operator can
// inspect them. Keeping the body in one field leaves room for a future sealed
// payload. Relay control frames necessarily remain relay-readable.
type Envelope struct {
	V  int    `json:"v"`
	ID string `json:"id"`
	// From is assigned by the relay for app-originated requests. Clients must
	// not trust a value they supplied themselves; the relay overwrites it
	// before forwarding so daemon subscription ownership cannot be spoofed.
	From string `json:"from,omitempty"`
	// ReplyTo echoes the ID of the request this answers.
	ReplyTo string          `json:"replyTo,omitempty"`
	To      Peer            `json:"to"`
	Payload json.RawMessage `json:"payload"`
}

/* ----------------------------- app → daemon ------------------------------ */

// RequestType discriminates an app request.
type RequestType string

const (
	ReqListSessions  RequestType = "list_sessions"
	ReqSubscribe     RequestType = "subscribe"
	ReqUnsubscribe   RequestType = "unsubscribe"
	ReqFetchMessages RequestType = "fetch_messages"
	ReqSendMessage   RequestType = "send_message"
	ReqInterrupt     RequestType = "interrupt"
	// ReqAnswer selects an option in a pending question.
	ReqAnswer RequestType = "answer_question"
	// ReqRegisterPush hands the daemon an Expo push token so it can reach the
	// phone once iOS has suspended the app and the websocket is gone. Additive:
	// a daemon that predates it rejects the type and the app carries on with
	// local notifications, so this needs no protocol version bump.
	ReqRegisterPush RequestType = "register_push"
	// ReqOpenServer shares one of a session's servers as a preview link, and
	// ReqCloseServer stops sharing it. Additive, like register_push: an older
	// daemon rejects the type and the app says the Mac needs updating.
	ReqOpenServer  RequestType = "open_server"
	ReqCloseServer RequestType = "close_server"
	// ReqStopServer ends the process listening on a port. Unlike close_server,
	// which only withdraws the public link, this one is not undoable from the
	// phone: nothing here can start a dev server again.
	ReqStopServer  RequestType = "stop_server"
	ReqListFiles   RequestType = "list_files"
	ReqReadFile    RequestType = "read_file"
	ReqListChanges RequestType = "list_changes"
	ReqFileDiff    RequestType = "file_diff"
	// ReqReadSeenFile reads one absolute path, and only one the session's
	// agent already opened. It is what lets a screenshot written to a temp
	// directory be looked at, without the daemon serving the whole disk.
	ReqReadSeenFile RequestType = "read_seen_file"
	// A paired phone can browse launchable folders beneath the Mac user's home
	// and start a new local agent process in one of them.
	ReqListDirectories RequestType = "list_directories"
	ReqStartSession    RequestType = "start_session"
)

// Request is anything the app asks of the daemon.
//
// Subscribe/Unsubscribe are what keep the zero-storage design affordable: the
// app subscribes when a session screen gains focus and drops it on blur, so
// only the session actually being watched streams anything.
type Request struct {
	Type      RequestType `json:"type"`
	SessionID string      `json:"sessionId,omitempty"`
	// Before is a cursor from a previous page; empty means newest.
	Before string `json:"before,omitempty"`
	Limit  int    `json:"limit,omitempty"`
	Text   string `json:"text,omitempty"`
	// OptionKey selects an option on ReqAnswer.
	QuestionID string `json:"questionId,omitempty"`
	OptionKey  string `json:"optionKey,omitempty"`
	// OptionKeys and AnswerText support API questions that allow several
	// selections or a custom response. OptionKey remains the terminal-menu path.
	OptionKeys []string `json:"optionKeys,omitempty"`
	AnswerText string   `json:"answerText,omitempty"`
	// ClientID is echoed on SendResult so the app can settle its optimistic
	// message bubble.
	ClientID string `json:"clientId,omitempty"`
	// PushToken carries an Expo push token on ReqRegisterPush.
	PushToken string `json:"pushToken,omitempty"`
	// Port names the server on ReqOpenServer and ReqCloseServer.
	Port int `json:"port,omitempty"`
	// Path is relative to the session's working directory for workspace reads,
	// or relative to the Mac user's home for launch directory requests. It is
	// never an absolute path supplied by the phone.
	Path string `json:"path,omitempty"`
	// UploadIDs names images the phone left with the relay, to be collected by
	// the daemon and handed to the agent as file paths. They are tickets, not
	// filenames: nothing in them reaches the filesystem.
	UploadIDs []string `json:"uploadIds,omitempty"`
	// Kind and Path select a local agent and a directory relative to home.
	Kind Kind `json:"kind,omitempty"`
}

/* ----------------------------- daemon → app ------------------------------ */

// EventType discriminates a daemon event.
type EventType string

const (
	EvtSessions      EventType = "sessions"
	EvtSessionUpdate EventType = "session_update"
	EvtSessionGone   EventType = "session_gone"
	EvtMessages      EventType = "messages"
	EvtPage          EventType = "page"
	EvtTurnComplete  EventType = "turn_complete"
	EvtSendResult    EventType = "send_result"
	// EvtServerOpened answers ReqOpenServer with the server's link.
	EvtServerOpened EventType = "server_opened"
	// EvtServerStopped answers ReqStopServer once the process is gone.
	EvtServerStopped  EventType = "server_stopped"
	EvtError          EventType = "error"
	EvtWorkspace      EventType = "workspace"
	EvtDirectories    EventType = "directories"
	EvtSessionStarted EventType = "session_started"
)

// SendStatus is how far a sent message actually got.
//
// StatusQueued is neither success nor failure: it means a hook-delivery
// session will receive the text when its current turn ends. The app shows it
// distinctly rather than pretending the message was delivered.
type SendStatus string

const (
	StatusDelivered SendStatus = "delivered"
	StatusQueued    SendStatus = "queued"
	StatusFailed    SendStatus = "failed"
)

// Event is anything the daemon reports to the app.
type Event struct {
	Type      EventType `json:"type"`
	SessionID string    `json:"sessionId,omitempty"`
	Sessions  []Session `json:"sessions,omitempty"`
	Session   *Session  `json:"session,omitempty"`
	Messages  []Message `json:"messages,omitempty"`
	Page      *Page     `json:"page,omitempty"`

	// Turn completion — the signal that rings the phone.
	SessionName string `json:"sessionName,omitempty"`
	Preview     string `json:"preview,omitempty"`

	// Send results.
	ClientID string     `json:"clientId,omitempty"`
	Status   SendStatus `json:"status,omitempty"`

	// Port and Link are set on EvtServerOpened.
	Port      int              `json:"port,omitempty"`
	Link      string           `json:"link,omitempty"`
	Workspace *WorkspaceResult `json:"workspace,omitempty"`
	// Directory names only; the app never needs the Mac's absolute home path.
	Directories []string `json:"directories,omitempty"`
	Path        string   `json:"path,omitempty"`

	Error string `json:"error,omitempty"`
}

// WorkspaceResult is a bounded, read-only view of one session's directory.
type WorkspaceResult struct {
	Kind      string            `json:"kind"`
	SessionID string            `json:"sessionId"`
	Path      string            `json:"path,omitempty"`
	Entries   []WorkspaceEntry  `json:"entries,omitempty"`
	Changes   []WorkspaceChange `json:"changes,omitempty"`
	Text      string            `json:"text,omitempty"`
	Image     string            `json:"image,omitempty"` // base64, only for supported small images
	MIME      string            `json:"mime,omitempty"`
	Diff      string            `json:"diff,omitempty"`
	Truncated bool              `json:"truncated,omitempty"`
	// Hidden counts entries withheld by privatePart. A listing that quietly
	// drops things is worse than one that refuses: the app said "no changes"
	// while twenty files were waiting, and nothing on screen could have told
	// you otherwise.
	Hidden int `json:"hidden,omitempty"`
}

type WorkspaceEntry struct {
	Name      string `json:"name"`
	Directory bool   `json:"directory"`
	Size      int64  `json:"size,omitempty"`
}

type WorkspaceChange struct {
	Path   string `json:"path"`
	Status string `json:"status"`
	// Added and Removed are line counts for this file. "Modified" says a file
	// changed; "+48 -3" says how much, which is what decides whether it is
	// worth opening on a phone. Zero for a binary file, where git reports no
	// line counts at all.
	Added   int `json:"added,omitempty"`
	Removed int `json:"removed,omitempty"`
}

/* ------------------------------ relay control ---------------------------- */

// ControlType discriminates a relay control frame.
type ControlType string

const (
	// CtlHello is sent by the relay on connect.
	CtlHello ControlType = "hello"
	// CtlPairRequest is sent by the daemon to obtain a pairing code.
	CtlPairRequest ControlType = "pair_request"
	// CtlPairCode returns that code.
	CtlPairCode ControlType = "pair_code"
	// CtlDaemonOnline and CtlDaemonOffline tell an app whether the Mac is
	// reachable. Offline is reported immediately rather than by timeout,
	// because the relay buffers nothing.
	CtlDaemonOnline  ControlType = "daemon_online"
	CtlDaemonOffline ControlType = "daemon_offline"
	// CtlAppDisconnected lets the daemon release only that connection's live
	// subscriptions without disrupting another phone watching the same agent.
	CtlAppDisconnected ControlType = "app_disconnected"
	CtlError           ControlType = "error"
)

// Control is a frame the relay itself originates or consumes.
type Control struct {
	Type ControlType `json:"type"`
	// DaemonOnline is set on CtlHello.
	DaemonOnline bool `json:"daemonOnline,omitempty"`
	// Code and ExpiresAt are set on CtlPairCode.
	Code      string `json:"code,omitempty"`
	Token     string `json:"token,omitempty"`
	ExpiresAt int64  `json:"expiresAt,omitempty"`
	// LastSeenAt is set on CtlDaemonOffline when the daemon has been seen
	// before, so the app can say how long it has been gone.
	LastSeenAt int64  `json:"lastSeenAt,omitempty"`
	DeviceID   string `json:"deviceId,omitempty"`
	Message    string `json:"message,omitempty"`
}

// NewEnvelope marshals a payload into an envelope addressed to peer.
func NewEnvelope(id string, to Peer, payload any) (Envelope, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return Envelope{}, err
	}
	return Envelope{V: Version, ID: id, To: to, Payload: body}, nil
}
