package main

import (
	"context"
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/lsp"
	"github.com/lenajeremy/agentman/internal/protocol"
)

func TestOnlyTheLocalSocketHandsOutTheLanguageServers(t *testing.T) {
	languages := lsp.NewHost(lsp.Options{})
	defer languages.Close()
	var passed []protocol.RequestType
	daemon := func(_ context.Context, _ string, req protocol.Request) protocol.Event {
		passed = append(passed, req.Type)
		return protocol.Event{Type: protocol.EvtError, Error: "unknown request"}
	}
	local := appRequests(daemon, languages)

	reply := local(context.Background(), "local:1", protocol.Request{Type: protocol.ReqLSPEndpoint})
	if reply.Type != protocol.EvtLSPEndpoint || reply.LSP == nil || reply.LSP.Token == "" ||
		!strings.HasPrefix(reply.LSP.URL, "ws://127.0.0.1:") {
		t.Fatalf("local socket got %+v", reply)
	}
	again := local(context.Background(), "local:2", protocol.Request{Type: protocol.ReqLSPEndpoint})
	if *again.LSP != *reply.LSP {
		t.Error("the endpoint should stay the same for the daemon's life")
	}
	local(context.Background(), "local:1", protocol.Request{Type: protocol.ReqListSessions})
	if len(passed) != 1 || passed[0] != protocol.ReqListSessions {
		t.Errorf("passed to the daemon: %v", passed)
	}
	// The relay's requests go straight to the daemon, which doesn't know it.
	if relayed := daemon(context.Background(), "phone", protocol.Request{Type: protocol.ReqLSPEndpoint}); relayed.LSP != nil {
		t.Error("the relay's path handed out the endpoint")
	}
}
