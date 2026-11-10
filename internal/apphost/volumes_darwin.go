package apphost

import "syscall"

// mntDontBrowse is MNT_DONTBROWSE: a volume Finder and the open panel
// leave out, such as Recovery.
const mntDontBrowse = 0x00100000

func browsable(path string) bool {
	var fs syscall.Statfs_t
	if err := syscall.Statfs(path, &fs); err != nil {
		return false
	}
	return fs.Flags&mntDontBrowse == 0
}
