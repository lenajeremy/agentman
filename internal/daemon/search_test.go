package daemon

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/search"
)

// talkingSource is a running Claude whose conversation has a message and a
// tool's output that both say "notarize".
type talkingSource struct{ streamingSource }

func (s *talkingSource) Page(context.Context, string, string, int) (protocol.Page, error) {
	return protocol.NewPage("claude:s1", []protocol.Message{
		{ID: "m1", SessionID: "claude:s1", Role: protocol.RoleUser, Ts: 1000, Text: "Please notarize the build"},
		{ID: "m2", SessionID: "claude:s1", Role: protocol.RoleTool, Ts: 2000, Text: "notarize", Tool: &protocol.Tool{Name: "Bash"}},
	}, "", false), nil
}

// A search reads what running agents said up to their newest message, and
// names the conversation as it runs, so opening the hit opens the session.
func TestSearchFindsWhatRunningAgentsSaid(t *testing.T) {
	ctx := context.Background()
	d, _ := agentWithSource(t, &talkingSource{})

	starting := d.Handle(ctx, protocol.Request{Type: protocol.ReqSearch, Text: "notarize"})
	if starting.Type != protocol.EvtError || !strings.Contains(starting.Error, "starting") {
		t.Fatalf("before the index is open: %+v", starting)
	}
	if empty := d.Handle(ctx, protocol.Request{Type: protocol.ReqSearch}); empty.Type != protocol.EvtError {
		t.Fatalf("an empty search: %+v", empty)
	}

	d.refresh(ctx, true)
	index, err := search.Open(filepath.Join(t.TempDir(), "search.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()
	d.search = search.NewIndexer(index, searchReader{d}, nil)

	found := d.Handle(ctx, protocol.Request{Type: protocol.ReqSearch, Text: "notarize"})
	if found.Type != protocol.EvtSearch || found.Search == nil || len(found.Search.Hits) != 1 {
		t.Fatalf("search: %+v", found)
	}
	hit := found.Search.Hits[0]
	if hit.MessageID != "m1" || hit.Role != protocol.RoleUser || hit.Snippet != "Please notarize the build" {
		t.Fatalf("hit %+v", hit)
	}
	if hit.Session == nil || hit.Session.ID != "claude:s1" || hit.Session.State == protocol.StateEnded {
		t.Fatalf("the running session: %+v", hit.Session)
	}

	inOne := d.Handle(ctx, protocol.Request{Type: protocol.ReqSearch, Text: "notarize", SessionID: "claude:s1"})
	if inOne.Search == nil || len(inOne.Search.Hits) != 1 || inOne.Search.Hits[0].Session != nil ||
		inOne.Search.SessionID != "claude:s1" {
		t.Fatalf("in one conversation: %+v", inOne.Search)
	}
}
