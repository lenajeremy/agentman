// Package procinfo reads the process table from the kernel itself.
//
// Discovery used to ask `ps` for the process table every second. ps is a
// process of its own, setuid root on macOS, and each run cost more CPU than
// the rest of a discovery sweep put together. The same answer is one sysctl
// away, and a process's arguments one more.
//
// Only macOS is read directly. Elsewhere Supported is false and the callers
// keep asking ps.
package procinfo

import "errors"

// ErrUnsupported is what every reader returns where Supported is false.
var ErrUnsupported = errors.New("procinfo: not supported on this platform")

// Process is one entry of the process table.
type Process struct {
	PID  int
	PPID int
	// UID is the user the process runs as. Only the user's own processes
	// can be asked for their arguments.
	UID int
	// Started is when the process began, in microseconds. A pid is reused
	// once its process ends; the pid and its start time together name one
	// process for as long as it lives.
	Started int64
	// Comm is the kernel's short name for the executable, cut to 16 bytes.
	Comm string
	// Zombie is a process that has exited and not yet been reaped.
	Zombie bool
}
