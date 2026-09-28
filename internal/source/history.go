package source

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// History is implemented by adapters that can find sessions which have already
// exited.
//
// Discover answers "what is running", and every adapter gates on a live
// process to do it — a pid in a registry, a lock file, a tmux pane. That is
// right for the status board and wrong for the question the folder filter
// asks, "what have I run here", because a transcript outlives its process by
// design. The sessions are still on disk weeks after the CLI quit; nothing new
// has to be recorded, only indexed by directory.
//
// An adapter with no on-disk store simply omits this, and its agents are
// absent from a folder's history rather than the folder being wrong.
type History interface {
	// Past returns sessions this agent has run under dir, newest first,
	// however long ago, stopping at limit.
	//
	// dir is absolute and matches its whole subtree. Implementations may
	// include sessions that are also live; the registry prefers the live
	// reading when both describe one session.
	Past(ctx context.Context, dir string, limit int) ([]protocol.Session, error)

	// Directories reports every working directory this agent has recorded,
	// with how many sessions it holds and when it was last touched.
	//
	// This is what puts a count beside a folder while browsing, so it runs
	// over the whole store rather than one directory — which is exactly why
	// it must not parse transcripts. One directory on the author's machine
	// holds 1,442 of them; counting entries is instant and reading them is
	// not.
	Directories(ctx context.Context) ([]protocol.Folder, error)
}

// DefaultPastLimit bounds one directory's history.
//
// A page of a phone list is tens of rows, and naming a session costs a bounded
// read of its transcript — so this is the ceiling on that work, not on what a
// folder may contain. One directory on the author's machine holds 1,442 Claude
// transcripts; counting them is cheap and parsing them all is not.
const DefaultPastLimit = 200

// historyHeadBytes is how much of a transcript's head an adapter reads to name
// a session.
//
// The opening lines carry the working directory and the first prompt. A large
// pasted first message, or a system preamble ahead of it, can push the prompt
// back, so this is generous — but it is per session and multiplied by the
// limit above, so it cannot be unbounded.
const historyHeadBytes = 256 * 1024

// underDirectory reports whether cwd is dir or sits beneath it.
//
// Both are cleaned first, so a trailing slash or a "." segment cannot decide
// the answer, and the separator test is what stops /src/agentman-old from
// matching /src/agentman.
func underDirectory(cwd, dir string) bool {
	if cwd == "" || dir == "" {
		return false
	}
	cwd = filepath.Clean(cwd)
	dir = filepath.Clean(dir)
	if cwd == dir {
		return true
	}
	return strings.HasPrefix(cwd, dir+string(filepath.Separator))
}

// scanHead applies extract to each line at the start of a file, returning the
// first non-empty result.
//
// The mirror of modelFromTranscript, which reads the tail because the newest
// model is the answer. Here the oldest lines are: a session is named by the
// prompt that opened it, and it keeps that name however long it later ran.
func scanHead(path string, extract func([]byte) bool) {
	file, err := os.Open(path)
	if err != nil {
		return
	}
	defer file.Close()

	buffer := make([]byte, historyHeadBytes)
	n, err := file.Read(buffer)
	if n <= 0 {
		if err != nil {
			return
		}
		return
	}
	buffer = buffer[:n]

	for start := 0; start < len(buffer); {
		end := start
		for end < len(buffer) && buffer[end] != '\n' {
			end++
		}
		// A final line with no newline may have been cut by the read budget;
		// it either parses or is skipped, like any other malformed line.
		if extract(buffer[start:end]) {
			return
		}
		start = end + 1
	}
}

// historyEntry is one candidate transcript, before it is parsed.
type historyEntry struct {
	path    string
	modTime int64
}

// newestTranscripts lists files in dir matching suffix, newest first, capped
// at limit.
//
// Ordering by modification time before reading anything is what keeps a
// directory of a thousand transcripts affordable: only the ones that can
// appear on screen are opened.
func newestTranscripts(dir, suffix string, limit int) []historyEntry {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	found := make([]historyEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), suffix) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		found = append(found, historyEntry{
			path:    filepath.Join(dir, entry.Name()),
			modTime: info.ModTime().UnixMilli(),
		})
	}
	sort.Slice(found, func(i, j int) bool { return found[i].modTime > found[j].modTime })
	if limit > 0 && len(found) > limit {
		found = found[:limit]
	}
	return found
}

// historyName turns a session's first prompt into a row label.
//
// Session names elsewhere are short and machine-authored, so a prompt is
// trimmed to one line of similar weight rather than wrapped. A session with no
// readable prompt falls back to its folder, which is what claudeName does for
// a live session with no name.
func historyName(prompt, cwd string) string {
	prompt = strings.TrimSpace(strings.ReplaceAll(prompt, "\n", " "))
	for strings.Contains(prompt, "  ") {
		prompt = strings.ReplaceAll(prompt, "  ", " ")
	}
	if prompt != "" {
		const maxRunes = 60
		runes := []rune(prompt)
		if len(runes) > maxRunes {
			return strings.TrimSpace(string(runes[:maxRunes])) + "…"
		}
		return prompt
	}
	if base := filepath.Base(cwd); base != "." && base != string(filepath.Separator) {
		return base
	}
	return ""
}

// limitOrDefault normalises a caller's cap.
func limitOrDefault(limit int) int {
	if limit <= 0 || limit > DefaultPastLimit {
		return DefaultPastLimit
	}
	return limit
}
