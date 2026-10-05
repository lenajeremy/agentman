package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/source"
)

// maxFolderPathBytes bounds a directory the phone names.
//
// The same ceiling validateLaunchPath uses. Unlike a launch path this one may
// be absolute, because the folder filter has to reach directories the browser
// cannot — a dot-directory, a checkout outside the Mac user's home — but it is
// still only ever compared against what the index already found.
const maxFolderPathBytes = 4096

// recentFolderLimit is how many folders the Recent list carries.
//
// It is a shortcut list, not an archive: the browser is there for the rest.
const recentFolderLimit = 12

// validateFolderPath checks a directory named for reading history.
//
// Deliberately looser than validateLaunchPath, which guards a path the daemon
// is about to *spawn a process in*. Nothing is executed here and the path is
// never opened: it is matched against working directories already recorded in
// the agents' own stores, so an absolute path and a dot-directory are both
// legitimate — that is where a good deal of the history lives.
func validateFolderPath(raw string) error {
	if raw == "" {
		return errors.New("daemon: a folder is required")
	}
	if len(raw) > maxFolderPathBytes || strings.ContainsAny(raw, "\\\x00") {
		return errors.New("daemon: invalid folder")
	}
	return nil
}

// folderDirectory resolves what the phone named to an absolute directory.
//
// The browser walks relative to the Mac user's home, exactly as the New
// session browser does, while Recent hands back the absolute path it was
// given. Both arrive here.
func folderDirectory(raw string) (string, error) {
	if err := validateFolderPath(raw); err != nil {
		return "", err
	}
	if filepath.IsAbs(raw) {
		return filepath.Clean(raw), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Clean(filepath.Join(home, raw)), nil
}

// listFolders answers ReqListFolders with the directories agents ran in.
func (d *Daemon) listFolders(ctx context.Context) ([]protocol.Folder, error) {
	index, err := d.registry.Folders(ctx)
	if index == nil {
		return nil, err
	}
	// A partial index is still worth showing: one agent's store being
	// unreadable should not blank the folders of the other five.
	return index.Recent(recentFolderLimit), nil
}

// annotateDirectories attaches an agent count to each browsable child.
//
// Counts are for the whole subtree, so the number beside "Desktop" is the
// number selecting it would show. A child with no agents is left out rather
// than sent as a zero, which keeps the common case — a home directory of
// folders no agent has touched — from paying for a field per row.
func (d *Daemon) annotateDirectories(ctx context.Context, parent string, names []string) []protocol.Folder {
	index, _ := d.registry.Folders(ctx)
	if index == nil || len(names) == 0 {
		return nil
	}
	base, err := folderDirectory(parent)
	if err != nil {
		return nil
	}
	folders := make([]protocol.Folder, 0, len(names))
	for _, name := range names {
		folder := index.Under(filepath.Join(base, name))
		if folder.Agents == 0 {
			continue
		}
		// The phone joins this onto the path it is browsing, so it travels as
		// the child's name rather than as an absolute path it did not ask for.
		folder.Path = name
		folders = append(folders, folder)
	}
	return folders
}

// directorySessions answers ReqDirectorySessions.
func (d *Daemon) directorySessions(ctx context.Context, raw string) ([]protocol.Session, error) {
	dir, err := folderDirectory(raw)
	if err != nil {
		return nil, err
	}
	sessions, err := d.registry.InDirectory(ctx, dir, source.DefaultPastLimit)
	if sessions == nil && err != nil {
		return nil, err
	}
	// Held to the same bounds as the board: the app refuses a whole list
	// over one session's out-of-range field.
	for i := range sessions {
		normalizeSessionStatus(&sessions[i])
	}
	return sessions, nil
}
