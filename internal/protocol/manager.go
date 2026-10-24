package protocol

// Agentman, the manager: one conversation that reads every agent for the
// user. It lives in the daemon, and any connected app can talk to it with
// these requests and hears it through these events. All of it is additive:
// an older daemon rejects the request types, and an older app ignores the
// events.

const (
	// ReqManagerSend is the user saying something to Agentman, in Text.
	ReqManagerSend RequestType = "manager_send"
	// ReqManagerHistory pages back through the conversation: Limit entries
	// before the one named by Before, newest last. Empty Before is the end.
	ReqManagerHistory RequestType = "manager_history"
	// ReqManagerStop cuts short the reply Agentman is writing.
	ReqManagerStop RequestType = "manager_stop"
	// ReqManagerSettings reads the settings; ReqManagerSetSettings replaces
	// them with Request.Manager.
	ReqManagerSettings    RequestType = "manager_settings"
	ReqManagerSetSettings RequestType = "manager_set_settings"
	// ReqManagerSetKey stores the Anthropic API key in Text, in the Keychain.
	// Empty Text removes it. The key is never sent back to any app.
	ReqManagerSetKey RequestType = "manager_set_key"
	// ReqManagerBriefs lists the latest brief for every agent.
	ReqManagerBriefs RequestType = "manager_briefs"
	// ReqManagerCardReply answers the card named by CardID: OptionKey (or
	// OptionKeys, with AnswerText) for a question, Text for a reply to an
	// agent. ReqManagerCardDismiss puts a card away unanswered.
	ReqManagerCardReply   RequestType = "manager_card_reply"
	ReqManagerCardDismiss RequestType = "manager_card_dismiss"
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
)

// ManagerEntry is one entry in the conversation with Agentman.
type ManagerEntry struct {
	ID string `json:"id"`
	// Role is who it is from: "user", "assistant", "activity" (something
	// Agentman did, such as reading an agent), "brief" (news from an agent)
	// or "error".
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
	// Card is set on a "card" entry: something only the user can decide,
	// put in front of them to answer on the spot. When it is settled, the
	// entry comes again with the card's new state.
	Card *ManagerCard `json:"card,omitempty"`
}

// Card kinds.
const (
	// CardQuestion is an agent's question or permission request that
	// Agentman may not answer itself.
	CardQuestion = "question"
	// CardNeedsYou is an agent waiting on the user for something that is not
	// a question on screen: a decision, an approval, an answer in words.
	CardNeedsYou = "needs_you"
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
	// Suggested is the option Agentman would pick, when the user asked it to
	// answer and the answer needs their OK.
	Suggested string `json:"suggested,omitempty"`
	// Outcome says how it was settled: "You chose Yes", "Answered in the
	// agent".
	Outcome string `json:"outcome,omitempty"`
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
	// Style is how Agentman talks; Instructions is anything else it should
	// know or do. Both go into its prompt.
	Style        string `json:"style"`
	Instructions string `json:"instructions"`
	// KeySet reports whether an API key is stored. Read only.
	KeySet bool `json:"keySet"`
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
