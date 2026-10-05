package source

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/question"
)

// Kiro switches a session's agent with `/agent swap <name>` and its model with
// `/model <id>`, typed at its prompt, and says the result on its status line:
//
//	• Agent changed to kiro_guide          kiro_guide · auto · ◔ 4%
//	• Model changed to claude-haiku-4.5    kiro_default · claude-haiku-4.5 · ◔ 4%
//
// Both are this session's alone. Checked with Kiro CLI 2.27.1: after a
// /model switch a fresh Kiro still started on its default model, and Kiro's
// settings file was unchanged. A name Kiro does not know prints "Unknown
// agent: …" or "Unknown model: …" and changes nothing.
//
// The phone offers an agent as a mode: Kiro's agents are its modes, and the
// planner is the one Kiro's own interface calls a mode.

// kiroBuiltinAgents are the agents Kiro's /agent picker offers in every
// folder (2.27.1). `kiro-cli agent list` names kiro_help instead of
// kiro_guide; the picker, which is what is driven here, has kiro_guide.
var kiroBuiltinAgents = []string{"kiro_default", "kiro_planner", "kiro_guide"}

// kiroAgentNamePattern is what an agent name must look like to be typed into
// `/agent swap`: one word, nothing a terminal or Kiro's parser could read as
// more than a name.
var kiroAgentNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// Limits from the protocol: what the daemon would trim anyway.
const (
	maxKiroModes  = 16
	maxKiroModels = 64
)

// kiroAgents lists the agents a session in cwd can swap to: Kiro's built-in
// ones, then the user's global agents, then the folder's own.
func (s *KiroSource) kiroAgents(cwd, current string) []string {
	agents := append([]string(nil), kiroBuiltinAgents...)
	seen := map[string]bool{}
	for _, agent := range agents {
		seen[agent] = true
	}
	add := func(name string) {
		if len(agents) < maxKiroModes && !seen[name] && kiroAgentNamePattern.MatchString(name) {
			seen[name] = true
			agents = append(agents, name)
		}
	}
	dirs := []string{filepath.Join(s.home, ".kiro", "agents")}
	if filepath.IsAbs(cwd) {
		dirs = append(dirs, filepath.Join(cwd, ".kiro", "agents"))
	}
	for _, dir := range dirs {
		for _, name := range kiroAgentFiles(dir) {
			add(name)
		}
	}
	add(current)
	return agents
}

// kiroAgentFiles names the agents defined in a directory of agent files, by
// the name each file gives itself.
func kiroAgentFiles(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		raw, err := readBoundedFile(filepath.Join(dir, entry.Name()), 1<<20)
		if err != nil {
			continue
		}
		var agent struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(raw, &agent) == nil && agent.Name != "" {
			names = append(names, agent.Name)
		}
	}
	sort.Strings(names)
	return names
}

// kiroModelCatalog is the list of models Kiro offers, from
// `kiro-cli chat --list-models -f json`. Asking takes a second and a half and
// reaches Kiro's service, so the answer is kept for ten minutes and refreshed
// in the background: discovery never waits for it, and goes without until the
// first answer arrives.
type kiroModelCatalog struct {
	fetch func(context.Context) ([]string, error)

	mu       sync.Mutex
	models   []string
	fetched  time.Time
	fetching bool
}

const kiroModelCatalogTTL = 10 * time.Minute

// list returns the models known now, starting a refresh when they are stale.
func (c *kiroModelCatalog) list() []string {
	if c == nil || c.fetch == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.fetching && time.Since(c.fetched) >= kiroModelCatalogTTL {
		c.fetching = true
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			models, err := c.fetch(ctx)
			c.mu.Lock()
			defer c.mu.Unlock()
			c.fetching = false
			// A failure is retried after the same wait as a success, rather
			// than every sweep: a Kiro that is logged out stays logged out.
			c.fetched = time.Now()
			if err == nil {
				c.models = models
			}
		}()
	}
	return c.models
}

// kiroListModels asks the installed Kiro which models it offers.
func kiroListModels(ctx context.Context) ([]string, error) {
	binary, err := exec.LookPath("kiro-cli")
	if err != nil {
		return nil, err
	}
	out, err := exec.CommandContext(ctx, binary, "chat", "--list-models", "-f", "json").Output()
	if err != nil {
		return nil, err
	}
	return parseKiroModels(out)
}

// parseKiroModels reads `kiro-cli chat --list-models -f json`:
//
//	{"models":[{"model_id":"auto","model_name":"auto",…},…],"default_model":"auto"}
func parseKiroModels(raw []byte) ([]string, error) {
	var listing struct {
		Models []struct {
			ID string `json:"model_id"`
		} `json:"models"`
	}
	if err := json.Unmarshal(raw, &listing); err != nil {
		return nil, err
	}
	var models []string
	for _, model := range listing.Models {
		id := strings.TrimSpace(model.ID)
		if id != "" && len(id) <= 128 && kiroModelPattern.MatchString(id) && len(models) < maxKiroModels {
			models = append(models, id)
		}
	}
	return models, nil
}

// kiroModelPattern is what a model id must look like to be typed into
// `/model`: "claude-sonnet-4.5", "qwen3-coder-next".
var kiroModelPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]+$`)

// offerSwitches lists what the phone may switch a session to. Only a session
// in a pane Agentman owns can be switched: the switch is typed at its prompt.
func (s *KiroSource) offerSwitches(session *protocol.Session) {
	session.Modes, session.Models, session.ModelScope = nil, nil, ""
	if session.Inject != protocol.InjectTmux {
		return
	}
	session.Modes = s.kiroAgents(session.Cwd, session.Mode)
	if models := s.models.list(); len(models) > 0 {
		session.Models = append([]string(nil), models...)
		session.ModelScope = protocol.ModelScopeSession
	}
}

// SetMode implements ModeSetter: it swaps the session's agent.
func (s *KiroSource) SetMode(ctx context.Context, sessionID, mode string) error {
	if !kiroAgentNamePattern.MatchString(mode) {
		return fmt.Errorf("source: %q is not a Kiro agent name", mode)
	}
	return s.switchTo(ctx, sessionID, "/agent swap "+mode, "agent "+mode, func(status kiroStatus) bool {
		return kiroMode(status.agent) == mode
	})
}

// SetModel implements ModelSetter: it switches the session's model, for this
// session alone.
func (s *KiroSource) SetModel(ctx context.Context, sessionID, model string) error {
	if !kiroModelPattern.MatchString(model) {
		return fmt.Errorf("source: %q is not a Kiro model id", model)
	}
	return s.switchTo(ctx, sessionID, "/model "+model, "model "+model, func(status kiroStatus) bool {
		return status.model == model
	})
}

// kiroSwitchSettle bounds how long a switch waits to see its result on the
// status line, per attempt. A variable so tests of a switch that never shows
// need not wait it out.
var kiroSwitchSettle = 4 * time.Second

// kiroSwitchAttempts is how many times a switch is typed before giving up: a
// second time covers keystrokes lost to a redraw, and more would only print
// "Unknown agent" again.
const kiroSwitchAttempts = 2

var errKiroBusy = errors.New("source: Kiro is working; switch when this turn has finished")

// switchTo types a slash command at an idle Kiro prompt and waits until the
// status line shows what it asked for.
//
// Only at the prompt: typed while Kiro works, the command would be handed to
// the agent as a message; typed into a menu or picker, it would choose in it.
func (s *KiroSource) switchTo(
	ctx context.Context, sessionID, command, what string, done func(kiroStatus) bool,
) error {
	session, err := s.session(sessionID)
	if err != nil {
		return err
	}
	if session.tmuxName == "" {
		return errors.New("source: only sessions started with `am kiro` can be switched from the phone")
	}
	pane, err := s.capturePane(ctx, session.tmuxName)
	if err != nil {
		return fmt.Errorf("source: could not inspect the terminal: %w", err)
	}
	if question.DetectKiro(pane) != nil {
		return fmt.Errorf("source: answer the pending question first")
	}
	if question.KiroOverlayOpen(pane) {
		return errKiroOverlay
	}
	if !KiroReadyForInput(pane) {
		return errKiroBusy
	}
	if status, ok := kiroPaneStatus(pane); ok && done(status) {
		return nil
	}
	for attempt := 0; attempt < kiroSwitchAttempts; attempt++ {
		if err := s.sendText(ctx, session.tmuxName, command); err != nil {
			return err
		}
		overlay := false
		matched := s.waitForPaneFor(ctx, session.tmuxName, kiroSwitchSettle, func(pane string) bool {
			if question.KiroOverlayOpen(pane) {
				overlay = true
				return true
			}
			status, ok := kiroPaneStatus(pane)
			return ok && done(status)
		})
		if overlay {
			// A picker opened where a switch was expected. Nothing runs at
			// the prompt, so Escape only closes it.
			_ = s.sendKeys(ctx, session.tmuxName, "Escape")
			return fmt.Errorf("source: Kiro opened a menu instead of switching to %s; nothing was changed", what)
		}
		if matched {
			return nil
		}
	}
	return fmt.Errorf("source: Kiro did not switch to %s", what)
}

// waitForPaneFor is waitForPane with a bound of its own.
func (s *KiroSource) waitForPaneFor(
	ctx context.Context, tmuxName string, bound time.Duration, ready func(pane string) bool,
) bool {
	deadline := time.Now().Add(bound)
	for {
		if pane, err := s.capturePane(ctx, tmuxName); err == nil && ready(pane) {
			return true
		}
		if ctx.Err() != nil || time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(50 * time.Millisecond):
		}
	}
}

var (
	_ ModeSetter  = (*KiroSource)(nil)
	_ ModelSetter = (*KiroSource)(nil)
)
