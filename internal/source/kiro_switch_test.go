package source

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/tmux"
)

// kiroPrompt is an idle Kiro that answers /agent swap and /model the way
// Kiro CLI 2.27.1 does: a known name changes the status line, an unknown one
// prints "Unknown …" and changes nothing. It records every command typed.
type kiroPrompt struct {
	mu        sync.Mutex
	agent     string // as the status line shows it: "Plan" for kiro_planner
	model     string
	busy      bool
	picker    bool // a picker is open over the prompt
	loseFirst bool // the first command typed is lost
	opens     bool // a command opens a picker instead of switching
	typed     []string
	keys      []string
}

var kiroPromptKnows = map[string]bool{
	"kiro_default": true, "kiro_planner": true, "kiro_guide": true, "agentman": true,
	"auto": true, "claude-haiku-4.5": true, "claude-sonnet-4.5": true,
}

func (k *kiroPrompt) capture(context.Context, string) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.picker {
		return kiroModelPickerPane, nil
	}
	input := "›  ask a question or describe a task ↵"
	if k.busy {
		input = "›  Kiro is working · 3s · Type to steer · Ctrl+S to queue"
	}
	return fmt.Sprintf("────────\n%s · %s · ◔ 4%%             /work\n\n%s\n", k.agent, k.model, input), nil
}

func (k *kiroPrompt) send(_ context.Context, _ string, text string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.typed = append(k.typed, text)
	if k.loseFirst {
		k.loseFirst = false
		return nil
	}
	if k.opens {
		k.picker = true
		return nil
	}
	switch {
	case strings.HasPrefix(text, "/agent swap "):
		if name := strings.TrimPrefix(text, "/agent swap "); kiroPromptKnows[name] {
			k.agent = name
			if name == "kiro_planner" {
				k.agent = "Plan"
			}
		}
	case strings.HasPrefix(text, "/model "):
		if id := strings.TrimPrefix(text, "/model "); kiroPromptKnows[id] {
			k.model = id
		}
	}
	return nil
}

func (k *kiroPrompt) press(_ context.Context, _ string, keys ...string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.keys = append(k.keys, keys...)
	for _, key := range keys {
		if key == "Escape" {
			k.picker = false
		}
	}
	return nil
}

func kiroWithPrompt(t *testing.T, prompt *kiroPrompt) (*KiroSource, string, string) {
	t.Helper()
	home := t.TempDir()
	pid := os.Getpid()
	work := filepath.Join(home, "work")
	writeKiroSession(t, home, "s", work, pid, kiroLinePrompt, kiroLineReply)
	pane := tmux.Session{Name: "agentman-kiro-1-a", PanePID: pid, Cwd: work}
	s := newTestKiro(t, home, fmt.Sprintf("%d 1 kiro-cli-chat\n", pid), pane)
	s.capturePane, s.sendText, s.sendKeys = prompt.capture, prompt.send, prompt.press
	s.models = &kiroModelCatalog{models: []string{"auto", "claude-haiku-4.5", "claude-sonnet-4.5"}, fetched: time.Now(),
		fetch: func(context.Context) ([]string, error) { return nil, errors.New("not asked in this test") }}
	discoverKiro(t, s)
	return s, "kiro:tmux-agentman-kiro-1-a", work
}

// The phone is offered Kiro's agents as modes — the built-in three, then the
// user's own and the folder's — and Kiro's models, for this session alone.
func TestKiroOffersItsAgentsAndModels(t *testing.T) {
	prompt := &kiroPrompt{agent: "kiro_default", model: "auto"}
	s, id, work := kiroWithPrompt(t, prompt)
	for dir, name := range map[string]string{
		filepath.Join(s.home, ".kiro", "agents"): "agentman",
		filepath.Join(work, ".kiro", "agents"):   "reviewer",
	} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		body := fmt.Sprintf(`{"name":%q,"description":"x"}`, name)
		if err := os.WriteFile(filepath.Join(dir, name+".json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	session := discoverKiro(t, s)[id]
	if got := strings.Join(session.Modes, " "); got != "kiro_default kiro_planner kiro_guide agentman reviewer" {
		t.Errorf("modes = %q", got)
	}
	if session.Mode != "kiro_default" {
		t.Errorf("mode = %q", session.Mode)
	}
	if got := strings.Join(session.Models, " "); got != "auto claude-haiku-4.5 claude-sonnet-4.5" ||
		session.ModelScope != protocol.ModelScopeSession {
		t.Errorf("models = %q scope %q", got, session.ModelScope)
	}
	// Outside a pane Agentman owns there is no prompt to type a switch at.
	// Its lock names a live process that is not in the pane.
	writeKiroSession(t, s.home, "outside", "/other", os.Getppid(), kiroLinePrompt, kiroLineReply)
	outside, ok := discoverKiro(t, s)["kiro:outside"]
	if !ok || outside.Inject != protocol.InjectNone {
		t.Fatalf("outside session = %+v (found %v)", outside, ok)
	}
	if outside.Modes != nil || outside.Models != nil || outside.ModelScope != "" {
		t.Errorf("a session outside tmux offers switches: %+v", outside)
	}
}

// A switch is typed at the prompt and is done only when the status line says
// so; the next discovery reports it.
func TestKiroSwitchesAgentAndModel(t *testing.T) {
	prompt := &kiroPrompt{agent: "kiro_default", model: "auto"}
	s, id, _ := kiroWithPrompt(t, prompt)
	ctx := context.Background()
	if err := s.SetMode(ctx, id, "kiro_planner"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetModel(ctx, id, "claude-haiku-4.5"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(prompt.typed, " | "); got != "/agent swap kiro_planner | /model claude-haiku-4.5" {
		t.Errorf("typed %q", got)
	}
	session := discoverKiro(t, s)[id]
	if session.Mode != "kiro_planner" || session.Model != "claude-haiku-4.5" {
		t.Errorf("after switching: mode %q model %q", session.Mode, session.Model)
	}
	// Already there: nothing is typed.
	prompt.typed = nil
	if err := s.SetMode(ctx, id, "kiro_planner"); err != nil || len(prompt.typed) != 0 {
		t.Errorf("switching to the current agent typed %q (%v)", prompt.typed, err)
	}
}

// A switch whose result never shows is an error, after a second try for
// keystrokes a redraw swallowed; and a lost first try is recovered.
func TestKiroSwitchReadsBackOrFails(t *testing.T) {
	settle := kiroSwitchSettle
	kiroSwitchSettle = 300 * time.Millisecond
	t.Cleanup(func() { kiroSwitchSettle = settle })
	prompt := &kiroPrompt{agent: "kiro_default", model: "auto", loseFirst: true}
	s, id, _ := kiroWithPrompt(t, prompt)
	ctx := context.Background()
	if err := s.SetModel(ctx, id, "claude-sonnet-4.5"); err != nil || prompt.model != "claude-sonnet-4.5" || len(prompt.typed) != 2 {
		t.Fatalf("lost first try: err %v, model %q, typed %q", err, prompt.model, prompt.typed)
	}
	prompt.typed = nil
	if err := s.SetMode(ctx, id, "kiro_unknown"); err == nil || len(prompt.typed) != kiroSwitchAttempts {
		t.Errorf("an agent Kiro does not know: err %v, typed %q", err, prompt.typed)
	}
}

// Never typed anywhere but the idle prompt: mid-turn it would reach the agent
// as a message, and into a picker it would choose there.
func TestKiroSwitchOnlyAtThePrompt(t *testing.T) {
	ctx := context.Background()
	busy := &kiroPrompt{agent: "kiro_default", model: "auto"}
	s, id, _ := kiroWithPrompt(t, busy)
	busy.busy = true
	if err := s.SetModel(ctx, id, "claude-haiku-4.5"); !errors.Is(err, errKiroBusy) || len(busy.typed) != 0 {
		t.Errorf("busy: err %v, typed %q", err, busy.typed)
	}
	picker := &kiroPrompt{agent: "kiro_default", model: "auto"}
	s, id, _ = kiroWithPrompt(t, picker)
	picker.picker = true
	if err := s.SetMode(ctx, id, "kiro_guide"); !errors.Is(err, errKiroOverlay) || len(picker.typed) != 0 {
		t.Errorf("picker open: err %v, typed %q", err, picker.typed)
	}
	// A command that opens a picker instead of switching: closed again, and
	// reported.
	opens := &kiroPrompt{agent: "kiro_default", model: "auto"}
	s, id, _ = kiroWithPrompt(t, opens)
	opens.opens = true
	if err := s.SetModel(ctx, id, "claude-haiku-4.5"); err == nil || len(opens.keys) != 1 || opens.keys[0] != "Escape" || opens.picker {
		t.Errorf("picker instead of a switch: err %v, keys %q, still open %v", err, opens.keys, opens.picker)
	}
}

// What `kiro-cli chat --list-models -f json` printed (2.27.1), shortened.
const kiroModelListing = `{"models":[{"model_name":"auto","description":"Models chosen by task","model_id":"auto",` +
	`"context_window_tokens":1000000,"rate_multiplier":1.0,"rate_unit":"Credit"},` +
	`{"model_name":"claude-sonnet-4.5","description":"Claude Sonnet 4.5 model","model_id":"claude-sonnet-4.5",` +
	`"context_window_tokens":200000,"rate_multiplier":1.3,"rate_unit":"Credit"},` +
	`{"model_name":"bad","model_id":"two words"},` +
	`{"model_name":"qwen3-coder-next","model_id":"qwen3-coder-next"}],"default_model":"auto"}`

func TestKiroModelListIsReadAndKept(t *testing.T) {
	models, err := parseKiroModels([]byte(kiroModelListing))
	if err != nil || strings.Join(models, " ") != "auto claude-sonnet-4.5 qwen3-coder-next" {
		t.Fatalf("models = %q (%v)", models, err)
	}
	var calls int
	var mu sync.Mutex
	catalog := &kiroModelCatalog{fetch: func(context.Context) ([]string, error) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		return parseKiroModels([]byte(kiroModelListing))
	}}
	// The first sweep does not wait for Kiro.
	if got := catalog.list(); got != nil {
		t.Errorf("first list = %q", got)
	}
	deadline := time.Now().Add(2 * time.Second)
	for catalog.list() == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	for range 50 {
		catalog.list()
	}
	mu.Lock()
	defer mu.Unlock()
	if got := catalog.list(); len(got) != 3 || calls != 1 {
		t.Errorf("after many sweeps: %q from %d listings, want one listing", got, calls)
	}
}
