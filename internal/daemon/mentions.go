package daemon

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/lenajeremy/agentman/internal/protocol"
)

const (
	// maxMentionQueryBytes bounds what is typed after "@".
	maxMentionQueryBytes = 256
	// maxMentionResults is how many matches go back: more than a phone
	// shows, few enough to send on every pause in typing.
	maxMentionResults = 40
	// maxIndexedFiles bounds a folder's index; a folder with more is
	// matched on its first maxIndexedFiles.
	maxIndexedFiles = 50_000
	// mentionIndexAge is how long a folder's index is reused: typing a name
	// asks many times in a few seconds.
	mentionIndexAge = 10 * time.Second
)

// quietFolders are left out of a folder walked without git: heavy, and
// rarely what anyone means. Git leaves them out through .gitignore.
var quietFolders = map[string]bool{
	"node_modules": true, ".build": true, "build": true, "dist": true, ".next": true, "DerivedData": true,
	"Pods": true, "target": true, "__pycache__": true, ".venv": true, "venv": true, ".expo": true, ".git": true,
}

// mentionIndex is each folder's files, as paths inside it, kept briefly.
type mentionIndex struct {
	mu      sync.Mutex
	folders map[string]indexedFolder
}

type indexedFolder struct {
	files []string
	at    time.Time
}

func (m *mentionIndex) files(ctx context.Context, cwd string) []string {
	m.mu.Lock()
	if m.folders == nil {
		m.folders = map[string]indexedFolder{}
	}
	if folder, ok := m.folders[cwd]; ok && time.Since(folder.at) < mentionIndexAge {
		m.mu.Unlock()
		return folder.files
	}
	m.mu.Unlock()

	files := folderFiles(ctx, cwd)
	m.mu.Lock()
	// Folders not asked about for a while are let go.
	for other, folder := range m.folders {
		if time.Since(folder.at) > time.Minute {
			delete(m.folders, other)
		}
	}
	m.folders[cwd] = indexedFolder{files: files, at: time.Now()}
	m.mu.Unlock()
	return files
}

// folderFiles lists a folder's files as git sees them (tracked, and new ones
// it doesn't ignore), or by walking it when it isn't a repository.
func folderFiles(ctx context.Context, cwd string) []string {
	if text, _, err := runGit(ctx, cwd, "ls-files", "-co", "--exclude-standard", "-z"); err == nil {
		return splitPaths(text, maxIndexedFiles)
	}
	var files []string
	_ = filepath.WalkDir(cwd, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || ctx.Err() != nil {
			return filepath.SkipDir
		}
		if entry.IsDir() {
			if path != cwd && (quietFolders[entry.Name()] || strings.HasPrefix(entry.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if len(files) == maxIndexedFiles {
			return filepath.SkipAll
		}
		if rel, err := filepath.Rel(cwd, path); err == nil && entry.Type().IsRegular() {
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	return files
}

func splitPaths(text string, limit int) []string {
	var paths []string
	for _, path := range strings.Split(text, "\x00") {
		if path == "" {
			continue
		}
		if len(paths) == limit {
			break
		}
		paths = append(paths, path)
	}
	return paths
}

// findFiles answers ReqFindFiles.
func (d *Daemon) findFiles(ctx context.Context, req protocol.Request) protocol.Event {
	d.mu.Lock()
	session, known := d.sessions[req.SessionID]
	d.mu.Unlock()
	if !known || session.Cwd == "" {
		return workspaceError(req, "that session is no longer available")
	}
	if info, err := os.Stat(session.Cwd); err != nil || !info.IsDir() {
		return workspaceError(req, "the session directory is unavailable")
	}
	query := strings.TrimSpace(req.Text)
	var found []string
	if query == "" {
		// Before anything is typed: what changed, the likeliest to be named.
		if text, _, err := runGit(ctx, session.Cwd, "ls-files", "-mo", "--exclude-standard", "-z"); err == nil {
			found = splitPaths(text, maxMentionResults)
		}
	} else {
		found = rankFiles(query, d.mentions.files(ctx, session.Cwd), maxMentionResults)
	}
	if found == nil {
		found = []string{}
	}
	return protocol.Event{Type: protocol.EvtFiles, SessionID: req.SessionID, Files: found}
}

// rankFiles is the files that match query, best first: the letters together
// in the name, then in the path, then scattered through the name, then the
// path. The Mac's editor ranks its `@` and ⌘P the same way.
func rankFiles(query string, files []string, limit int) []string {
	type scored struct {
		path  string
		score int
	}
	var matches []scored
	for _, path := range files {
		if score, ok := fuzzyScore(query, path); ok {
			matches = append(matches, scored{path, score})
		}
	}
	slices.SortStableFunc(matches, func(a, b scored) int { return b.score - a.score })
	out := make([]string, 0, min(limit, len(matches)))
	for _, match := range matches[:min(limit, len(matches))] {
		out = append(out, match.path)
	}
	return out
}

func fuzzyScore(query, path string) (int, bool) {
	typed := strings.ToLower(strings.TrimSpace(query))
	needle := []rune(strings.Join(strings.Fields(typed), ""))
	if len(needle) == 0 {
		return 0, true
	}
	length := len([]rune(path))
	name := path[strings.LastIndex(path, "/")+1:]
	for _, candidate := range []struct {
		text string
		base int
	}{{name, 40_000}, {path, 30_000}} {
		lower := strings.ToLower(candidate.text)
		if at := strings.Index(lower, typed); at >= 0 {
			start := at == 0 || strings.ContainsRune(" /._-", rune(lower[at-1]))
			bonus := 0
			if start {
				bonus = 5_000
			}
			return candidate.base + bonus - length, true
		}
	}
	if inName, ok := scatteredMatch(needle, name); ok {
		return 10_000 + inName*10 - length, true
	}
	if inPath, ok := scatteredMatch(needle, path); ok {
		return inPath*10 - length, true
	}
	return 0, false
}

// scatteredMatch finds needle's letters in order through candidate, scoring
// letters next to each other and at the start of a word higher.
func scatteredMatch(needle []rune, candidate string) (int, bool) {
	original := []rune(candidate)
	score, found, last := 0, 0, -2
	for index, r := range original {
		if found == len(needle) {
			break
		}
		if unicode.ToLower(r) != needle[found] {
			continue
		}
		points := 1
		if index == last+1 {
			points += 5
		}
		if index == 0 || strings.ContainsRune("/._- ", original[index-1]) ||
			(unicode.IsUpper(r) && unicode.IsLower(original[index-1])) {
			points += 8
		}
		score += points
		last = index
		found++
	}
	return score, found == len(needle)
}
