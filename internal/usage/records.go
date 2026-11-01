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
	"strconv"
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
	// Mode is how the call was made, when not the usual way: modeFast for
	// Claude's fast mode, charged more, or modeAdvisor for a model consulted
	// during another's turn.
	Mode string
	// Credits are what the call cost for an agent that bills in credits
	// (Kiro), whatever its tokens.
	Credits float64
}

const (
	modeFast    = "fast"
	modeAdvisor = "advisor"
)

// What is kept of the logs is what the summaries need: a log's calls are
// summed as they are read, by label and by the slot of time they fall in.
// Only a call that another log holds too is kept by itself, so that it is
// counted once.
//
// A Claude reply is written over several lines as it streams, its output
// growing from one line to the next, and its speed and iterations only on
// the later lines: a reply counts once, at the time of its first line, with
// the largest counts its lines, and the copies other logs hold, give.

// label is what calls are summed by, besides their time and mode: the
// agent, the model and the session. Each is made once, however many sums
// share it.
type label struct {
	agent    protocol.Kind
	provider string // the catalog's provider: anthropic, openai, google
	model    string
	session  string
	// limit is the prompt length past which the model's price rises, by the
	// catalog the label was made with: 0 for none.
	limit int64
}

// spend is calls summed: one call, or a log's calls of one label and mode in
// one slot.
type spend struct {
	// at is when the call was made, or when its slot starts, in nanoseconds
	// since 1970.
	at    int64
	label *label
	// key names a call kept by itself, to count it once however many logs
	// hold it: 0 for sums.
	key     uint64
	tokens  protocol.Tokens
	write1h int64
	credits float64
	// long is true for calls whose prompt passed their model's limit,
	// charged the dearer price; fast for calls run in Claude's fast mode, and
	// advisor for a model consulted during another's turn.
	long, fast, advisor bool
}

// grow takes in another line or copy of the same call: the largest of each
// count, as a reply's output grows from line to line.
func (s *spend) grow(o spend) {
	s.tokens = protocol.Tokens{
		Input: max(s.tokens.Input, o.tokens.Input), CacheRead: max(s.tokens.CacheRead, o.tokens.CacheRead),
		CacheWrite: max(s.tokens.CacheWrite, o.tokens.CacheWrite), Output: max(s.tokens.Output, o.tokens.Output),
	}
	s.write1h = max(s.write1h, o.write1h)
	s.credits = max(s.credits, o.credits)
	s.long, s.fast = s.long || o.long, s.fast || o.fast
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

const quarterHour = int64(15 * time.Minute)

// written is what a log keeps of a call with a key once it is summed: the
// slot its first line fell in and whether it ran fast, and the most output
// its lines have counted, so that a later line counting more adds only the
// difference. A reply's lines don't change its other counts: on this Mac's
// 69,000 replies written more than once, never.
type written struct {
	key uint64
	// at is the start of the slot, in quarter hours since 1970, doubled,
	// and one more for a call run fast.
	at     int32
	output int32
}

func (w written) slot() int64 { return int64(w.at>>1) * quarterHour }
func (w written) fast() bool  { return w.at&1 == 1 }

// fileState is what was read from one append-only log, so the next read
// starts where this one stopped.
type fileState struct {
	size    int64
	mod     time.Time
	offset  int64
	skipped int
	// first is when the log's earliest call was made, 0 before one.
	first int64
	// slots sums the log's calls by slot, label, mode and length; calls are
	// those another log holds too, one by one, each counted once.
	slots []spend
	calls []spend
	// keys are the log's calls with a key, sorted by it.
	keys []written
	// fresh are this read's calls with a key, until it is known whether
	// another log holds them too; added finds each by its key. reread is
	// set when a call summed before has to be summed again.
	fresh  []spend
	added  map[uint64]int
	reread bool
	// index finds a slot's sum, and held a call kept by itself, while the
	// log is read; last is the label of the call read last: the next is
	// likely the same.
	index map[slotKey]int
	held  map[uint64]int
	last  *label
	// Codex counts tokens as running totals: the last one seen, whether
	// one has been, and the model the turn runs on.
	total   protocol.Tokens
	counted bool
	model   string
}

type slotKey struct {
	at                  int64
	label               *label
	long, fast, advisor bool
}

// slotOf finds the sum of a slot, making it if asked.
func (s *fileState) slotOf(key slotKey, make bool) (int, bool) {
	if s.index == nil {
		s.index = map[slotKey]int{}
		for i, sum := range s.slots {
			s.index[slotKey{sum.at, sum.label, sum.long, sum.fast, sum.advisor}] = i
		}
	}
	if i, ok := s.index[key]; ok || !make {
		return i, ok
	}
	s.index[key] = len(s.slots)
	s.slots = append(s.slots, spend{at: key.at, label: key.label, long: key.long, fast: key.fast, advisor: key.advisor})
	return len(s.slots) - 1, true
}

// sum adds a call to its slot's sum.
func (s *fileState) sum(c spend, loc *time.Location) {
	i, _ := s.slotOf(slotKey{slot(c.at, loc), c.label, c.long, c.fast, c.advisor}, true)
	s.slots[i].tokens = s.slots[i].tokens.Add(c.tokens)
	s.slots[i].write1h += c.write1h
	s.slots[i].credits += c.credits
}

// find is where the log keeps a key, if it does.
func (s *fileState) find(key uint64) (int, bool) {
	return slices.BinarySearchFunc(s.keys, key, func(w written, key uint64) int { return cmp.Compare(w.key, key) })
}

// summed says whether the log summed a call with one of these keys (sorted)
// into its slots, rather than keeping it by itself.
func (s *fileState) summed(keys []uint64) bool {
	if len(keys) == 0 {
		return false
	}
	for _, w := range s.keys {
		if _, found := slices.BinarySearch(keys, w.key); found &&
			!slices.ContainsFunc(s.calls, func(c spend) bool { return c.key == w.key }) {
			return true
		}
	}
	return false
}

// grow takes in a later line of a call the log holds already: a call kept
// by itself takes its larger counts; a summed one adds to its slot the
// output it counts past the most counted before. A summed call whose later
// line says it ran fast when the first didn't is summed again, from a fresh
// read of the log.
func (s *fileState) grow(c spend, loc *time.Location) {
	if i, ok := s.added[c.key]; ok {
		s.fresh[i].grow(c)
		return
	}
	if s.held == nil {
		s.held = map[uint64]int{}
		for i, call := range s.calls {
			s.held[call.key] = i
		}
	}
	if i, ok := s.held[c.key]; ok {
		s.calls[i].grow(c)
		return
	}
	i, _ := s.find(c.key)
	w := &s.keys[i]
	if c.tokens.Output <= int64(w.output) && (!c.fast || w.fast()) {
		return
	}
	sum, ok := s.slotOf(slotKey{w.slot(), c.label, c.long, w.fast(), c.advisor}, false)
	if !ok || c.fast && !w.fast() || slot(c.at, loc) < w.slot() {
		s.reread = true
		return
	}
	s.slots[sum].tokens.Output += c.tokens.Output - int64(w.output)
	w.output = int32(c.tokens.Output)
}

// settle sums this read's calls, keeping by itself each one whose key is
// shared, and lets go of what was needed only while reading.
func (s *fileState) settle(shared []uint64, loc *time.Location) {
	if len(s.fresh) > 0 {
		keys := make([]written, 0, len(s.keys)+len(s.fresh))
		keys = append(keys, s.keys...)
		for _, c := range s.fresh {
			at := int32(slot(c.at, loc)/quarterHour) * 2
			if c.fast {
				at++
			}
			keys = append(keys, written{key: c.key, at: at, output: int32(c.tokens.Output)})
			if _, found := slices.BinarySearch(shared, c.key); found {
				s.calls = append(s.calls, c)
			} else {
				s.sum(c, loc)
			}
		}
		slices.SortFunc(keys, func(a, b written) int { return cmp.Compare(a.key, b.key) })
		s.keys = keys
	}
	s.fresh, s.added, s.index, s.held, s.last = nil, nil, nil, nil, nil
	s.slots, s.calls = trim(s.slots), trim(s.calls)
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
	parse  parser
	match  func(path string) bool
	labels *labels
	// loc is the time zone the slots were cut for.
	loc *time.Location
	// shared are the keys of the calls more than one log holds, sorted.
	shared []uint64
}

// parser reads a log's line, telling add each call it records.
type parser func(line []byte, path string, state *fileState, add func(Record)) error

func newLogs(match func(string) bool, parse parser) *logs {
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
// carries its earlier calls along), its log is read again, for the call to
// stand by itself.
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
			for _, w := range s.keys {
				all = append(all, w.key)
			}
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
	add := func(r Record) { l.take(state, r) }
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			// A last line still being written is read next time, whole.
			return
		}
		state.offset += int64(len(line))
		if err := l.parse(bytes.TrimSpace(line), path, state, add); err != nil {
			state.skipped++
		}
	}
}

// take adds a call to its log: summed at once when it has no key, held until
// it is known whether another log holds it too when it has, and taken into
// what the log holds of it when it is a later line of a call already there.
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
	c := spend{
		at: at, label: lb, tokens: r.Tokens, write1h: r.CacheWrite1h,
		long: lb.limit > 0 && r.Context > lb.limit, fast: r.Mode == modeFast, advisor: r.Mode == modeAdvisor,
	}
	if c.key = hashKey(r.Key); c.key == 0 {
		s.sum(c, l.loc)
		return
	}
	if _, ok := s.added[c.key]; ok {
		s.grow(c, l.loc)
		return
	}
	if _, ok := s.find(c.key); ok {
		s.grow(c, l.loc)
		return
	}
	if s.added == nil {
		s.added = map[uint64]int{}
	}
	s.added[c.key] = len(s.fresh)
	s.fresh = append(s.fresh, c)
}

// spends makes records spends, each a call by itself.
func spends(records []Record, catalog Catalog) []spend {
	made := &labels{catalog: catalog}
	out := make([]spend, 0, len(records))
	for _, r := range records {
		lb := made.get(r.Agent, r.Provider, r.Model, r.Session)
		out = append(out, spend{
			at: r.At.UnixNano(), label: lb, key: hashKey(r.Key), tokens: r.Tokens, write1h: r.CacheWrite1h, credits: r.Credits,
			long: lb.limit > 0 && r.Context > lb.limit, fast: r.Mode == modeFast, advisor: r.Mode == modeAdvisor,
		})
	}
	return out
}

// Claude Code: an assistant line's message carries the model and its usage.
// A reply written over several lines repeats them, under one message id.
// A turn run in fast mode says so (speed), and is charged more. A turn that
// consulted an advisor lists each consultation among its iterations, under
// the advisor's model: the turn's own usage leaves them out, and they are
// charged at the advisor's prices, at the usual speed.

var (
	usageMark     = []byte(`"usage"`)
	assistantMark = []byte(`"assistant"`)
)

type claudeUsage struct {
	Input         int64 `json:"input_tokens"`
	CacheCreation int64 `json:"cache_creation_input_tokens"`
	CacheRead     int64 `json:"cache_read_input_tokens"`
	Output        int64 `json:"output_tokens"`
	Detail        *struct {
		OneHour int64 `json:"ephemeral_1h_input_tokens"`
	} `json:"cache_creation"`
}

func (u claudeUsage) record(session, model string, at time.Time) Record {
	r := Record{
		Agent: protocol.KindClaude, Session: session, Model: model, Provider: "anthropic", At: at,
		Tokens:  protocol.Tokens{Input: u.Input, CacheRead: u.CacheRead, CacheWrite: u.CacheCreation, Output: u.Output},
		Context: u.Input + u.CacheRead + u.CacheCreation,
	}
	if u.Detail != nil {
		r.CacheWrite1h = min(u.Detail.OneHour, u.CacheCreation)
	}
	return r
}

func parseClaude(line []byte, path string, _ *fileState, add func(Record)) error {
	if !bytes.Contains(line, usageMark) || !bytes.Contains(line, assistantMark) {
		return nil
	}
	var l struct {
		Type         string `json:"type"`
		Timestamp    string `json:"timestamp"`
		SessionID    string `json:"sessionId"`
		RequestID    string `json:"requestId"`
		AdvisorModel string `json:"advisorModel"`
		Message      struct {
			ID    string `json:"id"`
			Model string `json:"model"`
			Usage *struct {
				claudeUsage
				Speed      string `json:"speed"`
				Iterations []struct {
					claudeUsage
					Type  string `json:"type"`
					Model string `json:"model"`
				} `json:"iterations"`
			} `json:"usage"`
		} `json:"message"`
	}
	if err := json.Unmarshal(line, &l); err != nil {
		return err
	}
	if l.Type != "assistant" || l.Message.Usage == nil || l.Message.Model == "" || l.Message.Model == "<synthetic>" {
		return nil
	}
	at, err := time.Parse(time.RFC3339Nano, l.Timestamp)
	if err != nil {
		return err
	}
	u := l.Message.Usage
	session := l.SessionID
	if session == "" {
		session = strings.TrimSuffix(filepath.Base(path), ".jsonl")
	}
	r := u.record(session, l.Message.Model, at)
	if u.Speed == "fast" {
		r.Mode = modeFast
	}
	if l.Message.ID != "" {
		r.Key = l.Message.ID + ":" + l.RequestID
	}
	add(r)
	advised := 0
	for _, it := range u.Iterations {
		if it.Type != "advisor_message" {
			continue
		}
		advice := it.record(session, cmp.Or(it.Model, l.AdvisorModel), at)
		advice.Mode = modeAdvisor
		if r.Key != "" {
			advice.Key = r.Key + "/advisor/" + strconv.Itoa(advised)
		}
		advised++
		if advice.Model != "" && advice.Tokens.Total() > 0 {
			add(advice)
		}
	}
	return nil
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

func parseCodex(line []byte, path string, state *fileState, add func(Record)) error {
	turn, count := bytes.Contains(line, turnMark), bytes.Contains(line, tokenCountMark)
	if !turn && !count {
		return nil
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
		return err
	}
	if l.Type == "turn_context" {
		if l.Payload.Model != "" {
			state.model = l.Payload.Model
		}
		return nil
	}
	if l.Payload.Type != "token_count" || l.Payload.Info == nil || l.Payload.Info.Total == nil {
		return nil
	}
	at, err := time.Parse(time.RFC3339Nano, l.Timestamp)
	if err != nil {
		return err
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
		return nil
	}
	var context int64
	if last := l.Payload.Info.Last; last != nil {
		context = last.Input
	}
	session := strings.TrimSuffix(filepath.Base(path), ".jsonl")
	add(Record{
		Agent: protocol.KindCodex, Session: session, Model: state.model, Provider: "openai", At: at,
		Tokens: call, Context: context,
	})
	return nil
}

func isClaudeLog(path string) bool { return strings.HasSuffix(path, ".jsonl") }

func isCodexLog(path string) bool {
	return strings.HasSuffix(path, ".jsonl") && strings.HasPrefix(filepath.Base(path), "rollout-")
}
