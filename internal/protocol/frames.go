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
	EvtError         EventType = "error"
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

	Error string `json:"error,omitempty"`
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
