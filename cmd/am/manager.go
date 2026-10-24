package main

import (
	"context"

	"github.com/lenajeremy/agentman/internal/daemon"
	"github.com/lenajeremy/agentman/internal/protocol"
)

// managerAgents is the daemon as Agentman sees it: the agents it watches,
// and its request handler, which Agentman reads agents through under its own
// subscriber ID, exactly as an app would.
type managerAgents struct {
	daemon *daemon.Daemon
}

func (a managerAgents) Sessions() []protocol.Session {
	return a.daemon.Sessions()
}

func (a managerAgents) Request(ctx context.Context, req protocol.Request) protocol.Event {
	return a.daemon.HandleFrom(ctx, "agentman", req)
}
