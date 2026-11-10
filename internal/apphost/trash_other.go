//go:build !darwin

package apphost

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// The freedesktop.org Trash (the home trash): the item moves into
// Trash/files, with a note in Trash/info of where it was, so the file
// manager can put it back. An item on another disk goes through gio.

func trashFolder() string {
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		data = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(data, "Trash", "files")
}

func trashItem(path string) (string, error) {
	files := trashFolder()
	if files == "" {
		return "", errors.New("There's no Trash to move it to.")
	}
	info := filepath.Join(filepath.Dir(files), "info")
	if err := os.MkdirAll(files, 0o700); err != nil {
		return "", plainError(err)
	}
	if err := os.MkdirAll(info, 0o700); err != nil {
		return "", plainError(err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	base := filepath.Base(abs)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	for n := 1; ; n++ {
		name := base
		if n > 1 {
			name = fmt.Sprintf("%s %d%s", stem, n, ext)
		}
		note := filepath.Join(info, name+".trashinfo")
		handle, err := os.OpenFile(note, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", plainError(err)
		}
		escaped := (&url.URL{Path: abs}).EscapedPath()
		fmt.Fprintf(handle, "[Trash Info]\nPath=%s\nDeletionDate=%s\n", escaped, time.Now().Format("2006-01-02T15:04:05"))
		handle.Close()
		into := filepath.Join(files, name)
		if err := os.Rename(abs, into); err != nil {
			os.Remove(note)
			if errors.Is(err, syscall.EXDEV) {
				// On another disk: gio knows that disk's own Trash.
				if out, gioErr := exec.Command("gio", "trash", abs).CombinedOutput(); gioErr != nil {
					return "", fmt.Errorf("Couldn't move %s to the Trash: %s", base, strings.TrimSpace(string(out)))
				}
				return "", nil
			}
			return "", plainError(err)
		}
		return into, nil
	}
}

// forgetTrashed removes the Trash's note for an item taken back out of it.
func forgetTrashed(path string) {
	files := trashFolder()
	if files == "" || filepath.Dir(filepath.Clean(path)) != files {
		return
	}
	os.Remove(filepath.Join(filepath.Dir(files), "info", filepath.Base(path)+".trashinfo"))
}

// copyFileURLs: am serve has no clipboard of its own to put files on; the
// page puts the paths on as text instead (see the caller).
func copyFileURLs([]string) bool { return false }
