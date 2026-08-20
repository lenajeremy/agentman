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
	"context"
	"fmt"
	"io"
	"os"

	"github.com/lenajeremy/agentman/internal/protocol"
)

const (
	// DefaultChunk is the window size for backward reads. Large enough that a
	// typical page needs one read, small enough to stay off the heap profile.
	DefaultChunk = 64 * 1024

	// DefaultScanBytes is the maximum amount of transcript data a backwards
	// page normally inspects. Filtering can otherwise turn a request for one
	// message into a scan of an arbitrarily large file when nothing matches.
	DefaultScanBytes int64 = 8 * 1024 * 1024

	// MaxBackwardScanBytes is a hard ceiling for caller-provided scan budgets.
	// A scan may finish the current line after crossing its softer budget, but
	// MaxLineBytes below still puts an absolute bound on that extra work.
	MaxBackwardScanBytes int64 = 64 * 1024 * 1024

	// MaxLineBytes caps how much we will buffer hunting for a newline. A single
	// line can legitimately be enormous (a tool result holding a whole file),
	// but a corrupt or binary file must not be able to exhaust memory.
	MaxLineBytes = 32 * 1024 * 1024

	// tailReadChunk bounds how much newly appended data one Tail.Read call can
	// allocate. Tool output can grow a transcript by hundreds of megabytes
	// between polls; allocating size-offset before inspecting it lets one turn
	// exhaust the daemon. Subsequent polls continue from the advanced offset.
	tailReadChunk = 1024 * 1024

	// maxCollectMessages is a defensive ceiling for callers of CollectBackward.
	// Public pagination applies a much smaller limit, but keeping a bound here is
	// important because Want is also used as a slice capacity. An unchecked
	// negative value panics and an absurd positive value can exhaust memory before
	// the transcript is read.
	maxCollectMessages = 1000

	// Custom chunks are useful for tests and tuning, but must not turn one read
	// into an unbounded allocation. Larger values offer no practical advantage
	// to a backwards line scanner.
	maxBackwardChunk = 1024 * 1024
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
	// MaxScanBytes bounds bytes read while looking for useful records. Zero uses
	// DefaultScanBytes. The scanner finishes at a safe line boundary, so the
	// actual count can exceed this by at most one bounded line. If the budget
	// stops a page early, HasMore and NextCursor identify the unscanned prefix
	// even when no messages matched.
	MaxScanBytes int64
	// ChunkSize overrides DefaultChunk. Mainly useful for tests.
	ChunkSize int
}

// BackwardResult is one page of scrollback.
type BackwardResult struct {
	// Messages are in chronological order, even though we read right-to-left.
	Messages []protocol.Message
	// NextCursor is a safe exclusive byte offset to pass back as Before. It is
	// normally the start of the earliest complete line inspected; it can also
	// skip a trailing partial record when the writer is mid-append. It can be
	// set even when no messages matched. NoCursor means the head was reached.
	NextCursor int64
	HasMore    bool
	// ScannedBytes is the physical file data read for this page. It is exposed
	// for diagnostics and performance regression tests.
	ScannedBytes int64
}

// CollectBackward walks backwards from Before, collecting up to Want messages.
//
// It reads fixed-size chunks right-to-left and only ever decodes complete
// lines. A line straddling chunk boundaries is retained as fragments and
// assembled once when its start is found.
func CollectBackward(path string, opts BackwardOptions) (BackwardResult, error) {
	return CollectBackwardContext(context.Background(), path, opts)
}

// CollectBackwardContext is CollectBackward with caller-controlled
// cancellation. File reads themselves are local and bounded; the context is
// checked before and after each read and each mapped line.
func CollectBackwardContext(ctx context.Context, path string, opts BackwardOptions) (BackwardResult, error) {
	if opts.Want <= 0 || opts.Want > maxCollectMessages {
		return BackwardResult{NextCursor: NoCursor}, fmt.Errorf(
			"jsonl: message count must be between 1 and %d", maxCollectMessages)
	}
	if opts.Map == nil {
		return BackwardResult{NextCursor: NoCursor}, fmt.Errorf("jsonl: map function is required")
	}
	if err := ctx.Err(); err != nil {
		return BackwardResult{NextCursor: NoCursor}, err
	}

	maxScan := opts.MaxScanBytes
	if maxScan == 0 {
		maxScan = DefaultScanBytes
	}
	if maxScan < 0 || maxScan > MaxBackwardScanBytes {
		return BackwardResult{NextCursor: NoCursor}, fmt.Errorf(
			"jsonl: scan budget must be zero or between 1 and %d bytes", MaxBackwardScanBytes)
	}

	chunk := opts.ChunkSize
	if chunk <= 0 {
		chunk = DefaultChunk
	}
	if chunk > maxBackwardChunk {
		chunk = maxBackwardChunk
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
	upperBound := pos

	found := make([]protocol.Message, 0, opts.Want)
	cursor := NoCursor
	var scanned int64

	// Fragments hold one line that crosses chunk boundaries. They are retained
	// as slices in right-to-left discovery order and copied only once, when the
	// line's left boundary is found. Prepending each new chunk to one growing
	// pending buffer copied the entire line on every read and was O(n²).
	var fragments [][]byte
	fragmentBytes := 0
	haveRightBoundary := false
	trailingBytes := 0

	for pos > 0 && len(found) < opts.Want {
		if err := ctx.Err(); err != nil {
			return BackwardResult{NextCursor: NoCursor}, err
		}

		readLen := min(int64(chunk), pos)
		if remaining := maxScan - scanned; remaining > 0 && readLen > remaining {
			readLen = remaining
		}
		start := pos - readLen

		buf := make([]byte, readLen)
		if _, err := f.ReadAt(buf, start); err != nil && err != io.EOF {
			return BackwardResult{NextCursor: NoCursor}, err
		}
		scanned += readLen
		if err := ctx.Err(); err != nil {
			return BackwardResult{NextCursor: NoCursor}, err
		}

		end := len(buf)
		for end > 0 {
			if err := ctx.Err(); err != nil {
				return BackwardResult{NextCursor: NoCursor}, err
			}
			newline := bytes.LastIndexByte(buf[:end], '\n')
			if newline < 0 {
				break
			}

			if !haveRightBoundary {
				// At the live end of a file, bytes after the last newline are a
				// record the writer has not completed. Discard them, but still
				// account for them so an unterminated record stays memory/work
				// bounded.
				trailingBytes += end - newline - 1
				if trailingBytes > MaxLineBytes {
					return BackwardResult{NextCursor: NoCursor}, lineTooLong(path, start)
				}
				haveRightBoundary = true
				trailingBytes = 0
				end = newline
				// If reaching this newline discarded a partial record, it is a
				// safe and strictly earlier continuation point by itself. Do not
				// scan another potentially huge line merely to produce a cursor.
				partialStart := start + int64(newline+1)
				if scanned >= maxScan && partialStart > 0 && partialStart < upperBound {
					return finishBackward(found, partialStart, scanned, true), nil
				}
				continue
			}

			lineStart := start + int64(newline+1)
			line, err := assembleBackwardLine(buf[newline+1:end], fragments, fragmentBytes)
			if err != nil {
				return BackwardResult{NextCursor: NoCursor}, fmt.Errorf(
					"jsonl: %s: line exceeds %d bytes at offset %d", path, MaxLineBytes, lineStart)
			}
			appendReversed(opts.Map(string(line), lineStart), &found)
			if err := ctx.Err(); err != nil {
				return BackwardResult{NextCursor: NoCursor}, err
			}
			cursor = lineStart
			fragments = nil
			fragmentBytes = 0
			end = newline

			if len(found) >= opts.Want || scanned >= maxScan {
				return finishBackward(found, cursor, scanned, cursor > 0), nil
			}
		}

		if !haveRightBoundary {
			trailingBytes += end
			if trailingBytes > MaxLineBytes {
				return BackwardResult{NextCursor: NoCursor}, lineTooLong(path, start)
			}
		} else if end > 0 {
			if end > MaxLineBytes-fragmentBytes {
				return BackwardResult{NextCursor: NoCursor}, lineTooLong(path, start)
			}
			fragments = append(fragments, buf[:end])
			fragmentBytes += end
		}
		pos = start

		// Once at least one complete line has established a safe continuation
		// cursor, do not keep reading an earlier partial line merely to fill the
		// page. The next request resumes from cursor without skipping it.
		if scanned >= maxScan && cursor > 0 {
			return finishBackward(found, cursor, scanned, true), nil
		}

		if pos == 0 && haveRightBoundary {
			line, err := assembleBackwardLine(nil, fragments, fragmentBytes)
			if err != nil {
				return BackwardResult{NextCursor: NoCursor}, lineTooLong(path, 0)
			}
			appendReversed(opts.Map(string(line), 0), &found)
			if err := ctx.Err(); err != nil {
				return BackwardResult{NextCursor: NoCursor}, err
			}
			cursor = 0
		}
	}

	// Anything after the final newline on the first pass is a partial line the
	// agent is still writing. It is never emitted, so a half-written record
	// cannot surface as corrupt output.

	return finishBackward(found, NoCursor, scanned, false), nil
}

func assembleBackwardLine(prefix []byte, fragments [][]byte, fragmentBytes int) ([]byte, error) {
	if len(prefix) > MaxLineBytes-fragmentBytes {
		return nil, fmt.Errorf("line too long")
	}
	if len(fragments) == 0 {
		return prefix, nil
	}
	line := make([]byte, 0, len(prefix)+fragmentBytes)
	line = append(line, prefix...)
	for i := len(fragments) - 1; i >= 0; i-- {
		line = append(line, fragments[i]...)
	}
	return line, nil
}

func lineTooLong(path string, offset int64) error {
	return fmt.Errorf("jsonl: %s: line exceeds %d bytes at offset %d", path, MaxLineBytes, offset)
}

func finishBackward(found []protocol.Message, cursor, scanned int64, hasMore bool) BackwardResult {
	reverse(found)
	if !hasMore {
		cursor = NoCursor
	}
	return BackwardResult{
		Messages:     found,
		NextCursor:   cursor,
		HasMore:      hasMore,
		ScannedBytes: scanned,
	}
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
	path     string
	offset   int64
	carry    []byte
	identity os.FileInfo
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
	t.identity = info
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

	// A path can be atomically replaced with a different file whose size is
	// equal to or larger than the old offset. Size-only rotation detection then
	// skips its prefix—or the whole replacement. File identity catches that
	// case; shrinkage still catches in-place truncation of the same inode.
	replaced := t.identity != nil && !os.SameFile(t.identity, info)
	if replaced || size < t.offset {
		t.offset = 0
		t.carry = nil
	}
	t.identity = info
	if size == t.offset {
		return nil, nil
	}

	readLen := min(size-t.offset, int64(tailReadChunk))
	buf := make([]byte, readLen)
	if _, err := f.ReadAt(buf, t.offset); err != nil && err != io.EOF {
		return nil, err
	}
	nextOffset := t.offset + readLen

	var lines []Line
	lineStart := 0
	lineOffset := t.offset

	// Complete a carried line directly from the new buffer. When no newline is
	// present, append in place and let slice capacity grow geometrically. The
	// old carry+buf reconstruction copied the whole accumulated line on every
	// poll, making a long tool-result record quadratic in bytes copied.
	if len(t.carry) > 0 {
		newline := bytes.IndexByte(buf, '\n')
		if newline < 0 {
			if len(buf) > MaxLineBytes-len(t.carry) {
				return nil, fmt.Errorf("jsonl: %s: unterminated line exceeds %d bytes", t.path, MaxLineBytes)
			}
			t.carry = append(t.carry, buf...)
			t.offset = nextOffset
			return nil, nil
		}
		if newline > MaxLineBytes-len(t.carry) {
			return nil, fmt.Errorf("jsonl: %s: line exceeds %d bytes at offset %d",
				t.path, MaxLineBytes, t.offset-int64(len(t.carry)))
		}
		first := make([]byte, 0, len(t.carry)+newline)
		first = append(first, t.carry...)
		first = append(first, buf[:newline]...)
		lines = append(lines, Line{
			Text:   string(first),
			Offset: t.offset - int64(len(t.carry)),
		})
		t.carry = nil
		lineStart = newline + 1
		lineOffset = t.offset + int64(lineStart)
	}

	for {
		n := bytes.IndexByte(buf[lineStart:], '\n')
		if n < 0 {
			break
		}
		end := lineStart + n
		if end-lineStart > MaxLineBytes {
			return nil, fmt.Errorf("jsonl: %s: line exceeds %d bytes at offset %d",
				t.path, MaxLineBytes, lineOffset)
		}
		lines = append(lines, Line{
			Text:   string(buf[lineStart:end]),
			Offset: lineOffset,
		})
		lineStart = end + 1
		lineOffset = t.offset + int64(lineStart)
	}

	rest := buf[lineStart:]
	if len(rest) > MaxLineBytes {
		return nil, fmt.Errorf("jsonl: %s: unterminated line exceeds %d bytes", t.path, MaxLineBytes)
	}
	t.carry = append([]byte(nil), rest...)
	t.offset = nextOffset

	return lines, nil
}
