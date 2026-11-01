package source

import (
	"strconv"
	"strings"

	"github.com/lenajeremy/agentman/internal/procinfo"
)

// kernelOpenFiles is what `lsof -w -a -p PIDS -Fpfn` prints of the files
// those processes hold open, read from the kernel instead; see procinfo.
//
// Antigravity's and the Cursor CLI's discovery ask which conversation or chat
// store each of their processes holds, every sweep while one runs. Asked of
// lsof, that was a process of its own a second, and about as dear as lsof's
// scan of every process on the machine: 60 ms of CPU. The answer is put in
// lsof's own form so the readings of it stay exactly as they were. A process
// that has gone is left out, as lsof leaves it out.
func kernelOpenFiles(pids []int) []byte {
	var out strings.Builder
	for _, pid := range pids {
		files, err := procinfo.Files(pid)
		if err != nil {
			continue
		}
		out.WriteString("p" + strconv.Itoa(pid) + "\n")
		if cwd, err := procinfo.Cwd(pid); err == nil {
			out.WriteString("fcwd\nn" + cwd + "\n")
		}
		for _, file := range files {
			out.WriteString("f" + strconv.Itoa(file.FD) + "\nn" + file.Path + "\n")
		}
	}
	return []byte(out.String())
}
