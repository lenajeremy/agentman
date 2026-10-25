package source

import (
	"bytes"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
)

// heldSends follows messages Agentman sent to an agent that reads a message
// only after its current step, until the agent's transcript shows each read.
//
// Codex is the case: a message typed into a running turn is held until the
// tool call under way finishes, then written to the rollout as the user
// message it is. Nothing records it while it waits, so the phone's own sends
// are all there is to show. A message typed at the Mac is not seen until it
// is read.
type heldSends struct {
	mu    sync.Mutex
	tails map[string]*heldTail
	// userText is the text of the user message a transcript line records,
	// or "" for any other line.
	userText func(line []byte) string
}

type heldTail struct {
	path   string
	offset int64
	// partial is a line the last read ended in the middle of.
	partial []byte
	waiting []string
}

func newHeldSends(userText func(line []byte) string) *heldSends {
	return &heldSends{tails: map[string]*heldTail{}, userText: userText}
}

// add records a message sent to sessionID while its agent was working.
// Only what the transcript gains from here on can show it read.
func (h *heldSends) add(sessionID, path, text string) {
	text = strings.TrimSpace(text)
	if h == nil || text == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	tail := h.tails[sessionID]
	if tail == nil || tail.path != path {
		tail = &heldTail{path: path, offset: fileSize(path)}
		h.tails[sessionID] = tail
	}
	tail.waiting = append(tail.waiting, text)
}

// pending reports the messages still waiting, oldest first, reading whatever
// the transcript gained since the last call. When the agent is not working
// its turn is over and everything was read, so the list is cleared.
func (h *heldSends) pending(sessionID, path string, working bool) []string {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	tail := h.tails[sessionID]
	if tail == nil {
		return nil
	}
	if tail.path == "" && path != "" {
		// The transcript began after the message was sent: all of it is new.
		tail.path, tail.offset = path, 0
	}
	for _, line := range tail.appended() {
		if !bytes.Contains(line, []byte("UserMessage")) && !bytes.Contains(line, []byte(`"user"`)) {
			continue
		}
		read := strings.TrimSpace(h.userText(line))
		if read == "" {
			continue
		}
		tail.waiting = slices.DeleteFunc(tail.waiting, func(waiting string) bool {
			return strings.Contains(read, waiting)
		})
	}
	if !working {
		tail.waiting = nil
	}
	if len(tail.waiting) == 0 {
		return nil
	}
	return slices.Clone(tail.waiting)
}

// forget drops sessions that are no longer running.
func (h *heldSends) forget(live map[string]bool) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for id := range h.tails {
		if !live[id] {
			delete(h.tails, id)
		}
	}
}

// appended returns the complete lines the file gained since the last call.
func (t *heldTail) appended() [][]byte {
	if t.path == "" {
		return nil
	}
	file, err := os.Open(t.path)
	if err != nil {
		return nil
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil
	}
	size := info.Size()
	if size < t.offset {
		// Replaced: only what is written from now on is new.
		t.offset, t.partial = size, nil
		return nil
	}
	if size == t.offset {
		return nil
	}
	buf := make([]byte, min(size-t.offset, queueReadLimit))
	read, err := file.ReadAt(buf, t.offset)
	if err != nil && err != io.EOF {
		return nil
	}
	t.offset += int64(read)
	lines := bytes.Split(append(t.partial, buf[:read]...), []byte{'\n'})
	t.partial = append([]byte(nil), lines[len(lines)-1]...)
	return lines[:len(lines)-1]
}

func fileSize(path string) int64 {
	if path == "" {
		return 0
	}
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}
