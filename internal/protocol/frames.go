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
	// ReqReadFileChunk and ReqReadSeenFileChunk read one piece of an image the
	// matching request above can preview, from Offset.
	//
	// A preview is resized to fit a phone and the relay. The file itself is
	// fetched a piece at a time, each piece asked for after the last arrived,
	// so the whole of it never has to fit in one frame and a slow phone is
	// never sent more than it has asked for. Additive: an older daemon rejects
	// the type and the app keeps the preview it has.
	ReqReadFileChunk     RequestType = "read_file_chunk"
	ReqReadSeenFileChunk RequestType = "read_seen_file_chunk"
	// A paired phone can browse launchable folders beneath the Mac user's home
	// and start a new local agent process in one of them.
	ReqListDirectories RequestType = "list_directories"
	ReqStartSession    RequestType = "start_session"
	// ReqListFolders names every directory an agent has ever run in, with a
	// count. It is what the folder filter's Recent list shows, and the only
	// route to a directory the browser cannot reach: that one skips
	// dot-directories and stops at the Mac user's home.
	ReqListFolders RequestType = "list_folders"
	// ReqDirectorySessions lists the sessions recorded under one directory,
	// ended ones included. Discovery answers "what is running"; this answers
	// "what have I run here", which no sweep can, because a session stops
	// being discoverable the moment its process exits.
	ReqDirectorySessions RequestType = "directory_sessions"
	// ReqResumeSession reopens a session in a tmux pane so it can be typed
	// into. Most of what a folder holds is read-only — the process is gone —
	// but every CLI can reopen one of its own sessions by id, so read-only is
	// a state to leave rather than a fact to live with.
	ReqResumeSession RequestType = "resume_session"
	// ReqEndSession closes the pane a session runs in.
	//
	// Not "stop the agent": the transcript survives and the session reappears
	// in its folder's history. What ends is the cost of leaving it open,
	// which is what made panes pile up once opening them became easy.
	ReqEndSession RequestType = "end_session"
	// ReqCreateDirectory makes one folder for a new session to start in.
	// Starting an agent somewhere new should not need a trip to the Mac.
	ReqCreateDirectory RequestType = "create_directory"
	// ReqListArtifacts lists the documents a session's agent wrote for the
	// user (see Artifact), answered with EvtArtifacts. ReqReadArtifact reads
	// one, named by Path, and is answered with EvtWorkspace of kind
	// "artifact": text, or an image preview, under the same limits as a
	// workspace file.
	//
	// ReqReviewArtifact answers the agent's request to approve one: Path names
	// it, Approve says which way, and Text carries the user's comment when
	// they ask for changes. It delivers a message to the agent, so it is
	// answered like a send, with EvtSendResult for ClientID.
	//
	// Additive: an older daemon rejects the types, and the app only offers
	// them for a session that reports artifacts at all.
	ReqListArtifacts  RequestType = "list_artifacts"
	ReqReadArtifact   RequestType = "read_artifact"
	ReqReviewArtifact RequestType = "review_artifact"
	// ReqSetMode and ReqSetModel switch a session's mode or model to Text,
	// which must be one of the session's Modes or Models. Each is a terminal
	// action like a send — under the session's lock, in order, refused while
	// a question is pending — and is answered with EvtSendResult for
	// ClientID once the adapter has seen the switch take.
	ReqSetMode  RequestType = "set_mode"
	ReqSetModel RequestType = "set_model"
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
	// Notify carries that phone's choice of alerts on ReqRegisterPush. Absent
	// from an app that predates the choice, which keeps whatever the phone
	// last chose, or both kinds for a phone that never chose.
	Notify *NotifyPrefs `json:"notify,omitempty"`
	// Port names the server on ReqOpenServer and ReqCloseServer.
	Port int `json:"port,omitempty"`
	// Path is relative to the session's working directory for workspace reads,
	// or relative to the Mac user's home for launch directory requests. It is
	// never an absolute path supplied by the phone.
	Path string `json:"path,omitempty"`
	// Offset is where a chunk request starts reading, in bytes.
	Offset int64 `json:"offset,omitempty"`
	// UploadIDs names images the phone left with the relay, to be collected by
	// the daemon and handed to the agent as file paths. They are tickets, not
	// filenames: nothing in them reaches the filesystem.
	UploadIDs []string `json:"uploadIds,omitempty"`
	// Kind and Path select a local agent and a directory relative to home.
	Kind Kind `json:"kind,omitempty"`
	// Approve is the verdict on ReqReviewArtifact: true approves the
	// artifact, false asks for changes, described by Text.
	Approve bool `json:"approve,omitempty"`
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
	// EvtFolders answers ReqListFolders.
	EvtFolders EventType = "folders"
	// EvtSessionEnded answers ReqEndSession once the pane is gone.
	EvtSessionEnded EventType = "session_ended"
	// EvtDirectorySessions answers ReqDirectorySessions.
	//
	// Deliberately not EvtSessions. That one is the authoritative live
	// snapshot: the app replaces the status board with it and forgets the
	// transcripts of everything missing from it. A folder's list is neither
	// — it is a reply to one question, and most of it has already ended — so
	// sending it under that type put a month of finished sessions on the
	// board and left them there when the filter was cleared.
	EvtDirectorySessions EventType = "directory_sessions"
	// EvtArtifacts answers ReqListArtifacts.
	EvtArtifacts EventType = "artifacts"
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
	Sessions  []Session `json:"sessions,omitzero"` // empty travels as []; the app requires the array
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
	// Folders carries agent counts. On EvtFolders it is the whole answer; on
	// EvtDirectories it rides alongside Directories, one entry per browsable
	// child that has agents under it, so a phone too old to know the field
	// simply browses without counts.
	Folders []Folder `json:"folders,omitempty"`
	// Artifacts is the answer on EvtArtifacts. omitzero rather than
	// omitempty, so a session with none says so with [] instead of leaving
	// the field out.
	Artifacts []Artifact `json:"artifacts,omitzero"`
	// Daemon rides on EvtSessions: which agentman this Mac runs, and what is
	// newer. A phone too old to know the field ignores it.
	Daemon *DaemonInfo `json:"daemon,omitempty"`

	Error string `json:"error,omitempty"`
}

// DaemonInfo describes the agentman running on this Mac.
type DaemonInfo struct {
	Version string `json:"version"`
	// Latest is the newest release. Empty until a check has succeeded, or
	// when checking is switched off.
	Latest string `json:"latest,omitempty"`
	// Behind counts the releases newer than Version.
	Behind int `json:"behind,omitempty"`
	// Releases are the newer releases, newest first, with what each changed.
	Releases []ReleaseNote `json:"releases,omitempty"`
	// Upgrade is the command that upgrades this install, chosen by how it was
	// installed: Homebrew, npm or the install script.
	Upgrade string `json:"upgrade,omitempty"`
	// Changelog links to every release's notes.
	Changelog string `json:"changelog,omitempty"`
}

// ReleaseNote is one release this Mac is missing.
type ReleaseNote struct {
	Version string `json:"version"`
	// Date is when it was published, in milliseconds.
	Date    int64    `json:"date,omitempty"`
	Changes []string `json:"changes,omitempty"`
	URL     string   `json:"url,omitempty"`
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
	// Source describes the file Image was made from.
	Source *ImageSource `json:"source,omitempty"`
	// Data is one piece of a file, base64, on kind "chunk". Offset is where it
	// starts and Size is the length of the whole file, so the app knows both
	// what to ask for next and when it has everything.
	Data   string `json:"data,omitempty"`
	Offset int64  `json:"offset,omitempty"`
	Size   int64  `json:"size,omitempty"`
	// Version changes when the file does. Pieces are read in separate
	// requests, and an agent can rewrite a screenshot between two of them; a
	// download stitched from both would be neither image.
	Version string `json:"version,omitempty"`
}

// ImageSource describes the file an image preview was made from.
//
// A preview is not always the file. One too large for a phone, or for the
// relay to carry in a frame, is resized first, and nothing in the bytes the
// app receives says so. This does, along with what fetching the file itself
// would bring, so the app can offer that and say how large it is.
type ImageSource struct {
	Size int64  `json:"size"`
	MIME string `json:"mime"`
	// Width and Height are zero for a format the daemon cannot decode.
	Width  int `json:"width,omitempty"`
	Height int `json:"height,omitempty"`
	// Reduced is true when the preview is a smaller copy rather than the file
	// itself, byte for byte.
	Reduced bool `json:"reduced,omitempty"`
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

// NotifyPrefs is which alerts one phone wants pushed to it.
type NotifyPrefs struct {
	// Finished: an agent finished its turn.
	Finished bool `json:"finished"`
	// NeedsYou: an agent is blocked on an approval or a question.
	NeedsYou bool `json:"needsYou"`
}
