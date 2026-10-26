package protocol

// Agents run on a tmux server am serve starts and owns, so they outlive the
// terminals that show them. `am claude` asks for that server before it starts
// an agent, and Agentman for Mac and `am attach` ask how to show an agent's
// terminal again. Both go over the local socket only: they are about this
// Mac's own processes.

const (
	// ReqAgentsTmux asks am serve to make sure its tmux server is running,
	// answered with an EvtAgentsTmux carrying AgentsTmux.
	ReqAgentsTmux RequestType = "agents_tmux"
	EvtAgentsTmux EventType   = "agents_tmux"

	// ReqSessionTerminal asks how to show a session's terminal (SessionID),
	// answered with an EvtSessionTerminal carrying Terminal.
	ReqSessionTerminal RequestType = "session_terminal"
	EvtSessionTerminal EventType   = "session_terminal"
)

// AgentsTmux is the tmux server agents run on.
type AgentsTmux struct {
	// Socket is the server's socket, for `tmux -S`.
	Socket string `json:"socket"`
	PID    int    `json:"pid,omitempty"`
	// Service is whether am serve runs as a login service, owned by the
	// system rather than by a terminal: only then do agents outlive every
	// terminal app.
	Service bool `json:"service"`
}

// SessionTerminal is how to show a session's terminal.
type SessionTerminal struct {
	// Name is the tmux session the agent runs in.
	Name string `json:"name"`
	// Argv is the command that attaches a terminal to it.
	Argv []string `json:"argv"`
}
