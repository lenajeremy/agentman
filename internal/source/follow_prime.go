package source

import (
	"os"

	"github.com/lenajeremy/agentman/internal/jsonl"
)

// followPrimeBytes is how much of a transcript's end a follow reads before it
// starts. A tool call still running when the phone subscribes is in this
// stretch; a call older than that has almost always finished.
const followPrimeBytes = 512 << 10

// primeFollow positions a follow's tail at the end of the transcript, after
// first feeding the last stretch of it to the follow's parser and throwing the
// output away.
//
// A follow starts at the end because the backlog is Page's to deliver. But a
// tool call made before that moment and finished after it is settled by a
// result line the follow does read, and a parser that never saw the call has
// nothing to settle: the row Page showed as running then spun forever. Primed,
// the parser knows the calls still open and can close them.
//
// A line still being written when the end is measured is left for the follow
// to read whole, rather than parsed here and lost.
func primeFollow(tail *jsonl.Tail, parse jsonl.MapFunc) error {
	info, err := os.Stat(tail.Path())
	if err != nil {
		return err
	}
	end := info.Size()
	start := max(end-followPrimeBytes, 0)
	primer := jsonl.NewTail(tail.Path())
	primer.SeekToOffset(start)
	skipPartial := start > 0
	// Where the follow resumes: just past the last whole line parsed here.
	resume := int64(-1)
	for primer.Offset() < end {
		lines, err := primer.Read()
		if err != nil {
			return err
		}
		if len(lines) == 0 {
			break
		}
		for _, line := range lines {
			if skipPartial {
				// Seeking into the middle of the file lands mid-line.
				skipPartial = false
				continue
			}
			lineEnd := line.Offset + int64(len(line.Text)) + 1
			if lineEnd > end {
				// Not finished when the end was measured, or written since:
				// the follow reads it whole, from its start.
				tail.SeekToOffset(line.Offset)
				return nil
			}
			parse(line.Text, line.Offset)
			resume = lineEnd
		}
	}
	if resume >= 0 && resume < end {
		// A last line still unterminated at the measured end.
		tail.SeekToOffset(resume)
		return nil
	}
	tail.SeekTo(info)
	return nil
}
