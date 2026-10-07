package daemon

import (
	"context"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/source"
)

// A phone learns which agentman the Mac runs, and what is newer, from the
// session list it asks for on connecting, and again whenever a check finds
// a new release while it is connected.
func TestTheSessionListSaysWhichAgentmanThisIs(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	sink := &recordingSink{}
	agent := New(source.NewRegistry(), sink)
	ctx := context.Background()

	if list := agent.HandleFrom(ctx, "phone", protocol.Request{Type: protocol.ReqListSessions}); list.Daemon != nil {
		t.Fatalf("a daemon that was told nothing about itself sent %+v", list.Daemon)
	}

	info := &protocol.DaemonInfo{Version: "0.14.1", Latest: "0.15.0", Behind: 2}
	agent.SetDaemonInfo(func() *protocol.DaemonInfo { return info })
	list := agent.HandleFrom(ctx, "phone", protocol.Request{Type: protocol.ReqListSessions})
	if list.Daemon == nil || list.Daemon.Behind != 2 || list.Daemon.Latest != "0.15.0" {
		t.Fatalf("the session list carried %+v", list.Daemon)
	}

	agent.AnnounceDaemonInfo()
	if len(sink.events) != 1 || sink.events[0].Type != protocol.EvtSessions || sink.events[0].Daemon != info {
		t.Fatalf("announced %+v, want the session list with the daemon's details", sink.events)
	}
}
