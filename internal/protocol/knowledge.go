package protocol

// Knowledge and keys: the user's notes for their agents, and the API keys
// Agentman hands out. Apps manage both; agents reach them through am mcp,
// which talks to the daemon over the local socket with the agent_* requests.
// A key's value never travels: apps set it, and a release writes it into a
// project's .env.

const (
	// ReqKnowledgeList lists the notes, without bodies, in Notes.
	ReqKnowledgeList RequestType = "knowledge_list"
	// ReqKnowledgeRead reads the note named Path, in tier Kind ("shared" or
	// "private"; empty looks in both), into Note.
	ReqKnowledgeRead RequestType = "knowledge_read"
	// ReqKnowledgeWrite saves Note; ReqKnowledgeDelete removes the note
	// named Path in tier Kind.
	ReqKnowledgeWrite  RequestType = "knowledge_write"
	ReqKnowledgeDelete RequestType = "knowledge_delete"

	// ReqKeysList lists the keys, names only, in Keys.
	ReqKeysList RequestType = "keys_list"
	// ReqKeySet saves Key's name, description and rule, and its value from
	// Text when Text is not empty. ReqKeyDelete removes the key named Path.
	ReqKeySet    RequestType = "key_set"
	ReqKeyDelete RequestType = "key_delete"
)

// Requests from agents, through am mcp. Path is the agent's working folder
// and Port its process ID, so the daemon can tell which agent is asking.
const (
	// ReqAgentKnowledgeSearch searches the shared notes for Text.
	ReqAgentKnowledgeSearch RequestType = "agent_knowledge_search"
	// ReqAgentKnowledgeRead reads the shared note named Text.
	ReqAgentKnowledgeRead RequestType = "agent_knowledge_read"
	// ReqAgentKeys lists the keys an agent can ask for.
	ReqAgentKeys RequestType = "agent_keys"
	// ReqAgentRequestKey asks for the key named Text, for the reason in
	// AnswerText, to be put in the agent's .env.
	ReqAgentRequestKey RequestType = "agent_request_key"
	// ReqAgentTell is an agent telling Agentman something, in Text, and
	// asking for the user when Approve is set.
	ReqAgentTell RequestType = "agent_tell"
)

const (
	// EvtKnowledge carries Notes, or one Note.
	EvtKnowledge EventType = "knowledge"
	// EvtKeys carries Keys.
	EvtKeys EventType = "keys"
	// EvtAgentReply answers an agent_* request, in Preview, for the agent to
	// read.
	EvtAgentReply EventType = "agent_reply"
)

// Note is one of the user's notes. Body is empty in a list.
type Note struct {
	Name      string `json:"name"`
	Tier      string `json:"tier"`
	Title     string `json:"title"`
	Always    bool   `json:"always,omitempty"`
	Body      string `json:"body,omitempty"`
	UpdatedAt int64  `json:"updatedAt"`
}

// Key rules: when Agentman may hand a key out without asking.
const (
	// KeyAuto: to any agent that needs it.
	KeyAuto = "auto"
	// KeyProjects: to agents in the listed folders; others ask.
	KeyProjects = "projects"
	// KeyAsk: always ask the user first.
	KeyAsk = "ask"
)

// Key is an API key Agentman keeps, without its value.
type Key struct {
	// Name is the environment variable it becomes: ELEVENLABS_API_KEY.
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Rule        string   `json:"rule"`
	Projects    []string `json:"projects,omitempty"`
	// Set reports whether a value is stored. Read only.
	Set       bool  `json:"set"`
	UpdatedAt int64 `json:"updatedAt"`
}
