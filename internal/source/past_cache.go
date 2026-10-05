package source

import (
	"container/list"
	"os"
	"sync"
	"time"
)

// maxPastFacts bounds what is remembered per adapter. Codex lists folders by
// reading up to codexHistoryScanFiles rollout headers, so the bound sits above
// that, or every folder listing would evict what the last one read.
const maxPastFacts = 5000

// pastFacts remembers what a history listing read from each transcript: its
// directory, name and model. Those take a read of the head and the tail of a
// file that can be hundreds of megabytes, and a past session's file rarely
// changes, so they are kept until its size or modification time does.
//
// This is apart from what discovery remembers about running sessions, which
// each sweep drops once a session stops running. Sharing that memory meant
// every listing of a folder read every ended transcript again.
type pastFacts[T any] struct {
	mu      sync.Mutex
	entries map[string]*list.Element
	// order runs from the most to the least recently used.
	order list.List
}

type pastFact[T any] struct {
	path  string
	size  int64
	mod   time.Time
	value T
}

// get returns what was read from path, if the file is as it was then.
func (c *pastFacts[T]) get(path string, info os.FileInfo) (T, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	element, ok := c.entries[path]
	if !ok {
		var zero T
		return zero, false
	}
	fact := element.Value.(*pastFact[T])
	if fact.size != info.Size() || !fact.mod.Equal(info.ModTime()) {
		var zero T
		return zero, false
	}
	c.order.MoveToFront(element)
	return fact.value, true
}

// put records what was read from path as it is described by info.
func (c *pastFacts[T]) put(path string, info os.FileInfo, value T) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]*list.Element{}
	}
	fact := &pastFact[T]{path: path, size: info.Size(), mod: info.ModTime(), value: value}
	if element, ok := c.entries[path]; ok {
		element.Value = fact
		c.order.MoveToFront(element)
		return
	}
	c.entries[path] = c.order.PushFront(fact)
	for c.order.Len() > maxPastFacts {
		oldest := c.order.Back()
		c.order.Remove(oldest)
		delete(c.entries, oldest.Value.(*pastFact[T]).path)
	}
}
