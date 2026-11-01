package source

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
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
	s.startHold(native)
	return nil
}

// startHold marks the session held and watches it until Muse is done. The
// watch starts listening before this returns, so a turn that ends at once
// is still seen ending.
func (s *MuseSource) startHold(native string) {
	holdCtx, cancel := context.WithCancel(context.Background())
	s.holds.mu.Lock()
	if s.holds.held == nil {
		s.holds.held = map[string]context.CancelFunc{}
	}
	s.holds.held[native] = cancel
	s.holds.mu.Unlock()
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
	go func() {
		defer stop()
		s.hold(holdCtx, native, statuses)
	}()
}

// hold lets the session go once it has been idle with nothing waiting on the
// user. A turn only just started may still read as idle, so it waits for
// one sign of work first, or a few checks of quiet.
func (s *MuseSource) hold(ctx context.Context, native string, statuses <-chan museStatus) {
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
			// Muse refuses to let go while it is still busy; the hold
			// carries on and tries again.
			if status.Status == "idle" && worked && s.letGo(native, false) {
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
			if (worked || quiet >= 2) && s.letGo(native, false) {
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
// session closes cleanly rather than as an abandoned run. It reports whether
// Agentman is done holding it: released, or no longer its to hold (Muse
// restarted, the session is gone). Muse refuses to let go mid-turn; then the
// hold stays, unless stop ends the turn first.
func (s *MuseSource) letGo(native string, stop bool) bool {
	commandID := museCommandID()
	outcome := make(chan string, 1)
	stopListening := s.host.listen(func(message museMessage) {
		if message.Method != "session/leaveCompleted" {
			return
		}
		var params struct {
			CommandID string `json:"commandId"`
			Outcome   string `json:"outcome"`
		}
		if json.Unmarshal(message.Params, &params) == nil && params.CommandID == commandID {
			select {
			case outcome <- params.Outcome:
			default:
			}
		}
	})
	defer stopListening()
	ctx, cancel := context.WithTimeout(context.Background(), museLeaveWait)
	defer cancel()
	params := map[string]any{"commandId": commandID, "sessionId": native}
	if stop {
		params["stop"] = true
	}
	done := false
	if err := s.host.call(ctx, "session/leave", params, nil); err != nil {
		var rpc *museRPCError
		// Busy keeps the hold; any other refusal means there is nothing
		// left to hold. A dropped connection releases the grant with it.
		done = !museBusy(err) && (errors.As(err, &rpc) || !s.host.isUp())
	} else {
		select {
		case result := <-outcome:
			// "released", or an outcome newer than this code: the set is open,
			// and only "failed" keeps the session where it was.
			done = result != "failed"
		case <-ctx.Done():
		}
	}
	if done {
		s.holds.mu.Lock()
		release := s.holds.held[native]
		delete(s.holds.held, native)
		s.holds.mu.Unlock()
		if release != nil {
			release()
		}
	}
	return done
}

// holding reports whether Agentman drives the session now.
func (s *MuseSource) holding(native string) bool {
	s.holds.mu.Lock()
	defer s.holds.mu.Unlock()
	_, ok := s.holds.held[native]
	return ok
}

// Launch starts a Muse session in dir with its first message, through Muse
// itself: no terminal. Agentman drives it for that first turn and lets it go
// after, as it does a send; its id is the one discovery gives it.
func (s *MuseSource) Launch(ctx context.Context, dir, prompt string) (string, error) {
	// Muse wants the canonical path ("/private/tmp", not "/tmp").
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	var started struct {
		Session museSession `json:"session"`
	}
	if err := s.host.call(ctx, "session/start", map[string]any{
		"commandId": museCommandID(), "workspaceRoots": []string{dir},
	}, &started); err != nil {
		return "", err
	}
	native := started.Session.SessionID
	if native == "" {
		return "", errors.New("source: Muse started no session")
	}
	// Starting it made this connection its driver.
	s.startHold(native)
	if _, err := s.Inject(ctx, musePrefix+native, prompt); err != nil {
		s.letGo(native, false)
		return "", err
	}
	return musePrefix + native, nil
}

// ErrMuseWorking is a release refused because Agentman is running a turn in
// the session, perhaps waiting on an approval: Muse will not hand over a
// session mid-turn. Releasing with stop ends that turn first.
var ErrMuseWorking = errors.New("Agentman is running a turn in this Muse session")

// Release lets go of a session Agentman is driving, so a terminal can open
// it (`am muse resume`). Mid-turn Muse refuses, and the turn is ended first
// only when stop says so; otherwise the refusal is ErrMuseWorking.
func (s *MuseSource) Release(ctx context.Context, sessionID string, stop bool) error {
	native := strings.TrimPrefix(sessionID, musePrefix)
	if !s.holding(native) || s.letGo(native, stop) {
		return nil
	}
	if !stop {
		return ErrMuseWorking
	}
	return errors.New("source: Muse did not let go of the session")
}
