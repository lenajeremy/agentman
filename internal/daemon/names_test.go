package daemon

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/source"
)

func namedDaemon(t *testing.T, path string) (*Daemon, *recordingSink) {
	t.Helper()
	registry := source.NewRegistry()
	registry.Add(&bulkDiscoverySource{sessions: []protocol.Session{{ID: "opencode:s1", Kind: protocol.KindOpenCode, Name: "Awaiting next instruction"}}})
	sink := &recordingSink{}
	agent := New(registry, sink)
	agent.SetNamesFile(path)
	agent.refresh(context.Background(), true)
	return agent, sink
}

// A rename shows at once, keeps the agent's own name and ID, and lasts
// through the next sweep and a restart.
func TestARenameShowsEverywhereAndLasts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "names.json")
	agent, _ := namedDaemon(t, path)

	reply := agent.Handle(context.Background(), protocol.Request{Type: protocol.ReqRenameSession, SessionID: "opencode:s1", Text: "  Hacking   course\n"})
	if reply.Type != protocol.EvtSessionUpdate || reply.Session.Name != "Hacking course" ||
		reply.Session.OriginalName != "Awaiting next instruction" || reply.Session.ID != "opencode:s1" {
		t.Fatalf("reply = %+v", reply.Session)
	}
	agent.refresh(context.Background(), false)
	if got := agent.Sessions()[0]; got.Name != "Hacking course" {
		t.Fatalf("after a sweep: %+v", got)
	}

	restarted, _ := namedDaemon(t, path)
	if got := restarted.Sessions()[0]; got.Name != "Hacking course" || got.ID != "opencode:s1" {
		t.Fatalf("after a restart: %+v", got)
	}

	// An empty name, or the agent's own, gives the session its name back.
	reset := restarted.Handle(context.Background(), protocol.Request{Type: protocol.ReqRenameSession, SessionID: "opencode:s1", Text: ""})
	if reset.Session.Name != "Awaiting next instruction" || reset.Session.OriginalName != "" {
		t.Fatalf("reset = %+v", reset.Session)
	}
	restarted.Handle(context.Background(), protocol.Request{Type: protocol.ReqRenameSession, SessionID: "opencode:s1", Text: "Course"})
	own := restarted.Handle(context.Background(), protocol.Request{Type: protocol.ReqRenameSession, SessionID: "opencode:s1", Text: "Awaiting next instruction"})
	if own.Session.Name != "Awaiting next instruction" || own.Session.OriginalName != "" {
		t.Fatalf("renamed to its own name = %+v", own.Session)
	}
}

// Every app hears a rename when it happens, not at the next sweep.
func TestARenameReachesEveryApp(t *testing.T) {
	agent, sink := namedDaemon(t, filepath.Join(t.TempDir(), "names.json"))
	agent.Handle(context.Background(), protocol.Request{Type: protocol.ReqRenameSession, SessionID: "opencode:s1", Text: "Course"})
	var heard bool
	sink.mu.Lock()
	events := append([]protocol.Event(nil), sink.events...)
	sink.mu.Unlock()
	for _, event := range events {
		heard = heard || (event.Type == protocol.EvtSessionUpdate && event.Session != nil && event.Session.Name == "Course")
	}
	if !heard {
		t.Fatal("the rename wasn't sent to the apps")
	}
}

func TestBadNamesAreRefused(t *testing.T) {
	agent, _ := namedDaemon(t, "")
	for _, bad := range []protocol.Request{
		{Type: protocol.ReqRenameSession, SessionID: "opencode:s1", Text: "bell\x07"},
		{Type: protocol.ReqRenameSession, SessionID: "opencode:s1", Text: strings.Repeat("x", 81)},
		{Type: protocol.ReqRenameSession, SessionID: "opencode:gone", Text: "Ghost"},
		{Type: protocol.ReqRenameSession, Text: "No session"},
	} {
		if event := agent.Handle(context.Background(), bad); event.Type != protocol.EvtError {
			t.Errorf("%+v was accepted: %+v", bad, event)
		}
	}
}
