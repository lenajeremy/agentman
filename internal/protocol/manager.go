package protocol

// Agentman, the manager: one conversation that reads every agent for the
// user. It lives in the daemon, and any connected app can talk to it with
// these requests and hears it through these events. All of it is additive:
// an older daemon rejects the request types, and an older app ignores the
// events.

const (
	// ReqManagerSend is the user saying something to Agentman, in Text,
	// with any images in Images.
	ReqManagerSend RequestType = "manager_send"
	// ReqManagerImage reads an image kept with the conversation, named by
	// Path as an entry's Images name it, into Workspace.
	ReqManagerImage RequestType = "manager_image"
	// ReqManagerHistory pages back through the conversation: Limit entries
	// before the one named by Before, newest last. Empty Before is the end.
	ReqManagerHistory RequestType = "manager_history"
	// ReqManagerOpenCards lists the cards still waiting on the user, as a
	// manager_history event, for an app that does not show the whole
	// conversation: the phone, for the notes Agentman suggests.
	ReqManagerOpenCards RequestType = "manager_open_cards"
	// ReqManagerStop cuts short the reply Agentman is writing.
	ReqManagerStop RequestType = "manager_stop"
	// ReqManagerSettings reads the settings; ReqManagerSetSettings replaces
	// them with Request.Manager.
	ReqManagerSettings    RequestType = "manager_settings"
	ReqManagerSetSettings RequestType = "manager_set_settings"
	// ReqManagerSetKey stores an API key in Text, in the Keychain: the
	// Anthropic key, or the Gemini key when Kind is "gemini". Empty Text
	// removes it. A key is never sent back to any app.
	ReqManagerSetKey RequestType = "manager_set_key"
	// ReqManagerBriefs lists the latest brief for every agent.
	ReqManagerBriefs RequestType = "manager_briefs"
	// ReqManagerCardReply answers the card named by CardID: OptionKey (or
	// OptionKeys, with AnswerText) for a question, Text for a reply to an
	// agent. ReqManagerCardDismiss puts a card away unanswered.
	ReqManagerCardReply   RequestType = "manager_card_reply"
	ReqManagerCardDismiss RequestType = "manager_card_dismiss"
	// ReqManagerSchedules lists what Agentman has scheduled for itself;
	// ReqManagerScheduleCancel cancels the one named by ScheduleID.
	ReqManagerSchedules      RequestType = "manager_schedules"
	ReqManagerScheduleCancel RequestType = "manager_schedule_cancel"
	// ReqManagerCheckPermission asks what Agentman would do with a request,
	// as the Settings' "Try it" does: Kind ("command", "edit", "read" or
	// "network"), Text (the command, path or URL) and Path (the agent's
	// folder). It is answered with EvtManagerPermission.
	ReqManagerCheckPermission RequestType = "manager_check_permission"
)

const (
	// EvtManagerEntry is a new or finished entry in the conversation.
	EvtManagerEntry EventType = "manager_entry"
	// EvtManagerDelta is more text for the entry named by EntryID while
	// Agentman is still writing it.
	EvtManagerDelta EventType = "manager_delta"
	// EvtManagerHistory answers ReqManagerHistory, in Entries.
	EvtManagerHistory EventType = "manager_history"
	// EvtManagerStatus says what Agentman is doing, and whether it can.
	EvtManagerStatus EventType = "manager_status"
	// EvtManagerSettings answers the settings requests.
	EvtManagerSettings EventType = "manager_settings"
	// EvtManagerBrief is an agent's brief, new or updated; on
	// ReqManagerBriefs, Briefs holds them all.
	EvtManagerBrief EventType = "manager_brief"
	// EvtManagerSchedules is everything Agentman has scheduled, in
	// Schedules, whenever that changes.
	EvtManagerSchedules EventType = "manager_schedules"
	// EvtManagerPermission answers ReqManagerCheckPermission, in Permission.
	EvtManagerPermission EventType = "manager_permission"
)

// Schedule kinds.
const (
	// ScheduleReminder wakes Agentman at a time, once or on a repeat, with
	// a message it left itself.
	ScheduleReminder = "reminder"
	// ScheduleWatch wakes Agentman when an agent reaches a state, with
	// what happened, or when it gives up waiting.
	ScheduleWatch = "watch"
)

// What a watch waits for.
const (
	WatchTurnEnd   = "turn_end"
	WatchNeedsUser = "needs_user"
	WatchIdle      = "idle"
	WatchEnded     = "ended"
)

// ManagerSchedule is something Agentman scheduled for itself: a reminder,
// or a watch on an agent whose result comes back to it.
type ManagerSchedule struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	// Text is what Agentman will be told: the reminder it left itself, or
	// why it is watching.
	Text string `json:"text"`
	// Due is when a reminder next fires, or when a watch gives up, in
	// milliseconds.
	Due int64 `json:"due"`
	// Every is a repeating reminder's interval, in seconds.
	Every int `json:"every,omitempty"`
	// Times is how many times a repeat fires in all; Until is when it stops,
	// in milliseconds. Fired is how many times it has.
	Times int   `json:"times,omitempty"`
	Until int64 `json:"until,omitempty"`
	Fired int   `json:"fired,omitempty"`
	// SessionID, AgentName and Until are a watch's agent and what it waits
	// for.
	SessionID string `json:"sessionId,omitempty"`
	AgentName string `json:"agentName,omitempty"`
	Condition string `json:"condition,omitempty"`
	CreatedAt int64  `json:"createdAt"`
}

// ManagerEntry is one entry in the conversation with Agentman.
type ManagerEntry struct {
	ID string `json:"id"`
	// Role is who it is from: "user", "assistant", "activity" (something
	// Agentman did, such as reading an agent), "brief" (news from an agent),
	// "agent" (an agent telling Agentman something, through am mcp), "card",
	// "scheduled" (one of Agentman's own reminders or watches waking it) or
	// "error".
	Role string `json:"role"`
	Text string `json:"text"`
	Ts   int64  `json:"ts"`
	// SessionID is the agent an activity or brief is about.
	SessionID string `json:"sessionId,omitempty"`
	// Tool names what an activity did, such as "read_messages".
	Tool string `json:"tool,omitempty"`
	// Streaming is set on an assistant entry Agentman is still writing; its
	// text arrives in deltas, and a final entry without it carries it all.
	Streaming bool `json:"streaming,omitempty"`
	// Path is an image an activity looked at, which the app shows too.
	Path string `json:"path,omitempty"`
	// Images names the images a user's message came with, kept beside the
	// conversation; ReqManagerImage reads one.
	Images []string `json:"images,omitempty"`
	// Card is set on a "card" entry: something only the user can decide,
	// put in front of them to answer on the spot. When it is settled, the
	// entry comes again with the card's new state.
	Card *ManagerCard `json:"card,omitempty"`
}

// InlineImage is an image sent with a message: its type and its bytes, in
// base64.
type InlineImage struct {
	MIME string `json:"mime"`
	Data string `json:"data"`
}

// Card kinds.
const (
	// CardQuestion is an agent's question or permission request that
	// Agentman may not answer itself.
	CardQuestion = "question"
	// CardNeedsYou is an agent waiting on the user for something that is not
	// a question on screen: a decision, an approval, an answer in words.
	CardNeedsYou = "needs_you"
	// CardKey is a key an agent asked for, which its rule says to ask the
	// user about, or which needs .env added to .gitignore first.
	CardKey = "key"
	// CardNote is a note Agentman suggests keeping. Nothing is saved until
	// the user approves it, as it is or as they edited it.
	CardNote = "note"
)

// Card states.
const (
	CardOpen = "open"
	// CardAnswered: the user answered it here, on the card.
	CardAnswered = "answered"
	// CardSettled: it no longer needs the user. They answered the agent
	// directly, the agent moved on or ended, or Agentman put it away. Apps
	// take it out of the conversation.
	CardSettled = "settled"
	// CardDismissed: the user put it away unanswered.
	CardDismissed = "dismissed"
)

// ManagerCard is a decision put in front of the user: an agent's question,
// with its options, or its need in words, with a box to reply.
type ManagerCard struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	State     string `json:"state"`
	SessionID string `json:"sessionId"`
	AgentName string `json:"agentName"`
	// Title says what is asked, in a line: "wants to run `git push`".
	Title string `json:"title"`
	// Detail is the command, the file, or what the agent needs, in full.
	Detail string `json:"detail,omitempty"`
	// Reason is why Agentman did not answer it itself: "it pushes to a
	// remote".
	Reason string `json:"reason,omitempty"`
	// Context is Agentman's latest brief on the agent, for what it is
	// working on when it asks.
	Context string `json:"context,omitempty"`
	// QuestionID and Options are the agent's question, on a question card:
	// an answer is checked against QuestionID, so a card for a question
	// already answered cannot answer the next one.
	QuestionID string           `json:"questionId,omitempty"`
	Options    []QuestionOption `json:"options,omitempty"`
	Multiple   bool             `json:"multiple,omitempty"`
	Custom     bool             `json:"custom,omitempty"`
	// KeyName is the key a key card is about.
	KeyName string `json:"keyName,omitempty"`
	// Suggested is the option Agentman would pick, when the user asked it to
	// answer and the answer needs their OK.
	Suggested string `json:"suggested,omitempty"`
	// Outcome says how it was settled: "You chose Yes", "Answered in the
	// agent".
	Outcome string `json:"outcome,omitempty"`
	// Note is the note a note card suggests.
	Note *Note `json:"note,omitempty"`
}

// ManagerSettings is how Agentman works, as the user set it.
type ManagerSettings struct {
	// ChatModel answers the user; BriefModel writes the per-agent briefs.
	ChatModel  string `json:"chatModel"`
	BriefModel string `json:"briefModel"`
	// Effort is how hard the chat model thinks: low, medium, high, xhigh or max.
	Effort string `json:"effort"`
	// Briefs turns the per-agent briefs on or off.
	Briefs bool `json:"briefs"`
	// AutoApprove lets Agentman answer low-risk requests itself: reading,
	// searching, tests and builds, "continue?", and changes to files in the
	// agent's own folder. Everything else becomes a card.
	AutoApprove bool `json:"autoApprove"`
	// Approve narrows which low-risk kinds AutoApprove covers; nil means
	// all of them. On a set, nil keeps what is stored.
	Approve *ApproveKinds `json:"approve,omitempty"`
	// Rules are the user's own, checked before the built-in ones: allow a
	// command or path, or always ask about it, everywhere or in one folder.
	// An "ask" rule wins over everything. On a set, nil keeps what is
	// stored and an empty list clears them.
	Rules []PermissionRule `json:"rules,omitempty"`
	// Style is how Agentman talks; Instructions is anything else it should
	// know or do. Both go into its prompt.
	Style        string `json:"style"`
	Instructions string `json:"instructions"`
	// KeySet reports whether the chat model's engine has its API key, so
	// Agentman can run. Read only.
	KeySet bool `json:"keySet"`
	// Provider is the chat model's engine, "anthropic" or "gemini"; the two
	// below report each engine's key. Read only.
	Provider        string `json:"provider,omitempty"`
	AnthropicKeySet bool   `json:"anthropicKeySet"`
	GeminiKeySet    bool   `json:"geminiKeySet"`
}

// ManagerStatus is what Agentman is doing.
type ManagerStatus struct {
	// State is "idle", "thinking", "needs_key" or "error".
	State string `json:"state"`
	Error string `json:"error,omitempty"`
	// KeySet reports whether an API key is stored.
	KeySet bool `json:"keySet"`
}

// Brief is Agentman's living summary of one agent: what it is doing, and
// what it needs, so neither Agentman nor the user has to read everything.
type Brief struct {
	SessionID string `json:"sessionId"`
	Name      string `json:"name"`
	// Summary is the brief in a sentence or two.
	Summary string   `json:"summary"`
	Goal    string   `json:"goal,omitempty"`
	Done    []string `json:"done,omitempty"`
	Now     string   `json:"now,omitempty"`
	Blocked string   `json:"blocked,omitempty"`
	// NeedsYou is what the agent wants from the user, if anything.
	NeedsYou string   `json:"needsYou,omitempty"`
	Files    []string `json:"files,omitempty"`
	// Notable marks a brief worth telling the user about now.
	Notable bool `json:"notable,omitempty"`
	// UpdatedAt is when it was written, in milliseconds.
	UpdatedAt int64 `json:"updatedAt"`
}

// ApproveKinds are the kinds of low-risk request Agentman may answer by
// itself, each the user's to switch off.
type ApproveKinds struct {
	// Reads is reading and searching files inside the agent's folder.
	Reads bool `json:"reads"`
	// Edits is changing files inside the agent's folder.
	Edits bool `json:"edits"`
	// Commands is read-only commands, tests, builds and linters.
	Commands bool `json:"commands"`
	// Continue is a bare "continue?".
	Continue bool `json:"continue"`
}

// PermissionRule is one of the user's own rules.
type PermissionRule struct {
	// Action is "allow" (Agentman may approve it) or "ask" (it always waits
	// for the user).
	Action string `json:"action"`
	// Kind is "command", "edit", "read", "network" or "any".
	Kind string `json:"kind"`
	// Pattern is matched against the command, or the path (inside the
	// agent's folder, or absolute) or URL: * stands for anything, ? for one
	// character.
	Pattern string `json:"pattern"`
	// Folder limits the rule to agents working in it, or below it; empty
	// for every agent.
	Folder string `json:"folder,omitempty"`
}

// PermissionCheck is what Agentman would do with one request.
type PermissionCheck struct {
	// Auto means Agentman would approve it by itself.
	Auto bool `json:"auto"`
	// Summary says what is asked; Reason why it would wait, when it would.
	Summary string `json:"summary"`
	Reason  string `json:"reason,omitempty"`
}
