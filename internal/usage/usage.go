// Package usage reads what the agents on this Mac spent, from each agent's
// own records, and sums it up for a stretch of time: tokens by kind, and
// what the API would have charged for them.
package usage

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Scanner reads the records, keeping what it read of each log so the next
// read only takes what was added.
type Scanner struct {
	// mu makes one summary at a time: the sums a read returns are the
	// logs' own, added to by the next read.
	mu     sync.Mutex
	home   string
	claude *logs
	codex  *logs
	prices *Prices
	// openCodeDB is OpenCode's database, read on each ask: small, and
	// indexed by time.
	openCodeDB string
	// antigravity reads agy's conversations, one database each, under
	// antigravityRoots.
	antigravity      *antigravityReader
	antigravityRoots []string
	// muse reads Muse's session logs under museRoot; kiro Kiro's chats in
	// kiroDir.
	muse     *museReader
	museRoot string
	kiro     *kiroReader
	kiroDir  string
}

func NewScanner(home string, prices *Prices) *Scanner {
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		data = filepath.Join(home, ".local", "share")
	}
	return &Scanner{
		home:             home,
		claude:           newLogs(isClaudeLog, parseClaude),
		codex:            newLogs(isCodexLog, parseCodex),
		prices:           prices,
		openCodeDB:       filepath.Join(data, "opencode", "opencode.db"),
		antigravity:      newAntigravityReader(),
		antigravityRoots: antigravityRoots(home),
		muse:             newMuseReader(),
		museRoot:         filepath.Join(data, "muse", "sessions"),
		kiro:             newKiroReader(),
		kiroDir:          filepath.Join(home, ".kiro", "sessions", "cli"),
	}
}

// antigravityRoots are where agy and its IDE keep their conversations, or
// the folders ANTIGRAVITY_DATA_DIR names, comma-separated.
func antigravityRoots(home string) []string {
	roots := []string{
		filepath.Join(home, ".gemini", "antigravity"), filepath.Join(home, ".gemini", "antigravity-cli"),
		filepath.Join(home, ".gemini", "antigravity-ide"), filepath.Join(home, ".gemini", "antigravity-backup"),
		filepath.Join(home, ".config", "antigravity"),
	}
	if set := os.Getenv("ANTIGRAVITY_DATA_DIR"); strings.TrimSpace(set) != "" {
		roots = nil
		for _, root := range strings.Split(set, ",") {
			if root = strings.TrimSpace(root); root != "" {
				roots = append(roots, root)
			}
		}
	}
	// One folder linked under two names is read once.
	seen := map[string]bool{}
	var out []string
	for _, root := range roots {
		if real, err := filepath.EvalSymlinks(root); err == nil {
			root = real
		}
		if !seen[root] {
			seen[root] = true
			out = append(out, root)
		}
	}
	return out
}

// read reads every agent's calls since since: what they add up to, when the
// earliest was made, and how many records couldn't be read. Slots are cut
// for loc's clock.
func (s *Scanner) read(ctx context.Context, since time.Time, catalog Catalog, loc *time.Location) ([][]spend, time.Time, int) {
	// A log changed in the range may hold calls from before it; they are
	// left out when summed.
	sums, first, skipped := s.claude.read(filepath.Join(s.home, ".claude", "projects"), since, catalog, loc)
	codexHome := filepath.Join(s.home, ".codex")
	for _, folder := range []string{"sessions", "archived_sessions"} {
		codex, firstCodex, skippedCodex := s.codex.read(filepath.Join(codexHome, folder), since, catalog, loc)
		sums = append(sums, codex...)
		if firstCodex != 0 && (first == 0 || firstCodex < first) {
			first = firstCodex
		}
		skipped += skippedCodex
	}
	opencode, skippedOpenCode := readOpenCode(ctx, s.openCodeDB, since)
	antigravity, skippedAntigravity := s.antigravity.read(ctx, s.antigravityRoots, since)
	muse, skippedMuse := s.muse.read(s.museRoot, since)
	kiro, skippedKiro := s.kiro.read(s.kiroDir, since)
	others := spends(slices.Concat(opencode, antigravity, muse, kiro), catalog)
	for _, c := range others {
		if first == 0 || c.at < first {
			first = c.at
		}
	}
	earliest := time.Time{}
	if first != 0 {
		earliest = time.Unix(0, first)
	}
	return append(sums, others), earliest, skipped + skippedOpenCode + skippedAntigravity + skippedMuse + skippedKiro
}

// Summary sums the last days days, or everything the records hold for 0.
func (s *Scanner) Summary(ctx context.Context, days int, now time.Time) protocol.Usage {
	s.mu.Lock()
	defer s.mu.Unlock()
	catalog := s.prices.Catalog(ctx)
	if days == 0 {
		sums, earliest, skipped := s.read(ctx, time.Time{}, catalog, now.Location())
		first := now
		if !earliest.IsZero() && earliest.Before(first) {
			first = earliest
		}
		from, unit := AllTime(first, now)
		return summarize(sums, catalog, days, from, unit, now, skipped)
	}
	from, unit := Window(days, now)
	sums, _, skipped := s.read(ctx, from, catalog, now.Location())
	return summarize(sums, catalog, days, from, unit, now, skipped)
}

// Unit is how wide a range's buckets are.
type Unit string

const (
	Hour  Unit = "hour"
	Day   Unit = "day"
	Week  Unit = "week"
	Month Unit = "month"
)

// Window is where a range of days begins, and how wide its buckets are:
// the last day by the hour, up to six months by local day from the
// midnight that makes days days with today, a year by week.
func Window(days int, now time.Time) (time.Time, Unit) {
	y, m, d := now.Date()
	switch {
	case days <= 1:
		return now.Truncate(time.Hour).Add(-23 * time.Hour), Hour
	case days <= 180:
		return time.Date(y, m, d-(days-1), 0, 0, 0, 0, now.Location()), Day
	default:
		return startOf(time.Date(y, m, d-(days-1), 0, 0, 0, 0, now.Location()), Week), Week
	}
}

// AllTime is where everything begins, from the first record: by week, or
// by month past two years.
func AllTime(first, now time.Time) (time.Time, Unit) {
	first = first.In(now.Location())
	if now.Sub(first) > 2*365*24*time.Hour {
		return startOf(first, Month), Month
	}
	return startOf(first, Week), Week
}

// startOf is the local start of the week (Monday) or month holding t.
func startOf(t time.Time, unit Unit) time.Time {
	y, m, d := t.Date()
	if unit == Month {
		return time.Date(y, m, 1, 0, 0, 0, 0, t.Location())
	}
	monday := (int(t.Weekday()) + 6) % 7
	return time.Date(y, m, d-monday, 0, 0, 0, 0, t.Location())
}

// next is the start of the bucket after the one starting at t. Days, weeks
// and months go by the calendar: a day can be 23 or 25 hours long.
func next(t time.Time, unit Unit) time.Time {
	y, m, d := t.Date()
	switch unit {
	case Hour:
		return t.Add(time.Hour)
	case Week:
		return time.Date(y, m, d+7, 0, 0, 0, 0, t.Location())
	case Month:
		return time.Date(y, m+1, 1, 0, 0, 0, 0, t.Location())
	default:
		return time.Date(y, m, d+1, 0, 0, 0, 0, t.Location())
	}
}

// readOpenCode reads OpenCode's assistant messages from its database with
// the sqlite3 tool, read only, as the daemon reads Cursor's and agy's.
func readOpenCode(ctx context.Context, db string, since time.Time) ([]Record, int) {
	if _, err := os.Stat(db); err != nil {
		return nil, 0
	}
	bin, err := exec.LookPath("sqlite3")
	if err != nil {
		return nil, 0
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	tables, err := exec.CommandContext(ctx, bin, "-readonly", "file:"+db+"?mode=ro",
		`select name from sqlite_master where type = 'table' and name in ('message', 'session_message')`).Output()
	if err != nil {
		return nil, 1
	}
	has := func(name string) bool { return slices.Contains(strings.Fields(string(tables)), name) }
	after := strconv.FormatInt(since.UnixMilli(), 10)
	// OpenCode 2 keeps a session's messages in session_message, and copied
	// the ones from before into it under the same ids; OpenCode 1 kept them
	// in message. A reply in both is read from session_message.
	var parts []string
	if has("session_message") {
		parts = append(parts, `select session_id as s, time_created as t, json_extract(data,'$.model.id') as m, json_extract(data,'$.model.providerID') as p,`+
			` json_extract(data,'$.tokens.input') as i, json_extract(data,'$.tokens.output') as o, json_extract(data,'$.tokens.reasoning') as r,`+
			` json_extract(data,'$.tokens.cache.read') as cr, json_extract(data,'$.tokens.cache.write') as cw`+
			` from session_message where type = 'assistant' and time_created >= `+after)
	}
	if has("message") {
		old := `select session_id as s, time_created as t, json_extract(data,'$.modelID') as m, json_extract(data,'$.providerID') as p,` +
			` json_extract(data,'$.tokens.input') as i, json_extract(data,'$.tokens.output') as o, json_extract(data,'$.tokens.reasoning') as r,` +
			` json_extract(data,'$.tokens.cache.read') as cr, json_extract(data,'$.tokens.cache.write') as cw` +
			` from message where time_created >= ` + after + ` and json_extract(data,'$.role') = 'assistant'`
		if has("session_message") {
			old += ` and id not in (select id from session_message)`
		}
		parts = append(parts, old)
	}
	if len(parts) == 0 {
		return nil, 0
	}
	out, err := exec.CommandContext(ctx, bin, "-readonly", "-json", "file:"+db+"?mode=ro", strings.Join(parts, " union all ")).Output()
	if err != nil || len(out) == 0 {
		return nil, 0
	}
	var rows []struct {
		Session   string  `json:"s"`
		Time      int64   `json:"t"`
		Model     string  `json:"m"`
		Provider  string  `json:"p"`
		Input     float64 `json:"i"`
		Output    float64 `json:"o"`
		Reasoning float64 `json:"r"`
		Read      float64 `json:"cr"`
		Write     float64 `json:"cw"`
	}
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil, 1
	}
	records := make([]Record, 0, len(rows))
	for _, r := range rows {
		tokens := protocol.Tokens{Input: int64(r.Input), CacheRead: int64(r.Read), CacheWrite: int64(r.Write), Output: int64(r.Output + r.Reasoning)}
		if tokens.Total() == 0 {
			continue
		}
		records = append(records, Record{
			Agent: protocol.KindOpenCode, Session: r.Session, Model: r.Model, Provider: r.Provider,
			At: time.UnixMilli(r.Time), Tokens: tokens, Context: tokens.Input + tokens.CacheRead + tokens.CacheWrite,
		})
	}
	return records, 0
}

// Cost is what the API would charge for a call at these prices.
func Cost(r Record, p Price) protocol.UsageCost {
	return cost(r.Tokens, r.CacheWrite1h, p.Above != nil && r.Context > p.AboveTokens, p)
}

// cost is what the API would charge for tokens, past the prompt length
// prices rise at when long.
func cost(tokens protocol.Tokens, write1h int64, long bool, p Price) protocol.UsageCost {
	if long && p.Above != nil {
		p = *p.Above
	}
	writes5m := tokens.CacheWrite - write1h
	return protocol.UsageCost{
		Input:      float64(tokens.Input) * p.Input / 1e6,
		CacheRead:  float64(tokens.CacheRead) * p.CacheRead / 1e6,
		CacheWrite: (float64(writes5m)*p.CacheWrite + float64(write1h)*p.Input*2) / 1e6,
		Output:     float64(tokens.Output) * p.Output / 1e6,
	}
}

// Summarize sums the records from from to now in buckets a unit wide.
func Summarize(records []Record, catalog Catalog, days int, from time.Time, unit Unit, now time.Time, skipped int) protocol.Usage {
	return summarize([][]spend{spends(records, catalog)}, catalog, days, from, unit, now, skipped)
}

// summarize sums what was spent from from to now in buckets a unit wide. A
// call more than one log holds is counted once, where it is first found,
// with the largest counts any of them holds: one may hold only a reply's
// first lines, written before its output was all counted.
func summarize(sums [][]spend, catalog Catalog, days int, from time.Time, unit Unit, now time.Time, skipped int) protocol.Usage {
	u := protocol.Usage{Days: days, Hourly: unit == Hour, Unit: string(unit), Skipped: skipped}
	var starts []int64
	for t := from; !t.After(now); t = next(t, unit) {
		starts = append(starts, t.UnixNano())
	}
	u.Buckets = make([]protocol.UsageBucket, len(starts))
	for i, start := range starts {
		u.Buckets[i] = protocol.UsageBucket{Start: start / 1e6, Cost: map[protocol.Kind]float64{}, Tokens: map[protocol.Kind]int64{}}
	}
	bucket := func(at int64) int {
		i := sort.Search(len(starts), func(i int) bool { return starts[i] > at }) - 1
		return i
	}

	type modelKey struct {
		kind  protocol.Kind
		model string
	}
	type sessionKey struct {
		kind    protocol.Kind
		session string
	}
	type priced struct {
		Price
		ok bool
	}
	type priceKey struct {
		label *label
		fast  bool
	}
	agents := map[protocol.Kind]*protocol.AgentUsage{}
	models := map[modelKey]*protocol.ModelUsage{}
	sessions := map[sessionKey]bool{}
	prices := map[priceKey]priced{}
	largest := map[uint64]spend{}
	for _, part := range sums {
		for _, s := range part {
			if s.key == 0 {
				continue
			}
			if most, ok := largest[s.key]; ok {
				most.grow(s)
				largest[s.key] = most
			} else {
				largest[s.key] = s
			}
		}
	}
	counted := map[uint64]bool{}
	start, end := from.UnixNano(), now.UnixNano()
	for _, part := range sums {
		for _, s := range part {
			if s.key != 0 {
				if counted[s.key] {
					continue
				}
				counted[s.key] = true
				most := largest[s.key]
				s.tokens, s.write1h, s.long, s.fast, s.credits = most.tokens, most.write1h, most.long, most.fast, most.credits
			}
			if s.at < start || s.at > end {
				continue
			}
			l := s.label
			price, found := prices[priceKey{l, s.fast}]
			if !found {
				price.Price, price.ok = catalog.Lookup(l.provider, l.model)
				if s.fast {
					price.Price = price.fast(l.model)
				}
				prices[priceKey{l, s.fast}] = price
			}
			var spent protocol.UsageCost
			if price.ok {
				spent = cost(s.tokens, s.write1h, s.long, price.Price)
				u.CacheSavings += float64(s.tokens.CacheRead) * (price.Input - price.CacheRead) / 1e6
			}
			a := agents[l.agent]
			if a == nil {
				a = &protocol.AgentUsage{Kind: l.agent}
				agents[l.agent] = a
			}
			a.Tokens = a.Tokens.Add(s.tokens)
			a.Cost += spent.Total()
			a.Credits += s.credits
			sessions[sessionKey{l.agent, l.session}] = true

			key := modelKey{l.agent, l.model}
			m := models[key]
			if m == nil {
				m = &protocol.ModelUsage{Model: l.model, Kind: l.agent, Priced: price.ok}
				models[key] = m
			}
			m.Tokens = m.Tokens.Add(s.tokens)
			m.Cost += spent.Total()
			m.Credits += s.credits
			if part := modePart(m, s); part != nil {
				part.Tokens = part.Tokens.Add(s.tokens)
				part.Cost += spent.Total()
			}

			u.Tokens = u.Tokens.Add(s.tokens)
			u.Credits += s.credits
			u.Cost.Input += spent.Input
			u.Cost.CacheRead += spent.CacheRead
			u.Cost.CacheWrite += spent.CacheWrite
			u.Cost.Output += spent.Output

			if i := bucket(s.at); i >= 0 {
				u.Buckets[i].Cost[l.agent] += spent.Total()
				u.Buckets[i].Tokens[l.agent] += s.tokens.Total()
				if s.credits > 0 {
					if u.Buckets[i].Credits == nil {
						u.Buckets[i].Credits = map[protocol.Kind]float64{}
					}
					u.Buckets[i].Credits[l.agent] += s.credits
				}
			}
		}
	}
	for session := range sessions {
		agents[session.kind].Sessions++
	}
	for _, a := range agents {
		u.Agents = append(u.Agents, *a)
	}
	sort.Slice(u.Agents, func(i, j int) bool {
		if u.Agents[i].Cost != u.Agents[j].Cost {
			return u.Agents[i].Cost > u.Agents[j].Cost
		}
		return u.Agents[i].Tokens.Total() > u.Agents[j].Tokens.Total()
	})
	for _, m := range models {
		u.Models = append(u.Models, *m)
	}
	sort.Slice(u.Models, func(i, j int) bool {
		if u.Models[i].Cost != u.Models[j].Cost {
			return u.Models[i].Cost > u.Models[j].Cost
		}
		return u.Models[i].Tokens.Total() > u.Models[j].Tokens.Total()
	})
	u.Sessions = len(sessions)
	return u
}

// modePart is the part of a model's share calls run fast or advising add
// to, made when first needed: nil for calls made the usual way.
func modePart(m *protocol.ModelUsage, s spend) *protocol.UsagePart {
	var part **protocol.UsagePart
	switch {
	case s.fast:
		part = &m.Fast
	case s.advisor:
		part = &m.Advisor
	default:
		return nil
	}
	if *part == nil {
		*part = &protocol.UsagePart{}
	}
	return *part
}
