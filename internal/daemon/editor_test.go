package daemon

import (
	"context"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/source"
)

// The phone's requests reach the daemon through the relay; the language
// servers' endpoint is answered only on the Mac's local socket, before the
// daemon (cmd/am/editor.go), so the daemon itself must refuse it.
func TestTheDaemonDoesNotHandOutTheLanguageServers(t *testing.T) {
	agent := New(source.NewRegistry(), &recordingSink{})
	reply := agent.HandleFrom(context.Background(), "phone", protocol.Request{Type: protocol.ReqLSPEndpoint})
	if reply.LSP != nil || reply.Type == protocol.EvtLSPEndpoint {
		t.Fatalf("daemon answered %+v", reply)
	}
}
