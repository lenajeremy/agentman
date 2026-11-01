//go:build darwin

package procinfo

import "encoding/binary"

const (
	procPIDFDVnodePathInfo   = 2
	proxFDTypeVnode          = 1
	sizeofVnodeFDInfoPath    = 1200 // struct vnode_fdinfowithpath
	vnodeFDInfoPathOffset    = 24 + 152
	vnodeFDInfoPathMaxLength = maxPath
)

// Files lists the files and directories a process holds open by descriptor:
// what `lsof -p` lists under a number. The memory-mapped files lsof also
// lists, the program and its libraries among them, are not read here.
func Files(pid int) ([]OpenFile, error) {
	fds, n, err := listFDs(pid, make([]byte, initialFDBuffer))
	if err != nil {
		return nil, err
	}
	info := make([]byte, sizeofVnodeFDInfoPath)
	var files []OpenFile
	for offset := 0; offset+sizeofProcFDInfo <= n; offset += sizeofProcFDInfo {
		if binary.NativeEndian.Uint32(fds[offset+4:]) != proxFDTypeVnode {
			continue
		}
		fd := int32(binary.NativeEndian.Uint32(fds[offset:]))
		got, err := pidFDInfo(pid, fd, procPIDFDVnodePathInfo, info)
		if err != nil || got < sizeofVnodeFDInfoPath {
			continue // closed since the list
		}
		path := cString(info[vnodeFDInfoPathOffset : vnodeFDInfoPathOffset+vnodeFDInfoPathMaxLength])
		if path != "" {
			files = append(files, OpenFile{FD: int(fd), Path: path})
		}
	}
	return files, nil
}

// listFDs reads a process's descriptor table into buf, growing it when the
// table does not fit, and returns the buffer and how much of it was filled.
func listFDs(pid int, buf []byte) ([]byte, int, error) {
	n, err := pidInfo(pid, procPIDListFDs, 0, buf)
	// A full buffer may have cut the list short.
	for err == nil && n >= len(buf) && len(buf) < maxFDBuffer {
		buf = make([]byte, 2*len(buf))
		n, err = pidInfo(pid, procPIDListFDs, 0, buf)
	}
	return buf, n, err
}
