//go:build darwin

package procinfo

import (
	"encoding/binary"
	"net/netip"
	"syscall"
	"unsafe"
)

// What a process holds open, from <sys/proc_info.h>. The offsets are into
// struct socket_fdinfo, and were read from the SDK's own header with
// offsetof; TestListeningFindsThisProcessesSockets checks them against real
// sockets.
const (
	procInfoCallPIDFDInfo = 3
	procPIDListFDs        = 1
	procPIDTBSDInfo       = 3
	procPIDVnodePathInfo  = 9
	procPIDFDSocketInfo   = 3
	proxFDTypeSocket      = 2

	sizeofProcFDInfo    = 8
	sizeofProcBSDInfo   = 136
	sizeofVnodePathInfo = 2352
	sizeofSocketFDInfo  = 792
	maxPath             = 1024
	initialFDBuffer     = 1024 * sizeofProcFDInfo
	maxFDBuffer         = 1 << 20

	bsdInfoCommOffset  = 48
	bsdInfoNameOffset  = 64
	vnodePathCwdOffset = 152

	socketKindTCP  = 2 // SOCKINFO_TCP
	tcpStateListen = 1 // TSI_S_LISTEN
	familyInet     = 2
	familyInet6    = 30

	socketFamilyOffset    = 24 + 160
	socketKindOffset      = 24 + 232
	socketTCPOffset       = 24 + 240
	tcpLocalPortOffset    = socketTCPOffset + 4
	tcpLocalAddressOffset = socketTCPOffset + 48
	tcpStateOffset        = socketTCPOffset + 80
)

// Name returns the command name lsof reports for a process: the kernel's
// longer name for it, or its short one.
func Name(pid int) (string, error) {
	buf := make([]byte, sizeofProcBSDInfo)
	n, err := pidInfo(pid, procPIDTBSDInfo, 0, buf)
	if err != nil {
		return "", err
	}
	if n < sizeofProcBSDInfo {
		return "", syscall.EINVAL
	}
	if name := cString(buf[bsdInfoNameOffset : bsdInfoNameOffset+32]); name != "" {
		return name, nil
	}
	return cString(buf[bsdInfoCommOffset : bsdInfoCommOffset+17]), nil
}

// Cwd returns a process's working directory.
func Cwd(pid int) (string, error) {
	buf := make([]byte, sizeofVnodePathInfo)
	n, err := pidInfo(pid, procPIDVnodePathInfo, 0, buf)
	if err != nil {
		return "", err
	}
	if n < vnodePathCwdOffset+maxPath {
		return "", syscall.EINVAL
	}
	if cwd := cString(buf[vnodePathCwdOffset : vnodePathCwdOffset+maxPath]); cwd != "" {
		return cwd, nil
	}
	return "", syscall.ENOENT
}

// Listening finds the listening TCP sockets of every process that can be
// read, by pid — what `lsof -iTCP -sTCP:LISTEN` lists for a user who is not
// root.
func Listening() (map[int][]Listener, error) {
	processes, err := List()
	if err != nil {
		return nil, err
	}
	fds := make([]byte, initialFDBuffer)
	info := make([]byte, sizeofSocketFDInfo)
	found := map[int][]Listener{}
	for _, process := range processes {
		if process.Zombie {
			continue
		}
		var n int
		fds, n, err = listFDs(process.PID, fds)
		if err != nil {
			continue // another user's process, or one that has just exited
		}
		for offset := 0; offset+sizeofProcFDInfo <= n; offset += sizeofProcFDInfo {
			if binary.NativeEndian.Uint32(fds[offset+4:]) != proxFDTypeSocket {
				continue
			}
			fd := int32(binary.NativeEndian.Uint32(fds[offset:]))
			got, err := pidFDInfo(process.PID, fd, procPIDFDSocketInfo, info)
			if err != nil || got < sizeofSocketFDInfo {
				continue
			}
			if listener, ok := parseListener(info); ok {
				found[process.PID] = append(found[process.PID], listener)
			}
		}
	}
	return found, nil
}

// parseListener reads a socket_fdinfo, keeping a listening TCP socket.
func parseListener(info []byte) (Listener, bool) {
	family := int32(binary.NativeEndian.Uint32(info[socketFamilyOffset:]))
	if family != familyInet && family != familyInet6 {
		return Listener{}, false
	}
	if int32(binary.NativeEndian.Uint32(info[socketKindOffset:])) != socketKindTCP ||
		int32(binary.NativeEndian.Uint32(info[tcpStateOffset:])) != tcpStateListen {
		return Listener{}, false
	}
	// The port is kept in network byte order.
	port := int(info[tcpLocalPortOffset])<<8 | int(info[tcpLocalPortOffset+1])
	if port == 0 {
		return Listener{}, false
	}
	address := info[tcpLocalAddressOffset : tcpLocalAddressOffset+16]
	var addr netip.Addr
	if family == familyInet {
		// An IPv4 address sits at the end of room for an IPv6 one.
		addr = netip.AddrFrom4([4]byte(address[12:16]))
	} else {
		addr = netip.AddrFrom16([16]byte(address))
	}
	return Listener{Port: port, Addr: addr}, true
}

func pidFDInfo(pid int, fd int32, flavor int, buf []byte) (int, error) {
	n, _, errno := syscall.Syscall6(sysProcInfo, procInfoCallPIDFDInfo, uintptr(pid), uintptr(flavor),
		uintptr(fd), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if errno != 0 {
		return 0, errno
	}
	return int(n), nil
}
