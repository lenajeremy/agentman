package limits

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Claude Code answers its SDK's get_usage request with the numbers its /usage
// screen shows: how much of the plan's five hours and week is used, and of
// any per-model week, which it reads from Anthropic with its own sign-in.
// Asking takes starting Claude Code with no prompt, as T3 Code does: nothing
// is sent to a model, no conversation is written, no hook or MCP server runs.
// The protocol is marked experimental, so everything that knows its shape is
// in this file. Checked against Claude Code 2.1.316 on 2026-11-01.

const (
	// claudeTimeout bounds starting Claude Code and asking it, which reads
	// the account over the network.
	claudeTimeout = 30 * time.Second
	// claudeExit is how long Claude Code is given to leave once answered.
	claudeExit = 3 * time.Second
	// claudeLineLimit bounds one line Claude Code writes. Its answer to
	// initialize lists every command, agent and model it knows.
	claudeLineLimit = 32 << 20
)

// claudeArgs start Claude Code speaking its SDK's protocol on stdin and
// stdout, with nothing kept and nothing run besides it: no conversation
// written, the hooks Agentman and others put in the user's settings off, and
// no MCP server started. The user's own settings are still read, as they may
// say how Claude Code signs in.
var claudeArgs = []string{
	"--input-format", "stream-json",
	"--output-format", "stream-json",
	"--verbose",
	"--no-session-persistence",
	"--setting-sources", "user",
	"--settings", `{"disableAllHooks":true}`,
	"--strict-mcp-config",
}

// claudeEnv is the daemon's environment for Claude Code, without what would
// make it think it runs inside another Claude Code, and with it told to leave
// updates, IDEs and claude.ai's MCP servers alone.
func claudeEnv(env []string) []string {
	out := make([]string, 0, len(env)+4)
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		if name == "CLAUDECODE" || name == "CLAUDE_CODE_ENTRYPOINT" {
			continue
		}
		out = append(out, entry)
	}
	return append(out,
		"DISABLE_AUTOUPDATER=1",
		"ENABLE_CLAUDEAI_MCP_SERVERS=false",
		"CLAUDE_CODE_AUTO_CONNECT_IDE=0",
		"CLAUDE_CODE_IDE_SKIP_AUTO_INSTALL=1",
	)
}

// claudeUsage is the part of get_usage's answer read here.
type claudeUsage struct {
	// SubscriptionType is "pro", "max", "team" or "enterprise"; null for an
	// API key.
	SubscriptionType *string `json:"subscription_type"`
	// RateLimitsAvailable is false when the plan has no such limits: an API
	// key, Bedrock or Vertex.
	RateLimitsAvailable bool              `json:"rate_limits_available"`
	RateLimits          *claudeRateLimits `json:"rate_limits"`
}

type claudeRateLimits struct {
	FiveHour       *claudeWindow `json:"five_hour"`
	SevenDay       *claudeWindow `json:"seven_day"`
	SevenDayOpus   *claudeWindow `json:"seven_day_opus"`
	SevenDaySonnet *claudeWindow `json:"seven_day_sonnet"`
	// ModelScoped is each model's own week, named by the server.
	ModelScoped []struct {
		DisplayName string `json:"display_name"`
		claudeWindow
	} `json:"model_scoped"`
	// ExtraUsage is paid usage past the plan, with a monthly cap.
	ExtraUsage *struct {
		IsEnabled    bool     `json:"is_enabled"`
		MonthlyLimit *float64 `json:"monthly_limit"`
		UsedCredits  *float64 `json:"used_credits"`
		Utilization  *float64 `json:"utilization"`
	} `json:"extra_usage"`
}

// claudeWindow is one window: Utilization is 0 to 100, ResetsAt ISO 8601.
type claudeWindow struct {
	Utilization *float64 `json:"utilization"`
	ResetsAt    *string  `json:"resets_at"`
}

// readClaude asks Claude Code, when it is installed.
func (r *Reader) readClaude(ctx context.Context) *result {
	if _, ok := installed("claude"); !ok {
		return nil
	}
	usage, err := r.claude(ctx)
	if err != nil {
		return &result{failed: true, AgentLimits: protocol.AgentLimits{
			Kind: protocol.KindClaude, CheckedAt: millis(r.now()),
			Problem: "Claude Code didn't say what is left of its plan just now.",
		}}
	}
	return &result{AgentLimits: claudeLimits(usage, r.now())}
}

// claudeLimits turns get_usage's answer into windows, the five hours first.
// Opus's and Sonnet's own weeks come from older Claude Codes, each model's
// week from newer ones; a week named both ways is shown once.
func claudeLimits(usage claudeUsage, now time.Time) protocol.AgentLimits {
	agent := protocol.AgentLimits{Kind: protocol.KindClaude, CheckedAt: millis(now)}
	if usage.SubscriptionType != nil {
		agent.Plan = planName(*usage.SubscriptionType)
	}
	switch {
	case !usage.RateLimitsAvailable:
		agent.Problem = "Claude Code isn't signed in to a Claude plan, so there are no plan limits."
		return agent
	case usage.RateLimits == nil:
		agent.Problem = "Claude Code couldn't read its plan's limits just now."
		return agent
	}
	limits := usage.RateLimits
	add := func(id, kind, label string, minutes int, window *claudeWindow) {
		if window == nil || window.Utilization == nil {
			return
		}
		for _, have := range agent.Windows {
			if have.ID == id {
				return
			}
		}
		out := protocol.LimitWindow{ID: id, Kind: kind, Label: label, Used: clamp(*window.Utilization), Minutes: minutes}
		if window.ResetsAt != nil {
			if at, err := time.Parse(time.RFC3339, *window.ResetsAt); err == nil {
				out.ResetsAt = at.UnixMilli()
			}
		}
		agent.Windows = append(agent.Windows, out)
	}
	add("five_hour", "session", "Session", 300, limits.FiveHour)
	add("seven_day", "weekly", "Weekly", 10080, limits.SevenDay)
	for _, model := range limits.ModelScoped {
		if name := strings.TrimSpace(model.DisplayName); name != "" {
			add(weekOf(name), "weekly", "Weekly · "+name, 10080, &model.claudeWindow)
		}
	}
	add(weekOf("Opus"), "weekly", "Weekly · Opus", 10080, limits.SevenDayOpus)
	add(weekOf("Sonnet"), "weekly", "Weekly · Sonnet", 10080, limits.SevenDaySonnet)
	if extra := limits.ExtraUsage; extra != nil && extra.IsEnabled {
		used := extra.Utilization
		if extra.UsedCredits != nil && extra.MonthlyLimit != nil && *extra.MonthlyLimit > 0 {
			share := *extra.UsedCredits / *extra.MonthlyLimit * 100
			used = &share
		}
		add("extra_usage", "monthly", "Extra usage", 0, &claudeWindow{Utilization: used})
	}
	if len(agent.Windows) == 0 {
		agent.Problem = "Claude Code's plan reported no limits."
	}
	return agent
}

var notWordy = regexp.MustCompile(`[^a-z0-9]+`)

// weekOf is the id of a model's own week: "seven_day_opus".
func weekOf(model string) string {
	return "seven_day_" + strings.Trim(notWordy.ReplaceAllString(strings.ToLower(model), "_"), "_")
}

// planName writes a plan as its maker does: "max" is "Max".
func planName(plan string) string {
	plan = strings.TrimSpace(strings.ReplaceAll(plan, "_", " "))
	if plan == "" {
		return ""
	}
	return strings.ToUpper(plan[:1]) + plan[1:]
}

// claudeMessage is one line from Claude Code: of them, only the answers to
// what was asked are read.
type claudeMessage struct {
	Type     string `json:"type"`
	Response struct {
		Subtype   string          `json:"subtype"`
		RequestID string          `json:"request_id"`
		Error     string          `json:"error"`
		Response  json.RawMessage `json:"response"`
	} `json:"response"`
}

// askClaude starts Claude Code, introduces itself as its SDK does, and asks
// for the plan's usage, leaving out the scan of local transcripts that the
// /usage screen also shows.
func (r *Reader) askClaude(ctx context.Context) (claudeUsage, error) {
	binary, ok := installed("claude")
	if !ok {
		return claudeUsage{}, errors.New("limits: Claude Code is not installed")
	}
	return askClaude(ctx, binary, r.home, claudeEnv(os.Environ()))
}

func askClaude(ctx context.Context, binary, dir string, env []string) (claudeUsage, error) {
	ctx, cancel := context.WithTimeout(ctx, claudeTimeout)
	defer cancel()
	cmd := exec.Command(binary, claudeArgs...)
	cmd.Dir = dir
	cmd.Env = env
	inGroup(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return claudeUsage{}, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return claudeUsage{}, err
	}
	if err := cmd.Start(); err != nil {
		return claudeUsage{}, err
	}
	// Claude Code leaves when its input ends; one that doesn't, or that was
	// given up on, is stopped with all it started. Wait then closes its
	// output, which ends the reading below.
	defer func() {
		stdin.Close()
		exited := make(chan struct{})
		go func() {
			cmd.Wait()
			close(exited)
		}()
		select {
		case <-exited:
		case <-time.After(claudeExit):
			killGroup(cmd)
			<-exited
		}
	}()

	// The answers are read on their own, so that a Claude Code that stops
	// talking can be given up on.
	answers := make(chan claudeMessage, 4)
	asked := make(chan struct{})
	defer close(asked)
	go func() {
		defer close(answers)
		lines := bufio.NewScanner(stdout)
		lines.Buffer(make([]byte, 0, 64<<10), claudeLineLimit)
		for lines.Scan() {
			var message claudeMessage
			if json.Unmarshal(lines.Bytes(), &message) != nil || message.Type != "control_response" {
				continue
			}
			select {
			case answers <- message:
			case <-asked:
				return
			}
		}
	}()
	ask := func(id string, request map[string]any) (json.RawMessage, error) {
		line, err := json.Marshal(map[string]any{"type": "control_request", "request_id": id, "request": request})
		if err != nil {
			return nil, err
		}
		if _, err := stdin.Write(append(line, '\n')); err != nil {
			return nil, err
		}
		for {
			select {
			case message, ok := <-answers:
				if !ok {
					return nil, io.ErrUnexpectedEOF
				}
				if message.Response.RequestID != id {
					continue
				}
				if message.Response.Subtype != "success" {
					return nil, fmt.Errorf("limits: Claude Code refused %s", request["subtype"])
				}
				return message.Response.Response, nil
			case <-ctx.Done():
				killGroup(cmd)
				return nil, ctx.Err()
			}
		}
	}
	if _, err := ask("agentman-init", map[string]any{"subtype": "initialize"}); err != nil {
		return claudeUsage{}, err
	}
	answer, err := ask("agentman-usage", map[string]any{"subtype": "get_usage", "skip_behaviors": true})
	if err != nil {
		return claudeUsage{}, err
	}
	var usage claudeUsage
	if err := json.Unmarshal(answer, &usage); err != nil {
		return claudeUsage{}, err
	}
	return usage, nil
}
