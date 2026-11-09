package daemon

import (
	"context"
	"strings"

	"github.com/lenajeremy/agentman/internal/knowledge"
	"github.com/lenajeremy/agentman/internal/parser"
	"github.com/lenajeremy/agentman/internal/protocol"
)

// Muse and Cursor read their instructions only when a session opens, so a note
// added since goes along with the session's next message. Nothing wakes an idle
// agent for it: one that isn't doing anything doesn't need it yet. A message
// can be lost when a conversation is compacted, so after one the notes that
// came that way are sent again.

const (
	// removedNote is what Delivered holds for a note the session was told to
	// stop following.
	removedNote = "-"
	// maxNotesInMessage keeps one message from being swamped. What doesn't
	// fit goes with the next.
	maxNotesInMessage = 16 << 10
)

func notesTargetFor(session protocol.Session) (string, bool) {
	switch session.Kind {
	case protocol.KindMuse:
		return museNotesTarget, true
	case protocol.KindCursorCLI:
		return cursorNotesPrefix + session.Cwd, true
	}
	return "", false
}

// attach is text with the notes this session doesn't have added to it, and
// what to call once it has been delivered. count says how many times the
// session has compacted, or -1 when that can't be told; it reads the whole
// transcript, so it is asked only when the answer could change something.
func (n *sharedNotes) attach(session protocol.Session, count func() int, text string) (string, func()) {
	nothing := func() {}
	if n == nil {
		return text, nothing
	}
	target, ok := notesTargetFor(session)
	if !ok {
		return text, nothing
	}
	// A command is for the agent's terminal, and more words would break it.
	if trimmed := strings.TrimSpace(text); strings.HasPrefix(trimmed, "/") || strings.HasPrefix(trimmed, "!") {
		return text, nothing
	}
	always, err := n.store.Always()
	if err != nil {
		return text, nothing
	}

	n.mu.Lock()
	sentByMessage := n.state.Sessions[session.ID] != nil && len(n.state.Sessions[session.ID].Delivered) > 0
	n.mu.Unlock()
	compactions := -1
	if sentByMessage {
		compactions = count()
	}

	n.mu.Lock()
	now := n.now().UnixMilli()
	record := n.state.Sessions[session.ID]
	delivered := map[string]string{}
	if record != nil {
		record.Seen = now
		if compactions > record.Compactions {
			record.Delivered = map[string]string{}
			record.Compactions = compactions
		}
		for name, hash := range record.Delivered {
			delivered[name] = hash
		}
	}

	onDisk := n.state.Targets[target]
	opened := func(at int64) bool { return session.StartedAt > 0 && at != notOnDisk && session.StartedAt >= at }
	live := make(map[string]bool, len(always))
	var add []knowledge.Note
	var hashes []string
	size := 0
	for _, note := range always {
		live[note.Name] = true
		hash := noteHash(note)
		if delivered[note.Name] == hash {
			continue
		}
		if kept, known := onDisk[note.Name]; known && kept.Hash == hash && kept.RemovedAt == 0 && opened(kept.At) {
			continue
		}
		if size += len(note.Title) + len(note.Body); size > maxNotesInMessage && len(add) > 0 {
			break
		}
		add, hashes = append(add, note), append(hashes, hash)
	}
	var stop []string
	var stopped []string
	for name, kept := range onDisk {
		if live[name] || kept.RemovedAt == 0 || delivered[name] == removedNote {
			continue
		}
		_, sent := delivered[name]
		if sent || (opened(kept.At) && session.StartedAt < kept.RemovedAt) {
			stop, stopped = append(stop, kept.Title), append(stopped, name)
		}
	}
	first := record == nil
	n.mu.Unlock()
	if len(add) == 0 && len(stop) == 0 {
		return text, nothing
	}
	// The first time anything goes by message: where the count stands, so
	// only a compaction after this one counts.
	baseline := 0
	if first || !sentByMessage {
		baseline = max(count(), 0)
	}
	withNotes := strings.TrimRight(text, " \t\r\n") + "\n\n" + sharedNotesMessage(add, stop)
	return withNotes, func() {
		n.mu.Lock()
		defer n.mu.Unlock()
		kept := n.state.Sessions[session.ID]
		if kept == nil {
			kept = &notesSession{}
			n.state.Sessions[session.ID] = kept
		}
		if kept.Delivered == nil {
			kept.Delivered = map[string]string{}
		}
		if !sentByMessage {
			kept.Compactions = baseline
		}
		kept.Seen = n.now().UnixMilli()
		for i, note := range add {
			kept.Delivered[note.Name] = hashes[i]
		}
		for _, name := range stopped {
			kept.Delivered[name] = removedNote
		}
		n.saveLocked()
	}
}

func sharedNotesMessage(add []knowledge.Note, stop []string) string {
	var b strings.Builder
	b.WriteString(parser.SharedNotesOpen + "\n")
	b.WriteString(parser.SharedNotesHeader + " the user's shared notes for all their agents. Follow them from now on, in place of any earlier version. You don't need to reply about them.\n")
	for _, note := range add {
		b.WriteString("\n### " + note.Title + "\n\n" + strings.TrimSpace(note.Body) + "\n")
	}
	for _, title := range stop {
		b.WriteString("\nStop following the shared note \"" + title + "\": it no longer applies.\n")
	}
	b.WriteString(parser.SharedNotesClose)
	return b.String()
}

// withSharedNotes is attach for a session by ID.
func (d *Daemon) withSharedNotes(ctx context.Context, sessionID, text string) (string, func()) {
	d.mu.Lock()
	notes := d.sharedNotes
	session, known := d.sessions[sessionID]
	d.mu.Unlock()
	if notes == nil || !known {
		return text, func() {}
	}
	if _, ok := notesTargetFor(session); !ok {
		return text, func() {}
	}
	return notes.attach(session, func() int {
		if compactions, ok := d.registry.Compactions(ctx, sessionID); ok {
			return compactions
		}
		return -1
	}, text)
}
