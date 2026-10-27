package main

import (
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// openLink opens an agentman:// link in Agentman for Mac. A variable so the
// tests can stand in for the app.
var openLink = func(link string) error {
	out, err := exec.Command("/usr/bin/open", link).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// runOpen opens a folder in Agentman for Mac's editor: `am open [folder]`,
// the current folder when none is given. A file opens in its folder's
// editor, in a tab. An agent working on the folder shows in the editor's
// agent panel.
func runOpen(args []string) error {
	fs := flag.NewFlagSet("open", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprintln(fs.Output(), "Usage: am open [folder or file]") }
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return errors.New("usage: am open [folder or file]")
	}
	target := "."
	if fs.NArg() == 1 {
		target = fs.Arg(0)
	}
	link, folder, err := editorLink(target)
	if err != nil {
		return err
	}
	if runtime.GOOS != "darwin" {
		return errors.New("am open opens Agentman for Mac, which runs on macOS")
	}
	if err := openLink(link); err != nil {
		return fmt.Errorf("could not open Agentman for Mac (%v); get it at https://agentman-nu.vercel.app/mac", err)
	}
	fmt.Printf("Opened %s in Agentman\n", tildePath(folder))
	return nil
}

// editorLink is the link that opens target in the editor: its folder, with
// the file in a tab when target is one, and the agent panel on the agent
// working there. Paths are real ones, as an agent's working folder is
// reported, so a folder reached through a link still finds its agent.
func editorLink(target string) (link, folder string, err error) {
	path, err := filepath.Abs(target)
	if err == nil {
		path, err = filepath.EvalSymlinks(path)
	}
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", "", fmt.Errorf("%s: no such folder or file", target)
		}
		return "", "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", "", err
	}
	query := url.Values{"agent": {"here"}}
	folder = path
	if !info.IsDir() {
		folder = filepath.Dir(path)
		query.Set("file", filepath.Base(path))
	}
	query.Set("folder", folder)
	return "agentman://edit?" + query.Encode(), folder, nil
}

// tildePath shortens a path in the home folder to ~/….
func tildePath(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == home {
		return "~"
	}
	if rest, ok := strings.CutPrefix(path, home+string(filepath.Separator)); ok {
		return "~/" + rest
	}
	return path
}
