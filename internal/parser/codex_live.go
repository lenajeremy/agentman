package parser

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// CodexLive turns what Codex's shared daemon reports while a turn runs into
// provisional rows: the reply as it is written, and a command, an edit or an
// MCP call from the moment it starts. The rollout says none of this until an
// item is finished, so without it a reply arrived whole and a long command
// showed nothing until it ended.
//
// A provisional row carries the id of the rollout item it stands for (the
// daemon and the rollout name items alike: "msg_…" for a reply, "exec-…" for
// a command), so the finished row the rollout parser makes replaces it in
// place, and the thread ends up exactly as the rollout alone would make it.
// Once the rollout has delivered an item (Seen), nothing here speaks for it
// again, so a late preview can never overwrite the finished row.
type CodexLive struct {
	sessionID string
	rows      map[string]*codexLiveRow
	// order is the ids in the order they started, so a flush keeps it.
	order []string
	// settled are items the rollout has delivered.
	settled map[string]bool
}

type codexLiveRow struct {
	message protocol.Message
	// text is the reply so far, or a command's output so far.
	text  strings.Builder
	kind  string
	dirty bool
}

// NewCodexLive starts the live rows of one session.
func NewCodexLive(sessionID string) *CodexLive {
	return &CodexLive{sessionID: sessionID, rows: map[string]*codexLiveRow{}, settled: map[string]bool{}}
}

// codexLiveItem is the daemon's ThreadItem, as much of it as a row needs.
type codexLiveItem struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	Text string `json:"text"`
	// commandExecution
	Command        string `json:"command"`
	CommandActions []struct {
		Command string `json:"command"`
	} `json:"commandActions"`
	AggregatedOutput *string `json:"aggregatedOutput"`
	ExitCode         *int    `json:"exitCode"`
	Status           string  `json:"status"`
	// fileChange
	Changes []struct {
		Path string `json:"path"`
	} `json:"changes"`
	// mcpToolCall
	Server    string          `json:"server"`
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
}

// Started records an item the daemon has started. at is when, in
// milliseconds.
func (l *CodexLive) Started(raw json.RawMessage, at int64) {
	var item codexLiveItem
	if json.Unmarshal(raw, &item) != nil || item.ID == "" || l.settled[item.ID] {
		return
	}
	switch item.Type {
	case "agentMessage", "commandExecution", "fileChange", "mcpToolCall":
	default:
		// Reasoning is dropped, as the rollout parser drops it; anything else
		// waits for the rollout.
		return
	}
	row := l.row(item.ID, item.Type, at)
	switch item.Type {
	case "agentMessage":
		// Shown from its first words; see Flush.
		row.message.Role = protocol.RoleAssistant
	case "commandExecution":
		row.message.Role = protocol.RoleTool
		row.message.Tool = &protocol.Tool{Name: "Shell", Summary: ClipBlock(item.commandLine(), CommandLines, CommandChars), Status: protocol.ToolRunning}
	case "fileChange":
		paths := make([]string, 0, len(item.Changes))
		for _, change := range item.Changes {
			paths = append(paths, change.Path)
		}
		sort.Strings(paths)
		row.message.Role = protocol.RoleTool
		row.message.Tool = &protocol.Tool{Name: "Edit", Summary: clip(codexEditSummary(paths), SummaryChars), Status: protocol.ToolRunning}
	case "mcpToolCall":
		name := item.mcpName()
		args, own := codexArguments(item.Arguments)
		summary := summarizeToolInput(name, args)
		title := ""
		if own != "" {
			title = DescribeTool(name, summary, own)
		}
		row.message.Role = protocol.RoleTool
		row.message.Tool = &protocol.Tool{Name: name, Summary: summary, Status: protocol.ToolRunning, Title: title}
	}
}

// Delta adds to a reply, or to a command's output.
func (l *CodexLive) Delta(itemID, delta string, at int64) {
	if itemID == "" || delta == "" || l.settled[itemID] {
		return
	}
	row, ok := l.rows[itemID]
	if !ok {
		// A reply whose start was missed (the session was joined mid-item).
		row = l.row(itemID, "agentMessage", at)
		row.message.Role = protocol.RoleAssistant
	}
	row.text.WriteString(delta)
	row.dirty = true
}

// Completed records an item the daemon has finished. A reply's words are
// final now; a tool's outcome is known. The rollout's row follows and
// replaces this one, but a rollout that is late, or missing, does not leave
// the row half-written or running.
func (l *CodexLive) Completed(raw json.RawMessage) {
	var item codexLiveItem
	if json.Unmarshal(raw, &item) != nil || item.ID == "" || l.settled[item.ID] {
		return
	}
	row, ok := l.rows[item.ID]
	if !ok || row.kind != item.Type {
		return
	}
	switch item.Type {
	case "agentMessage":
		if item.Text != "" {
			row.text.Reset()
			row.text.WriteString(item.Text)
		}
	case "commandExecution":
		if item.AggregatedOutput != nil {
			row.text.Reset()
			row.text.WriteString(*item.AggregatedOutput)
		}
		status := protocol.ToolOK
		if item.Status == "failed" || item.Status == "declined" || item.ExitCode != nil && *item.ExitCode != 0 {
			status = protocol.ToolError
		}
		row.message.Tool.Status = status
	case "fileChange", "mcpToolCall":
		status := protocol.ToolOK
		if item.Status == "failed" || item.Status == "declined" {
			status = protocol.ToolError
		}
		row.message.Tool.Status = status
	}
	row.dirty = true
}

// Seen records items the rollout has delivered: their finished rows are the
// rollout's from now on.
func (l *CodexLive) Seen(messages []protocol.Message) {
	for _, message := range messages {
		l.settled[message.ID] = true
		if _, ok := l.rows[message.ID]; ok {
			delete(l.rows, message.ID)
			for i, id := range l.order {
				if id == message.ID {
					l.order = append(l.order[:i], l.order[i+1:]...)
					break
				}
			}
		}
	}
}

// Flush returns every row that changed since the last flush, in the order
// their items started.
func (l *CodexLive) Flush() []protocol.Message {
	var out []protocol.Message
	for _, id := range l.order {
		row := l.rows[id]
		if row == nil || !row.dirty {
			continue
		}
		row.dirty = false
		message := row.message
		switch row.kind {
		case "agentMessage":
			message.Text = codexCitations(row.text.String())
			if message.Text == "" {
				continue
			}
		case "commandExecution":
			message.Text = ClipBlock(row.text.String(), PreviewLines, PreviewChars)
		}
		if message.Tool != nil {
			tool := *message.Tool
			message.Tool = &tool
		}
		out = append(out, message)
	}
	return out
}

func (l *CodexLive) row(id, kind string, at int64) *codexLiveRow {
	if row, ok := l.rows[id]; ok {
		return row
	}
	row := &codexLiveRow{
		message: protocol.Message{ID: id, SessionID: l.sessionID, Ts: at},
		kind:    kind,
		dirty:   true,
	}
	l.rows[id] = row
	l.order = append(l.order, id)
	return row
}

// commandLine is the command as Codex itself shows it: the readable action
// when it parsed one, otherwise what the shell runs.
func (item codexLiveItem) commandLine() string {
	if len(item.CommandActions) > 0 && item.CommandActions[0].Command != "" {
		return item.CommandActions[0].Command
	}
	return item.Command
}

func (item codexLiveItem) mcpName() string {
	server, tool := item.Server, item.Tool
	if server == "" {
		server = "mcp"
	}
	if tool == "" {
		tool = "call"
	}
	return server + "." + tool
}
