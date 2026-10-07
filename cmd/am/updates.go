package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/update"
)

// installedPath is the running am with symlinks resolved, which is what tells
// how it was installed: Homebrew's link in bin points into its Caskroom.
func installedPath() string {
	path := mustExecutable()
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}

// daemonInfo is what the phone is told about this agentman: its version
// always, and what is newer once a check has succeeded.
func daemonInfo(checker *update.Checker, upgrade string) *protocol.DaemonInfo {
	info := &protocol.DaemonInfo{Version: strings.TrimPrefix(version, "v")}
	status, checked := checker.Status()
	if !checked {
		return info
	}
	info.Latest, info.Behind = status.Latest, status.Behind
	info.Upgrade, info.Changelog = upgrade, update.ChangelogURL
	for _, release := range status.Releases {
		note := protocol.ReleaseNote{Version: release.Version, Changes: release.Changes, URL: release.URL}
		if !release.Date.IsZero() {
			note.Date = release.Date.UnixMilli()
		}
		info.Releases = append(info.Releases, note)
	}
	return info
}

// describeBehind says how far behind this agentman is, in one line.
func describeBehind(status update.Status) string {
	versions := "versions"
	if status.Behind == 1 {
		versions = "version"
	}
	return fmt.Sprintf("agentman %s is out; this is %s, %d %s behind", status.Latest, status.Current, status.Behind, versions)
}
