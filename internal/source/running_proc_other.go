//go:build !linux

package source

import "github.com/lenajeremy/agentman/internal/procinfo"

// The processes, and the files one holds open, for heldOpen: read from the
// kernel on a Mac; elsewhere (not Linux) unsupported, and the pid decides.
var (
	listProcesses = procinfo.List
	openFiles     = procinfo.Files
)
