// Package limits reads what is left of each agent's plan: a Claude
// subscription's five-hour and weekly windows, a ChatGPT plan's for Codex.
//
// Each agent is asked in its own way, and none of them hands Agentman a
// credential. Claude Code is started with no prompt and asked for its /usage
// numbers, which it reads from Anthropic with its own sign-in. Codex's shared
// daemon is asked the same; while it isn't running, Codex's rollouts say what
// was left as of its last reply. Neither token is read, copied or sent on.
//
// Answers are kept for a couple of minutes, so a page opened again and again
// doesn't start Claude Code each time.
package limits

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

const (
	// keep is how long an answer is given again without reading anew.
	keep = 2 * time.Minute
	// soonest is how soon after one read another is made even when asked
	// to read again, so a refresh pressed over and over reads once.
	soonest = 15 * time.Second
	// readTimeout bounds one read of every agent's limits.
	readTimeout = 45 * time.Second
)

// Reader answers ReqLimits.
type Reader struct {
	home string
	// codexDaemon asks Codex's shared daemon; nil, or an error, reads the
	// rollouts instead.
	codexDaemon func(context.Context) (CodexSnapshot, error)
	// claude asks Claude Code for its numbers; replaced in tests.
	claude func(context.Context) (claudeUsage, error)
	now    func() time.Time

	mu sync.Mutex
	// answer is the last read, made at read.
	answer protocol.Limits
	read   time.Time
	// reading is closed when the read in flight is done; nil when none is.
	reading chan struct{}
	// good is each agent's last answer that had numbers, shown with a
	// problem when a later read fails rather than nothing at all.
	good map[protocol.Kind]protocol.AgentLimits
}

// Option configures a Reader.
type Option func(*Reader)

// WithCodexDaemon asks Codex's shared daemon for the plan's limits before
// falling back to its rollouts.
func WithCodexDaemon(ask func(context.Context) (CodexSnapshot, error)) Option {
	return func(r *Reader) { r.codexDaemon = ask }
}

// NewReader reads the limits of the agents under home.
func NewReader(home string, options ...Option) *Reader {
	r := &Reader{home: home, now: time.Now, good: map[protocol.Kind]protocol.AgentLimits{}}
	r.claude = r.askClaude
	for _, option := range options {
		option(r)
	}
	return r
}

// Read answers with each agent's limits: the last answer when it is recent,
// else a fresh read, which every caller asking meanwhile shares. force reads
// again unless the last read was moments ago. A caller that gives up early
// is answered with what was last read; the read itself carries on, so the
// next ask finds it done.
func (r *Reader) Read(ctx context.Context, force bool) protocol.Limits {
	r.mu.Lock()
	if !r.read.IsZero() {
		age := r.now().Sub(r.read)
		if age < soonest || !force && age < keep {
			answer := r.answer
			r.mu.Unlock()
			return answer
		}
	}
	reading := r.reading
	if reading == nil {
		reading = make(chan struct{})
		r.reading = reading
		go r.readAll(reading)
	}
	r.mu.Unlock()

	select {
	case <-reading:
	case <-ctx.Done():
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.answer.Agents == nil {
		return protocol.Limits{Agents: []protocol.AgentLimits{}}
	}
	return r.answer
}

// readAll reads every agent at once and keeps the answer.
func (r *Reader) readAll(done chan struct{}) {
	ctx, cancel := context.WithTimeout(context.Background(), readTimeout)
	defer cancel()

	var claude, codex *result
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		claude = r.readClaude(ctx)
	}()
	go func() {
		defer wg.Done()
		codex = r.readCodex(ctx)
	}()
	wg.Wait()

	agents := []protocol.AgentLimits{}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, agent := range []*result{claude, codex} {
		if agent != nil {
			agents = append(agents, r.remembered(*agent))
		}
	}
	r.answer = protocol.Limits{Agents: agents}
	r.read = r.now()
	r.reading = nil
	close(done)
}

// result is one agent's answer.
type result struct {
	protocol.AgentLimits
	// failed is true when the agent couldn't be asked, as against having
	// answered that it has no limits.
	failed bool
}

// remembered keeps an answer with numbers as the agent's last good one, and
// gives a failed read the last good numbers, if there are any, with its
// problem saying why they are old. Call with r.mu held.
func (r *Reader) remembered(agent result) protocol.AgentLimits {
	if len(agent.Windows) > 0 {
		r.good[agent.Kind] = agent.AgentLimits
		return agent.AgentLimits
	}
	if !agent.failed {
		delete(r.good, agent.Kind)
		return agent.AgentLimits
	}
	last, ok := r.good[agent.Kind]
	if !ok {
		return agent.AgentLimits
	}
	last.Problem = agent.Problem
	return last
}

// installed reports whether a command is on the PATH.
func installed(command string) (string, bool) {
	path, err := exec.LookPath(command)
	return path, err == nil
}

// exists reports whether there is anything at path.
func exists(path ...string) bool {
	_, err := os.Stat(filepath.Join(path...))
	return err == nil
}

// millis is a time in milliseconds since 1970, 0 for the zero time.
func millis(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

// clamp keeps a share between 0 and 100.
func clamp(percent float64) float64 {
	return max(0, min(100, percent))
}
