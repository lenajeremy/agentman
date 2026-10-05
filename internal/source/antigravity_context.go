package source

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// How full the model's context is, from what agy records with every response:
//
//	{"type":"PLANNER_RESPONSE", …, "input_tokens":3236, "cache_read_tokens":32589, "output_tokens":40}
//
// What the next request carries is roughly the last one's input, cached or
// not, plus what came back. Checked against agy's own /context panel, which
// read "40.9k/1.0M tokens (3.9%)" where this gives 3.4% — the panel also
// counts the prompt about to be sent — so the two agree to within a point.

// antigravityUsageTail bounds how far back the last response is looked for.
const antigravityUsageTail = 256 << 10

// antigravityWindow is the context window of the models agy runs, by the
// label its footer and settings use. Gemini's is the one agy's /context panel
// was seen to report (1.0M); for any other model no percentage is claimed.
func antigravityWindow(model string) int64 {
	if strings.HasPrefix(model, "Gemini ") {
		return 1 << 20
	}
	return 0
}

type antigravityUsageEntry struct {
	size   int64
	mtime  time.Time
	tokens int64
}

type antigravityUsageCache struct {
	mu      sync.Mutex
	entries map[string]antigravityUsageEntry
}

// contextPercent is how full the context window is, in whole percent, and
// whether that can be told at all.
func (s *AntigravitySource) contextPercent(transcript, model string) (int, bool) {
	window := antigravityWindow(model)
	if window == 0 || transcript == "" {
		return 0, false
	}
	tokens := s.lastUsage(transcript)
	if tokens <= 0 {
		return 0, false
	}
	percent := int((tokens*100 + window/2) / window)
	return min(max(percent, 0), 100), true
}

// lastUsage is the token count of the newest response in a transcript.
func (s *AntigravitySource) lastUsage(transcript string) int64 {
	info, err := os.Stat(transcript)
	if err != nil {
		return 0
	}
	s.usage.mu.Lock()
	defer s.usage.mu.Unlock()
	if cached, ok := s.usage.entries[transcript]; ok && cached.size == info.Size() && cached.mtime.Equal(info.ModTime()) {
		return cached.tokens
	}
	tokens := readAntigravityUsage(transcript, info.Size())
	if s.usage.entries == nil {
		s.usage.entries = map[string]antigravityUsageEntry{}
	}
	s.usage.entries[transcript] = antigravityUsageEntry{size: info.Size(), mtime: info.ModTime(), tokens: tokens}
	return tokens
}

func readAntigravityUsage(path string, size int64) int64 {
	file, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer file.Close()
	start := max(size-antigravityUsageTail, 0)
	buf := make([]byte, size-start)
	if _, err := file.ReadAt(buf, start); err != nil && err != io.EOF {
		return 0
	}
	lines := strings.Split(string(buf), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if !strings.Contains(lines[i], `"input_tokens"`) {
			continue
		}
		var step struct {
			Input  int64 `json:"input_tokens"`
			Cached int64 `json:"cache_read_tokens"`
			Output int64 `json:"output_tokens"`
		}
		if json.Unmarshal([]byte(lines[i]), &step) != nil {
			continue // the first line of the window can be a fragment
		}
		return step.Input + step.Cached + step.Output
	}
	return 0
}

// forgetUsage drops cached usage for transcripts no longer live.
func (s *AntigravitySource) forgetUsage(live map[string]bool) {
	s.usage.mu.Lock()
	defer s.usage.mu.Unlock()
	for path := range s.usage.entries {
		if !live[path] {
			delete(s.usage.entries, path)
		}
	}
}
