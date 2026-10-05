package tmux

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// A discovery sweep runs every adapter side by side, once a second, and each
// of them used to ask the same questions on its own: five `tmux
// list-sessions` and three `ps` runs a sweep, for one answer each. A sweep
// now carries a memory of those answers in its context.
//
// Only discovery carries one. Launching, sending and answering need the panes
// as they are at that moment, never as they were when the sweep began, so
// List, SnapshotProcessTree and Capture consult the memory only when the
// context they are given holds a sweep.

type sweepKey struct{}

type sweep struct {
	// ctx is the sweep's own context. The reads it shares run on it rather
	// than on whichever adapter asked first, so one adapter giving up early
	// cannot leave the others an empty answer.
	ctx   context.Context
	ended atomic.Bool

	listOnce sync.Once
	sessions []Session
	listErr  error

	treeOnce sync.Once
	tree     *ProcessTree
	treeErr  error

	captureMu sync.Mutex
	captured  bool
	// panes holds each pane's capture until it is read. See capture.
	panes map[string]string
}

// WithSweep returns a context for one discovery sweep, and a function to call
// when the sweep is over. Anything that keeps the context past that point
// asks tmux and ps afresh.
func WithSweep(ctx context.Context) (context.Context, func()) {
	s := &sweep{ctx: ctx}
	return context.WithValue(ctx, sweepKey{}, s), func() { s.ended.Store(true) }
}

// InSweep reports whether ctx belongs to a discovery sweep that is still
// running.
func InSweep(ctx context.Context) bool { return sweepOf(ctx) != nil }

func sweepOf(ctx context.Context) *sweep {
	s, _ := ctx.Value(sweepKey{}).(*sweep)
	if s == nil || s.ended.Load() {
		return nil
	}
	return s
}

func (s *sweep) list() ([]Session, error) {
	s.listOnce.Do(func() { s.sessions, s.listErr = listSessions(s.ctx) })
	return slices.Clone(s.sessions), s.listErr
}

func (s *sweep) processTree() (*ProcessTree, error) {
	s.treeOnce.Do(func() { s.tree, s.treeErr = snapshotProcessTree(s.ctx) })
	return s.tree, s.treeErr
}

// capture returns a pane as the sweep captured it.
//
// The first capture asked for in a sweep captures every listed pane in one
// tmux run. Each pane is handed out once: a second look in the same sweep is
// fresh, so a flow that captures, presses a key and captures again sees what
// the key did. A pane the batch did not reach is not found here either, and
// the caller captures it alone.
func (s *sweep) capture(name string) (string, bool) {
	s.captureMu.Lock()
	defer s.captureMu.Unlock()
	if !s.captured {
		s.captured = true
		sessions, _ := s.list()
		s.panes = captureAll(s.ctx, sessions)
	}
	pane, ok := s.panes[name]
	delete(s.panes, name)
	return pane, ok
}

// forget drops a pane's capture, because something was just sent to it.
func (s *sweep) forget(name string) {
	s.captureMu.Lock()
	defer s.captureMu.Unlock()
	delete(s.panes, name)
}

// captureAll captures every session's pane in one tmux run:
//
//	capture-pane -t A -p ; display-message -p MARK0 ; capture-pane -t B -p ; ...
//
// Each pane's text is what a capture of it alone prints, and ends where its
// marker begins. The marker follows the capture, so a pane is only taken once
// tmux has printed all of it. Tmux stops at the first pane that has closed
// since the list; the panes before it are kept.
//
// A marker cannot be mistaken for pane text: capture-pane prints a screen's
// cells, and the record separator it starts with is a control character no
// cell holds.
func captureAll(ctx context.Context, sessions []Session) map[string]string {
	if len(sessions) == 0 {
		return nil
	}
	var nonce [8]byte
	_, _ = rand.Read(nonce[:])
	marker := func(index int) string {
		return "\x1eagentman-" + hex.EncodeToString(nonce[:]) + "-" + strconv.Itoa(index)
	}
	args := make([]string, 0, len(sessions)*8)
	for index, session := range sessions {
		if index > 0 {
			args = append(args, ";")
		}
		args = append(args, "capture-pane", "-t", session.Name, "-p", ";", "display-message", "-p", marker(index))
	}
	out, _ := runOutput(ctx, args...)
	panes := make(map[string]string, len(sessions))
	for index, session := range sessions {
		end := strings.Index(out, marker(index)+"\n")
		if end < 0 {
			break
		}
		panes[session.Name] = out[:end]
		out = out[end+len(marker(index))+1:]
	}
	return panes
}

// sendsTo returns the pane a tmux command changes the screen of, if any.
func sendsTo(args []string) (string, bool) {
	if len(args) == 0 {
		return "", false
	}
	switch args[0] {
	case "send-keys", "paste-buffer", "kill-session", "respawn-pane", "clear-history":
	default:
		return "", false
	}
	for index := 1; index+1 < len(args); index++ {
		if args[index] == "-t" {
			return args[index+1], true
		}
	}
	return "", false
}
