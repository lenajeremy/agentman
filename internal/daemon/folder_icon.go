package daemon

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// A folder's picture: the apps show it beside an agent in place of the
// folder's initials. Projects say who they are with a favicon, a logo.png or
// an icon.png, near the top of the tree: app/favicon.ico in a Next.js site,
// public/favicon.ico in most others, assets/images/icon.png in an Expo app.

const (
	// folderIconDepth is how many folders down a picture is looked for. An
	// Expo app keeps its icon three down (mobile/assets/images/icon.png).
	folderIconDepth = 3
	// folderIconBudget bounds one search, so a folder like the home folder
	// costs a moment rather than a walk of the whole disk.
	folderIconBudget = 4000
	// folderIconMaxBytes keeps a picture small enough to send to a phone.
	folderIconMaxBytes = 1 << 20
)

// folderIconSkip are folders that hold dependencies, builds or generated
// projects, never the picture a project is known by. Hidden folders are
// skipped as well.
var folderIconSkip = map[string]struct{}{
	"node_modules": {}, "vendor": {}, "dist": {}, "build": {}, "out": {},
	"target": {}, "Pods": {}, "DerivedData": {}, "__pycache__": {}, "venv": {},
	"coverage": {}, "tmp": {}, "android": {}, "ios": {}, "Library": {},
}

// folderIcons remembers each folder's picture. It is cosmetic, so a folder
// is searched once, the first time an agent is seen in it, and the answer
// (a picture or none) is kept for as long as the daemon runs: a logo added
// later turns up after the next restart.
type folderIcons struct {
	mu    sync.Mutex
	found map[string]string
	home  string
	find  func(root string) string
}

func newFolderIcons() *folderIcons {
	home, _ := os.UserHomeDir()
	return &folderIcons{
		found: map[string]string{},
		home:  filepath.Clean(home),
		find:  findFolderIcon,
	}
}

// lookup returns the folder's picture, relative to it, or "". The search
// runs outside the lock, so other folders' answers are never held up by it.
func (f *folderIcons) lookup(cwd string) string {
	if cwd == "" {
		return ""
	}
	cwd = filepath.Clean(cwd)
	// The home folder and the root are not a project: anything found there
	// belongs to something else.
	if cwd == "/" || cwd == f.home {
		return ""
	}
	f.mu.Lock()
	path, ok := f.found[cwd]
	f.mu.Unlock()
	if ok {
		return path
	}
	path = f.find(cwd)
	f.mu.Lock()
	f.found[cwd] = path
	f.mu.Unlock()
	return path
}

// findFolderIcon looks through the top of a folder, a level at a time, for
// the picture it is known by, and returns it relative to the folder. The
// shallowest wins; at one depth, logo.png before icon.png before an .ico.
func findFolderIcon(root string) string {
	level := []string{""}
	seen := 0
	for depth := 0; depth <= folderIconDepth && len(level) > 0; depth++ {
		var found []string
		var next []string
		for _, dir := range level {
			entries, err := os.ReadDir(filepath.Join(root, dir))
			if err != nil {
				continue
			}
			for _, entry := range entries {
				seen++
				if seen > folderIconBudget {
					return bestFolderIcon(found)
				}
				name := entry.Name()
				rel := filepath.Join(dir, name)
				if entry.IsDir() {
					if _, skip := folderIconSkip[name]; !skip && !strings.HasPrefix(name, ".") {
						next = append(next, rel)
					}
					continue
				}
				if folderIconRank(name) < 0 || !entry.Type().IsRegular() {
					continue
				}
				if info, err := entry.Info(); err != nil || info.Size() == 0 || info.Size() > folderIconMaxBytes {
					continue
				}
				found = append(found, rel)
			}
		}
		if best := bestFolderIcon(found); best != "" {
			return best
		}
		sort.Strings(next)
		level = next
	}
	return ""
}

// folderIconRank orders the names a picture may have, lowest first; -1 is
// not a picture of the folder.
func folderIconRank(name string) int {
	lower := strings.ToLower(name)
	switch {
	case lower == "logo.png":
		return 0
	case lower == "icon.png":
		return 1
	case lower == "favicon.ico":
		return 2
	case strings.HasSuffix(lower, ".ico"):
		return 3
	}
	return -1
}

func bestFolderIcon(found []string) string {
	best, bestRank := "", 4
	for _, rel := range found {
		rank := folderIconRank(filepath.Base(rel))
		if rank < bestRank || (rank == bestRank && rel < best) {
			best, bestRank = rel, rank
		}
	}
	return best
}
