package apphost

import (
	"context"
	"io/fs"
	"path/filepath"
	"time"
)

// The folder watcher (the Mac app's FolderWatcher): files created, changed
// and deleted under an editor's folder, told to the language servers, so
// they hear of files an agent changes that aren't open. Here the folder is
// looked over every 2 s, which works the same on every system; git's own
// files and heavy folders are left out, as FSEvents' are on the Mac.
// The page hears of them over /app/watch (cmd/am/webapp.go).

// FileChange is a file and what happened to it, numbered as LSP numbers
// them: 1 created, 2 changed, 3 deleted.
type FileChange struct {
	Path string `json:"path"`
	Type int    `json:"type"`
}

// FilesChanged is the "agentman:editor-files" event: a folder's changes.
type FilesChanged struct {
	Root    string       `json:"root"`
	Changes []FileChange `json:"changes"`
}

type fileState struct {
	modified int64
	size     int64
}

const (
	watchEvery = 2 * time.Second
	watchLimit = 50000
)

// Watch watches a folder while ctx lasts (am serve holds one open for each
// editor tab's /app/watch stream), telling changed what changed every few
// seconds. It stops when ctx ends or changed fails.
func Watch(ctx context.Context, root string, changed func(FilesChanged) error) {
	before := scanFolder(root)
	tick := time.NewTicker(watchEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		now := scanFolder(root)
		changes := diffFolder(root, before, now)
		before = now
		if len(changes) > 0 {
			if err := changed(FilesChanged{Root: root, Changes: changes}); err != nil {
				return
			}
		}
	}
}

// scanFolder notes every file's time and size, by its path in the folder.
func scanFolder(root string) map[string]fileState {
	files := map[string]fileState{}
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != root && (d.Name() == ".git" || editorQuietFolders[d.Name()]) {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		files[rel] = fileState{modified: info.ModTime().UnixNano(), size: info.Size()}
		if len(files) >= watchLimit {
			return filepath.SkipAll
		}
		return nil
	})
	return files
}

func diffFolder(root string, before, now map[string]fileState) []FileChange {
	changes := []FileChange{}
	for path, state := range now {
		was, ok := before[path]
		switch {
		case !ok:
			changes = append(changes, FileChange{Path: filepath.Join(root, path), Type: 1})
		case was != state:
			changes = append(changes, FileChange{Path: filepath.Join(root, path), Type: 2})
		}
	}
	for path := range before {
		if _, ok := now[path]; !ok {
			changes = append(changes, FileChange{Path: filepath.Join(root, path), Type: 3})
		}
	}
	return changes
}
