package daemon

import (
	"context"
	"slices"
	"strings"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// What one fetch_prompts reads. The conversation is read a page at a time,
// newest first, and each page is let go once its prompts are taken: only
// the prompts are kept, so a long conversation never sits in memory whole.
// A page of prompts is about defaultPrompts of them; a stretch of the
// conversation with few is read no further than promptScanPages pages, and
// the cursor says where the next fetch carries on.
const (
	defaultPrompts  = 20
	maxPrompts      = 50
	promptScanPages = 25
)

// prompts is a page of what the user said to a session, oldest first, from
// before (newest when empty), for ↑ in a message box.
func (d *Daemon) prompts(ctx context.Context, sessionID, before string, limit int) (protocol.Page, error) {
	if limit <= 0 || limit > maxPrompts {
		limit = defaultPrompts
	}
	var found []protocol.Message // newest first
	cursor := before
	for range promptScanPages {
		page, err := d.registry.Page(ctx, sessionID, cursor, maxPageMessages)
		if err != nil {
			return protocol.Page{}, err
		}
		for _, message := range slices.Backward(page.Messages) {
			if isPrompt(message) {
				found = append(found, protocol.Message{
					ID: message.ID, SessionID: message.SessionID, Role: message.Role, Ts: message.Ts, Text: message.Text,
				})
			}
		}
		if !page.HasMore || page.NextCursor == "" || page.NextCursor == cursor {
			slices.Reverse(found)
			return protocol.NewPage(sessionID, found, "", false), nil
		}
		cursor = page.NextCursor
		if len(found) >= limit || ctx.Err() != nil {
			break
		}
	}
	slices.Reverse(found)
	return protocol.NewPage(sessionID, found, cursor, true), nil
}

// isPrompt is a message the user wrote to the agent: theirs, with words,
// not a tool's result, a subagent's, or what the agent writes in the user's
// place: the summary of an earlier conversation, or Claude Code's "[Request
// interrupted by user]". As the Mac's PromptHistory.isPrompt.
func isPrompt(message protocol.Message) bool {
	text := strings.TrimSpace(message.Text)
	return message.Role == protocol.RoleUser && message.Tool == nil && !message.IsSidechain && text != "" &&
		!isCompactionSummary(text) && !strings.HasPrefix(text, "[Request interrupted by user")
}

// compactionOpening begins the summary Claude Code writes when it compacts
// a conversation; newer versions put a note or two before it, opening with
// a tag such as <artifact-content-authored-by-others/>.
const compactionOpening = "This session is being continued from a previous conversation"

func isCompactionSummary(text string) bool {
	if strings.HasPrefix(text, compactionOpening) {
		return true
	}
	head := text[:min(len(text), 2000)]
	return strings.HasPrefix(text, "<") && strings.Contains(head, "\n"+compactionOpening)
}
