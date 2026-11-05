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
	"strconv"
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
// the session's. A call written in more than one log is counted once: the
// session's log writes each of its helpers' calls again, under a usage id
// of its own but with the same owner (the helper) and the same counts, so
// a helper's call is known by those, and kept as its own log has it, on
// its own model.
//
// Before a step that needs approval, Muse's automated reviewer asks a model
// whether to allow it. That call is no goal_usage_attribution: its tokens
// and model are on the automated_review_completed event, once per review.

var (
	museUsageMark  = []byte(`"goal_usage_attribution"`)
	museModelMark  = []byte(`"run.model.configured"`)
	museReviewMark = []byte(`"automated_review_completed"`)
)

// museLog is what was read from one log, kept until it changes.
type museLog struct {
	size    int64
	mod     time.Time
	records []Record
	bad     bool
	// helper is true for a helper's own log.
	helper bool
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
	// kept is where each call by its key is in out, and whether a helper's
	// own log gave it.
	type at struct {
		index  int
		helper bool
	}
	kept := map[string]at{}
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
			log = museLog{size: info.Size(), mod: info.ModTime(), records: records, bad: err != nil, helper: museHelper(root, path)}
			m.logs[path] = log
		}
		if log.bad {
			skipped++
		}
		for _, r := range log.records {
			if r.At.Before(since) {
				continue
			}
			if r.Key == "" {
				out = append(out, r)
				continue
			}
			if k, ok := kept[r.Key]; ok {
				if log.helper && !k.helper {
					out[k.index] = r
					kept[r.Key] = at{k.index, true}
				}
				continue
			}
			kept[r.Key] = at{len(out), log.helper}
			out = append(out, r)
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
					Unit      string `json:"unit"`
					Input     int64  `json:"input_tokens"`
					Output    int64  `json:"output_tokens"`
					Cached    int64  `json:"cached_tokens"`
					Reasoning int64  `json:"reasoning_tokens"`
				} `json:"quantity"`
				// Owner is whose call it was: the session's own run
				// (owner_type main_root), or one of its helpers, named by
				// owner_id.
				Owner struct {
					ID   string `json:"owner_id"`
					Type string `json:"owner_type"`
				} `json:"owner"`
			} `json:"record"`
			// A review's: read only for automated_review_completed, as
			// other events use the names otherwise.
			ReviewID string          `json:"review_id"`
			Model    json.RawMessage `json:"model"`
			Usage    json.RawMessage `json:"usage"`
		} `json:"event"`
	} `json:"payload"`
}

// museReview is the model an automated review asked, and what it took.
type museReview struct {
	Model struct {
		ProviderID string `json:"provider_id"`
		ModelID    string `json:"model_id"`
	}
	Usage struct {
		Input  int64 `json:"input_tokens"`
		Cached int64 `json:"cached_input_tokens"`
		Output int64 `json:"output_tokens"`
	}
}

func readMuseReview(model, usage json.RawMessage) (museReview, error) {
	var r museReview
	if len(model) > 0 && json.Unmarshal(model, &r.Model) != nil || json.Unmarshal(usage, &r.Usage) != nil {
		return r, errors.New("usage: a Muse review's model or usage is not what it was")
	}
	return r, nil
}

// tokens are the review's: its input counts its cached part.
func (r museReview) tokens() protocol.Tokens {
	return protocol.Tokens{Input: max(r.Usage.Input-r.Usage.Cached, 0), CacheRead: r.Usage.Cached, Output: r.Usage.Output}
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

// museHelper is whether a log is a helper's own, not its session's.
func museHelper(root, path string) bool {
	if rel, err := filepath.Rel(root, path); err == nil {
		parts := strings.Split(filepath.ToSlash(rel), "/")
		return len(parts) >= 7 && parts[4] == "subagent"
	}
	return false
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
		if len(line) > 0 && (bytes.Contains(line, museUsageMark) || bytes.Contains(line, museModelMark) || bytes.Contains(line, museReviewMark)) {
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
				key := ""
				if e.Record.UsageID != "" {
					key = "muse:" + e.Record.UsageID
				}
				if owner := e.Record.Owner; owner.ID != "" && owner.Type != "" && owner.Type != "main_root" {
					key = "muse:" + owner.ID + ":" + strconv.FormatInt(q.Input, 10) + "/" + strconv.FormatInt(q.Cached, 10) +
						"/" + strconv.FormatInt(q.Output, 10) + "/" + strconv.FormatInt(q.Reasoning, 10)
				}
				if tokens.Total() > 0 {
					out = append(out, Record{
						Agent: protocol.KindMuse, Session: session, Model: model, Provider: provider,
						At: time.UnixMicro(l.RecordedAt), Tokens: tokens, Context: q.Input, Key: key,
					})
				}
			} else if e != nil && e.Kind == "automated_review_completed" && len(e.Usage) > 0 {
				if r, err := readMuseReview(e.Model, e.Usage); err != nil {
					bad = err
				} else if tokens := r.tokens(); tokens.Total() > 0 {
					reviewModel, reviewProvider := model, provider
					if r.Model.ModelID != "" {
						reviewModel = r.Model.ModelID
					}
					if r.Model.ProviderID != "" {
						reviewProvider = r.Model.ProviderID
					}
					key := ""
					if e.ReviewID != "" {
						key = "muse:review:" + e.ReviewID
					}
					out = append(out, Record{
						Agent: protocol.KindMuse, Session: session, Model: reviewModel, Provider: reviewProvider,
						At: time.UnixMicro(l.RecordedAt), Tokens: tokens, Context: r.Usage.Input, Key: key,
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
