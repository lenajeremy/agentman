//go:build darwin

package procinfo

import (
	"bytes"
	"encoding/binary"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/unix"
)

// Supported reports whether this platform's process table is read here.
const Supported = true

// The kernel's own numbers, from <sys/sysctl.h>, <sys/syscall.h> and
// <sys/proc_info.h>. x/sys/unix carries the process table's layout but none
// of proc_info, which libproc wraps and Go cannot call without cgo.
const (
	ctlKern       = 1
	kernProc      = 14
	kernProcAll   = 0
	kernProcArgs2 = 49
	processZombie = 5 // SZOMB

	sysProcInfo         = 336
	procInfoCallPIDInfo = 2
	procPIDPathInfo     = 11
	pathInfoSize        = 4 * 1024 // PROC_PIDPATHINFO_MAXSIZE
)

// table is the buffer the process table is read into, kept between reads:
// the table is about a megabyte, and it is read every second.
var table struct {
	mu  sync.Mutex
	buf []unix.KinfoProc
}

// List reads the whole process table, every user's processes, in one call.
func List() ([]Process, error) {
	table.mu.Lock()
	defer table.mu.Unlock()
	mib := []int32{ctlKern, kernProc, kernProcAll}
	for attempt := 0; ; attempt++ {
		var size uintptr
		if err := sysctl(mib, nil, &size); err != nil {
			return nil, err
		}
		// Room for the processes that start between asking and reading.
		want := int(size/unix.SizeofKinfoProc) + 64
		if len(table.buf) < want {
			table.buf = make([]unix.KinfoProc, want+want/4)
		}
		size = uintptr(len(table.buf)) * unix.SizeofKinfoProc
		err := sysctl(mib, (*byte)(unsafe.Pointer(&table.buf[0])), &size)
		if err == unix.ENOMEM && attempt < 3 {
			table.buf = nil
			continue
		}
		if err != nil {
			return nil, err
		}
		entries := table.buf[:size/unix.SizeofKinfoProc]
		processes := make([]Process, 0, len(entries))
		for i := range entries {
			entry := &entries[i]
			if entry.Proc.P_pid <= 0 {
				continue
			}
			processes = append(processes, Process{
				PID:     int(entry.Proc.P_pid),
				PPID:    int(entry.Eproc.Ppid),
				UID:     int(entry.Eproc.Ucred.Uid),
				Started: entry.Proc.P_starttime.Sec*1_000_000 + int64(entry.Proc.P_starttime.Usec),
				Comm:    cString(entry.Proc.P_comm[:]),
				Zombie:  entry.Proc.P_stat == processZombie,
			})
		}
		return processes, nil
	}
}

// Args returns the arguments a process was started with, or has since
// rewritten in place, as `ps -o args` shows them; the first is the name it
// was run by. Only the user's own processes can be read.
func Args(pid int) ([]string, error) {
	mib := []int32{ctlKern, kernProcArgs2, int32(pid)}
	var size uintptr
	if err := sysctl(mib, nil, &size); err != nil {
		return nil, err
	}
	if size == 0 {
		return nil, syscall.EINVAL
	}
	buf := make([]byte, size)
	if err := sysctl(mib, &buf[0], &size); err != nil {
		return nil, err
	}
	return parseProcArgs(buf[:size])
}

// parseProcArgs reads KERN_PROCARGS2: the argument count, the path the
// program was executed from, padding, then the arguments themselves, each
// ending in a NUL, followed by the environment.
func parseProcArgs(raw []byte) ([]string, error) {
	if len(raw) < 4 {
		return nil, syscall.EINVAL
	}
	argc := int(int32(binary.NativeEndian.Uint32(raw)))
	rest := raw[4:]
	end := bytes.IndexByte(rest, 0)
	if end < 0 {
		return nil, syscall.EINVAL
	}
	rest = bytes.TrimLeft(rest[end:], "\x00")
	var args []string
	for len(args) < argc && len(rest) > 0 {
		end := bytes.IndexByte(rest, 0)
		if end < 0 {
			break
		}
		args = append(args, string(rest[:end]))
		rest = rest[end+1:]
	}
	if len(args) == 0 || args[0] == "" {
		return nil, syscall.EINVAL
	}
	return args, nil
}

// Path returns the file a process is executing. Unlike its arguments, this
// can be read for any user's process.
func Path(pid int) (string, error) {
	buf := make([]byte, pathInfoSize)
	if _, err := pidInfo(pid, procPIDPathInfo, 0, buf); err != nil {
		return "", err
	}
	if path := cString(buf); path != "" {
		return path, nil
	}
	return "", syscall.ESRCH
}

func sysctl(mib []int32, old *byte, oldlen *uintptr) error {
	_, _, errno := syscall.Syscall6(syscall.SYS___SYSCTL,
		uintptr(unsafe.Pointer(&mib[0])), uintptr(len(mib)),
		uintptr(unsafe.Pointer(old)), uintptr(unsafe.Pointer(oldlen)), 0, 0)
	if errno != 0 {
		return errno
	}
	return nil
}

func pidInfo(pid, flavor int, arg uint64, buf []byte) (int, error) {
	n, _, errno := syscall.Syscall6(sysProcInfo, procInfoCallPIDInfo, uintptr(pid), uintptr(flavor),
		uintptr(arg), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if errno != 0 {
		return 0, errno
	}
	return int(n), nil
}

func cString(raw []byte) string {
	if end := bytes.IndexByte(raw, 0); end >= 0 {
		raw = raw[:end]
	}
	return string(raw)
}
