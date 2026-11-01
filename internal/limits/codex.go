package limits

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Codex reports what is left of the plan two ways. Its shared daemon answers
// account/rateLimits/read, reading OpenAI with Codex's own sign-in; and each
// reply's token_count event in a rollout carries the same numbers as they
// stood then. The daemon is asked first. While it isn't running, the newest
// rollout says what was left after Codex's last reply, which stays true until
// the plan is used again, from this Mac or anywhere else.
//
// Codex's auth.json is never read.

const (
	// codexLookback is how far back rollouts are read for limits: a week's
	// window and a day over. Anything older has reset since.
	codexLookback = 8 * 24 * time.Hour
	// codexFiles is how many of the newest rollouts are read for limits.
	codexFiles = 8
	// codexTail is how much of the end of a rollout is read: the last
	// reply's numbers are near it.
	codexTail = 2 << 20
)

// CodexSnapshot is what is left of the plan Codex is signed in with.
type CodexSnapshot struct {
	// Plan is "plus", "pro", "team" and so on.
	Plan string
	// Primary and Secondary are the windows by position: the five hours
	// and the week, on a paid plan.
	Primary, Secondary *CodexWindow
	// At is when Codex reported it.
	At time.Time
}

// CodexWindow is one window of Codex's plan.
type CodexWindow struct {
	Used float64
	// Minutes is how long the window lasts; 0 when Codex doesn't say.
	Minutes int
	// ResetsAt is when it resets; the zero time when Codex doesn't say.
	ResetsAt time.Time
}

// readCodex asks Codex's shared daemon, then its rollouts, when Codex has
// been used on this Mac.
func (r *Reader) readCodex(ctx context.Context) *result {
	now := r.now()
	if r.codexDaemon != nil {
		if snapshot, err := r.codexDaemon(ctx); err == nil && (snapshot.Primary != nil || snapshot.Secondary != nil) {
			if snapshot.At.IsZero() {
				snapshot.At = now
			}
			return &result{AgentLimits: codexLimits(snapshot, false, now)}
		}
	}
	home := filepath.Join(r.home, ".codex")
	if !exists(home) {
		return nil
	}
	if snapshot, ok := lastCodexRecord(home, now); ok {
		return &result{AgentLimits: codexLimits(snapshot, true, now)}
	}
	return &result{AgentLimits: protocol.AgentLimits{
		Kind: protocol.KindCodex, CheckedAt: millis(now),
		Problem: "Codex hasn't said what is left of its plan in the past week. It does with each reply.",
	}}
}

// codexLimits turns a snapshot into windows. The windows are positions, not
// lengths: without a length, a paid plan's first is its five hours and a
// free plan's its month, and the second is the week. A window the rollouts
// say has reset since is back to nothing, its next reset not yet known.
func codexLimits(snapshot CodexSnapshot, lastTurn bool, now time.Time) protocol.AgentLimits {
	agent := protocol.AgentLimits{
		Kind: protocol.KindCodex, Plan: planName(snapshot.Plan),
		CheckedAt: millis(snapshot.At), LastTurn: lastTurn,
	}
	first := 300
	if snapshot.Plan == "free" || snapshot.Plan == "go" {
		first = 43200
	}
	for _, position := range []struct {
		id      string
		window  *CodexWindow
		minutes int
	}{{"primary", snapshot.Primary, first}, {"secondary", snapshot.Secondary, 10080}} {
		window := position.window
		if window == nil {
			continue
		}
		minutes := window.Minutes
		if minutes <= 0 {
			minutes = position.minutes
		}
		kind, label := "session", "Session"
		switch {
		case minutes >= 43200:
			kind, label = "monthly", "Monthly"
		case minutes >= 10080:
			kind, label = "weekly", "Weekly"
		}
		out := protocol.LimitWindow{ID: position.id, Kind: kind, Label: label, Used: clamp(window.Used),
			ResetsAt: millis(window.ResetsAt), Minutes: minutes}
		if lastTurn && !window.ResetsAt.IsZero() && !window.ResetsAt.After(now) {
			out.Used, out.ResetsAt = 0, 0
		}
		agent.Windows = append(agent.Windows, out)
	}
	return agent
}

// codexRecord is the part of a rollout line read for limits.
type codexRecord struct {
	Timestamp time.Time `json:"timestamp"`
	Type      string    `json:"type"`
	Payload   struct {
		Type       string `json:"type"`
		RateLimits *struct {
			// LimitID is the metered bucket: "codex", or absent from older
			// Codex, for Codex's own; another product's is passed over.
			LimitID   *string            `json:"limit_id"`
			PlanType  string             `json:"plan_type"`
			Primary   *codexRecordWindow `json:"primary"`
			Secondary *codexRecordWindow `json:"secondary"`
		} `json:"rate_limits"`
	} `json:"payload"`
}

type codexRecordWindow struct {
	UsedPercent   float64 `json:"used_percent"`
	WindowMinutes int     `json:"window_minutes"`
	// ResetsAt is in seconds since 1970; older Codex said how many seconds
	// from the reply instead.
	ResetsAt        int64 `json:"resets_at"`
	ResetsInSeconds int64 `json:"resets_in_seconds"`
}

func (w *codexRecordWindow) window(at time.Time) *CodexWindow {
	if w == nil {
		return nil
	}
	out := &CodexWindow{Used: w.UsedPercent, Minutes: w.WindowMinutes}
	switch {
	case w.ResetsAt > 0:
		out.ResetsAt = time.Unix(w.ResetsAt, 0)
	case w.ResetsInSeconds > 0:
		out.ResetsAt = at.Add(time.Duration(w.ResetsInSeconds) * time.Second)
	}
	return out
}

// lastCodexRecord finds the newest limits in the rollouts written in the past
// week: of the few written to last, the newest reply that carried them.
func lastCodexRecord(codexHome string, now time.Time) (CodexSnapshot, bool) {
	type rollout struct {
		path    string
		written time.Time
	}
	var rollouts []rollout
	since := now.Add(-codexLookback)
	for _, dir := range []string{"sessions", "archived_sessions"} {
		filepath.WalkDir(filepath.Join(codexHome, dir), func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") ||
				!strings.HasPrefix(entry.Name(), "rollout-") {
				return nil
			}
			if info, err := entry.Info(); err == nil && info.ModTime().After(since) {
				rollouts = append(rollouts, rollout{path, info.ModTime()})
			}
			return nil
		})
	}
	sort.Slice(rollouts, func(i, j int) bool { return rollouts[i].written.After(rollouts[j].written) })
	var best CodexSnapshot
	found := false
	for i, file := range rollouts {
		// A rollout written to before the newest numbers found can't hold
		// newer ones.
		if i >= codexFiles || found && file.written.Before(best.At) {
			break
		}
		if snapshot, ok := lastRecordIn(file.path); ok && (!found || snapshot.At.After(best.At)) {
			best, found = snapshot, true
		}
	}
	return best, found
}

// lastRecordIn reads the end of one rollout for the last reply that carried
// Codex's own limits.
func lastRecordIn(path string) (CodexSnapshot, bool) {
	file, err := os.Open(path)
	if err != nil {
		return CodexSnapshot{}, false
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return CodexSnapshot{}, false
	}
	start := max(0, info.Size()-codexTail)
	data, err := io.ReadAll(io.NewSectionReader(file, start, info.Size()-start))
	if err != nil {
		return CodexSnapshot{}, false
	}
	for len(data) > 0 {
		cut := bytes.LastIndexByte(data, '\n')
		line := data[cut+1:]
		data = data[:max(cut, 0)]
		if !bytes.Contains(line, []byte(`"rate_limits"`)) {
			continue
		}
		var record codexRecord
		if json.Unmarshal(line, &record) != nil || record.Type != "event_msg" || record.Payload.Type != "token_count" {
			continue
		}
		limits := record.Payload.RateLimits
		if limits == nil || limits.LimitID != nil && *limits.LimitID != "" && *limits.LimitID != "codex" ||
			limits.Primary == nil && limits.Secondary == nil {
			continue
		}
		return CodexSnapshot{
			Plan:      limits.PlanType,
			Primary:   limits.Primary.window(record.Timestamp),
			Secondary: limits.Secondary.window(record.Timestamp),
			At:        record.Timestamp,
		}, true
	}
	return CodexSnapshot{}, false
}
