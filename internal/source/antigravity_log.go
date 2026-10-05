package source

import (
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// An agy run in a terminal of its own has no pane for discovery to read, and
// its transcript misses the two moments that matter most: it never records a
// permission prompt — the call is written, then nothing until it is answered
// — and a turn cancelled with Esc writes nothing at all, so it read as
// working for ever.
//
// Each agy process writes a log of its own, log/cli-<started>.log, and holds
// it open for as long as it runs, which is how lsof ties the two together.
// Among its lines (glog format, from agy 1.2.17):
//
//	… conversation_manager.go:779] Forwarding user message to conversation <id> (items=1, media=0)
//	… tool_confirmation_manager.go:226] Surfacing tool confirmation: "RunCommand" at step 16
//	… input_loop.go:706] Responding to tool confirmation: convID=<id>, stepIdx=16, approved=true, …
//	… conversation_manager.go:1164] Surfacing ask_question at step 36
//	… conversation_manager.go:1520] Cancelling in-progress response for conversation <id>
//
// The log also carries the user's prompts verbatim. Nothing here keeps or
// passes on any line; only the state it implies leaves this file.

// antigravityLogTail bounds how much of a log one sweep reads. The lines that
// matter are the last few a turn wrote; a turn writes a few dozen.
const antigravityLogTail = 256 << 10

var (
	antigravityLogSurfaced  = regexp.MustCompile(`\] Surfacing (?:tool confirmation: "[^"]*"|ask_question) at step (\d+)\s*$`)
	antigravityLogResponded = regexp.MustCompile(`\] Responding to tool confirmation: convID=([0-9a-f-]+), stepIdx=(\d+),`)
	antigravityLogForwarded = regexp.MustCompile(`\] Forwarding user message to conversation ([0-9a-f-]+)\b`)
	antigravityLogCancelled = regexp.MustCompile(`\] Cancelling in-progress response for conversation ([0-9a-f-]+)\s*$`)
)

// antigravityLogCache holds one read of each log, by size and mtime.
type antigravityLogCache struct {
	mu      sync.Mutex
	entries map[string]antigravityLogEntry
}

type antigravityLogEntry struct {
	size  int64
	mtime time.Time
	text  string
}

// logState reads what the process's log says about a conversation whose
// transcript ends at lastStep: waiting on the user, idle after a cancel, or
// nothing the transcript does not already say.
func (s *AntigravitySource) logState(path, conversation string, lastStep int) (protocol.State, bool) {
	text, ok := s.logTail(path)
	if !ok {
		return "", false
	}
	return antigravityLogState(text, conversation, lastStep)
}

func (s *AntigravitySource) logTail(path string) (string, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return "", false
	}
	s.logs.mu.Lock()
	defer s.logs.mu.Unlock()
	if cached, ok := s.logs.entries[path]; ok && cached.size == info.Size() && cached.mtime.Equal(info.ModTime()) {
		return cached.text, true
	}
	file, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer file.Close()
	start := max(info.Size()-antigravityLogTail, 0)
	buf := make([]byte, info.Size()-start)
	if _, err := file.ReadAt(buf, start); err != nil && err != io.EOF {
		return "", false
	}
	text := string(buf)
	if start > 0 {
		// Began mid-line.
		if cut := strings.IndexByte(text, '\n'); cut >= 0 {
			text = text[cut+1:]
		}
	}
	if s.logs.entries == nil {
		s.logs.entries = map[string]antigravityLogEntry{}
	}
	s.logs.entries[path] = antigravityLogEntry{size: info.Size(), mtime: info.ModTime(), text: text}
	return text, true
}

// forgetLogs drops cached logs no live process holds any more.
func (s *AntigravitySource) forgetLogs(live map[string]bool) {
	s.logs.mu.Lock()
	defer s.logs.mu.Unlock()
	for path := range s.logs.entries {
		if !live[path] {
			delete(s.logs.entries, path)
		}
	}
}

// antigravityLogState replays a log's lines for one conversation.
//
// A confirmation is surfaced with its step but not its conversation, so it is
// only counted while the process's latest message went to this one. It stays
// pending until a response for that step is logged or the step itself reaches
// the transcript — ask_question logs no response, only its answer.
func antigravityLogState(text, conversation string, lastStep int) (protocol.State, bool) {
	active, cancelled := false, false
	pending := -1
	for _, line := range strings.Split(text, "\n") {
		switch {
		case strings.Contains(line, "] Forwarding user message to conversation "):
			match := antigravityLogForwarded.FindStringSubmatch(line)
			active = match != nil && match[1] == conversation
			cancelled, pending = false, -1
		case strings.Contains(line, "] Cancelling in-progress response for conversation "):
			if match := antigravityLogCancelled.FindStringSubmatch(line); match != nil && match[1] == conversation {
				cancelled, pending = true, -1
			}
		case strings.Contains(line, "] Surfacing "):
			if match := antigravityLogSurfaced.FindStringSubmatch(line); match != nil && active {
				pending, _ = strconv.Atoi(match[1])
				cancelled = false
			}
		case strings.Contains(line, "] Responding to tool confirmation: "):
			match := antigravityLogResponded.FindStringSubmatch(line)
			if match != nil && match[1] == conversation {
				if step, _ := strconv.Atoi(match[2]); step == pending {
					pending = -1
				}
			}
		}
	}
	switch {
	case pending >= 0 && lastStep < pending:
		return protocol.StateWaitingInput, true
	case cancelled:
		return protocol.StateIdle, true
	}
	return "", false
}
