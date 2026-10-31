package main

import (
	"io/fs"
	"path/filepath"
	"sync"
	"time"
)

// The folder watcher (the Mac app's FolderWatcher): files created, changed
// and deleted under an editor's folder, told to the language servers, so
// they hear of files an agent changes that aren't open. Here the folder is
// looked over every 2 s, which works the same on every system; git's own
// files and heavy folders are left out, as FSEvents' are on the Mac.

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

type watchers struct {
	mu   sync.Mutex
	list map[string]*watcher
}

type watcher struct {
	root string
	// seen is when a window last said it still wants the folder watched.
	seen time.Time
}

type fileState struct {
	modified int64
	size     int64
}

const (
	watchEvery = 2 * time.Second
	watchFor   = 40 * time.Second
	watchLimit = 50000
)

// Watch watches a folder for its editor window. A window says so again
// every few seconds; a folder no window has asked about for 40 s stops
// being watched.
func (e *EditorFiles) Watch(root string) {
	e.watchers.mu.Lock()
	defer e.watchers.mu.Unlock()
	if e.watchers.list == nil {
		e.watchers.list = map[string]*watcher{}
	}
	if w, ok := e.watchers.list[root]; ok {
		w.seen = time.Now()
		return
	}
	w := &watcher{root: root, seen: time.Now()}
	e.watchers.list[root] = w
	go e.watch(w)
}

func (e *EditorFiles) watch(w *watcher) {
	before := scanFolder(w.root)
	for {
		time.Sleep(watchEvery)
		e.watchers.mu.Lock()
		if time.Since(w.seen) > watchFor {
			delete(e.watchers.list, w.root)
			e.watchers.mu.Unlock()
			return
		}
		e.watchers.mu.Unlock()
		now := scanFolder(w.root)
		changes := diffFolder(w.root, before, now)
		before = now
		if len(changes) > 0 {
			emitEditor("agentman:editor-files", FilesChanged{Root: w.root, Changes: changes})
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
