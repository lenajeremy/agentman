// Package procinfo reads the process table, and what a process holds open,
// from the kernel itself.
//
// Discovery used to ask `ps` for the process table every second, and `lsof`
// for listening ports every three. Each is a process of its own — ps is
// setuid root on macOS, which makes it dearer still — and between them they
// cost several times the daemon's own CPU. The same answers are a handful of
// system calls away: sysctl for the process table and a process's
// arguments, proc_info for its sockets and working directory.
//
// Only macOS is read directly. Elsewhere Supported is false and the callers
// keep asking ps; Linux finds listening ports through /proc already.
package procinfo

import "errors"

// ErrUnsupported is what every reader returns where Supported is false.
var ErrUnsupported = errors.New("procinfo: not supported on this platform")

// Process is one entry of the process table.
type Process struct {
	PID  int
	PPID int
	// UID is the user the process runs as. Only the user's own processes
	// can be asked for their arguments or sockets.
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
