package protocol

// Slash commands: the "/" menu of an agent's message box. The daemon lists
// what the agent itself would offer, its own commands and the user's skills,
// and a command is sent like any message: typed into the agent's prompt,
// where the agent runs it. What it prints comes back in the transcript.
//
// A few commands answer in a panel of the agent's terminal instead, which
// stays open until dismissed and is never written to the transcript. Those
// are run with ReqCommandPanel: the daemon types the command, reads the
// panel, closes it, and answers with what it said.

const (
	// ReqListCommands lists a session's commands, answered with an
	// EvtCommands.
	ReqListCommands RequestType = "list_commands"
	EvtCommands     EventType   = "commands"

	// ReqCommandPanel runs the command in Text, one marked Panel, and is
	// answered with an EvtCommandPanel.
	ReqCommandPanel RequestType = "command_panel"
	EvtCommandPanel EventType   = "command_panel"
)

// Command is one entry of the "/" menu.
type Command struct {
	// Name is what follows the "/": "compact", "vercel:deploy".
	Name string `json:"name"`
	// Prefix is what goes before Name when it is typed, "/" when empty.
	// Codex's skills take "$": "$imagegen".
	Prefix string `json:"prefix,omitempty"`
	// Description says what it does, in one line.
	Description string `json:"description,omitempty"`
	// ArgumentHint shows what it takes, as the agent's own menu does:
	// "[instructions]", "<model>".
	ArgumentHint string `json:"argumentHint,omitempty"`
	// Source is where it comes from: "builtin", "bundled", "personal",
	// "project", "plugin", or "managed".
	Source string `json:"source,omitempty"`
	// Panel marks a command that, sent without arguments, answers in a panel
	// rather than the conversation: run it with ReqCommandPanel.
	Panel bool `json:"panel,omitempty"`
}

// CommandPanel is what a panel command showed.
type CommandPanel struct {
	// Command is what was run, "/usage".
	Command string `json:"command"`
	// Text is the panel as it read, without the terminal's colours.
	Text string `json:"text"`
}
