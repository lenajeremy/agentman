package apphost

import (
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Show in Finder and Open, as the Mac app's NSWorkspace calls: am serve is
// on the same computer as the browser, so it shows the file in the file
// manager there, or opens it in the app the system has for it.

// start runs a program without waiting on it; tests replace it.
var start = func(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }() // no zombies left in a long-running am serve
	return nil
}

// Reveal shows a file or folder in Finder, or in the Linux file manager
// (its FileManager1 interface, else the folder opened).
func Reveal(path string) error {
	if err := present(path); err != nil {
		return err
	}
	if runtime.GOOS == "darwin" {
		return start("open", "-R", path)
	}
	uri := strings.ReplaceAll((&url.URL{Scheme: "file", Path: path}).String(), "'", "%27")
	if err := exec.Command("gdbus", "call", "--session", "--dest", "org.freedesktop.FileManager1",
		"--object-path", "/org/freedesktop/FileManager1", "--method", "org.freedesktop.FileManager1.ShowItems",
		"['"+uri+"']", "").Run(); err == nil {
		return nil
	}
	dir := path
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		dir = filepath.Dir(path)
	}
	return start("xdg-open", dir)
}

// OpenPath opens a file in the app the system has for it.
func OpenPath(path string) error {
	if err := present(path); err != nil {
		return err
	}
	if runtime.GOOS == "darwin" {
		return start("open", path)
	}
	return start("xdg-open", path)
}

func present(path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("not an absolute path")
	}
	if _, err := os.Lstat(path); err != nil {
		return plainError(err)
	}
	return nil
}
