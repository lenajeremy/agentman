package apphost

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// The Trash on macOS, without cgo (am is built without it): macOS 15's own
// trash command, which moves an item as Finder does (Put Back works), and
// says where it went; on an older macOS, a move into ~/.Trash under a name
// not taken.

func trashFolder() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".Trash")
}

// movedTo reads where the trash command put it: # Moved "<from>" to "<to>".
var movedTo = regexp.MustCompile(`to "(.*)"\s*$`)

func trashItem(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if _, err := os.Lstat(abs); err != nil {
		return "", plainError(err)
	}
	if _, err := os.Stat("/usr/bin/trash"); err == nil {
		out, err := exec.Command("/usr/bin/trash", "-v", "-s", abs).CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("Couldn't move “%s” to the Trash: %s", filepath.Base(abs), strings.TrimSpace(string(out)))
		}
		if m := movedTo.FindStringSubmatch(strings.TrimSpace(string(out))); m != nil {
			return m[1], nil
		}
		return "", nil
	}
	return moveToTrash(abs, trashFolder())
}

// moveToTrash moves an item into the Trash folder by hand, as "name",
// "name 10.03.15 AM" or that and a count, the way Finder names a second
// item of one name.
func moveToTrash(abs, trash string) (string, error) {
	if trash == "" {
		return "", errors.New("There's no Trash to move it to.")
	}
	base := filepath.Base(abs)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	stamp := time.Now().Format("15.04.05 PM")
	for n := 0; n < 100; n++ {
		name := base
		if n == 1 {
			name = fmt.Sprintf("%s %s%s", stem, stamp, ext)
		} else if n > 1 {
			name = fmt.Sprintf("%s %s %d%s", stem, stamp, n, ext)
		}
		into := filepath.Join(trash, name)
		if _, err := os.Lstat(into); err == nil {
			continue
		}
		if err := os.Rename(abs, into); err != nil {
			return "", plainError(err)
		}
		return into, nil
	}
	return "", fmt.Errorf("Couldn't move “%s” to the Trash.", base)
}

// forgetTrashed has nothing to do on macOS: Finder keeps no notes.
func forgetTrashed(string) {}

// copyFileURLs: putting files on the pasteboard needs AppKit, which am
// (built without cgo) can't reach; the page puts the paths on as text
// instead (see the caller).
func copyFileURLs([]string) bool { return false }
