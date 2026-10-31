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
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Scanner reads the records, keeping what it read of each log so the next
// read only takes what was added.
type Scanner struct {
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

// Records reads every agent's calls since since.
func (s *Scanner) Records(ctx context.Context, since time.Time) ([]Record, int) {
	// A log changed in the range may hold calls from before it; they are
	// left out when summed.
	claude, skippedClaude := s.claude.read(filepath.Join(s.home, ".claude", "projects"), since)
	codexHome := filepath.Join(s.home, ".codex")
	codex, skippedCodex := s.codex.read(filepath.Join(codexHome, "sessions"), since)
	archived, skippedArchived := s.codex.read(filepath.Join(codexHome, "archived_sessions"), since)
	out := make([]Record, 0, len(claude)+len(codex)+len(archived))
	out = append(out, claude...)
	out = append(out, codex...)
	out = append(out, archived...)
	opencode, skippedOpenCode := readOpenCode(ctx, s.openCodeDB, since)
	out = append(out, opencode...)
	antigravity, skippedAntigravity := s.antigravity.read(ctx, s.antigravityRoots, since)
	out = append(out, antigravity...)
	return out, skippedClaude + skippedCodex + skippedArchived + skippedOpenCode + skippedAntigravity
}

// Summary sums the last days days.
func (s *Scanner) Summary(ctx context.Context, days int, now time.Time) protocol.Usage {
	from, step := Window(days, now)
	records, skipped := s.Records(ctx, from)
	return Summarize(records, s.prices.Catalog(ctx), days, from, step, now, skipped)
}

// Window is where a range of days begins, and how wide its buckets are:
// the last day by the hour, longer ranges by local day, starting at the
// midnight that makes days days with today.
func Window(days int, now time.Time) (time.Time, time.Duration) {
	if days <= 1 {
		return now.Truncate(time.Hour).Add(-23 * time.Hour), time.Hour
	}
	y, m, d := now.Date()
	return time.Date(y, m, d-(days-1), 0, 0, 0, 0, now.Location()), 24 * time.Hour
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
	query := `select session_id as s, time_created as t, json_extract(data,'$.modelID') as m, json_extract(data,'$.providerID') as p,` +
		` json_extract(data,'$.tokens.input') as i, json_extract(data,'$.tokens.output') as o, json_extract(data,'$.tokens.reasoning') as r,` +
		` json_extract(data,'$.tokens.cache.read') as cr, json_extract(data,'$.tokens.cache.write') as cw` +
		` from message where time_created >= ` + strconv.FormatInt(since.UnixMilli(), 10) +
		` and json_extract(data,'$.role') = 'assistant'`
	out, err := exec.CommandContext(ctx, bin, "-readonly", "-json", "file:"+db+"?mode=ro", query).Output()
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
	if p.Above != nil && r.Context > p.AboveTokens {
		p = *p.Above
	}
	writes5m := r.Tokens.CacheWrite - r.CacheWrite1h
	return protocol.UsageCost{
		Input:      float64(r.Tokens.Input) * p.Input / 1e6,
		CacheRead:  float64(r.Tokens.CacheRead) * p.CacheRead / 1e6,
		CacheWrite: (float64(writes5m)*p.CacheWrite + float64(r.CacheWrite1h)*p.Input*2) / 1e6,
		Output:     float64(r.Tokens.Output) * p.Output / 1e6,
	}
}

// Summarize sums the records from from to now in buckets step wide.
func Summarize(records []Record, catalog Catalog, days int, from time.Time, step time.Duration, now time.Time, skipped int) protocol.Usage {
	u := protocol.Usage{Days: days, Hourly: step == time.Hour, Skipped: skipped}
	// The buckets, local midnights apart for days (a day can be 23 or 25
	// hours long), hours apart for the last day.
	var starts []time.Time
	for t := from; !t.After(now); {
		starts = append(starts, t)
		if step == time.Hour {
			t = t.Add(time.Hour)
		} else {
			y, m, d := t.Date()
			t = time.Date(y, m, d+1, 0, 0, 0, 0, t.Location())
		}
	}
	u.Buckets = make([]protocol.UsageBucket, len(starts))
	for i, start := range starts {
		u.Buckets[i] = protocol.UsageBucket{Start: start.UnixMilli(), Cost: map[protocol.Kind]float64{}, Tokens: map[protocol.Kind]int64{}}
	}
	bucket := func(at time.Time) int {
		i := sort.Search(len(starts), func(i int) bool { return starts[i].After(at) }) - 1
		return i
	}

	type modelKey struct {
		kind  protocol.Kind
		model string
	}
	agents := map[protocol.Kind]*protocol.AgentUsage{}
	sessions := map[protocol.Kind]map[string]bool{}
	models := map[modelKey]*protocol.ModelUsage{}
	seen := map[string]bool{}
	allSessions := map[string]bool{}
	for _, r := range records {
		if r.At.Before(from) || r.At.After(now) {
			continue
		}
		if r.Key != "" {
			if seen[r.Key] {
				continue
			}
			seen[r.Key] = true
		}
		price, priced := catalog.Lookup(r.Provider, r.Model)
		var cost protocol.UsageCost
		if priced {
			cost = Cost(r, price)
			u.CacheSavings += float64(r.Tokens.CacheRead) * (price.Input - price.CacheRead) / 1e6
		}
		a := agents[r.Agent]
		if a == nil {
			a = &protocol.AgentUsage{Kind: r.Agent}
			agents[r.Agent] = a
			sessions[r.Agent] = map[string]bool{}
		}
		a.Tokens = a.Tokens.Add(r.Tokens)
		a.Cost += cost.Total()
		sessions[r.Agent][r.Session] = true
		allSessions[string(r.Agent)+"\x00"+r.Session] = true

		key := modelKey{r.Agent, r.Model}
		m := models[key]
		if m == nil {
			m = &protocol.ModelUsage{Model: r.Model, Kind: r.Agent, Priced: priced}
			models[key] = m
		}
		m.Tokens = m.Tokens.Add(r.Tokens)
		m.Cost += cost.Total()

		u.Tokens = u.Tokens.Add(r.Tokens)
		u.Cost.Input += cost.Input
		u.Cost.CacheRead += cost.CacheRead
		u.Cost.CacheWrite += cost.CacheWrite
		u.Cost.Output += cost.Output

		if i := bucket(r.At); i >= 0 {
			u.Buckets[i].Cost[r.Agent] += cost.Total()
			u.Buckets[i].Tokens[r.Agent] += r.Tokens.Total()
		}
	}
	for kind, a := range agents {
		a.Sessions = len(sessions[kind])
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
	u.Sessions = len(allSessions)
	return u
}
