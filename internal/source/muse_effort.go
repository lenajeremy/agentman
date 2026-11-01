package source

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Reasoning effort, switched from the apps as a Muse session's mode: Muse's
// terminal shows it beside the model ("muse-spark-1.3 · high"), and with one
// model on an account it is the choice that matters. The tiers are the
// session model's own (Muse's catalog), offered as "high effort" and the
// like, and a pick is session/setReasoningEffort.
//
// Muse records a session's effort only once it is set, so Agentman knows it
// from a set it made or a change it saw (session/reasoningEffortChanged);
// until then the apps show the choices with none marked.

const museEffortSuffix = " effort"

// museEffortWait bounds the wait for Muse to confirm an effort change.
const museEffortWait = 3 * time.Second

type museEfforts struct {
	mu    sync.Mutex
	known map[string]string // session id → effort tier
}

// trackEffort keeps the efforts Muse reports.
func (s *MuseSource) trackEffort(message museMessage) {
	if message.Method != "session/reasoningEffortChanged" {
		return
	}
	var params struct {
		SessionID       string `json:"sessionId"`
		ReasoningEffort string `json:"reasoningEffort"`
	}
	if json.Unmarshal(message.Params, &params) != nil || params.SessionID == "" || params.ReasoningEffort == "" {
		return
	}
	s.efforts.mu.Lock()
	defer s.efforts.mu.Unlock()
	if s.efforts.known == nil {
		s.efforts.known = map[string]string{}
	}
	s.efforts.known[params.SessionID] = params.ReasoningEffort
}

func (s *MuseSource) knownEffort(native string) string {
	s.efforts.mu.Lock()
	defer s.efforts.mu.Unlock()
	return s.efforts.known[native]
}

// sessionEfforts is the effort tiers of the session's model.
func (s *MuseSource) sessionEfforts(model string) []string {
	for _, entry := range s.museModels() {
		if entry.ModelID == model {
			return entry.efforts()
		}
	}
	return nil
}

// offerEfforts lists the effort tiers on a session Agentman can drive, as
// its modes.
func (s *MuseSource) offerEfforts(session *protocol.Session) {
	if session.Inject != protocol.InjectAPI {
		return
	}
	tiers := s.sessionEfforts(session.Model)
	if len(tiers) < 2 {
		return
	}
	session.Modes = make([]string, 0, len(tiers))
	for _, tier := range tiers {
		session.Modes = append(session.Modes, tier+museEffortSuffix)
	}
	if known := s.knownEffort(session.NativeID); known != "" && slices.Contains(tiers, known) {
		session.Mode = known + museEffortSuffix
	}
}

// SetMode implements ModeSetter: set the session's reasoning effort, and
// wait for Muse to report the change.
func (s *MuseSource) SetMode(ctx context.Context, sessionID, mode string) error {
	native := strings.TrimPrefix(sessionID, musePrefix)
	tier := strings.TrimSuffix(mode, museEffortSuffix)
	if s.openInTerminal()[native] && !s.holding(native) {
		return errMuseInTerminal
	}
	var read museRead
	if err := s.host.call(ctx, "session/read", map[string]any{"sessionId": native, "excludeItems": true}, &read); err != nil {
		return err
	}
	if tiers := s.sessionEfforts(read.Session.ModelID); !slices.Contains(tiers, tier) {
		return fmt.Errorf("source: %s doesn't take %s effort", read.Session.ModelID, tier)
	}
	changed := make(chan struct{}, 1)
	stop := s.host.listen(func(message museMessage) {
		if message.Method != "session/reasoningEffortChanged" {
			return
		}
		var params struct {
			SessionID       string `json:"sessionId"`
			ReasoningEffort string `json:"reasoningEffort"`
		}
		if json.Unmarshal(message.Params, &params) == nil && params.SessionID == native && params.ReasoningEffort == tier {
			select {
			case changed <- struct{}{}:
			default:
			}
		}
	})
	defer stop()
	if err := s.take(ctx, native); err != nil {
		return err
	}
	if err := s.host.call(ctx, "session/setReasoningEffort", map[string]any{
		"commandId": museCommandID(), "sessionId": native, "reasoningEffort": tier,
	}, nil); err != nil {
		if museInUse(err) {
			return errMuseInTerminal
		}
		return err
	}
	select {
	case <-changed:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(museEffortWait):
		return fmt.Errorf("source: Muse did not confirm %s effort", tier)
	}
}
