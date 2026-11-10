//go:build linux

package source

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lenajeremy/agentman/internal/procinfo"
)

// The processes, and the files one holds open, for heldOpen, read from
// /proc. Only for this check: procinfo leaves Linux to ps elsewhere, and
// that stays as it is.
var (
	listProcesses = procProcesses
	openFiles     = procFiles
)

// procProcesses is every process /proc shows: its pid, name and whether it
// is a zombie. One that ends while it is read is left out.
func procProcesses() ([]procinfo.Process, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	var out []procinfo.Process
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}
		comm, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "comm"))
		if err != nil {
			continue
		}
		process := procinfo.Process{PID: pid, Comm: strings.TrimSpace(string(comm))}
		// stat is "pid (comm) state ...": the state follows the last ")".
		if stat, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "stat")); err == nil {
			if at := strings.LastIndexByte(string(stat), ')'); at >= 0 && at+2 < len(stat) {
				process.Zombie = stat[at+2] == 'Z'
			}
		}
		out = append(out, process)
	}
	return out, nil
}

// procFiles is the files a process holds open: where each descriptor in
// /proc/<pid>/fd points. Only the user's own processes can be read.
func procFiles(pid int) ([]procinfo.OpenFile, error) {
	dir := filepath.Join("/proc", strconv.Itoa(pid), "fd")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]procinfo.OpenFile, 0, len(entries))
	for _, entry := range entries {
		fd, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		target, err := os.Readlink(filepath.Join(dir, entry.Name()))
		if err != nil || !strings.HasPrefix(target, "/") {
			continue // a socket or pipe, or gone
		}
		out = append(out, procinfo.OpenFile{FD: fd, Path: target})
	}
	return out, nil
}
