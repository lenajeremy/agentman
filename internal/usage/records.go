package usage

import (
	"bufio"
	"bytes"
	"cmp"
	"encoding/json"
	"hash/maphash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
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

// What is kept of the logs is what the summaries need: a log's calls are
// summed as they are read, by label and by the slot of time they fall in.
// Only a call whose count depends on the range asked for is kept by itself:
// one another log holds too, or one whose lines run from one slot into the
// next.

// label is what calls are summed by, besides their time: the agent, the
// model and the session. Each is made once, however many sums share it.
type label struct {
	agent    protocol.Kind
	provider string // the catalog's provider: anthropic, openai, google
	model    string
	session  string
	// limit is the prompt length past which the model's price rises, by the
	// catalog the label was made with: 0 for none.
	limit int64
}

// spend is calls summed: one call, or a log's calls of one label in one
// slot.
type spend struct {
	// at is when the call was made, or when its slot starts, in nanoseconds
	// since 1970.
	at    int64
	label *label
	// key names a call kept by itself, to count it once however many logs
	// or lines hold it: 0 for sums.
	key     uint64
	tokens  protocol.Tokens
	write1h int64
	// long is true for calls whose prompt passed their model's limit,
	// charged the dearer price.
	long bool
}

// keySeed hashes call keys. The chance that two of the hundred thousand or
// so a Mac's logs hold share a hash is under one in a billion.
var keySeed = maphash.MakeSeed()

// hashKey is a call's key as a number, 0 for a call without one.
func hashKey(key string) uint64 {
	if key == "" {
		return 0
	}
	if h := maphash.String(keySeed, key); h != 0 {
		return h
	}
	return 1
}

// labels makes each label once, its model's limit by the catalog.
type labels struct {
	mu      sync.Mutex
	made    map[label]*label
	catalog Catalog
}

func (ls *labels) get(agent protocol.Kind, provider, model, session string) *label {
	key := label{agent: agent, provider: provider, model: model, session: session}
	ls.mu.Lock()
	defer ls.mu.Unlock()
	if l := ls.made[key]; l != nil {
		return l
	}
	if ls.made == nil {
		ls.made = map[label]*label{}
	}
	l := key
	l.limit = limitOf(ls.catalog, provider, model)
	ls.made[key] = &l
	return &l
}

// stale says whether the catalog prices some model's long prompts past
// another length than the one its calls were summed by.
func (ls *labels) stale(catalog Catalog) bool {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	checked := map[[2]string]bool{}
	for _, l := range ls.made {
		model := [2]string{l.provider, l.model}
		if checked[model] {
			continue
		}
		checked[model] = true
		if limitOf(catalog, l.provider, l.model) != l.limit {
			return true
		}
	}
	return false
}

// limitOf is the prompt length past which a model's price rises, 0 for none.
func limitOf(catalog Catalog, provider, model string) int64 {
	if p, ok := catalog.Lookup(provider, model); ok && p.Above != nil {
		return p.AboveTokens
	}
	return 0
}

// slot is where a call made at at is summed: the start of its hour, or of
// its half or quarter hour where the clock is set off UTC by a half or a
// quarter (India, Nepal), so that the hours of the last day and local
// midnights all fall between slots, and a slot lies in one bucket of any
// range.
func slot(at int64, loc *time.Location) int64 {
	t := time.Unix(0, at)
	_, offset := t.In(loc).Zone()
	width := int64(time.Hour / time.Second)
	for width > 15*60 && int64(offset)%width != 0 {
		width /= 2
	}
	seconds := t.Unix()
	return (seconds - ((seconds%width)+width)%width) * int64(time.Second)
}

// fileState is what was read from one append-only log, so the next read
// starts where this one stopped.
type fileState struct {
	size    int64
	mod     time.Time
	offset  int64
	skipped int
	// first is when the log's earliest call was made, 0 before one.
	first int64
	// slots sums the log's calls by slot, label and length. calls are kept
	// one by one instead where which line of a call counts depends on the
	// range: those another log holds too, counted from the first log that
	// holds them, and those whose lines fall in more than one slot (split),
	// counted from the first line in the range, as when every line was kept.
	slots []spend
	calls []spend
	split []uint64
	// keys are the keys of the log's calls, sorted, and quarters the quarter
	// hour each was first written in: a call written again in it, on the
	// next line or much later, adds nothing.
	keys     []uint64
	quarters []int32
	// fresh are this read's calls with a key, until it is known whether
	// another log holds them too; added holds the quarter of each new key.
	// reread is set when a call summed before turns out to be split.
	fresh  []spend
	added  map[uint64]int32
	reread bool
	// index finds a slot's sum while the log is read, and last is the label
	// of the call read last: the next is likely the same.
	index map[slotKey]int
	last  *label
	// Codex counts tokens as running totals: the last one seen, whether
	// one has been, and the model the turn runs on.
	total   protocol.Tokens
	counted bool
	model   string
}

type slotKey struct {
	at    int64
	label *label
	long  bool
}

// sum adds a call to its slot's sum.
func (s *fileState) sum(c spend, loc *time.Location) {
	c.at, c.key = slot(c.at, loc), 0
	key := slotKey{c.at, c.label, c.long}
	if s.index == nil {
		s.index = make(map[slotKey]int, len(s.slots))
		for i, sum := range s.slots {
			s.index[slotKey{sum.at, sum.label, sum.long}] = i
		}
	}
	if i, ok := s.index[key]; ok {
		s.slots[i].tokens = s.slots[i].tokens.Add(c.tokens)
		s.slots[i].write1h += c.write1h
		return
	}
	s.index[key] = len(s.slots)
	s.slots = append(s.slots, c)
}

// quarter is the quarter hour a slot starts, counted from 1970.
func quarter(slot int64) int32 { return int32(slot / int64(15*time.Minute)) }

// written says in which quarter hour the log first wrote a call with this
// key, if it holds one.
func (s *fileState) written(key uint64) (int32, bool) {
	if i, found := slices.BinarySearch(s.keys, key); found {
		return s.quarters[i], true
	}
	q, found := s.added[key]
	return q, found
}

// apart says whether a call is kept by itself rather than summed.
func (s *fileState) apart(key uint64, shared []uint64) bool {
	_, found := slices.BinarySearch(shared, key)
	return found || slices.Contains(s.split, key)
}

// summed says whether the log summed a call with one of these keys (sorted)
// into its slots, rather than keeping it by itself.
func (s *fileState) summed(keys []uint64) bool {
	if len(keys) == 0 {
		return false
	}
	for _, key := range s.keys {
		if _, found := slices.BinarySearch(keys, key); found &&
			!slices.ContainsFunc(s.calls, func(c spend) bool { return c.key == key }) {
			return true
		}
	}
	return false
}

// settle sums this read's calls, keeping by itself each one shared or split,
// and lets go of what was needed only while reading.
func (s *fileState) settle(shared []uint64, loc *time.Location) {
	for _, c := range s.fresh {
		if s.apart(c.key, shared) {
			s.calls = append(s.calls, c)
		} else {
			s.sum(c, loc)
		}
	}
	if len(s.added) > 0 {
		type written struct {
			key     uint64
			quarter int32
		}
		all := make([]written, 0, len(s.keys)+len(s.added))
		for i, key := range s.keys {
			all = append(all, written{key, s.quarters[i]})
		}
		for key, q := range s.added {
			all = append(all, written{key, q})
		}
		slices.SortFunc(all, func(a, b written) int { return cmp.Compare(a.key, b.key) })
		s.keys, s.quarters = make([]uint64, len(all)), make([]int32, len(all))
		for i, w := range all {
			s.keys[i], s.quarters[i] = w.key, w.quarter
		}
	}
	s.fresh, s.added, s.index, s.last = nil, nil, nil, nil
	s.slots, s.calls, s.split = trim(s.slots), trim(s.calls), trim(s.split)
}

// trim is a slice kept for long, in an array its own size.
func trim[S ~[]E, E any](s S) S {
	if cap(s) == len(s) {
		return s
	}
	return append(S(nil), s...)
}

// logs reads the calls of a folder of JSONL logs, keeping what it summed.
type logs struct {
	mu     sync.Mutex
	files  map[string]*fileState
	parse  func(line []byte, path string, state *fileState) (Record, bool, error)
	match  func(path string) bool
	labels *labels
	// loc is the time zone the slots were cut for.
	loc *time.Location
	// shared are the keys of the calls more than one log holds, sorted.
	shared []uint64
}

func newLogs(match func(string) bool, parse func([]byte, string, *fileState) (Record, bool, error)) *logs {
	return &logs{files: map[string]*fileState{}, match: match, parse: parse, labels: &labels{}}
}

// read returns what the logs under root changed since since add up to,
// reading each new line once, several logs at a time: their sums, when the
// earliest of their calls was made, and how many lines couldn't be read.
func (l *logs) read(root string, since time.Time, catalog Catalog, loc *time.Location) ([][]spend, int64, int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	// Slots cut for another time zone, or long prompts told apart at
	// another length than the catalog's, are summed again.
	if loc != l.loc || l.labels.stale(catalog) {
		l.files, l.shared, l.loc, l.labels = map[string]*fileState{}, nil, loc, &labels{}
	}
	l.labels.catalog = catalog
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

	// Logs gone from this folder are let go. Those only past the range are
	// kept, so a longer range asked for next reads only what changed.
	prefix := filepath.Clean(root) + string(filepath.Separator)
	for path := range l.files {
		if strings.HasPrefix(path, prefix) && !present[path] {
			delete(l.files, path)
		}
	}
	l.settle()

	var sums [][]spend
	var first int64
	skipped := 0
	for _, state := range states {
		sums = append(sums, state.slots, state.calls)
		if state.first != 0 && (first == 0 || state.first < first) {
			first = state.first
		}
		skipped += state.skipped
	}
	return sums, first, skipped
}

// settle sums the calls just read. One that another log holds too is kept
// by itself, to be counted once; and when a call already summed turns out
// to be held by another log as well (a session resumed in a new file
// carries its earlier calls along), or to be split, its log is read again,
// for the call to stand by itself.
func (l *logs) settle() {
	added, held := 0, 0
	for _, s := range l.files {
		added += len(s.added)
		held += len(s.keys)
	}
	var newly []uint64
	if added > 0 {
		// A key found twice among every log's keys is shared.
		all := make([]uint64, 0, held+added)
		for _, s := range l.files {
			all = append(all, s.keys...)
			for key := range s.added {
				all = append(all, key)
			}
		}
		slices.Sort(all)
		var shared []uint64
		for i := 1; i < len(all); i++ {
			if all[i] != all[i-1] || len(shared) > 0 && shared[len(shared)-1] == all[i] {
				continue
			}
			shared = append(shared, all[i])
			if _, found := slices.BinarySearch(l.shared, all[i]); !found {
				newly = append(newly, all[i])
			}
		}
		l.shared = trim(shared)
	}
	var again []string
	for path, s := range l.files {
		if s.reread || s.summed(newly) {
			again = append(again, path)
			continue
		}
		s.settle(l.shared, l.loc)
	}
	for _, path := range again {
		s := l.files[path]
		*s = fileState{}
		info, err := os.Stat(path)
		if err != nil {
			delete(l.files, path)
			continue
		}
		l.readFrom(path, s)
		s.size, s.mod = info.Size(), info.ModTime()
		s.settle(l.shared, l.loc)
	}
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
			l.take(state, record)
		}
	}
}

// take adds a call to its log: summed at once when it has no key, and held
// until it is known whether another log holds it too when it has, unless the
// log wrote it already.
func (l *logs) take(s *fileState, r Record) {
	at := r.At.UnixNano()
	if s.first == 0 || at < s.first {
		s.first = at
	}
	lb := s.last
	if lb == nil || lb.agent != r.Agent || lb.provider != r.Provider || lb.model != r.Model || lb.session != r.Session {
		lb = l.labels.get(r.Agent, r.Provider, r.Model, r.Session)
		s.last = lb
	}
	c := spend{at: at, label: lb, tokens: r.Tokens, write1h: r.CacheWrite1h, long: lb.limit > 0 && r.Context > lb.limit}
	key := hashKey(r.Key)
	if key == 0 {
		s.sum(c, l.loc)
		return
	}
	c.key = key
	q := quarter(slot(at, l.loc))
	if first, held := s.written(key); held {
		// Written again in the slot it was first written in, a call adds
		// nothing. In another (its lines run past an hour's end), the line
		// counted depends on where a range begins: its lines are kept by
		// themselves, and the log read again if it summed the first.
		if q == first {
			return
		}
		if !slices.Contains(s.split, key) {
			s.split = append(s.split, key)
			if _, before := slices.BinarySearch(s.keys, key); before && !slices.ContainsFunc(s.calls, func(c spend) bool { return c.key == key }) {
				s.reread = true
			}
		}
		s.fresh = append(s.fresh, c)
		return
	}
	if s.added == nil {
		s.added = map[uint64]int32{}
	}
	s.added[key] = q
	s.fresh = append(s.fresh, c)
}

// spends makes records spends, each a call by itself.
func spends(records []Record, catalog Catalog) []spend {
	made := &labels{catalog: catalog}
	out := make([]spend, 0, len(records))
	for _, r := range records {
		lb := made.get(r.Agent, r.Provider, r.Model, r.Session)
		out = append(out, spend{
			at: r.At.UnixNano(), label: lb, key: hashKey(r.Key), tokens: r.Tokens, write1h: r.CacheWrite1h,
			long: lb.limit > 0 && r.Context > lb.limit,
		})
	}
	return out
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
