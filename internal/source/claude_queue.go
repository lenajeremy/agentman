package source

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
)

// claudeQueue follows the messages Claude Code is holding for each session:
// what someone sent while it was working, which it reads when its current
// step ends.
//
// Claude Code writes each one to the transcript as a "queue-operation"
// record: "enqueue" with the text when it is sent, then "remove" with the
// text when it is taken in the middle of a turn or deleted, or "dequeue",
// with no text, when it is read at the end of one. The queue also carries
// Claude Code's own traffic, such as background task notifications, and
// dequeue does not take strictly the oldest item, so replaying the records
// alone drifts: a message read long ago would still show as waiting.
//
// What settles a message is the transcript itself. Once read, it appears as
// a user record, or as a "queued_command" attachment when it was taken mid-
// turn, and either one containing its text clears it. A dequeue followed by a
// user record that contains none of them, and is not Claude's own
// notification, read the oldest one in some other form, so that one goes.
//
// Each transcript is read from where the last call stopped, so a busy
// session costs the bytes it wrote since the previous second, not a re-read.
type claudeQueue struct {
	mu    sync.Mutex
	tails map[string]*queueTail
}

type queueTail struct {
	path   string
	offset int64
	// partial is a line the last read ended in the middle of.
	partial []byte
	// waiting are the user's own messages still in the queue, oldest first.
	waiting []string
	// dequeues counts dequeue records whose message has not appeared yet.
	dequeues int
}

// queueStartWindow is how far back a transcript is read the first time it is
// seen. A queue only lives while a turn runs, so anything older has long been
// read; the window just has to reach back past the start of the current turn.
const queueStartWindow = 1 << 20

// queueReadLimit bounds one call's read, so a transcript that grew by a huge
// tool result does not stall discovery. The rest is read on the next call.
const queueReadLimit = 4 << 20

// machineQueuePrefixes mark queue items Claude Code wrote for itself rather
// than the user: a background task finishing, or a message from one of its
// own agents. They go through the same queue but are not something anyone sent.
var machineQueuePrefixes = []string{"<task-notification", "<agent-message"}

func newClaudeQueue() *claudeQueue {
	return &claudeQueue{tails: map[string]*queueTail{}}
}

// pending reports the user's messages still waiting in sessionID's queue,
// oldest first, reading whatever the transcript gained since the last call.
// working says whether the agent is mid-turn. When it is not, the queue has
// been read, so whatever replay still holds was missed and is dropped.
func (q *claudeQueue) pending(sessionID, path string, working bool) []string {
	if q == nil || path == "" {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	tail := q.tails[sessionID]
	if tail == nil || tail.path != path {
		tail = &queueTail{path: path, offset: -1}
		q.tails[sessionID] = tail
	}
	tail.advance()
	if !working {
		tail.waiting, tail.dequeues = nil, 0
		return nil
	}
	if len(tail.waiting) == 0 {
		return nil
	}
	return slices.Clone(tail.waiting)
}

// forget drops sessions that are no longer running.
func (q *claudeQueue) forget(live map[string]bool) {
	if q == nil {
		return
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	for id := range q.tails {
		if !live[id] {
			delete(q.tails, id)
		}
	}
}

// advance reads what the transcript gained and replays its queue records.
func (t *queueTail) advance() {
	file, err := os.Open(t.path)
	if err != nil {
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return
	}
	size := info.Size()
	if t.offset < 0 || size < t.offset {
		// First sight, or the file was replaced: start a window back, at the
		// beginning of a line, with nothing replayed yet.
		t.offset = max(0, size-queueStartWindow)
		t.partial = nil
		t.waiting, t.dequeues = nil, 0
		if t.offset > 0 {
			t.partial = []byte{} // discard up to the first newline
		}
	}
	if size == t.offset {
		return
	}
	n := min(size-t.offset, queueReadLimit)
	buf := make([]byte, n)
	read, err := file.ReadAt(buf, t.offset)
	if err != nil && err != io.EOF {
		return
	}
	buf = buf[:read]
	t.offset += int64(read)

	skipFirst := t.partial != nil && len(t.partial) == 0
	data := append(t.partial, buf...)
	lines := bytes.Split(data, []byte{'\n'})
	// The last piece has no newline after it yet; keep it for next time.
	t.partial = append([]byte(nil), lines[len(lines)-1]...)
	lines = lines[:len(lines)-1]
	if skipFirst && len(lines) > 0 {
		lines = lines[1:]
	}
	for _, line := range lines {
		t.apply(line)
	}
}

// queueRecord is the part of a queue-operation record the replay needs.
type queueRecord struct {
	Type      string          `json:"type"`
	Operation string          `json:"operation"`
	Content   json.RawMessage `json:"content"`
}

// readRecord is the part of a user or attachment record that can show a
// waiting message has been read.
type readRecord struct {
	Type    string `json:"type"`
	IsMeta  bool   `json:"isMeta"`
	Message *struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	Attachment *struct {
		Type   string          `json:"type"`
		Prompt json.RawMessage `json:"prompt"`
	} `json:"attachment"`
}

func (t *queueTail) apply(line []byte) {
	if bytes.Contains(line, []byte(`"queue-operation"`)) {
		t.applyQueue(line)
		return
	}
	// Only a record that could have read something is worth decoding, and
	// only while something is waiting: user records carry whole tool results.
	if len(t.waiting) == 0 && t.dequeues == 0 {
		return
	}
	if !bytes.Contains(line, []byte(`"user"`)) && !bytes.Contains(line, []byte(`"queued_command"`)) {
		return
	}
	var rec readRecord
	if json.Unmarshal(line, &rec) != nil {
		return
	}
	var text string
	fromUser := false
	switch {
	case rec.Type == "user" && !rec.IsMeta && rec.Message != nil:
		text, fromUser = promptText(rec.Message.Content), true
	case rec.Type == "attachment" && rec.Attachment != nil && rec.Attachment.Type == "queued_command":
		text = promptText(rec.Attachment.Prompt)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return // a tool result, or nothing anyone wrote
	}
	matched := false
	kept := t.waiting[:0]
	for _, waiting := range t.waiting {
		if strings.Contains(text, waiting) {
			matched = true
			continue
		}
		kept = append(kept, waiting)
	}
	t.waiting = kept
	if fromUser && t.dequeues > 0 {
		t.dequeues--
		if !matched && len(t.waiting) > 0 && humanQueued(text) {
			t.waiting = t.waiting[1:]
		}
	}
}

func (t *queueTail) applyQueue(line []byte) {
	var rec queueRecord
	if json.Unmarshal(line, &rec) != nil || rec.Type != "queue-operation" {
		return
	}
	var text string
	isText := json.Unmarshal(rec.Content, &text) == nil
	text = strings.TrimSpace(text)
	switch rec.Operation {
	case "enqueue":
		if isText && humanQueued(text) {
			t.waiting = append(t.waiting, text)
		}
	case "dequeue":
		t.dequeues++
	case "remove":
		if i := slices.Index(t.waiting, text); isText && i >= 0 {
			t.waiting = slices.Delete(t.waiting, i, i+1)
		}
	}
}

// promptText is what a user wrote, from a string or from content blocks;
// tool results and images contribute nothing.
func promptText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var plain string
	if json.Unmarshal(raw, &plain) == nil {
		return plain
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	var parts []string
	for _, block := range blocks {
		if block.Type == "text" && block.Text != "" {
			parts = append(parts, block.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func humanQueued(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	for _, prefix := range machineQueuePrefixes {
		if strings.HasPrefix(trimmed, prefix) {
			return false
		}
	}
	return true
}
