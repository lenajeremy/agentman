// Package jsonl provides incremental reading of append-only JSONL transcripts.
//
// This is the piece that lets agentman keep zero server-side storage. Agent
// transcripts already live on the user's own disk, so we never copy them
// anywhere: we tail the end for live output and page backwards through the
// file when the user scrolls.
//
// Two properties of append-only files make this safe, and the whole design
// leans on them:
//
//  1. Byte offsets of existing content never change, so a cursor stays valid
//     even while the agent is actively writing. Paging backwards from a fixed
//     offset cannot be disturbed by appends.
//  2. '\n' never appears inside a multi-byte UTF-8 sequence, so lines can be
//     split on raw bytes and decoded only once a *complete* line is in hand.
//     That sidesteps split-character corruption at chunk boundaries entirely,
//     which is why this code works in []byte rather than string.
package jsonl

import (
	"bytes"
	"fmt"
	"io"
	"os"

	"github.com/lenajeremy/agentman/internal/protocol"
)

const (
	// DefaultChunk is the window size for backward reads. Large enough that a
	// typical page needs one read, small enough to stay off the heap profile.
	DefaultChunk = 64 * 1024

	// MaxLineBytes caps how much we will buffer hunting for a newline. A single
	// line can legitimately be enormous (a tool result holding a whole file),
	// but a corrupt or binary file must not be able to exhaust memory.
	MaxLineBytes = 32 * 1024 * 1024
)

// NoCursor is returned as NextCursor when a read reached the head of the file,
// so there is nothing further back to ask for.
const NoCursor int64 = -1

// MapFunc turns one raw line into zero or more normalized messages.
//
// Returning nothing skips the line. Filtering happens inside the read loop so
// a page comes back full of *useful* messages rather than however many survive
// a post-filter. One transcript record often expands to several messages (an
// assistant turn with text plus tool calls); those are counted individually
// against Want and stay in chronological order.
//
// offset is the line's byte position, used to mint stable IDs for records that
// carry no identifier of their own.
type MapFunc func(line string, offset int64) []protocol.Message

// BackwardOptions configures CollectBackward.
type BackwardOptions struct {
	// Before is an exclusive upper bound: only bytes in [0, *Before) are
	// considered. It must be a line-start offset, which is exactly what
	// NextCursor hands back. Nil starts from the end of the file.
	Before *int64
	// Want is how many mapped messages to collect before stopping.
	Want int
	Map  MapFunc
	// ChunkSize overrides DefaultChunk. Mainly useful for tests.
	ChunkSize int
}

// BackwardResult is one page of scrollback.
type BackwardResult struct {
	// Messages are in chronological order, even though we read right-to-left.
	Messages []protocol.Message
	// NextCursor is the start offset of the oldest message returned, to be
	// passed as Before to continue. NoCursor when nothing was found.
	NextCursor int64
	HasMore    bool
}

// CollectBackward walks backwards from Before, collecting up to Want messages.
//
// It reads fixed-size chunks right-to-left and only ever decodes complete
// lines. A line straddling a chunk boundary is carried in pending (always
// newline-terminated) and completed by the next chunk to its left.
func CollectBackward(path string, opts BackwardOptions) (BackwardResult, error) {
	chunk := opts.ChunkSize
	if chunk <= 0 {
		chunk = DefaultChunk
	}

	f, err := os.Open(path)
	if err != nil {
		return BackwardResult{NextCursor: NoCursor}, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return BackwardResult{NextCursor: NoCursor}, err
	}

	// Clamp rather than fail: a cursor can outlive a rotated or truncated
	// transcript, and an empty page is a better answer than an error.
	pos := info.Size()
	if opts.Before != nil && *opts.Before < pos {
		pos = *opts.Before
	}
	if pos < 0 {
		pos = 0
	}

	found := make([]protocol.Message, 0, opts.Want)
	oldest := NoCursor
	var pending []byte

	for pos > 0 && len(found) < opts.Want {
		readLen := min(int64(chunk), pos)
		start := pos - readLen

		buf := make([]byte, readLen)
		if _, err := f.ReadAt(buf, start); err != nil && err != io.EOF {
			return BackwardResult{NextCursor: NoCursor}, err
		}

		// combined covers absolute bytes [start, start+len(combined)).
		combined := buf
		if len(pending) > 0 {
			combined = make([]byte, 0, len(buf)+len(pending))
			combined = append(combined, buf...)
			combined = append(combined, pending...)
		}

		newlines := indexNewlines(combined)
		if len(newlines) == 0 {
			// No line ends in this window: the whole thing is the middle of a
			// very long line. Carry it left and keep hunting for its start.
			if len(combined) > MaxLineBytes {
				return BackwardResult{NextCursor: NoCursor}, fmt.Errorf(
					"jsonl: %s: line exceeds %d bytes at offset %d", path, MaxLineBytes, start)
			}
			pending = combined
			pos = start
			continue
		}

		// Lines fully contained between two newlines, emitted right-to-left so
		// we can stop the moment the page is full.
		for j := len(newlines) - 1; j >= 1 && len(found) < opts.Want; j-- {
			from := newlines[j-1] + 1
			to := newlines[j]
			abs := start + int64(from)
			if appendReversed(opts.Map(string(combined[from:to]), abs), &found) {
				oldest = abs
			}
		}

		// Bytes before the first newline belong to a line beginning further
		// left — unless we are at the head of the file, where it is complete.
		if start == 0 && len(found) < opts.Want {
			if appendReversed(opts.Map(string(combined[:newlines[0]]), 0), &found) {
				oldest = 0
			}
		}

		// Keeping the newline means pending always ends on a line boundary.
		// That invariant guarantees the trailing region of the next combined
		// window is empty, so no line is ever re-emitted or lost.
		pending = combined[:newlines[0]+1]
		pos = start
	}

	// Anything after the final newline on the first pass is a partial line the
	// agent is still writing. It is never emitted, so a half-written record
	// cannot surface as corrupt output.

	reverse(found)
	return BackwardResult{
		Messages:   found,
		NextCursor: oldest,
		HasMore:    oldest > 0,
	}, nil
}

// appendReversed adds mapped messages to the accumulator, reporting whether
// anything landed.
//
// We walk right-to-left, so a record expanding to several messages must be
// pushed in reverse: the final reverse() then restores their true chronological
// order relative to each other.
func appendReversed(msgs []protocol.Message, found *[]protocol.Message) bool {
	if len(msgs) == 0 {
		return false
	}
	for k := len(msgs) - 1; k >= 0; k-- {
		*found = append(*found, msgs[k])
	}
	return true
}

func reverse(msgs []protocol.Message) {
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
}

func indexNewlines(buf []byte) []int {
	var out []int
	for i := 0; ; {
		n := bytes.IndexByte(buf[i:], '\n')
		if n < 0 {
			return out
		}
		i += n
		out = append(out, i)
		i++
	}
}

// Line is a complete record produced by a Tail.
type Line struct {
	Text string
	// Offset is the absolute byte position of the line's first character.
	Offset int64
}

// Tail follows an append-only JSONL file forwards.
//
// It holds a byte offset and a carry buffer for a trailing partial line, so
// repeated Read calls yield each complete line exactly once no matter how the
// writer chunks its output.
type Tail struct {
	path   string
	offset int64
	carry  []byte
}

// NewTail creates a tail positioned at the start of the file.
func NewTail(path string) *Tail {
	return &Tail{path: path}
}

// Path returns the file being followed.
func (t *Tail) Path() string { return t.path }

// Offset returns the current read position.
func (t *Tail) Offset() int64 { return t.offset }

// SeekToEnd skips existing content so only future appends are reported. Used
// when attaching to a session that is already running, whose backlog the app
// will pull on demand instead.
func (t *Tail) SeekToEnd() error {
	info, err := os.Stat(t.path)
	if err != nil {
		return err
	}
	t.offset = info.Size()
	t.carry = nil
	return nil
}

// Read returns complete lines appended since the previous call, and nothing
// when the file is idle.
func (t *Tail) Read() ([]Line, error) {
	f, err := os.Open(t.path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := info.Size()

	// Shrinkage means the file was truncated or replaced, so every offset we
	// hold is meaningless. Restart from the top rather than emit garbage.
	if size < t.offset {
		t.offset = 0
		t.carry = nil
	}
	if size == t.offset {
		return nil, nil
	}

	buf := make([]byte, size-t.offset)
	if _, err := f.ReadAt(buf, t.offset); err != nil && err != io.EOF {
		return nil, err
	}

	combined := buf
	if len(t.carry) > 0 {
		combined = make([]byte, 0, len(t.carry)+len(buf))
		combined = append(combined, t.carry...)
		combined = append(combined, buf...)
	}
	// Absolute position of combined[0], accounting for the carried fragment.
	base := t.offset - int64(len(t.carry))

	var lines []Line
	lineStart := 0
	for {
		n := bytes.IndexByte(combined[lineStart:], '\n')
		if n < 0 {
			break
		}
		end := lineStart + n
		lines = append(lines, Line{
			Text:   string(combined[lineStart:end]),
			Offset: base + int64(lineStart),
		})
		lineStart = end + 1
	}

	rest := combined[lineStart:]
	if len(rest) > MaxLineBytes {
		return nil, fmt.Errorf("jsonl: %s: unterminated line exceeds %d bytes", t.path, MaxLineBytes)
	}
	t.carry = append([]byte(nil), rest...)
	t.offset = size

	return lines, nil
}
