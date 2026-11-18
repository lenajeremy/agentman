package source

import (
	"context"
	"os"
	"sync"
	"time"

	"github.com/lenajeremy/agentman/internal/jsonl"
	"github.com/lenajeremy/agentman/internal/parser"
	"github.com/lenajeremy/agentman/internal/protocol"
)

// limitScanBytes bounds how far back a transcript is read for the line that
// says whether the agent is out of usage. The refusal is the last thing a
// turn writes, a few records from the end, so this is generous.
const limitScanBytes = 256 * 1024

// limitWatch keeps what each session's transcript last said about its
// plan's limit, read again only when the file changes. An agent out of
// usage writes nothing more, so its transcript is read once.
type limitWatch struct {
	mu   sync.Mutex
	seen map[string]limitReading
	now  func() time.Time
}

type limitReading struct {
	size  int64
	mod   time.Time
	limit *protocol.Limit
}

// limit is what the transcript at path says about the session's limit now:
// set while its last turn ended at the plan's limit, no turn has started
// since, and the reset it named has not come. A working agent is not out of
// usage, whatever its transcript last said, so it isn't read for one.
func (w *limitWatch) limit(ctx context.Context, id, path string, state protocol.State,
	line func(string) (*protocol.Limit, bool)) *protocol.Limit {
	if path == "" || state == protocol.StateBusy {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}
	w.mu.Lock()
	if w.seen == nil {
		w.seen = map[string]limitReading{}
	}
	known, ok := w.seen[id]
	w.mu.Unlock()
	if !ok || known.size != info.Size() || !known.mod.Equal(info.ModTime()) {
		known = limitReading{size: info.Size(), mod: info.ModTime(), limit: readLimit(ctx, path, line)}
		if ctx.Err() != nil {
			return nil
		}
		w.mu.Lock()
		w.seen[id] = known
		w.mu.Unlock()
	}
	now := time.Now
	if w.now != nil {
		now = w.now
	}
	return parser.LimitCurrent(known.limit, now())
}

// forget drops sessions that are gone.
func (w *limitWatch) forget(live map[string]bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for id := range w.seen {
		if !live[id] {
			delete(w.seen, id)
		}
	}
}

// readLimit reads back from the end of a transcript to the last line that
// settles the limit.
func readLimit(ctx context.Context, path string, line func(string) (*protocol.Limit, bool)) *protocol.Limit {
	var found *protocol.Limit
	done := false
	_, _ = jsonl.CollectBackwardContext(ctx, path, jsonl.BackwardOptions{
		Want:         1,
		ChunkSize:    32 * 1024,
		MaxScanBytes: limitScanBytes,
		Map: func(text string, _ int64) []protocol.Message {
			if done {
				return nil
			}
			limit, decides := line(text)
			if !decides {
				return nil
			}
			found, done = limit, true
			// One row stops the scan: this is the latest line that decides.
			return []protocol.Message{{}}
		},
	})
	return found
}
