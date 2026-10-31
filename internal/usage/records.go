package usage

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Record is one model call: who made it, with what model, when, and the
// tokens it took.
type Record struct {
	Agent    protocol.Kind
	Session  string
	Model    string
	Provider string // the catalog's provider: anthropic, openai, google
	At       time.Time
	Tokens   protocol.Tokens
	// CacheWrite1h is the part of the cache writes kept an hour, which
	// Anthropic charges at twice the input price rather than 1.25 times.
	CacheWrite1h int64
	// Context is the prompt's size, for prices that rise past a length.
	Context int64
	// Key names a call written more than once (a resumed Claude session
	// carries its earlier calls into its new file), to count it once.
	Key string
}

// fileState is what was read from one append-only log, so the next read
// starts where this one stopped.
type fileState struct {
	size    int64
	mod     time.Time
	offset  int64
	records []Record
	skipped int
	// Codex counts tokens as running totals: the last one seen, whether
	// one has been, and the model the turn runs on.
	total   protocol.Tokens
	counted bool
	model   string
}

// logs reads the records of a folder of JSONL logs, keeping what it read.
type logs struct {
	mu    sync.Mutex
	files map[string]*fileState
	parse func(line []byte, path string, state *fileState) (Record, bool, error)
	match func(path string) bool
}

func newLogs(match func(string) bool, parse func([]byte, string, *fileState) (Record, bool, error)) *logs {
	return &logs{files: map[string]*fileState{}, match: match, parse: parse}
}

// read returns the records of every log under root changed since since,
// reading each new line once, several logs at a time.
func (l *logs) read(root string, since time.Time) ([]Record, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	type job struct {
		path  string
		state *fileState
		info  fs.FileInfo
	}
	var jobs []job
	var states []*fileState
	present := map[string]bool{}
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !l.match(path) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		present[path] = true
		if info.ModTime().Before(since) {
			return nil
		}
		state := l.files[path]
		if state == nil || info.Size() < state.size {
			state = &fileState{}
			l.files[path] = state
		}
		if info.Size() != state.size || !info.ModTime().Equal(state.mod) {
			jobs = append(jobs, job{path, state, info})
		}
		states = append(states, state)
		return nil
	})
	// Each log is its own: they are read side by side.
	work := make(chan job)
	var wg sync.WaitGroup
	for range min(runtime.NumCPU(), 8) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range work {
				l.readFrom(j.path, j.state)
				j.state.size, j.state.mod = j.info.Size(), j.info.ModTime()
			}
		}()
	}
	for _, j := range jobs {
		work <- j
	}
	close(work)
	wg.Wait()

	var out []Record
	skipped := 0
	for _, state := range states {
		out = append(out, state.records...)
		skipped += state.skipped
	}
	// Logs gone from this folder are let go. Those only past the range are
	// kept, so a longer range asked for next reads only what changed.
	prefix := filepath.Clean(root) + string(filepath.Separator)
	for path := range l.files {
		if strings.HasPrefix(path, prefix) && !present[path] {
			delete(l.files, path)
		}
	}
	return out, skipped
}

func (l *logs) readFrom(path string, state *fileState) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	if _, err := f.Seek(state.offset, io.SeekStart); err != nil {
		return
	}
	reader := bufio.NewReaderSize(f, 1<<20)
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			// A last line still being written is read next time, whole.
			return
		}
		state.offset += int64(len(line))
		record, ok, perr := l.parse(bytes.TrimSpace(line), path, state)
		if perr != nil {
			state.skipped++
			continue
		}
		if ok {
			state.records = append(state.records, record)
		}
	}
}

// Claude Code: an assistant line's message carries the model and its usage.
// A reply written over several lines repeats them, under one message id.

var (
	usageMark     = []byte(`"usage"`)
	assistantMark = []byte(`"assistant"`)
)

func parseClaude(line []byte, path string, _ *fileState) (Record, bool, error) {
	if !bytes.Contains(line, usageMark) || !bytes.Contains(line, assistantMark) {
		return Record{}, false, nil
	}
	var l struct {
		Type      string `json:"type"`
		Timestamp string `json:"timestamp"`
		SessionID string `json:"sessionId"`
		RequestID string `json:"requestId"`
		Message   struct {
			ID    string `json:"id"`
			Model string `json:"model"`
			Usage *struct {
				Input         int64 `json:"input_tokens"`
				CacheCreation int64 `json:"cache_creation_input_tokens"`
				CacheRead     int64 `json:"cache_read_input_tokens"`
				Output        int64 `json:"output_tokens"`
				Detail        *struct {
					OneHour int64 `json:"ephemeral_1h_input_tokens"`
				} `json:"cache_creation"`
			} `json:"usage"`
		} `json:"message"`
	}
	if err := json.Unmarshal(line, &l); err != nil {
		return Record{}, false, err
	}
	if l.Type != "assistant" || l.Message.Usage == nil || l.Message.Model == "" || l.Message.Model == "<synthetic>" {
		return Record{}, false, nil
	}
	at, err := time.Parse(time.RFC3339Nano, l.Timestamp)
	if err != nil {
		return Record{}, false, err
	}
	u := l.Message.Usage
	session := l.SessionID
	if session == "" {
		session = strings.TrimSuffix(filepath.Base(path), ".jsonl")
	}
	r := Record{
		Agent: protocol.KindClaude, Session: session, Model: l.Message.Model, Provider: "anthropic", At: at,
		Tokens:  protocol.Tokens{Input: u.Input, CacheRead: u.CacheRead, CacheWrite: u.CacheCreation, Output: u.Output},
		Context: u.Input + u.CacheRead + u.CacheCreation,
	}
	if u.Detail != nil {
		r.CacheWrite1h = min(u.Detail.OneHour, u.CacheCreation)
	}
	if l.Message.ID != "" {
		r.Key = l.Message.ID + ":" + l.RequestID
	}
	return r, true, nil
}

// Codex: a turn_context line names the model; a token_count event carries
// the session's running totals, so each call is the change since the last.
// Its input counts the cached part, and the part written to the cache, too.
// A session carried on from another (compacted, or resumed in a new file)
// starts with that one's totals, already counted where they were made.

var (
	tokenCountMark = []byte(`"token_count"`)
	turnMark       = []byte(`"turn_context"`)
)

type codexUsage struct {
	Input      int64 `json:"input_tokens"`
	Cached     int64 `json:"cached_input_tokens"`
	CacheWrite int64 `json:"cache_write_input_tokens"`
	Output     int64 `json:"output_tokens"`
}

func (u codexUsage) tokens() protocol.Tokens {
	return protocol.Tokens{Input: max(u.Input-u.Cached-u.CacheWrite, 0), CacheRead: u.Cached, CacheWrite: u.CacheWrite, Output: u.Output}
}

func parseCodex(line []byte, path string, state *fileState) (Record, bool, error) {
	turn, count := bytes.Contains(line, turnMark), bytes.Contains(line, tokenCountMark)
	if !turn && !count {
		return Record{}, false, nil
	}
	var l struct {
		Timestamp string `json:"timestamp"`
		Type      string `json:"type"`
		Payload   struct {
			Type  string `json:"type"`
			Model string `json:"model"`
			Info  *struct {
				Total *codexUsage `json:"total_token_usage"`
				Last  *codexUsage `json:"last_token_usage"`
			} `json:"info"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(line, &l); err != nil {
		return Record{}, false, err
	}
	if l.Type == "turn_context" {
		if l.Payload.Model != "" {
			state.model = l.Payload.Model
		}
		return Record{}, false, nil
	}
	if l.Payload.Type != "token_count" || l.Payload.Info == nil || l.Payload.Info.Total == nil {
		return Record{}, false, nil
	}
	at, err := time.Parse(time.RFC3339Nano, l.Timestamp)
	if err != nil {
		return Record{}, false, err
	}
	total := l.Payload.Info.Total.tokens()
	call := protocol.Tokens{
		Input: total.Input - state.total.Input, CacheRead: total.CacheRead - state.total.CacheRead,
		CacheWrite: total.CacheWrite - state.total.CacheWrite, Output: total.Output - state.total.Output,
	}
	if !state.counted || call.Input < 0 || call.CacheRead < 0 || call.CacheWrite < 0 || call.Output < 0 {
		// The file's first totals, or totals that started again: only the
		// last call is new.
		call = protocol.Tokens{}
		if last := l.Payload.Info.Last; last != nil {
			call = last.tokens()
		}
	}
	state.total, state.counted = total, true
	if call.Total() == 0 {
		// The same totals again, as a rate-limit update repeats them.
		return Record{}, false, nil
	}
	var context int64
	if last := l.Payload.Info.Last; last != nil {
		context = last.Input
	}
	session := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	return Record{
		Agent: protocol.KindCodex, Session: session, Model: state.model, Provider: "openai", At: at,
		Tokens: call, Context: context,
	}, true, nil
}

func isClaudeLog(path string) bool { return strings.HasSuffix(path, ".jsonl") }

func isCodexLog(path string) bool {
	return strings.HasSuffix(path, ".jsonl") && strings.HasPrefix(filepath.Base(path), "rollout-")
}
