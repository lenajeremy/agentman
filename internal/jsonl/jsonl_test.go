package jsonl

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// writeFixture builds a newline-terminated JSONL file from raw line strings.
func writeFixture(t *testing.T, lines []string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "t.jsonl")
	body := ""
	if len(lines) > 0 {
		body = strings.Join(lines, "\n") + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func numbered(n int) []string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = fmt.Sprintf(`{"n":%d}`, i)
	}
	return lines
}

// asMessage maps a {"n":N} record onto a Message, stashing N in Text so tests
// can assert ordering without a real parser.
func asMessage(line string, offset int64) []protocol.Message {
	var rec struct {
		N   *int   `json:"n"`
		Pad string `json:"pad"`
	}
	if err := json.Unmarshal([]byte(line), &rec); err != nil || rec.N == nil {
		return nil
	}
	return []protocol.Message{{
		ID:   fmt.Sprintf("m%d", *rec.N),
		Text: fmt.Sprintf("%d", *rec.N),
		Ts:   offset,
	}}
}

func texts(msgs []protocol.Message) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.Text
	}
	return out
}

func ptr(v int64) *int64 { return &v }

/* ---------------------------- backward paging ---------------------------- */

func TestCollectBackwardReturnsNewestInChronologicalOrder(t *testing.T) {
	path := writeFixture(t, numbered(10))

	got, err := CollectBackward(path, BackwardOptions{Want: 3, Map: asMessage})
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"7", "8", "9"}
	if diff := strings.Join(texts(got.Messages), ","); diff != strings.Join(want, ",") {
		t.Fatalf("got %v, want %v", texts(got.Messages), want)
	}
	if !got.HasMore {
		t.Fatal("expected more pages to be available")
	}
}

func TestPagingBackwardsIsContiguousAndNeverOverlaps(t *testing.T) {
	const total = 250
	path := writeFixture(t, numbered(total))

	var seen []string
	var cursor *int64
	for i := range 101 {
		if i == 100 {
			t.Fatal("paging failed to terminate")
		}
		page, err := CollectBackward(path, BackwardOptions{Want: 40, Before: cursor, Map: asMessage})
		if err != nil {
			t.Fatal(err)
		}
		seen = append(texts(page.Messages), seen...)
		if !page.HasMore {
			break
		}
		cursor = ptr(page.NextCursor)
	}

	// Every record exactly once, in order — the property scrollback depends on.
	if len(seen) != total {
		t.Fatalf("saw %d records, want %d", len(seen), total)
	}
	for i, v := range seen {
		if v != fmt.Sprintf("%d", i) {
			t.Fatalf("record %d out of order: got %q", i, v)
		}
	}
}

func TestPageIsFilledWithUsefulItems(t *testing.T) {
	// Interleave records the parser rejects; a page should still come back full
	// rather than mostly empty after filtering.
	var lines []string
	for i := 0; i < 100; i++ {
		lines = append(lines, `{"noise":true}`, fmt.Sprintf(`{"n":%d}`, i))
	}
	path := writeFixture(t, lines)

	got, err := CollectBackward(path, BackwardOptions{Want: 10, Map: asMessage})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Messages) != 10 {
		t.Fatalf("got %d messages, want a full page of 10", len(got.Messages))
	}
	if got.Messages[0].Text != "90" || got.Messages[9].Text != "99" {
		t.Fatalf("unexpected window: %v", texts(got.Messages))
	}
}

func TestLinesStraddlingChunkBoundariesSurvive(t *testing.T) {
	lines := make([]string, 60)
	for i := range lines {
		lines[i] = fmt.Sprintf(`{"n":%d,"pad":%q}`, i, strings.Repeat("x", i*7))
	}
	path := writeFixture(t, lines)

	// A deliberately tiny chunk forces constant boundary crossings.
	got, err := CollectBackward(path, BackwardOptions{Want: 60, ChunkSize: 16, Map: asMessage})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Messages) != 60 {
		t.Fatalf("got %d messages, want 60", len(got.Messages))
	}
	for i, m := range got.Messages {
		if m.Text != fmt.Sprintf("%d", i) {
			t.Fatalf("line %d corrupted: %q", i, m.Text)
		}
	}
}

func TestMultiByteCharactersSplitAcrossChunks(t *testing.T) {
	// The reason the reader works in bytes: naive string chunking mangles these.
	const text = "日本語のテキスト🎉 émoji"
	lines := make([]string, 40)
	for i := range lines {
		lines[i] = fmt.Sprintf(`{"n":%d,"pad":%q}`, i, text)
	}
	path := writeFixture(t, lines)

	got, err := CollectBackward(path, BackwardOptions{
		Want: 40, ChunkSize: 7, // small enough to split characters constantly
		Map: func(line string, offset int64) []protocol.Message {
			var rec struct {
				N   *int   `json:"n"`
				Pad string `json:"pad"`
			}
			if err := json.Unmarshal([]byte(line), &rec); err != nil || rec.N == nil {
				return nil
			}
			if rec.Pad != text {
				t.Errorf("line %d: text corrupted across chunk boundary: %q", *rec.N, rec.Pad)
			}
			return []protocol.Message{{ID: "x", Text: rec.Pad}}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Messages) != 40 {
		t.Fatalf("got %d messages, want 40", len(got.Messages))
	}
}

func TestSingleLineLargerThanChunk(t *testing.T) {
	big := strings.Repeat("y", 200_000)
	path := writeFixture(t, []string{
		`{"n":0}`,
		fmt.Sprintf(`{"n":1,"pad":%q}`, big),
		`{"n":2}`,
	})

	got, err := CollectBackward(path, BackwardOptions{Want: 3, ChunkSize: 4096, Map: asMessage})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"0", "1", "2"}; strings.Join(texts(got.Messages), ",") != strings.Join(want, ",") {
		t.Fatalf("got %v, want %v", texts(got.Messages), want)
	}
}

func TestHalfWrittenTrailingLineIsNeverEmitted(t *testing.T) {
	path := writeFixture(t, []string{`{"n":0}`, `{"n":1}`})
	appendTo(t, path, `{"n":2,"incomp`) // agent mid-write

	got, err := CollectBackward(path, BackwardOptions{Want: 10, Map: asMessage})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"0", "1"}; strings.Join(texts(got.Messages), ",") != strings.Join(want, ",") {
		t.Fatalf("got %v, want %v", texts(got.Messages), want)
	}
}

func TestAppendsDoNotDisturbAnInFlightCursor(t *testing.T) {
	path := writeFixture(t, numbered(100))

	first, err := CollectBackward(path, BackwardOptions{Want: 20, Map: asMessage})
	if err != nil {
		t.Fatal(err)
	}
	if first.Messages[0].Text != "80" {
		t.Fatalf("unexpected first page: %v", texts(first.Messages))
	}

	// The agent keeps working while the user scrolls up.
	var more []string
	for i := 100; i < 105; i++ {
		more = append(more, fmt.Sprintf(`{"n":%d}`, i))
	}
	appendTo(t, path, strings.Join(more, "\n")+"\n")

	second, err := CollectBackward(path, BackwardOptions{
		Want: 20, Before: ptr(first.NextCursor), Map: asMessage,
	})
	if err != nil {
		t.Fatal(err)
	}
	// Still the 20 records before the cursor: no skips, no duplicates.
	if second.Messages[0].Text != "60" || second.Messages[19].Text != "79" {
		t.Fatalf("cursor disturbed by concurrent append: %v", texts(second.Messages))
	}
}

func TestEmptyAndSingleLineFiles(t *testing.T) {
	empty, err := CollectBackward(writeFixture(t, nil), BackwardOptions{Want: 5, Map: asMessage})
	if err != nil {
		t.Fatal(err)
	}
	if len(empty.Messages) != 0 || empty.HasMore {
		t.Fatalf("empty file should yield nothing, got %v", empty)
	}

	one, err := CollectBackward(writeFixture(t, numbered(1)), BackwardOptions{Want: 5, Map: asMessage})
	if err != nil {
		t.Fatal(err)
	}
	if len(one.Messages) != 1 {
		t.Fatalf("got %d messages, want 1", len(one.Messages))
	}
	if one.HasMore {
		t.Fatal("reaching the head of the file means no more pages")
	}
}

func TestReadingDoesNotScanWholeFile(t *testing.T) {
	// Guards the property the zero-storage design depends on: opening a huge
	// transcript must not cost a full read.
	lines := make([]string, 40_000)
	for i := range lines {
		lines[i] = fmt.Sprintf(`{"n":%d,"pad":%q}`, i, strings.Repeat("z", 1000))
	}
	path := writeFixture(t, lines)

	var decoded int
	got, err := CollectBackward(path, BackwardOptions{
		Want: 10,
		Map: func(line string, offset int64) []protocol.Message {
			decoded += len(line)
			return asMessage(line, offset)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Messages) != 10 || got.Messages[9].Text != "39999" {
		t.Fatalf("unexpected tail window: %v", texts(got.Messages))
	}
	// ~10 lines of ~1KB, not the 40MB the file holds.
	if decoded > 256*1024 {
		t.Fatalf("expected to touch only the tail, decoded %d bytes of a 40MB file", decoded)
	}
}

func TestBackwardReadRejectsNewlineTerminatedOversizedLine(t *testing.T) {
	path := writeFixture(t, []string{strings.Repeat("x", MaxLineBytes+1)})
	if _, err := CollectBackward(path, BackwardOptions{Want: 1, Map: asMessage}); err == nil {
		t.Fatal("newline-terminated oversized line bypassed the memory bound")
	}
}

/* ------------------------------- forward tail ---------------------------- */

func TestTailYieldsEachLineExactlyOnce(t *testing.T) {
	path := writeFixture(t, numbered(1))
	tail := NewTail(path)

	lines := mustRead(t, tail)
	if len(lines) != 1 || lines[0].Text != `{"n":0}` {
		t.Fatalf("unexpected first read: %v", lines)
	}
	if idle := mustRead(t, tail); len(idle) != 0 {
		t.Fatalf("idle read should yield nothing, got %v", idle)
	}

	appendTo(t, path, "{\"n\":1}\n{\"n\":2}\n")
	lines = mustRead(t, tail)
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}
}

func TestTailHoldsBackPartialLine(t *testing.T) {
	path := writeFixture(t, nil)
	tail := NewTail(path)

	appendTo(t, path, "{\"n\":0}\n{\"n\":")
	lines := mustRead(t, tail)
	if len(lines) != 1 {
		t.Fatalf("the partial second line must be withheld, got %d lines", len(lines))
	}

	appendTo(t, path, "1}\n")
	lines = mustRead(t, tail)
	if len(lines) != 1 || lines[0].Text != `{"n":1}` {
		t.Fatalf("completed line not emitted correctly: %v", lines)
	}
}

func TestTailOffsetsWorkAsBackwardCursors(t *testing.T) {
	path := writeFixture(t, numbered(5))
	tail := NewTail(path)
	lines := mustRead(t, tail)

	// Feed a tailed line's offset straight back in as a page boundary.
	got, err := CollectBackward(path, BackwardOptions{
		Want: 10, Before: ptr(lines[3].Offset), Map: asMessage,
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"0", "1", "2"}; strings.Join(texts(got.Messages), ",") != strings.Join(want, ",") {
		t.Fatalf("got %v, want %v", texts(got.Messages), want)
	}
}

func TestTailRestartsWhenFileTruncated(t *testing.T) {
	path := writeFixture(t, numbered(2))
	tail := NewTail(path)
	mustRead(t, tail)

	if err := os.Truncate(path, 0); err != nil {
		t.Fatal(err)
	}
	appendTo(t, path, "{\"n\":9}\n")

	lines := mustRead(t, tail)
	if len(lines) != 1 || lines[0].Text != `{"n":9}` {
		t.Fatalf("a shrunken file must reset rather than emit garbage, got %v", lines)
	}
}

func TestTailSeekToEndSkipsBacklog(t *testing.T) {
	path := writeFixture(t, numbered(100))
	tail := NewTail(path)
	if err := tail.SeekToEnd(); err != nil {
		t.Fatal(err)
	}

	if lines := mustRead(t, tail); len(lines) != 0 {
		t.Fatalf("backlog should be skipped, got %d lines", len(lines))
	}
	appendTo(t, path, "{\"n\":100}\n")
	if lines := mustRead(t, tail); len(lines) != 1 {
		t.Fatalf("new appends should still arrive, got %d lines", len(lines))
	}
}

func TestTailBoundsEachReadWhenTranscriptGrowsSuddenly(t *testing.T) {
	path := writeFixture(t, nil)
	// Enough complete records to exceed several read windows. The exact line
	// content is unimportant; this is a regression test for allocating the
	// entire file growth in one shot.
	record := strings.Repeat("x", 1023) + "\n"
	appendTo(t, path, strings.Repeat(record, tailReadChunk/len(record)*3))

	tail := NewTail(path)
	var total int
	previous := int64(0)
	for tail.Offset() < int64(tailReadChunk*3) {
		lines := mustRead(t, tail)
		total += len(lines)
		advanced := tail.Offset() - previous
		if advanced <= 0 || advanced > tailReadChunk {
			t.Fatalf("one read advanced %d bytes, want 1..%d", advanced, tailReadChunk)
		}
		previous = tail.Offset()
	}
	if total != tailReadChunk/len(record)*3 {
		t.Fatalf("tailed %d records, want %d", total, tailReadChunk/len(record)*3)
	}
}

func TestTailRejectsNewlineTerminatedOversizedLine(t *testing.T) {
	path := writeFixture(t, []string{strings.Repeat("x", MaxLineBytes+1)})
	tail := NewTail(path)
	for tail.Offset() < MaxLineBytes {
		if _, err := tail.Read(); err != nil {
			return
		}
	}
	if _, err := tail.Read(); err == nil {
		t.Fatal("newline-terminated oversized line bypassed the tail memory bound")
	}
}

func mustRead(t *testing.T, tail *Tail) []Line {
	t.Helper()
	lines, err := tail.Read()
	if err != nil {
		t.Fatal(err)
	}
	return lines
}

func appendTo(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(text); err != nil {
		t.Fatal(err)
	}
}
