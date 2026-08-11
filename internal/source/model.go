package source

import (
	"encoding/json"
	"os"
	"sync"
	"time"
)

// Which model an agent is running is recorded per assistant message, not in any
// session header, so finding it means looking at what the agent last said. That
// is far too expensive to repeat on every discovery sweep — transcripts reach
// hundreds of megabytes and discovery runs once a second — so it is read once
// and remembered.
//
// A model can change mid-session (`/model` in any of the three CLIs), so the
// answer is refreshed occasionally rather than pinned forever.
const modelRecheck = 2 * time.Minute

// modelCache remembers the model for a session, keyed by session id.
type modelCache struct {
	mu      sync.Mutex
	entries map[string]modelEntry
}

type modelEntry struct {
	model string
	at    time.Time
}

func newModelCache() *modelCache {
	return &modelCache{entries: map[string]modelEntry{}}
}

// get returns the cached model and whether it is still fresh enough to use.
//
// An empty answer is cached too. A session that has not replied yet records no
// model anywhere, and without remembering the miss every sweep would re-read
// the whole tail of its transcript looking for something that is not there.
func (c *modelCache) get(sessionID string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[sessionID]
	if !ok || time.Since(entry.at) > modelRecheck {
		return "", false
	}
	return entry.model, true
}

func (c *modelCache) put(sessionID, model string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[sessionID] = modelEntry{model: model, at: time.Now()}
}

// forget drops sessions that are no longer running, so the cache cannot grow
// without bound on a machine that starts agents all day.
func (c *modelCache) forget(live map[string]bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id := range c.entries {
		if !live[id] {
			delete(c.entries, id)
		}
	}
}

// modelScanBytes is how much of a transcript's tail to search.
//
// Enough to cover several turns, so a run of tool calls between assistant
// replies does not push the model out of range, and small enough that doing
// this per session stays trivial next to a multi-hundred-megabyte file.
const modelScanBytes = 256 * 1024

// modelFromTranscript finds the most recent model named in a JSONL transcript.
//
// Reads only the tail: the newest lines are what matter, and the file may be
// enormous. Lines are scanned backwards so the first match found is the latest.
func modelFromTranscript(path string, extract func([]byte) string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return ""
	}
	size := info.Size()
	offset := int64(0)
	length := size
	if size > modelScanBytes {
		offset = size - modelScanBytes
		length = modelScanBytes
	}

	buffer := make([]byte, length)
	if _, err := file.ReadAt(buffer, offset); err != nil && len(buffer) == 0 {
		return ""
	}

	// Walk backwards line by line. The first line that names a model is the
	// most recent one, which is the answer.
	end := len(buffer)
	for end > 0 {
		start := end - 1
		for start >= 0 && buffer[start] != '\n' {
			start--
		}
		line := buffer[start+1 : end]
		// A partial first line is possible when the tail cut mid-record; it
		// simply fails to parse and is skipped.
		if model := extract(line); model != "" {
			return model
		}
		end = start
		if end < 0 {
			break
		}
	}
	return ""
}

// claudeModelOf reads the model from one Claude transcript line.
func claudeModelOf(line []byte) string {
	var record struct {
		Type    string `json:"type"`
		Message struct {
			Model string `json:"model"`
		} `json:"message"`
	}
	if err := json.Unmarshal(line, &record); err != nil {
		return ""
	}
	if record.Type != "assistant" {
		return ""
	}
	return cleanModel(record.Message.Model)
}

// codexModelOf reads the model from one Codex rollout line.
func codexModelOf(line []byte) string {
	// Codex names the model in two places, both real and neither documented:
	// turn_context.payload.model is the direct one, and world_state nests it
	// under the collaboration mode. Either will do — whichever appears later in
	// the file is the current one, and the scan runs backwards.
	var record struct {
		Payload struct {
			Model             string `json:"model"`
			CollaborationMode struct {
				Model string `json:"model"`
			} `json:"collaboration_mode"`
			State struct {
				CollaborationMode struct {
					Model string `json:"model"`
				} `json:"collaboration_mode"`
			} `json:"state"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(line, &record); err != nil {
		return ""
	}
	for _, candidate := range []string{
		record.Payload.Model,
		record.Payload.CollaborationMode.Model,
		record.Payload.State.CollaborationMode.Model,
	} {
		if model := cleanModel(candidate); model != "" {
			return model
		}
	}
	return ""
}

// cleanModel filters out the placeholders the CLIs write for their own
// bookkeeping. Claude Code records "<synthetic>" for messages it generated
// itself, and showing that as the model would be worse than showing nothing.
func cleanModel(model string) string {
	if model == "" || model == "<synthetic>" || model == "unknown" {
		return ""
	}
	return model
}
