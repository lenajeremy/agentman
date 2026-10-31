package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/lenajeremy/agentman/internal/hook"
	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/update"
)

// am upgrade: the newest agentman, installed the way this one was, and the
// daemon moved onto it. With Homebrew that is brew upgrade, with npm npm
// install, and for an am the install script put down, the release's archive,
// checked against its published checksums and swapped in. The service is
// then restarted on the new am, its own copy replaced first if it runs one.
// Agents keep running: their tmux server is the service's, not am serve.
func runUpgrade(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("upgrade", flag.ContinueOnError)
	check := fs.Bool("check", false, "only say whether a newer agentman is out")
	configHome := fs.String("config-home", "", "separate home for Agentman state")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("upgrade: unexpected argument %q", fs.Arg(0))
	}
	current := strings.TrimPrefix(version, "v")
	if !update.IsRelease(current) {
		return fmt.Errorf("upgrade: this am is a development build (%s), and am upgrade moves releases forward. "+
			"For a release: curl -fsSL https://agentman-nu.vercel.app/install | sh", current)
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	client := &http.Client{Timeout: 3 * time.Minute}
	status, err := update.Check(ctx, client, update.ReleasesURL, current)
	if err != nil {
		return fmt.Errorf("upgrade: couldn't find out what the latest release is: %w", err)
	}
	resolved := installedPath()
	stable, _ := stableBinary()
	if status.Behind == 0 {
		fmt.Printf("agentman %s is the latest release.\n", current)
		if *check {
			return nil
		}
		// Upgraded some other way, the daemon may still run the old one.
		return moveDaemon(ctx, *configHome, stable, current)
	}
	fmt.Println(describeBehind(status) + ".")
	if *check {
		fmt.Println("am upgrade installs it.")
		return nil
	}

	how := update.HowInstalled(resolved)
	var upgraded string
	switch how {
	case update.Homebrew:
		fmt.Println("Upgrading with Homebrew…")
		if err := runLoud(ctx, "brew", "upgrade", "--cask", update.HomebrewCask); err != nil {
			return fmt.Errorf("upgrade: brew upgrade: %w", err)
		}
		upgraded = homebrewAm(ctx, stable, status.Latest)
	case update.Npm:
		fmt.Println("Upgrading with npm…")
		if err := runLoud(ctx, "npm", "install", "-g", "agentman@latest"); err != nil {
			return fmt.Errorf("upgrade: npm install: %w", err)
		}
		upgraded = resolved
	default:
		fmt.Printf("Downloading agentman %s for %s/%s…\n", status.Latest, runtime.GOOS, runtime.GOARCH)
		binary, err := update.Fetch(ctx, client, update.DownloadsURL, status.Latest, runtime.GOOS, runtime.GOARCH)
		if err != nil {
			return fmt.Errorf("upgrade: %w", err)
		}
		fmt.Println("Checksum verified.")
		if err := replaceBinary(resolved, binary); err != nil {
			if errors.Is(err, os.ErrPermission) {
				return fmt.Errorf("upgrade: %s is in a folder this user can't write; "+
					"install again with: curl -fsSL https://agentman-nu.vercel.app/install | sh", collapseHome(resolved))
			}
			return fmt.Errorf("upgrade: replacing %s: %w", collapseHome(resolved), err)
		}
		upgraded = resolved
	}
	installed, err := binaryVersion(ctx, upgraded)
	if err != nil {
		return fmt.Errorf("upgrade: the new am at %s doesn't run: %w", collapseHome(upgraded), err)
	}
	if installed != status.Latest {
		return fmt.Errorf("upgrade: %s is still agentman %s, not %s", collapseHome(upgraded), installed, status.Latest)
	}
	fmt.Printf("Installed agentman %s.\n", installed)
	return moveDaemon(ctx, *configHome, upgraded, installed)
}

// homebrewAm finds the am Homebrew just put down: the link am was run
// through, or the one on PATH, or the one in Homebrew's bin, whichever runs
// the new version. The cask's old folder, which am may have been run from,
// is gone by now.
func homebrewAm(ctx context.Context, stable, latest string) string {
	candidates := []string{stable}
	if onPath, err := exec.LookPath("am"); err == nil {
		candidates = append(candidates, onPath)
	}
	if prefix, err := exec.CommandContext(ctx, "brew", "--prefix").Output(); err == nil {
		candidates = append(candidates, filepath.Join(strings.TrimSpace(string(prefix)), "bin", "am"))
	}
	for _, candidate := range candidates {
		if v, err := binaryVersion(ctx, candidate); err == nil && v == latest {
			return candidate
		}
	}
	return stable
}

// moveDaemon puts am serve on the am at upgraded: the service restarted,
// after its own copy of am is brought up to date if it runs one. A copy that
// is a build of its own is left alone, and an am serve started from a
// terminal is that terminal's to restart.
func moveDaemon(ctx context.Context, configHome, upgraded, newVersion string) error {
	dir, err := hook.ConfigDir(configHome)
	if err != nil {
		return err
	}
	socket := filepath.Join(dir, "am.sock")
	if runtime.GOOS != "darwin" || !serviceLoaded() {
		if running, ok := daemonVersion(ctx, socket); ok && running != newVersion {
			fmt.Printf("am serve still runs %s; restart it to run %s.\n", running, newVersion)
		}
		return nil
	}
	plist, err := servicePlistPath()
	if err != nil {
		return err
	}
	binary, err := serviceBinary(plist)
	if err != nil {
		return err
	}
	if !sameFile(binary, upgraded) {
		copied, err := binaryVersion(ctx, binary)
		switch serviceCopyAction(copied, err, newVersion) {
		case copyReplace:
			data, err := os.ReadFile(upgraded)
			if err != nil {
				return err
			}
			if err := replaceBinary(binary, data); err != nil {
				return fmt.Errorf("upgrade: the service's copy of am, %s: %w", collapseHome(binary), err)
			}
			if v, err := binaryVersion(ctx, binary); err != nil || v != newVersion {
				return fmt.Errorf("upgrade: the service's new copy of am, %s, doesn't run as %s", collapseHome(binary), newVersion)
			}
			fmt.Printf("Updated the service's copy of am, %s.\n", collapseHome(binary))
		case copyLeave:
			if err != nil {
				copied = "a version it doesn't say"
			}
			fmt.Printf("The service runs %s (%s), a build of its own, so it was left as it is. "+
				"am service install moves the service onto %s.\n", collapseHome(binary), copied, collapseHome(upgraded))
			return nil
		}
	}
	if running, ok := daemonVersion(ctx, socket); ok && running == newVersion {
		fmt.Printf("am serve already runs %s.\n", newVersion)
		return nil
	}
	if err := launchctl("kickstart", "-k", serviceTarget()); err != nil {
		return err
	}
	if !waitFor(30*time.Second, func() bool { return socketAnswers(socket) }) {
		return fmt.Errorf("upgrade: restarted the service, but am serve hasn't answered yet; see %s",
			collapseHome(filepath.Join(dir, "serve.log")))
	}
	fmt.Printf("Restarted the service on agentman %s. Agents keep running.\n", newVersion)
	return nil
}

type copyAction int

const (
	// copyKeep: the copy already runs this release or a newer one.
	copyKeep copyAction = iota
	// copyReplace: the copy is an older release; it gets the new am.
	copyReplace
	// copyLeave: the copy is a build of unreleased work, or doesn't run.
	copyLeave
)

// serviceCopyAction decides what happens to a copy of am the service runs
// in place of the one being upgraded, from the version that copy reports.
func serviceCopyAction(copied string, err error, newVersion string) copyAction {
	switch {
	case err != nil || !update.IsRelease(copied):
		return copyLeave
	case update.Before(copied, newVersion):
		return copyReplace
	default:
		return copyKeep
	}
}

// replaceBinary puts data at path as a new file, renamed over the old: never
// written in place, which a running am would be cut off by, and which on
// Apple silicon leaves the kernel checking the new code against the old
// file's signature and killing it at launch.
func replaceBinary(path string, data []byte) error {
	temp, err := os.CreateTemp(filepath.Dir(path), ".am.new-*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Chmod(0o755); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}

// binaryVersion is the version the am at path reports.
func binaryVersion(ctx context.Context, path string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "version", "--json").Output()
	if err != nil {
		return "", err
	}
	var reported struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(out, &reported); err != nil || reported.Version == "" {
		return "", errors.New("it didn't say its version")
	}
	return strings.TrimPrefix(reported.Version, "v"), nil
}

// daemonVersion is the version am serve says it runs, if it answers.
func daemonVersion(ctx context.Context, socket string) (string, bool) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	event, err := socketCall(socket)(ctx, protocol.Request{Type: protocol.ReqListSessions})
	if err != nil || event.Daemon == nil || event.Daemon.Version == "" {
		return "", false
	}
	return strings.TrimPrefix(event.Daemon.Version, "v"), true
}

var serviceProgram = regexp.MustCompile(`(?s)<key>ProgramArguments</key>\s*<array>\s*<string>([^<]*)</string>`)

// serviceBinary is the am the service's launchd job runs.
func serviceBinary(plist string) (string, error) {
	content, err := os.ReadFile(plist)
	if err != nil {
		return "", fmt.Errorf("upgrade: reading the service's job: %w", err)
	}
	match := serviceProgram.FindSubmatch(content)
	if match == nil {
		return "", fmt.Errorf("upgrade: the service's job, %s, names no program", collapseHome(plist))
	}
	return html.UnescapeString(string(match[1])), nil
}

// sameFile reports whether a and b, symlinks followed, are one file.
func sameFile(a, b string) bool {
	x, errA := os.Stat(a)
	y, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(x, y)
}

// runLoud runs a command with its output on this terminal.
func runLoud(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}
