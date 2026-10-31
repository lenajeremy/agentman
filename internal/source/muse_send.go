package source

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Sending to a Muse session without keeping it. Muse lets one connection
// drive a session at a time, so Agentman takes a session only to run a turn
// (session/resume), and lets it go (session/leave) once Muse is idle with
// nothing waiting on the user. A terminal can then open the session, and a
// session from before Agentman can be continued from the phone.

const (
	// museHoldCheck is how often a held session is checked, besides its
	// events, so a missed one never keeps it held.
	museHoldCheck = 3 * time.Second
	// museLeaveWait bounds the wait for Muse to confirm a release.
	museLeaveWait = 5 * time.Second
)

// errMuseInTerminal is a send refused because a terminal is driving the
// session; the apps show it as it reads.
var errMuseInTerminal = errors.New("Muse has this session open in a terminal. Close it there to send from here.")

type museHolds struct {
	mu   sync.Mutex
	held map[string]context.CancelFunc
}

// Inject implements Injector: take the session, start the turn (queued
// behind one already running), and let go when Muse is done.
func (s *MuseSource) Inject(ctx context.Context, sessionID, text string) (protocol.InjectMode, error) {
	native := strings.TrimPrefix(sessionID, musePrefix)
	if err := s.take(ctx, native); err != nil {
		return "", err
	}
	params := map[string]any{
		"commandId": museCommandID(),
		"sessionId": native,
		"input":     []any{map[string]any{"type": "text", "text": text}},
		"ifBusy":    "queue",
	}
	if err := s.host.call(ctx, "turn/start", params, nil); err != nil {
		if museInUse(err) {
			return "", errMuseInTerminal
		}
		return "", err
	}
	return protocol.InjectAPI, nil
}

// Interrupt implements Interrupter: stop the turn Agentman is running. A
// turn a terminal runs is the terminal's to stop.
func (s *MuseSource) Interrupt(ctx context.Context, sessionID string) error {
	native := strings.TrimPrefix(sessionID, musePrefix)
	err := s.host.call(ctx, "turn/interrupt", map[string]any{"commandId": museCommandID(), "sessionId": native}, nil)
	if museInUse(err) {
		return errMuseInTerminal
	}
	return err
}

// take makes Agentman the session's driver until Muse is done with what it
// was asked, unless it already is.
func (s *MuseSource) take(ctx context.Context, native string) error {
	s.holds.mu.Lock()
	if s.holds.held == nil {
		s.holds.held = map[string]context.CancelFunc{}
	}
	_, holding := s.holds.held[native]
	s.holds.mu.Unlock()
	if holding {
		return nil
	}
	err := s.host.call(ctx, "session/resume", map[string]any{
		"commandId": museCommandID(), "sessionId": native, "excludeItems": true,
	}, nil)
	if museInUse(err) {
		return errMuseInTerminal
	}
	if err != nil {
		return err
	}
	holdCtx, cancel := context.WithCancel(context.Background())
	s.holds.mu.Lock()
	s.holds.held[native] = cancel
	s.holds.mu.Unlock()
	go s.hold(holdCtx, native)
	return nil
}

// hold lets the session go once it has been idle with nothing waiting on the
// user. A turn only just started may still read as idle, so it waits for
// one sign of work first, or a few checks of quiet.
func (s *MuseSource) hold(ctx context.Context, native string) {
	statuses := make(chan museStatus, 16)
	stop := s.host.listen(func(message museMessage) {
		if message.Method != "session/statusChanged" && message.Method != "turn/completed" {
			return
		}
		var params museStatus
		if json.Unmarshal(message.Params, &params) == nil && params.SessionID == native {
			select {
			case statuses <- params:
			default:
			}
		}
	})
	defer stop()
	ticker := time.NewTicker(museHoldCheck)
	defer ticker.Stop()
	worked, quiet := false, 0
	for {
		select {
		case <-ctx.Done():
			return
		case status := <-statuses:
			if status.Status == "running" || len(status.Attention) > 0 {
				worked = true
				continue
			}
			if status.Status == "idle" && worked {
				s.letGo(native)
				return
			}
		case <-ticker.C:
			var read struct {
				Session museSession `json:"session"`
			}
			if err := s.host.call(ctx, "session/read", map[string]any{"sessionId": native, "excludeItems": true}, &read); err != nil {
				continue
			}
			if read.Session.Status == "running" || museAttention(read.Session.Attention) {
				worked, quiet = true, 0
				continue
			}
			quiet++
			if worked || quiet >= 2 {
				s.letGo(native)
				return
			}
		}
	}
}

type museStatus struct {
	SessionID string   `json:"sessionId"`
	Status    string   `json:"status"`
	Attention []string `json:"attention"`
	Terminal  string   `json:"terminal"`
}

// letGo releases the session and waits for Muse to confirm it, so the
// session closes cleanly rather than as an abandoned run.
func (s *MuseSource) letGo(native string) {
	s.holds.mu.Lock()
	cancel := s.holds.held[native]
	delete(s.holds.held, native)
	s.holds.mu.Unlock()
	if cancel != nil {
		defer cancel()
	}
	commandID := museCommandID()
	done := make(chan struct{})
	var once sync.Once
	stop := s.host.listen(func(message museMessage) {
		if message.Method != "session/leaveCompleted" {
			return
		}
		var params struct {
			CommandID string `json:"commandId"`
		}
		if json.Unmarshal(message.Params, &params) == nil && params.CommandID == commandID {
			once.Do(func() { close(done) })
		}
	})
	defer stop()
	ctx, cancelCall := context.WithTimeout(context.Background(), museLeaveWait)
	defer cancelCall()
	if s.host.call(ctx, "session/leave", map[string]any{"commandId": commandID, "sessionId": native}, nil) != nil {
		return
	}
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// holding reports whether Agentman drives the session now.
func (s *MuseSource) holding(native string) bool {
	s.holds.mu.Lock()
	defer s.holds.mu.Unlock()
	_, ok := s.holds.held[native]
	return ok
}
