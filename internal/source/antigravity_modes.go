package source

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/question"
)

// agy's modes, in the order shift+tab steps through them (agy 1.2.17):
// default, then "Accept-edits mode: file edits auto-approved", then "Plan
// mode: research & plan only", then back to default. The footer names the two
// it marks ("accept-edits · Gemini 3.8 Flash · high") and shows nothing for
// the default, which the phone calls "default".
var antigravityModes = []string{"default", "accept-edits", "plan"}

// antigravityModeName is the mode a footer shows.
func antigravityModeName(footer antigravityFooter) string {
	if footer.mode == "" {
		return "default"
	}
	return footer.mode
}

var (
	_ ModeSetter  = (*AntigravitySource)(nil)
	_ ModelSetter = (*AntigravitySource)(nil)
)

// offerSwitches sets the mode and the switches a pane-backed session offers:
// the modes shift+tab cycles through and the models agy lists, but only once
// its footer can be read, since every switch is confirmed from it. Choosing a
// model with /model also makes it agy's default for new sessions, which the
// phone says before sending.
func (s *AntigravitySource) offerSwitches(session *protocol.Session, footer antigravityFooter) {
	// Only a pane has a footer; a session without one offers nothing.
	if footer.model == "" {
		session.Mode = footer.mode
		return
	}
	session.Mode = antigravityModeName(footer)
	session.Modes = slices.Clone(antigravityModes)
	if models := s.models.offered(); len(models) > 0 {
		session.Models = models
		session.ModelScope = protocol.ModelScopeDefault
	}
}

// antigravityKeyWait is how long a key is given to show on screen before the
// footer is read again.
const antigravityKeyWait = time.Second

// SetMode implements ModeSetter: shift+tab, one press at a time, each only
// after the footer has been read back, until the footer names the mode asked
// for. A full cycle without it is reported rather than pressed through again.
func (s *AntigravitySource) SetMode(ctx context.Context, sessionID, mode string) error {
	if !slices.Contains(antigravityModes, mode) {
		return fmt.Errorf("source: Antigravity has no %q mode", mode)
	}
	name, footer, err := s.switchablePane(ctx, sessionID)
	if err != nil {
		return err
	}
	current := antigravityModeName(footer)
	for presses := 0; current != mode; presses++ {
		if presses == len(antigravityModes)-1 {
			return fmt.Errorf("source: Antigravity stayed in %s mode; switch it on the Mac", current)
		}
		if err := s.keys.send(ctx, name, "BTab"); err != nil {
			return err
		}
		previous := current
		if err := s.waitForFooter(ctx, name, func(now antigravityFooter) bool {
			current = antigravityModeName(now)
			return now.model != "" && current != previous
		}); err != nil {
			return errors.New("source: Antigravity's mode did not change on the Mac; check it there")
		}
	}
	return nil
}

// SetModel implements ModelSetter by typing agy's own "/model <id>", which
// switches the session and also saves the model as agy's default for new
// sessions — the scope the phone confirms before sending. Done only at an
// idle prompt, and confirmed from the footer.
func (s *AntigravitySource) SetModel(ctx context.Context, sessionID, model string) error {
	id, ok := s.models.lookup(model)
	if !ok {
		return fmt.Errorf("source: Antigravity does not offer %q", model)
	}
	name, footer, err := s.switchablePane(ctx, sessionID)
	if err != nil {
		return err
	}
	if footerModelLabel(footer) == model {
		return nil
	}
	pane, err := s.capturePane(ctx, name)
	if err != nil {
		return err
	}
	if state, ok := antigravityPaneState(strings.Split(strings.TrimRight(pane, "\n"), "\n")); !ok || state != protocol.StateIdle {
		// Typed mid-turn, the command would wait in agy's queue of messages.
		return errors.New("source: switch the model once Antigravity has finished this turn")
	}
	if err := s.keys.typeText(ctx, name, "/model "+id); err != nil {
		return err
	}
	err = s.waitForFooter(ctx, name, func(now antigravityFooter) bool { return footerModelLabel(now) == model })
	if closeErr := s.closeAnyPanel(ctx, name); closeErr != nil {
		return fmt.Errorf("source: a panel is open in Antigravity on the Mac: %v", closeErr)
	}
	if err != nil {
		return errors.New("source: Antigravity did not switch to that model; check it on the Mac")
	}
	return nil
}

// switchablePane is the pane of a session whose mode or model can be
// changed now: one Agentman runs, at its prompt, with no question or panel
// open and a footer that can be read.
func (s *AntigravitySource) switchablePane(ctx context.Context, sessionID string) (string, antigravityFooter, error) {
	session, err := s.session(sessionID)
	if err != nil {
		return "", antigravityFooter{}, err
	}
	if session.tmuxName == "" {
		return "", antigravityFooter{}, errors.New("source: only sessions started with `am antigravity` can be switched from the phone")
	}
	if s.keys.send == nil {
		return "", antigravityFooter{}, errors.New("source: switch it on the Mac for now")
	}
	pane, err := s.capturePane(ctx, session.tmuxName)
	if err != nil {
		return "", antigravityFooter{}, err
	}
	if question.DetectAntigravity(pane) != nil {
		return "", antigravityFooter{}, errors.New("source: answer the pending question first")
	}
	if question.AntigravityPanelOpen(pane) {
		return "", antigravityFooter{}, errors.New("source: a panel is open in Antigravity on the Mac; close it there first")
	}
	footer := parseAntigravityFooter(strings.Split(strings.TrimRight(pane, "\n"), "\n"))
	if footer.model == "" {
		return "", antigravityFooter{}, errors.New("source: cannot read Antigravity's mode and model on the Mac")
	}
	return session.tmuxName, footer, nil
}

// waitForFooter reads the pane until done accepts its footer.
func (s *AntigravitySource) waitForFooter(ctx context.Context, name string, done func(antigravityFooter) bool) error {
	deadline := time.Now().Add(antigravityKeyWait)
	for {
		pane, err := s.capturePane(ctx, name)
		if err != nil {
			return err
		}
		if done(parseAntigravityFooter(strings.Split(strings.TrimRight(pane, "\n"), "\n"))) {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("source: the footer did not change")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(antigravityKeyPause):
		}
	}
}

// closeAnyPanel closes a panel or picker left over the prompt — /model opens
// its picker when given nothing it recognises — pressing Escape only while
// one is visibly open.
func (s *AntigravitySource) closeAnyPanel(ctx context.Context, name string) error {
	for attempt := 0; attempt < 3; attempt++ {
		pane, err := s.capturePane(ctx, name)
		if err != nil {
			return err
		}
		if !question.AntigravityPanelOpen(pane) {
			return nil
		}
		if attempt == 2 {
			break
		}
		if err := s.keys.send(ctx, name, "Escape"); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(antigravityKeyPause):
		}
	}
	return errors.New("it did not close")
}

// footerModelLabel is a footer's model as the session reports it:
// "Gemini 3.8 Flash (high)".
func footerModelLabel(footer antigravityFooter) string {
	if footer.model == "" {
		return ""
	}
	return footer.model + " (" + footer.effort + ")"
}

/* ------------------------------ the catalog ------------------------------ */

// `agy models` prints the models the signed-in account may use, one per line:
//
//	Fetching available models...          (on stderr)
//	gemini-3.8-flash-high	Gemini 3.8 Flash (High)
//	gemini-3.8-flash-low	Gemini 3.8 Flash (Low)
//	claude-opus-4-6-thinking	Claude Opus 4.6 (Thinking)
//
// It asks agy's service, so it is run in the background, at most every few
// minutes, and only while an Antigravity session Agentman runs is open.

const (
	antigravityModelsTTL     = 10 * time.Minute
	antigravityModelsRetry   = time.Minute
	antigravityModelsTimeout = 20 * time.Second
	antigravityModelsMax     = 64
)

var antigravityModelLine = regexp.MustCompile(`^([a-z0-9][a-z0-9._-]*)\t(\S.*\S)$`)

// antigravityModelCatalog is the last `agy models` listing.
type antigravityModelCatalog struct {
	mu      sync.Mutex
	at      time.Time
	running bool
	labels  []string
	ids     map[string]string
	// list runs `agy models`; injectable for tests.
	list func(ctx context.Context) ([]byte, error)
}

// offered returns the labels to advertise, starting a refresh when the
// listing is stale. It never waits for one.
func (c *antigravityModelCatalog) offered() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	ttl := antigravityModelsTTL
	if len(c.labels) == 0 {
		ttl = antigravityModelsRetry
	}
	if !c.running && time.Since(c.at) > ttl {
		c.running = true
		go c.refresh()
	}
	return slices.Clone(c.labels)
}

func (c *antigravityModelCatalog) refresh() {
	ctx, cancel := context.WithTimeout(context.Background(), antigravityModelsTimeout)
	defer cancel()
	list := c.list
	if list == nil {
		list = runAgyModels
	}
	raw, err := list(ctx)
	labels, ids := parseAntigravityModels(raw)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.running = false
	c.at = time.Now()
	if err != nil || len(labels) == 0 {
		return // keep the last good listing
	}
	c.labels, c.ids = labels, ids
}

func (c *antigravityModelCatalog) lookup(label string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	id, ok := c.ids[label]
	return id, ok
}

func runAgyModels(ctx context.Context) ([]byte, error) {
	bin, err := exec.LookPath("agy")
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, bin, "models")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	// stdin is left empty: agy waits on an inherited, unclosed pipe.
	if err := cmd.Run(); err != nil && stdout.Len() == 0 {
		return nil, err
	}
	return stdout.Bytes(), nil
}

// parseAntigravityModels reads the listing into labels in the form a session
// reports its model ("Gemini 3.8 Flash (high)", effort lower case as the
// footer prints it), and the id `/model` takes for each.
func parseAntigravityModels(raw []byte) ([]string, map[string]string) {
	var labels []string
	ids := map[string]string{}
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	for scanner.Scan() && len(labels) < antigravityModelsMax {
		match := antigravityModelLine.FindStringSubmatch(strings.TrimRight(scanner.Text(), " \r"))
		if match == nil {
			continue
		}
		label := antigravityModelLabelOf(match[2])
		if label == "" || len(label) > 128 || ids[label] != "" {
			continue
		}
		labels = append(labels, label)
		ids[label] = match[1]
	}
	return labels, ids
}

var antigravityLabelEffort = regexp.MustCompile(`^(.*\S)\s+\((Low|Medium|High|Max)\)$`)

// antigravityModelLabelOf writes "Gemini 3.8 Flash (High)" the way the footer
// reads it back, "Gemini 3.8 Flash (high)". A label whose bracket is not an
// effort ("Claude Opus 4.6 (Thinking)") is kept as it is.
func antigravityModelLabelOf(label string) string {
	if match := antigravityLabelEffort.FindStringSubmatch(label); match != nil {
		return match[1] + " (" + strings.ToLower(match[2]) + ")"
	}
	return strings.TrimSpace(label)
}
