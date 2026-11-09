package manager

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Agents that know about each other: two changing the same file in one
// repository, or one changing a file another has read and may still be
// relying on. It is read from their transcripts, in plain Go, and each pair
// and set of files is raised once.

const (
	// overlapRecent is how recently an agent must have worked to count.
	overlapRecent = 30 * time.Minute
	offerOverlap  = "overlap"
	// headsUpOpening starts what agents are told about each other.
	headsUpOpening = "Heads up: "
	maxNamedFiles  = 3
)

// Which way two agents overlap.
const (
	overlapBoth    = "both"
	overlapDepends = "depends"
)

// fileWork is what one agent did to files lately, by path within its repo.
type fileWork struct {
	session protocol.Session
	repo    string
	edits   map[string]int64
	reads   map[string]int64
}

func (m *Manager) checkOverlaps(ctx context.Context, settings protocol.ManagerSettings, sessions []protocol.Session) {
	if settings.Autopilot == nil || !settings.Autopilot.Overlap {
		return
	}
	var work []fileWork
	for _, session := range sessions {
		if session.State == protocol.StateEnded || m.now().Sub(time.UnixMilli(session.LastActivityAt)) > overlapRecent {
			continue
		}
		if w := m.readFileWork(ctx, session); len(w.edits)+len(w.reads) > 0 {
			work = append(work, w)
		}
	}
	for i, a := range work {
		for _, b := range work[i+1:] {
			if a.repo == b.repo {
				m.compareWork(ctx, settings, a, b)
			}
		}
	}
}

func (m *Manager) readFileWork(ctx context.Context, session protocol.Session) fileWork {
	work := fileWork{session: session, repo: m.repoOf(ctx, session.Cwd), edits: map[string]int64{}, reads: map[string]int64{}}
	for _, message := range m.tailMessages(ctx, session.ID) {
		if message.Tool == nil || message.IsSidechain || message.Tool.Status == protocol.ToolError || message.Tool.Summary == "" {
			continue
		}
		path := pathInFolder(message.Tool.Summary, session.Cwd)
		switch {
		case path == "":
		case toolChange(message.Tool.Name) == "edit":
			work.edits[path] = message.Ts
		case toolReads(message.Tool.Name):
			work.reads[path] = message.Ts
		}
	}
	return work
}

func toolReads(name string) bool {
	switch strings.ToLower(name) {
	case "read", "read_file", "view", "cat", "notebookread":
		return true
	}
	return false
}

// repoOf names the repository a folder is in, the same for all its
// worktrees; a folder that isn't one is its own.
func (m *Manager) repoOf(ctx context.Context, folder string) string {
	output, err := m.runCheck(ctx, folder, "git", "rev-parse", "--git-common-dir")
	common := strings.TrimSpace(output)
	if err != nil || common == "" {
		return filepath.Clean(folder)
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(folder, common)
	}
	return filepath.Clean(common)
}

// pathInFolder is a tool's target relative to the agent's folder, or "" for
// something outside it, or that isn't a path.
func pathInFolder(target, folder string) string {
	target = strings.Trim(strings.TrimSpace(target), `"'`)
	if target == "" || strings.ContainsAny(target, " \n") {
		return ""
	}
	if !filepath.IsAbs(target) {
		return filepath.Clean(target)
	}
	relative, err := filepath.Rel(folder, target)
	if err != nil || strings.HasPrefix(relative, "..") {
		return ""
	}
	return relative
}

func (m *Manager) compareWork(ctx context.Context, settings protocol.ManagerSettings, a, b fileWork) {
	var both []string
	for path := range a.edits {
		if _, ok := b.edits[path]; ok {
			both = append(both, path)
		}
	}
	if len(both) > 0 {
		slices.Sort(both)
		m.raiseOverlap(ctx, settings, overlapBoth, a.session, b.session, both)
		return
	}
	for _, pair := range [][2]fileWork{{a, b}, {b, a}} {
		editor, reader := pair[0], pair[1]
		var stale []string
		for path, edited := range editor.edits {
			if read, ok := reader.reads[path]; ok && read < edited {
				stale = append(stale, path)
			}
		}
		if len(stale) > 0 {
			slices.Sort(stale)
			m.raiseOverlap(ctx, settings, overlapDepends, editor.session, reader.session, stale)
		}
	}
}

func namedFiles(files []string) string {
	shown := files[:min(maxNamedFiles, len(files))]
	text := "`" + strings.Join(shown, "`, `") + "`"
	if more := len(files) - len(shown); more > 0 {
		text += fmt.Sprintf(" and %d more", more)
	}
	return text
}

func overlapMessages(kind string, a, b protocol.Session, files string) (forA, forB string) {
	if kind == overlapDepends {
		return "", fmt.Sprintf(headsUpOpening+"%s just changed %s, which you read earlier. Read it again before relying on it.", a.Name, files)
	}
	note := headsUpOpening + "%s is also changing %s in this repository. Check what's there now before you edit it, and don't overwrite its work."
	return fmt.Sprintf(note, b.Name, files), fmt.Sprintf(note, a.Name, files)
}

func (m *Manager) raiseOverlap(ctx context.Context, settings protocol.ManagerSettings, kind string, a, b protocol.Session, files []string) {
	key := kind + "|" + a.ID + "|" + b.ID + "|" + strings.Join(files, ",")
	if !m.verifies.once(key, m.now()) {
		return
	}
	m.cardMu.Lock()
	defer m.cardMu.Unlock()
	named := namedFiles(files)
	title := fmt.Sprintf("%s and %s are both changing %s.", a.Name, b.Name, named)
	label := "Tell them both"
	if kind == overlapDepends {
		title = fmt.Sprintf("%s changed %s, which %s read earlier.", a.Name, named, b.Name)
		label = "Tell " + b.Name
	}
	forA, forB := overlapMessages(kind, a, b, named)
	if trustLevel(settings, a.Cwd) == protocol.TrustHandle && trustLevel(settings, b.Cwd) == protocol.TrustHandle {
		m.tellOverlap(ctx, a.ID, b.ID, forA, forB)
		text := title + " Told them."
		m.addEntry(protocol.ManagerEntry{Role: "activity", Tool: "overlap", Text: text, SessionID: b.ID})
		m.note(text)
		return
	}
	id := newID()
	card := &protocol.ManagerCard{
		ID: id, Kind: protocol.CardOffer, State: protocol.CardOpen, SessionID: b.ID, AgentName: b.Name, Title: title,
		Offer:   map[string]string{"type": offerOverlap, "a": a.ID, "b": b.ID, "forA": forA, "forB": forB},
		Options: []protocol.QuestionOption{{Key: "tell", Label: label}, {Key: "leave", Label: "Leave them"}},
	}
	m.addEntry(protocol.ManagerEntry{ID: id, Role: "card", Text: title, SessionID: b.ID, Card: card})
}

// tellOverlap sends each agent its message; one that can't take it now is
// left out, as the other's still worth telling.
func (m *Manager) tellOverlap(ctx context.Context, a, b, forA, forB string) int {
	told := 0
	for _, each := range [][2]string{{a, forA}, {b, forB}} {
		session, live := m.session(each[0])
		if !live || each[1] == "" {
			continue
		}
		sendCtx, cancel := context.WithTimeout(ctx, agentSendTimeout)
		_, err := m.sendTo(sendCtx, session, each[1])
		cancel()
		if err == nil {
			told++
		}
	}
	return told
}

func (m *Manager) takeOverlapOffer(ctx context.Context, card *protocol.ManagerCard, option string) (string, error) {
	if option != "tell" {
		return "Left them to it", nil
	}
	told := m.tellOverlap(ctx, card.Offer["a"], card.Offer["b"], card.Offer["forA"], card.Offer["forB"])
	if told == 0 {
		return "", fmt.Errorf("neither agent could take a message just now")
	}
	return "Told them", nil
}
