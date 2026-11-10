package knowledge

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Every agent reads a file of its own when a session opens, and all but two
// read it again as it changes and after compacting. So a shared note marked
// "always" belongs there: outside every repository, and still there when a
// long conversation has been summarized away. Muse and Cursor only read theirs
// when a session opens; the daemon tells sessions already running (see
// daemon/shared_notes.go). Cursor has no personal file at all, so for it the
// notes go in the project's own AGENTS.md, kept out of git.

// Homes is where each agent keeps its files.
type Homes struct {
	Home         string
	ClaudeConfig string // CLAUDE_CONFIG_DIR, or empty for ~/.claude
	CodexHome    string // CODEX_HOME, or empty for ~/.codex
	ConfigHome   string // XDG_CONFIG_HOME, or empty for ~/.config
}

// HomesFromEnv is the Homes of the running user.
func HomesFromEnv() (Homes, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Homes{}, err
	}
	return Homes{
		Home: home, ClaudeConfig: os.Getenv("CLAUDE_CONFIG_DIR"),
		CodexHome: os.Getenv("CODEX_HOME"), ConfigHome: os.Getenv("XDG_CONFIG_HOME"),
	}, nil
}

// PersonalResult is what happened to one agent's personal file.
type PersonalResult struct {
	Kind string
	Path string
	// Installed is whether the agent is on this machine; nothing is written
	// for one that isn't.
	Installed bool
	Err       error
}

type personalFile struct {
	kind string
	// dir has to exist already: it is how an agent is known to be installed.
	dir  string
	path string
	// whole means the file is Agentman's alone, rather than the user's with
	// a block of Agentman's in it.
	whole bool
}

func personalFiles(h Homes) []personalFile {
	or := func(value, fallback string) string {
		if value != "" {
			return value
		}
		return fallback
	}
	claude := or(h.ClaudeConfig, filepath.Join(h.Home, ".claude"))
	codex := or(h.CodexHome, filepath.Join(h.Home, ".codex"))
	config := or(h.ConfigHome, filepath.Join(h.Home, ".config"))
	gemini := filepath.Join(h.Home, ".gemini")
	kiro := filepath.Join(h.Home, ".kiro")
	return []personalFile{
		{kind: "claude", dir: claude, path: filepath.Join(claude, "CLAUDE.md")},
		{kind: "codex", dir: codex, path: filepath.Join(codex, "AGENTS.md")},
		{kind: "opencode", dir: filepath.Join(config, "opencode"), path: filepath.Join(config, "opencode", "AGENTS.md")},
		{kind: "antigravity", dir: gemini, path: filepath.Join(gemini, "GEMINI.md")},
		{kind: "kiro", dir: kiro, path: filepath.Join(kiro, "steering", "agentman-shared-notes.md"), whole: true},
		{kind: "muse", dir: filepath.Join(config, "muse"), path: filepath.Join(config, "muse", "AGENTS.md")},
	}
}

// Always is the shared notes every agent follows.
func (s *Store) Always() ([]Note, error) {
	listed, err := s.List(Shared)
	if err != nil {
		return nil, err
	}
	var always []Note
	for _, meta := range listed {
		if !meta.Always {
			continue
		}
		if note, err := s.Read(Shared, meta.Name); err == nil {
			always = append(always, note)
		}
	}
	return always, nil
}

// SyncPersonal writes the notes marked "always" into the personal file of
// every agent on this machine, and takes them out again as they are unticked.
// Only Agentman's block is ever changed.
func (s *Store) SyncPersonal(h Homes, always []Note) []PersonalResult {
	kept := block(always)
	var results []PersonalResult
	for _, file := range personalFiles(h) {
		result := PersonalResult{Kind: file.kind, Path: file.path}
		if info, err := os.Stat(file.dir); err != nil || !info.IsDir() {
			results = append(results, result)
			continue
		}
		result.Installed = true
		if file.whole {
			result.Err = syncWhole(file.path, kept)
		} else {
			result.Err = syncBlock(file.path, kept)
		}
		results = append(results, result)
	}
	return results
}

func syncWhole(path, kept string) error {
	if kept == "" {
		if err := os.Remove(path); errors.Is(err, os.ErrNotExist) {
			return nil
		} else if err != nil {
			return err
		}
		// With the file went the reason for the folder, unless it holds
		// anything else; one this never wrote into stays.
		_ = os.Remove(filepath.Dir(path))
		return nil
	}
	if current, err := os.ReadFile(path); err == nil && string(current) == kept {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return writeKept(path, kept)
}

func syncBlock(path, kept string) error {
	current, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if errors.Is(err, os.ErrNotExist) && kept == "" {
		return nil
	}
	next := withBlock(string(current), kept)
	if next == string(current) {
		return nil
	}
	return writeKept(path, next)
}

// writeKept replaces a file in one step, so a crash can't leave the user's
// instructions half written, and through a symlink, since many people's are
// links into a dotfiles repository.
func writeKept(path, text string) error {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".agentman-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(text); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

const (
	projectFile    = "AGENTS.md"
	excludeComment = "# Agentman's shared notes, for Cursor"
	excludeLine    = "/AGENTS.md"
)

// SyncProject keeps the notes marked "always" in the AGENTS.md at the top of
// a git repository, for the agent with no personal file, and out of git with
// the repository's own local exclude list. It reports whether the file now
// carries them. It won't, and is left alone, when the folder isn't the top of
// a repository, when AGENTS.md is tracked (the notes would land in the user's
// commits), or when it is the user's own.
func SyncProject(dir string, always []Note) (bool, error) {
	top, err := runGit(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return false, nil
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil || filepath.Clean(top) != filepath.Clean(real) {
		return false, nil
	}
	if _, err := runGit(dir, "ls-files", "--error-unmatch", "--", projectFile); err == nil {
		return false, nil
	}
	path := filepath.Join(dir, projectFile)
	current, readErr := os.ReadFile(path)
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return false, readErr
	}
	ours := strings.Contains(string(current), beginMarker)
	if readErr == nil && !ours && strings.TrimSpace(string(current)) != "" {
		return false, nil
	}
	kept := block(always)
	if kept == "" {
		if readErr != nil {
			return false, nil
		}
		next := withBlock(string(current), "")
		if strings.TrimSpace(next) == "" {
			if err := os.Remove(path); err != nil {
				return false, err
			}
			return false, setExcluded(dir, false)
		}
		if err := writeKept(path, next); err != nil {
			return false, err
		}
		// What is left is the user's, and git should see it.
		return false, setExcluded(dir, false)
	}
	if err := setExcluded(dir, true); err != nil {
		return false, err
	}
	if next := withBlock(string(current), kept); next != string(current) {
		if err := writeKept(path, next); err != nil {
			return false, err
		}
	}
	return true, nil
}

func setExcluded(dir string, excluded bool) error {
	path, err := runGit(dir, "rev-parse", "--git-path", "info/exclude")
	if err != nil {
		return err
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	current, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	lines := strings.Split(string(current), "\n")
	at := -1
	for i, line := range lines {
		if line == excludeComment {
			at = i
		}
	}
	if !excluded {
		if at < 0 {
			return nil
		}
		kept := append([]string{}, lines[:at]...)
		if at+1 < len(lines) && lines[at+1] == excludeLine {
			at++
		}
		kept = append(kept, lines[at+1:]...)
		return writeKept(path, strings.Join(kept, "\n"))
	}
	for _, line := range lines {
		if line == excludeLine {
			return nil
		}
	}
	text := string(current)
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return writeKept(path, text+excludeComment+"\n"+excludeLine+"\n")
}

func runGit(dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	out, err := command.Output()
	return strings.TrimSpace(string(out)), err
}
