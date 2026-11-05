package limits

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Antigravity (agy) answers `agy -p "/usage" --output-format json` with its
// plan's quotas, which it reads from Google with its own sign-in: for each
// group of models that share a quota (Gemini; Claude and GPT; a model with
// a quota of its own), the share left of each window, five hours and a
// week, and when each resets. Since agy 1.1.11 a read-only slash command in
// print mode is answered without starting a turn, spending quota or writing
// a conversation; an older agy would send "/usage" to a model as a prompt,
// so agy's version is asked first. Nothing of agy's sign-in is read here.
// Checked against agy 1.5.1 on 2026-11-05.

const (
	// agyTimeout bounds asking agy its version and its quotas, which it
	// reads over the network.
	agyTimeout = 20 * time.Second
	// agyOutputLimit bounds what agy may write.
	agyOutputLimit = 1 << 20
)

// agyFirstVersion is the first agy that answers /usage in print mode.
var agyFirstVersion = [3]int{1, 1, 11}

var (
	errAgyTooOld = errors.New("limits: this agy can't answer /usage without a turn")
	errAgyFailed = errors.New("limits: agy couldn't read its quotas")
	errAgyTurn   = errors.New("limits: agy started a turn to answer /usage")
	errAgyLong   = errors.New("limits: agy wrote too much")
)

// agyUsage is the part of agy's answer read here.
type agyUsage struct {
	// Status is "SUCCESS" when agy read its quotas.
	Status         string `json:"status"`
	ConversationID string `json:"conversation_id"`
	NumTurns       int    `json:"num_turns"`
	Command        struct {
		Name string `json:"name"`
		Data struct {
			Groups []agyGroup `json:"groups"`
		} `json:"data"`
	} `json:"command"`
}

// agyGroup is models that share a quota: "Gemini Models".
type agyGroup struct {
	Name    string      `json:"name"`
	Buckets []agyBucket `json:"buckets"`
}

// agyBucket is one window of a group's quota. Window is "5h" or "weekly";
// RemainingFraction is 0 to 1; ResetTime is RFC 3339, in UTC.
type agyBucket struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	Window            string   `json:"window"`
	RemainingFraction *float64 `json:"remaining_fraction"`
	ResetTime         string   `json:"reset_time"`
}

// readAntigravity asks agy, when it is installed.
func (r *Reader) readAntigravity(ctx context.Context) *result {
	if _, ok := installed("agy"); !ok {
		return nil
	}
	usage, err := r.antigravity(ctx)
	now := r.now()
	switch {
	case errors.Is(err, errAgyTooOld):
		return &result{AgentLimits: protocol.AgentLimits{
			Kind: protocol.KindAntigravity, CheckedAt: millis(now),
			Problem: "This Antigravity is too old to say what is left of its plan. Update it with agy update.",
		}}
	case errors.Is(err, errAgyFailed):
		return &result{failed: true, AgentLimits: protocol.AgentLimits{
			Kind: protocol.KindAntigravity, CheckedAt: millis(now),
			Problem: "Antigravity couldn't read its plan's limits just now.",
		}}
	case err != nil:
		return &result{failed: true, AgentLimits: protocol.AgentLimits{
			Kind: protocol.KindAntigravity, CheckedAt: millis(now),
			Problem: "Antigravity didn't say what is left of its plan just now.",
		}}
	}
	return &result{AgentLimits: antigravityLimits(usage, now)}
}

// antigravityLimits turns agy's quotas into windows, each named by its
// window and its group: "Weekly · Gemini".
func antigravityLimits(usage agyUsage, now time.Time) protocol.AgentLimits {
	agent := protocol.AgentLimits{Kind: protocol.KindAntigravity, CheckedAt: millis(now)}
	seen := map[string]bool{}
	for _, group := range usage.Command.Data.Groups {
		name := agyGroupName(group.Name)
		for _, bucket := range group.Buckets {
			if bucket.RemainingFraction == nil {
				continue
			}
			kind, label, minutes := agyWindow(bucket.Window)
			if label == "" {
				label = strings.TrimSpace(bucket.Name)
			}
			if name != "" {
				label += " · " + name
			}
			id := strings.TrimSpace(bucket.ID)
			if id == "" {
				id = strings.Trim(notWordy.ReplaceAllString(strings.ToLower(group.Name+" "+bucket.Window), "_"), "_")
			}
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			out := protocol.LimitWindow{
				ID: id, Kind: kind, Label: label, Minutes: minutes,
				Used: clamp((1 - *bucket.RemainingFraction) * 100),
			}
			// A window not used yet says it resets a whole window from now,
			// a moment later each time it is asked: its clock starts at its
			// first use, so no reset is shown, as for Claude's.
			if at, err := time.Parse(time.RFC3339, bucket.ResetTime); err == nil &&
				!(out.Used == 0 && minutes > 0 && at.Sub(now) >= time.Duration(minutes)*time.Minute-time.Minute) {
				out.ResetsAt = at.UnixMilli()
			}
			agent.Windows = append(agent.Windows, out)
		}
	}
	if len(agent.Windows) == 0 {
		agent.Problem = "Antigravity's plan reported no limits."
	}
	return agent
}

var agyHours = regexp.MustCompile(`^(\d+)\s*h$`)

// agyWindow names a window as the other agents' are: "5h" is the session,
// "weekly" the week. One not known is left to its bucket's own name.
func agyWindow(window string) (kind, label string, minutes int) {
	window = strings.ToLower(strings.TrimSpace(window))
	switch window {
	case "weekly", "week", "7d":
		return "weekly", "Weekly", 10080
	case "monthly", "month", "30d":
		return "monthly", "Monthly", 43200
	case "daily", "day", "1d", "24h":
		return "other", "Daily", 1440
	}
	if match := agyHours.FindStringSubmatch(window); match != nil {
		if hours, err := strconv.Atoi(match[1]); err == nil && hours > 0 && hours < 24 {
			return "session", "Session", hours * 60
		}
	}
	return "other", "", 0
}

// agyGroupName shortens a group's name for a label: "Gemini Models" is
// Gemini, "Claude and GPT models" Claude & GPT.
func agyGroupName(name string) string {
	name = strings.TrimSpace(name)
	if lower := strings.ToLower(name); strings.HasSuffix(lower, " models") {
		name = strings.TrimSpace(name[:len(name)-len(" models")])
	}
	return strings.ReplaceAll(name, " and ", " & ")
}

// askAntigravity finds agy and asks it.
func (r *Reader) askAntigravity(ctx context.Context) (agyUsage, error) {
	binary, ok := installed("agy")
	if !ok {
		return agyUsage{}, errors.New("limits: agy is not installed")
	}
	return askAntigravity(ctx, binary, os.Environ())
}

// askAntigravity asks agy its version, and when it is new enough, its
// quotas, in an empty folder of its own: agy takes the folder it is started
// in as its workspace.
func askAntigravity(ctx context.Context, binary string, env []string) (agyUsage, error) {
	ctx, cancel := context.WithTimeout(ctx, agyTimeout)
	defer cancel()
	version, err := runAgy(ctx, binary, env, "", "--version")
	if err != nil {
		return agyUsage{}, err
	}
	if !agyAnswersInPrint(string(version)) {
		return agyUsage{}, errAgyTooOld
	}
	dir, err := os.MkdirTemp("", "agentman-agy-limits-")
	if err != nil {
		return agyUsage{}, err
	}
	defer os.RemoveAll(dir)
	out, err := runAgy(ctx, binary, env, dir, "-p", "/usage", "--output-format", "json", "--print-timeout", "15s")
	if err != nil {
		return agyUsage{}, err
	}
	usage, err := parseAgyUsage(out)
	if err != nil {
		return agyUsage{}, err
	}
	switch {
	case usage.NumTurns > 0 || usage.ConversationID != "":
		return agyUsage{}, errAgyTurn
	case usage.Status != "SUCCESS" || usage.Command.Name != "" && usage.Command.Name != "usage":
		return agyUsage{}, errAgyFailed
	}
	return usage, nil
}

// parseAgyUsage reads agy's answer: one JSON object, the last line that is
// one should agy write anything before it.
func parseAgyUsage(out []byte) (agyUsage, error) {
	var usage agyUsage
	err := json.Unmarshal(bytes.TrimSpace(out), &usage)
	if err == nil {
		return usage, nil
	}
	lines := bytes.Split(bytes.TrimSpace(out), []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		if line := bytes.TrimSpace(lines[i]); bytes.HasPrefix(line, []byte("{")) && json.Unmarshal(line, &usage) == nil {
			return usage, nil
		}
	}
	return agyUsage{}, err
}

var agyVersion = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

// agyAnswersInPrint is whether agy's --version names one that answers
// /usage in print mode.
func agyAnswersInPrint(version string) bool {
	match := agyVersion.FindStringSubmatch(version)
	if match == nil {
		return false
	}
	for i, first := range agyFirstVersion {
		part, _ := strconv.Atoi(match[i+1])
		if part != first {
			return part > first
		}
	}
	return true
}

// runAgy runs agy with nothing on its input (it waits on an open one) and
// gives what it wrote; stopped, with all it started, when ctx ends.
func runAgy(ctx context.Context, binary string, env []string, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = dir
	cmd.Env = env
	inGroup(cmd)
	cmd.Cancel = func() error {
		killGroup(cmd)
		return nil
	}
	cmd.WaitDelay = time.Second
	out := &cappedBuffer{limit: agyOutputLimit}
	cmd.Stdout = out
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil && out.Len() == 0 {
		return nil, err
	}
	if out.full {
		return nil, errAgyLong
	}
	return out.Bytes(), nil
}

// cappedBuffer keeps up to limit bytes, and fails a write past them.
type cappedBuffer struct {
	bytes.Buffer
	limit int
	full  bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.limit {
		b.full = true
		return 0, errAgyLong
	}
	return b.Buffer.Write(p)
}
