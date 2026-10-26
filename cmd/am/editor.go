package main

import (
	"context"

	"github.com/lenajeremy/agentman/internal/lsp"
	"github.com/lenajeremy/agentman/internal/protocol"
)

// appRequests answers what only an app on this Mac may ask, then hands the
// rest to the daemon, as the relay does. The phone's requests never come
// this way, so the language servers' endpoint and token stay on the Mac.
func appRequests(handle func(context.Context, string, protocol.Request) protocol.Event, languages *lsp.Host) func(context.Context, string, protocol.Request) protocol.Event {
	return func(ctx context.Context, from string, req protocol.Request) protocol.Event {
		if req.Type != protocol.ReqLSPEndpoint {
			return handle(ctx, from, req)
		}
		url, token, err := languages.Endpoint()
		if err != nil {
			return protocol.Event{Type: protocol.EvtError, Error: err.Error()}
		}
		return protocol.Event{Type: protocol.EvtLSPEndpoint, LSP: &protocol.LSPEndpoint{URL: url, Token: token}}
	}
}
