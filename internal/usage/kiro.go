package usage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Kiro keeps each chat's state in ~/.kiro/sessions/cli/<id>.json, rewritten
// after every turn. Its user_turn_metadatas say, turn by turn, when the turn
// ended, the model it ran on and what it cost in credits (metering_usage),
// Kiro's own measure: Kiro bills credits, and leaves its token counts at 0.

// kiroChat is what was read from one chat, kept until it changes.
type kiroChat struct {
	size    int64
	mod     time.Time
	records []Record
	bad     bool
}

type kiroReader struct {
	mu    sync.Mutex
	chats map[string]kiroChat
}

func newKiroReader() *kiroReader { return &kiroReader{chats: map[string]kiroChat{}} }

// read returns the turns that ended since since in every chat in dir, and
// how many chats couldn't be read.
func (k *kiroReader) read(dir string, since time.Time) ([]Record, int) {
	k.mu.Lock()
	defer k.mu.Unlock()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, 0
	}
	seen := map[string]bool{}
	var out []Record
	skipped := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		path := filepath.Join(dir, name)
		seen[path] = true
		chat, ok := k.chats[path]
		if !ok || chat.size != info.Size() || !chat.mod.Equal(info.ModTime()) {
			records, err := readKiroChat(path, strings.TrimSuffix(name, ".json"))
			chat = kiroChat{size: info.Size(), mod: info.ModTime(), records: records, bad: err != nil}
			k.chats[path] = chat
		}
		if chat.bad {
			skipped++
		}
		for _, r := range chat.records {
			if !r.At.Before(since) {
				out = append(out, r)
			}
		}
	}
	for path := range k.chats {
		if !seen[path] {
			delete(k.chats, path)
		}
	}
	return out, skipped
}

type kiroTurn struct {
	EndTimestamp string          `json:"end_timestamp"`
	Model        json.RawMessage `json:"model"`
	Input        int64           `json:"input_token_count"`
	Output       int64           `json:"output_token_count"`
	CacheRead    int64           `json:"cache_read_input_token_count"`
	CacheWrite   int64           `json:"cache_write_input_token_count"`
	Metering     []struct {
		Value float64 `json:"value"`
		Unit  string  `json:"unit"`
	} `json:"metering_usage"`
}

// readKiroChat reads one chat's turns.
func readKiroChat(path, id string) ([]Record, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var chat struct {
		SessionID    string `json:"session_id"`
		SessionState struct {
			Conversation struct {
				Turns []kiroTurn `json:"user_turn_metadatas"`
			} `json:"conversation_metadata"`
		} `json:"session_state"`
	}
	if err := json.Unmarshal(raw, &chat); err != nil {
		return nil, err
	}
	if chat.SessionID != "" {
		id = chat.SessionID
	}
	var out []Record
	for _, t := range chat.SessionState.Conversation.Turns {
		at, err := time.Parse(time.RFC3339Nano, t.EndTimestamp)
		if err != nil {
			continue
		}
		var credits float64
		for _, m := range t.Metering {
			if m.Unit == "credit" {
				credits += m.Value
			}
		}
		tokens := protocol.Tokens{Input: t.Input, CacheRead: t.CacheRead, CacheWrite: t.CacheWrite, Output: t.Output}
		if credits == 0 && tokens.Total() == 0 {
			continue
		}
		out = append(out, Record{
			Agent: protocol.KindKiro, Session: id, Model: kiroModel(t.Model), Provider: "kiro",
			At: at, Tokens: tokens, Credits: credits,
		})
	}
	return out, nil
}

// kiroModel is a turn's model: Kiro writes its id ("auto"), or an object
// that holds it.
func kiroModel(raw json.RawMessage) string {
	var id string
	if json.Unmarshal(raw, &id) == nil && id != "" {
		return id
	}
	var info struct {
		ModelID string `json:"model_id"`
		ID      string `json:"id"`
	}
	if json.Unmarshal(raw, &info) == nil {
		if info.ModelID != "" {
			return info.ModelID
		}
		if info.ID != "" {
			return info.ID
		}
	}
	return "auto"
}
