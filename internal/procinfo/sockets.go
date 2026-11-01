package procinfo

import "net/netip"

// Listener is a TCP socket listening for connections.
type Listener struct {
	Port int
	// Addr is the address the socket is bound to, unspecified for one bound
	// to every interface.
	Addr netip.Addr
}
