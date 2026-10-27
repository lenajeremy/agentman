package source

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/tmux"
)

// Since 0.164, Codex runs the conversations of every terminal in one shared
// app server, and a conversation resumed in a terminal (/resume, or
// `codex resume` typed by hand) goes on in the rollout it started in. So a
// pane can be showing a conversation older than itself, which discovery's
// timing rule refuses: a pane claims only a rollout started after it, since a
// folder alone could show a pane someone else's conversation. Nor does any
// process hold a file that says which pane has which thread; the app server
// holds them all. The pane then sat empty beside its own conversation, which
// showed as one nobody could type to.
//
// The pane's title says. Codex names the terminal after the conversation it
// has open, "Respond to greeting | intro-to-hacking", with the name it keeps
// in session_index.jsonl. The name and the pane's folder settle the thread;
// of several of one name there, the one written last.

// codexTitleSeparator parts the conversation's name from the folder in the
// title Codex sets.
const codexTitleSeparator = " | "

// codexTitleName is the conversation's name in a pane's title, or "" when
// the title names none: Codex's title before a conversation has a name, or
// what the shell set.
func codexTitleName(title string) string {
	cut := strings.LastIndex(title, codexTitleSeparator)
	if cut < 0 {
		return ""
	}
	// Whatever marks the conversation at work comes before its name.
	name := strings.TrimLeftFunc(title[:cut], func(r rune) bool {
		return unicode.Is(unicode.Braille, r) || unicode.IsSpace(r) || strings.ContainsRune("•◦●○◐◓◑◒✳✻✶✢·*", r)
	})
	return strings.TrimSpace(name)
}

// codexNames is session_index.jsonl read into the threads of each name,
// read again only when it changes.
type codexNames struct {
	mu      sync.Mutex
	read    bool
	version codexFileVersion
	threads map[string][]string
}

// named returns the threads named name, or whose name a title cut short
// with an ellipsis begins.
func (n *codexNames) named(index, name string) ([]string, codexFileVersion) {
	info, err := os.Stat(index)
	if err != nil {
		return nil, codexFileVersion{}
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if !n.read || !n.version.matches(info) {
		n.threads = readCodexNames(index)
		n.version = codexVersion(info)
		n.read = true
	}
	if short, cut := strings.CutSuffix(name, "…"); cut && short != "" {
		var threads []string
		for full, ids := range n.threads {
			if strings.HasPrefix(full, short) {
				threads = append(threads, ids...)
			}
		}
		return threads, n.version
	}
	return n.threads[name], n.version
}

// readCodexNames reads the index, where a thread renamed appears again with
// its new name: the last line for a thread is its name.
func readCodexNames(index string) map[string][]string {
	file, err := os.Open(index)
	if err != nil {
		return nil
	}
	defer file.Close()
	nameOf := map[string]string{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		var entry struct {
			ID   string `json:"id"`
			Name string `json:"thread_name"`
		}
		if json.Unmarshal(scanner.Bytes(), &entry) != nil || entry.ID == "" {
			continue
		}
		nameOf[entry.ID] = strings.TrimSpace(entry.Name)
	}
	threads := map[string][]string{}
	for id, name := range nameOf {
		if name != "" {
			threads[name] = append(threads[name], id)
		}
	}
	return threads
}

// codexTitled is what a pane's title settled last sweep, kept while neither
// the title nor the index changes: the thread and its rollout, or nothing.
type codexTitled struct {
	title  string
	index  codexFileVersion
	thread string
	path   string
}

// titledThread returns the thread a pane's title names, and its rollout: a
// conversation of that name in the pane's folder, the one written last.
func (s *CodexSource) titledThread(pane tmux.Session, dirs []string) (string, string) {
	name := codexTitleName(pane.Title)
	if name == "" {
		return "", ""
	}
	threads, version := s.names.named(filepath.Join(s.home, ".codex", "session_index.jsonl"), name)
	s.titleMu.Lock()
	settled, ok := s.titled[pane.Name]
	s.titleMu.Unlock()
	if ok && settled.title == pane.Title && settled.index == version {
		return settled.thread, settled.path
	}

	settled = codexTitled{title: pane.Title, index: version}
	var newest os.FileInfo
	for _, thread := range threads {
		path := s.threadRollout(thread, dirs)
		if path == "" {
			continue
		}
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		meta, err := s.cachedCodexMeta(path, info)
		if err != nil || meta.threadID() != thread || meta.isSubagent() || meta.Payload.Cwd != pane.Cwd {
			continue
		}
		if newest == nil || info.ModTime().After(newest.ModTime()) {
			newest = info
			settled.thread, settled.path = thread, path
		}
	}
	s.titleMu.Lock()
	if s.titled == nil {
		s.titled = map[string]codexTitled{}
	}
	s.titled[pane.Name] = settled
	s.titleMu.Unlock()
	return settled.thread, settled.path
}

// threadRollout finds a thread's rollout, named after it: in the days a
// sweep reads, else among those history has seen, else in all of them.
func (s *CodexSource) threadRollout(thread string, dirs []string) string {
	suffix := "-" + thread + ".jsonl"
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), "rollout-") && strings.HasSuffix(entry.Name(), suffix) {
				return filepath.Join(dir, entry.Name())
			}
		}
	}
	if path, ok := s.pastRollout(string(protocol.KindCodex) + ":" + thread); ok {
		return path
	}
	for _, entry := range s.allRollouts() {
		if strings.HasSuffix(entry.path, suffix) {
			return entry.path
		}
	}
	return ""
}

// forgetTitles drops what closed panes' titles settled.
func (s *CodexSource) forgetTitles(panes []tmux.Session) {
	open := make(map[string]bool, len(panes))
	for _, pane := range panes {
		open[pane.Name] = true
	}
	s.titleMu.Lock()
	for name := range s.titled {
		if !open[name] {
			delete(s.titled, name)
		}
	}
	s.titleMu.Unlock()
}
