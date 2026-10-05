package daemon

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/source"
)

// switchingSource offers modes and models the way an adapter would, and
// records what it was asked to switch to.
type switchingSource struct {
	streamingSource
	session protocol.Session

	mu       sync.Mutex
	switched []string
}

func (s *switchingSource) Discover(context.Context) ([]protocol.Session, error) {
	return []protocol.Session{s.session}, nil
}

func (s *switchingSource) SetMode(_ context.Context, _, mode string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.switched = append(s.switched, "mode:"+mode)
	return nil
}

func (s *switchingSource) SetModel(_ context.Context, _, model string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.switched = append(s.switched, "model:"+model)
	return nil
}

func (s *switchingSource) calls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.switched...)
}

func switchingDaemon(t *testing.T, scope string) (*Daemon, *switchingSource) {
	t.Helper()
	src := &switchingSource{session: protocol.Session{
		ID: "claude:s1", Kind: protocol.KindClaude, State: protocol.StateIdle,
		Mode: "default", Modes: []string{"default", "accept-edits", "plan"},
		Model: "opus", Models: []string{"opus", "sonnet"}, ModelScope: scope,
	}}
	registry := source.NewRegistry()
	registry.Add(src)
	d := New(registry, &recordingSink{})
	d.refresh(context.Background(), true)
	return d, src
}

func switchTo(d *Daemon, kind protocol.RequestType, value string) protocol.Event {
	return d.Handle(context.Background(), protocol.Request{
		Type: kind, SessionID: "claude:s1", ClientID: "switch-1", Text: value,
	})
}

func TestAModeOrModelTheSessionOffersIsSwitchedTo(t *testing.T) {
	d, src := switchingDaemon(t, protocol.ModelScopeSession)
	for _, event := range []protocol.Event{
		switchTo(d, protocol.ReqSetMode, "plan"),
		switchTo(d, protocol.ReqSetModel, "sonnet"),
	} {
		if event.Type != protocol.EvtSendResult || event.Status != protocol.StatusDelivered ||
			event.ClientID != "switch-1" || event.SessionID != "claude:s1" {
			t.Fatalf("switch answered %+v", event)
		}
	}
	if got := strings.Join(src.calls(), ","); got != "mode:plan,model:sonnet" {
		t.Fatalf("the adapter was asked for %q", got)
	}
}

// The adapter listed exactly what it can switch to and verify. Anything else
// never reaches it, however the request was made.
func TestOnlyAnAdvertisedValueReachesTheAdapter(t *testing.T) {
	d, src := switchingDaemon(t, protocol.ModelScopeDefault)
	for name, event := range map[string]protocol.Event{
		"unlisted mode":   switchTo(d, protocol.ReqSetMode, "yolo"),
		"model as mode":   switchTo(d, protocol.ReqSetMode, "opus"),
		"unlisted model":  switchTo(d, protocol.ReqSetModel, "gpt-5"),
		"mode as model":   switchTo(d, protocol.ReqSetModel, "plan"),
		"case changed":    switchTo(d, protocol.ReqSetMode, "Plan"),
		"padded":          switchTo(d, protocol.ReqSetMode, " plan"),
		"empty":           switchTo(d, protocol.ReqSetMode, ""),
		"escape":          switchTo(d, protocol.ReqSetMode, "plan\x1b[2J"),
		"newline":         switchTo(d, protocol.ReqSetModel, "opus\n"),
		"overlong model":  switchTo(d, protocol.ReqSetModel, strings.Repeat("m", maxWireModelBytes+1)),
		"overlong mode":   switchTo(d, protocol.ReqSetMode, strings.Repeat("m", maxWireModeBytes+1)),
		"unknown session": d.Handle(context.Background(), protocol.Request{Type: protocol.ReqSetMode, SessionID: "claude:gone", ClientID: "switch-1", Text: "plan"}),
	} {
		if event.Type != protocol.EvtSendResult || event.Status != protocol.StatusFailed || event.ClientID != "switch-1" {
			t.Errorf("%s answered %+v", name, event)
		}
	}
	if got := src.calls(); len(got) != 0 {
		t.Fatalf("the adapter was asked for %q", got)
	}
}

// A session that lists models but no scope offers no model switching.
func TestModelsWithoutAScopeAreNotSwitched(t *testing.T) {
	d, src := switchingDaemon(t, "")
	if event := switchTo(d, protocol.ReqSetModel, "sonnet"); event.Status != protocol.StatusFailed {
		t.Fatalf("switch answered %+v", event)
	}
	if got := src.calls(); len(got) != 0 {
		t.Fatalf("the adapter was asked for %q", got)
	}
}

// A switch drives the terminal like a send: a key pressed while a question is
// up lands in the menu, and two phones must not interleave their keys.
func TestASwitchIsHeldToTheRulesOfASend(t *testing.T) {
	for _, kind := range []protocol.RequestType{protocol.ReqSetMode, protocol.ReqSetModel} {
		if !requestMutatesSession(kind) {
			t.Errorf("%s does not take the session's action lock", kind)
		}
	}
	d, src := switchingDaemon(t, protocol.ModelScopeSession)
	d.mu.Lock()
	session := d.sessions["claude:s1"]
	session.Question = &protocol.Question{ID: "q", Prompt: "Run it?", Options: []protocol.QuestionOption{{Key: "1", Label: "Yes"}}}
	d.sessions["claude:s1"] = session
	d.mu.Unlock()
	for _, event := range []protocol.Event{
		switchTo(d, protocol.ReqSetMode, "plan"),
		switchTo(d, protocol.ReqSetModel, "sonnet"),
	} {
		if event.Status != protocol.StatusFailed || !strings.Contains(event.Error, "pending question") {
			t.Errorf("a switch during a question answered %+v", event)
		}
	}
	if got := src.calls(); len(got) != 0 {
		t.Fatalf("the adapter was asked for %q", got)
	}
}

// What a session offers is held to what the phone can send back: an entry
// that fails is dropped rather than repaired, because a repaired name is one
// the adapter never offered.
func TestOfferedSwitchesAreBoundedForThePhone(t *testing.T) {
	modes := []string{"default", "", " plan", "plan", "plan", "a\x1b", "two\nlines", strings.Repeat("m", maxWireModeBytes+1)}
	for i := range 30 {
		modes = append(modes, "mode-"+strings.Repeat("x", i))
	}
	models := []string{"opus", strings.Repeat("m", maxWireModelBytes+1)}
	for i := range 100 {
		models = append(models, "model-"+strings.Repeat("y", i%60)+string(rune('a'+i%26)))
	}
	original := append([]string(nil), modes...)
	found := normalizeDiscoveredSessions([]protocol.Session{
		{ID: "claude:s1", Modes: modes, Models: models, ModelScope: "global"},
		{ID: "claude:s2", Modes: []string{""}, ModelScope: protocol.ModelScopeDefault},
	})
	got := found[0]
	if len(got.Modes) != maxWireModes || got.Modes[0] != "default" || got.Modes[1] != "plan" ||
		got.Modes[2] != "mode-" {
		t.Fatalf("modes = %q", got.Modes)
	}
	if len(got.Models) != maxWireModels || got.Models[0] != "opus" {
		t.Fatalf("models = %d, first %q", len(got.Models), got.Models[0])
	}
	if got.ModelScope != "" {
		t.Errorf("an unknown scope survived as %q", got.ModelScope)
	}
	if found[1].Modes != nil || found[1].ModelScope != protocol.ModelScopeDefault {
		t.Errorf("second session = modes %q scope %q", found[1].Modes, found[1].ModelScope)
	}
	for i := range original {
		if modes[i] != original[i] {
			t.Fatal("the adapter's own list was rewritten")
		}
	}
}
