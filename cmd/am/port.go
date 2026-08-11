package main

import (
	"fmt"
	"net"
	"time"
)

// portBusy reports whether anything is listening on a local TCP port.
//
// Used only to tell two failures apart: a port held by a *different* program
// needs a different message from one that is simply free, and "address already
// in use" from a TUI that has already cleared the screen is not something the
// user will ever see.
func portBusy(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 500*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
