// Package tunnel shares one local port through the relay as a public link.
//
// `am expose 3000` dials the relay's /ws/tunnel endpoint and keeps that one
// websocket open. The relay answers with a hello naming the link, then both
// sides run yamux over the socket: every browser connection to the link becomes
// a new yamux stream, and this package pipes each stream to localhost:3000.
//
// The relay writes ordinary HTTP/1.1 into each stream, so this side never
// parses HTTP at all. It is a byte pipe, which is why keep-alive, chunked
// bodies, server-sent events and websocket upgrades (dev-server hot reload) all
// work without any code here knowing they exist.
//
// Nothing the relay sends can choose the destination. The port is fixed when
// the client is created and the dial is always to loopback, so a hostile or
// compromised relay can reach the one port the user shared and nothing else on
// their machine or network.
package tunnel

import (
	"io"
	"time"

	"github.com/hashicorp/yamux"
)

const (
	// Path is the relay endpoint a tunnel client dials.
	Path = "/ws/tunnel"
	// NonceHeader carries a random value the client picks once per process.
	// The relay derives the link from it and the account, so a client that
	// reconnects after a network blip gets the same link back without the
	// relay having to remember anything.
	NonceHeader = "X-Agentman-Tunnel-Nonce"
	// PortHeader tells the relay which local port is being shared. The relay
	// uses it only to present a matching Host header and to rewrite redirects
	// that point at localhost; it cannot make the client dial anything else.
	PortHeader = "X-Agentman-Tunnel-Port"

	// maxHelloBytes bounds the only message this side reads as a whole.
	maxHelloBytes = 16 << 10
)

// Hello is the first and only websocket message the relay sends as text.
// Everything after it is binary yamux framing.
type Hello struct {
	// ID is the link's subdomain label.
	ID string `json:"id"`
	// URL is the full public address to hand to whoever should see the app.
	URL string `json:"url"`
}

// MuxConfig is the yamux configuration both ends use.
//
// yamux handles the parts of multiplexing that are easy to get subtly wrong:
// per-stream flow control, so one large download cannot starve a small request
// sharing the socket, and keepalive pings, so an idle tunnel survives NAT and
// load-balancer timeouts.
func MuxConfig() *yamux.Config {
	cfg := yamux.DefaultConfig()
	// Library chatter would interleave with the CLI's own output.
	cfg.LogOutput = io.Discard
	// A stream the other side never acknowledges should fail as a clear error
	// rather than hang a browser request for the default 75 seconds.
	cfg.StreamOpenTimeout = 30 * time.Second
	return cfg
}
