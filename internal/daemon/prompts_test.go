package daemon

import (
	"context"
	"fmt"
	"strconv"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/source"
)

// longSource is a conversation read back a page at a time, as a
// transcript is: a cursor is where the page before it ends.
type longSource struct {
	streamingSource
	messages []protocol.Message
	pages    int
}

func (s *longSource) Page(_ context.Context, _ string, before string, limit int) (protocol.Page, error) {
	s.pages++
	end := len(s.messages)
	if before != "" {
		end, _ = strconv.Atoi(before)
	}
	start := max(0, end-limit)
	cursor := ""
	if start > 0 {
		cursor = strconv.Itoa(start)
	}
	return protocol.NewPage("claude:s1", s.messages[start:end], cursor, start > 0), nil
}

func fetchPrompts(t *testing.T, agent *Daemon, before string, limit int) protocol.Page {
	t.Helper()
	event := agent.Handle(context.Background(), protocol.Request{Type: protocol.ReqFetchPrompts, SessionID: "claude:s1", Before: before, Limit: limit})
	if event.Type != protocol.EvtPage || event.Page == nil {
		t.Fatalf("event = %+v", event)
	}
	return *event.Page
}

// ↑ in a message box goes back through what the user said, a page at a
// time: only their own messages, each once, in order, however far back.
func TestPromptsPageBackThroughWhatTheUserSaid(t *testing.T) {
	conversation := &longSource{}
	var want []string
	for i := range 600 {
		message := protocol.Message{ID: fmt.Sprint(i), SessionID: "claude:s1", Role: protocol.RoleAssistant, Text: "an answer"}
		switch {
		case i%10 == 0:
			message.Role, message.Text, message.Images = protocol.RoleUser, fmt.Sprintf("prompt %d", i), []string{"/tmp/shot.png"}
			want = append(want, message.Text)
		case i%10 == 3:
			message.Role, message.Text, message.Tool = protocol.RoleUser, "a tool's result", &protocol.Tool{Name: "Bash"}
		case i%10 == 5:
			message.Role, message.Text, message.IsSidechain = protocol.RoleUser, "a subagent's prompt", true
		case i%10 == 7:
			message.Role, message.Text = protocol.RoleUser, "  \n"
		}
		conversation.messages = append(conversation.messages, message)
	}
	registry := source.NewRegistry()
	registry.Add(conversation)
	agent := New(registry, &recordingSink{})
	agent.refresh(context.Background(), true)

	var got []string // newest first, as ↑ meets them
	cursor, fetches := "", 0
	for {
		conversation.pages = 0
		page := fetchPrompts(t, agent, cursor, 5)
		fetches++
		if conversation.pages > promptScanPages {
			t.Fatalf("one fetch read %d pages", conversation.pages)
		}
		for i := len(page.Messages) - 1; i >= 0; i-- {
			message := page.Messages[i]
			if message.Role != protocol.RoleUser || message.Tool != nil || len(message.Images) != 0 {
				t.Fatalf("not a bare prompt: %+v", message)
			}
			got = append(got, message.Text)
		}
		if !page.HasMore {
			break
		}
		if page.NextCursor == "" || fetches > 100 {
			t.Fatalf("page %d: cursor %q", fetches, page.NextCursor)
		}
		cursor = page.NextCursor
	}
	if len(got) != len(want) {
		t.Fatalf("%d prompts, want %d", len(got), len(want))
	}
	for i, text := range got {
		if text != want[len(want)-1-i] {
			t.Fatalf("prompt %d is %q, want %q", i, text, want[len(want)-1-i])
		}
	}
	if fetches < 2 {
		t.Errorf("%d fetches: pages weren't asked for a few prompts at a time", fetches)
	}
}

// A long stretch with nothing the user said is read only so far in one
// fetch; the cursor carries on from there.
func TestAFetchOfPromptsReadsOnlySoFar(t *testing.T) {
	conversation := &longSource{}
	for i := range 3000 {
		conversation.messages = append(conversation.messages, protocol.Message{ID: fmt.Sprint(i), SessionID: "claude:s1", Role: protocol.RoleAssistant, Text: "working"})
	}
	conversation.messages[0] = protocol.Message{ID: "0", SessionID: "claude:s1", Role: protocol.RoleUser, Text: "the first prompt"}
	registry := source.NewRegistry()
	registry.Add(conversation)
	agent := New(registry, &recordingSink{})
	agent.refresh(context.Background(), true)

	page := fetchPrompts(t, agent, "", 0)
	if conversation.pages != promptScanPages || len(page.Messages) != 0 || !page.HasMore || page.NextCursor == "" {
		t.Fatalf("read %d pages, got %d prompts, more %v, cursor %q", conversation.pages, len(page.Messages), page.HasMore, page.NextCursor)
	}
	for page.HasMore {
		page = fetchPrompts(t, agent, page.NextCursor, 0)
		if len(page.Messages) > 0 {
			break
		}
	}
	if len(page.Messages) != 1 || page.Messages[0].Text != "the first prompt" || page.HasMore {
		t.Fatalf("the end: %+v", page)
	}
	if event := agent.Handle(context.Background(), protocol.Request{Type: protocol.ReqFetchPrompts}); event.Type != protocol.EvtError {
		t.Fatalf("no session: %+v", event)
	}
}
