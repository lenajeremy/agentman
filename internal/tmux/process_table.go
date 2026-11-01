package tmux

import (
	"context"
	"sync"
	"time"

	"github.com/lenajeremy/agentman/internal/procinfo"
)

// Discovery used to read the process table with `ps -axo pid=,ppid=,comm=`
// once a second, and the server scan again every three. On macOS ps is setuid
// root, and each run cost about 60 ms of CPU — more than the rest of the
// daemon's work put together. The kernel answers the same question with one
// sysctl, in under a millisecond; see procinfo.
//
// What ps calls a process's command is the name it was run by, its first
// argument, which takes a call of its own per process. Over a thousand of
// those is still 10 ms, so the answer is remembered for as long as the
// process lives, and only new processes are asked.

// commandSettle is how long a new process's name is read again on every
// snapshot. A program can rewrite its arguments in place — Node does for
// process.title, as npm and Next.js do while they start — and ps shows the
// new name. Programs do that as they start, so a process older than this
// keeps the name it had.
const commandSettle = 10 * time.Second

// processKey names one process for as long as it lives. A pid is reused once
// its process ends, and a process that calls exec keeps its pid and start
// time but not its short name, which is what tells the new program apart.
type processKey struct {
	pid     int
	started int64
	comm    string
}

// commands is what each process was last read to be running. The map is
// kept and swept rather than made anew each snapshot: it holds an entry for
// every process on the machine, and a snapshot is taken every second.
var commands struct {
	mu        sync.Mutex
	sweep     uint64
	byProcess map[processKey]knownCommand
}

type knownCommand struct {
	command string
	// seen is the last snapshot the process was in.
	seen uint64
}

// kernelProcessTree reads the process table from the kernel.
func kernelProcessTree(ctx context.Context) (*ProcessTree, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	processes, err := procinfo.List()
	if err != nil {
		return nil, err
	}
	settled := time.Now().Add(-commandSettle).UnixMicro()
	tree := &ProcessTree{
		parents:  make(map[int]int, len(processes)),
		commands: make(map[int]string, len(processes)),
	}

	commands.mu.Lock()
	defer commands.mu.Unlock()
	if commands.byProcess == nil {
		commands.byProcess = make(map[processKey]knownCommand, len(processes))
	}
	commands.sweep++
	for _, process := range processes {
		tree.parents[process.PID] = process.PPID
		key := processKey{pid: process.PID, started: process.Started, comm: process.Comm}
		known, ok := commands.byProcess[key]
		if !ok || process.Started > settled {
			known.command = commandOf(process)
		}
		known.seen = commands.sweep
		commands.byProcess[key] = known
		if known.command != "" {
			tree.commands[process.PID] = known.command
		}
	}
	// Only the processes running now are kept, so this is never larger
	// than the process table.
	for key, known := range commands.byProcess {
		if known.seen != commands.sweep {
			delete(commands.byProcess, key)
		}
	}
	return tree, nil
}

// commandOf is what ps shows as a process's command: the name it was run by.
// Only root can read another user's arguments, so those processes, which are
// never an agent, are named by the file they execute.
func commandOf(process procinfo.Process) string {
	if process.Zombie {
		return "<defunct>"
	}
	if name, err := procinfo.Arg0(process.PID); err == nil {
		return name
	}
	if path, err := procinfo.Path(process.PID); err == nil {
		return path
	}
	return process.Comm
}
