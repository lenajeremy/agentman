package usage

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Muse writes each session's events to its own log,
// sessions/<yyyy>/<mm>/<dd>/<session id>/session.jsonl under its data folder.
// A model call's tokens are a goal_usage_attribution event, with an id of
// its own; the model is the one the run was set up with, in its
// run.model.configured record. A session's helpers keep logs of their own
// in its folder (<session id>/subagent/<id>/session.jsonl); their calls are
// the session's. A call written in more than one log is counted once.

var (
	museUsageMark = []byte(`"goal_usage_attribution"`)
	museModelMark = []byte(`"run.model.configured"`)
)

// museLog is what was read from one log, kept until it changes.
type museLog struct {
	size    int64
	mod     time.Time
	records []Record
	bad     bool
}

type museReader struct {
	mu   sync.Mutex
	logs map[string]museLog
}

func newMuseReader() *museReader { return &museReader{logs: map[string]museLog{}} }

// read returns the calls made since since in every log under root, and how
// many logs couldn't be read.
func (m *museReader) read(root string, since time.Time) ([]Record, int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	seen := map[string]bool{}
	var out []Record
	skipped := 0
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if path == root {
				return filepath.SkipAll
			}
			return nil
		}
		if entry.IsDir() || entry.Name() != "session.jsonl" {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		seen[path] = true
		log, ok := m.logs[path]
		if !ok || log.size != info.Size() || !log.mod.Equal(info.ModTime()) {
			records, err := readMuseLog(path, museSession(root, path))
			log = museLog{size: info.Size(), mod: info.ModTime(), records: records, bad: err != nil}
			m.logs[path] = log
		}
		if log.bad {
			skipped++
		}
		for _, r := range log.records {
			if !r.At.Before(since) {
				out = append(out, r)
			}
		}
		return nil
	})
	for path := range m.logs {
		if !seen[path] {
			delete(m.logs, path)
		}
	}
	return out, skipped
}

type museLine struct {
	RecordedAt  int64  `json:"recorded_at"`
	PayloadType string `json:"payload_type"`
	Payload     struct {
		Record *struct {
			ProviderID string `json:"provider_id"`
			ModelID    string `json:"model_id"`
		} `json:"record"`
		Event *struct {
			Kind   string `json:"kind"`
			Record struct {
				UsageID  string `json:"usage_id"`
				Family   string `json:"usage_family"`
				Quantity struct {
					Unit   string `json:"unit"`
					Input  int64  `json:"input_tokens"`
					Output int64  `json:"output_tokens"`
					Cached int64  `json:"cached_tokens"`
				} `json:"quantity"`
			} `json:"record"`
		} `json:"event"`
	} `json:"payload"`
}

// museSession is the session a log belongs to: the folder under the date
// it was started on, a helper's log included.
func museSession(root, path string) string {
	if rel, err := filepath.Rel(root, path); err == nil {
		if parts := strings.Split(filepath.ToSlash(rel), "/"); len(parts) >= 5 {
			return parts[3]
		}
	}
	return filepath.Base(filepath.Dir(path))
}

// readMuseLog reads one log's calls, as session's. A line that isn't JSON
// is passed over and reported once, as the rest of the log is still worth
// counting.
func readMuseLog(path, session string) ([]Record, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	model, provider := "", "meta"
	var out []Record
	var bad error
	reader := bufio.NewReaderSize(f, 64*1024)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 && (bytes.Contains(line, museUsageMark) || bytes.Contains(line, museModelMark)) {
			var l museLine
			if json.Unmarshal(line, &l) != nil {
				bad = errors.New("usage: a Muse log line is not JSON")
			} else if l.PayloadType == "run.model.configured" && l.Payload.Record != nil {
				if l.Payload.Record.ModelID != "" {
					model = l.Payload.Record.ModelID
				}
				if l.Payload.Record.ProviderID != "" {
					provider = l.Payload.Record.ProviderID
				}
			} else if e := l.Payload.Event; e != nil && e.Kind == "goal_usage_attribution" &&
				e.Record.Family == "provider" && e.Record.Quantity.Unit == "tokens" {
				q := e.Record.Quantity
				tokens := protocol.Tokens{Input: max(q.Input-q.Cached, 0), CacheRead: q.Cached, Output: q.Output}
				if tokens.Total() > 0 {
					out = append(out, Record{
						Agent: protocol.KindMuse, Session: session, Model: model, Provider: provider,
						At: time.UnixMicro(l.RecordedAt), Tokens: tokens, Context: q.Input, Key: "muse:" + e.Record.UsageID,
					})
				}
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return out, err
		}
	}
	return out, bad
}
