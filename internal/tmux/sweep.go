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

// list lists the panes once a sweep. The same tmux run captures the panes
// the last listing found, which every adapter goes on to read; see listOn.
func (s *sweep) list() ([]Session, error) {
	s.listOnce.Do(func() {
		var panes map[string]string
		s.sessions, panes, s.listErr = listAndCapture(s.ctx, true)
		s.captureMu.Lock()
		s.panes = panes
		s.captureMu.Unlock()
	})
	return slices.Clone(s.sessions), s.listErr
}

func (s *sweep) processTree() (*ProcessTree, error) {
	s.treeOnce.Do(func() { s.tree, s.treeErr = snapshotProcessTree(s.ctx) })
	return s.tree, s.treeErr
}

// capture returns a pane as the sweep captured it.
//
// The listing captured the panes it already knew. The first capture asked
// for in a sweep captures every other listed pane — one that is new since
// the last sweep, or that a closed pane kept the listing's run from
// reaching — in one tmux run. Each pane is handed out once: a second look in
// the same sweep is fresh, so a flow that captures, presses a key and
// captures again sees what the key did. A pane the batch did not reach is
// not found here either, and the caller captures it alone.
func (s *sweep) capture(name string) (string, bool) {
	sessions, _ := s.list()
	s.captureMu.Lock()
	defer s.captureMu.Unlock()
	if !s.captured {
		s.captured = true
		var missing []Session
		for _, session := range sessions {
			if _, captured := s.panes[session.Name]; !captured {
				missing = append(missing, session)
			}
		}
		for pane, text := range captureAll(s.ctx, missing) {
			if s.panes == nil {
				s.panes = map[string]string{}
			}
			s.panes[pane] = text
		}
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
// A marker is plain text with a random 64-bit nonce in it, fresh for every
// run, so no screen can hold one by accident. It used to start with a record
// separator, on the reasoning that no cell holds a control character — but
// older tmux (3.4, as Ubuntu ships it) escapes control characters in
// display-message output, so the marker never matched and every pane fell
// back to a capture of its own.
func captureAll(ctx context.Context, sessions []Session) map[string]string {
	if len(sessions) == 0 {
		return nil
	}
	// One run per server: a run reaches one.
	byServer := map[string][]Session{}
	var order []string
	for _, session := range sessions {
		key := strings.Join(serverOf(session.Name), " ")
		if _, seen := byServer[key]; !seen {
			order = append(order, key)
		}
		byServer[key] = append(byServer[key], session)
	}
	panes := make(map[string]string, len(sessions))
	for _, key := range order {
		for name, pane := range captureOn(ctx, serverOf(byServer[key][0].Name), byServer[key]) {
			panes[name] = pane
		}
	}
	return panes
}

// captureOn captures the panes of sessions that all run on one server.
func captureOn(ctx context.Context, server server, sessions []Session) map[string]string {
	names := make([]string, len(sessions))
	for index, session := range sessions {
		names[index] = session.Name
	}
	marker := newMarker()
	out, _ := runOutputOn(ctx, server, captureArgs(names, marker)...)
	return parseCaptures(out, names, marker)
}

// newMarker returns the markers of one tmux run, each with the run's nonce.
func newMarker() func(label string) string {
	var nonce [8]byte
	_, _ = rand.Read(nonce[:])
	prefix := "agentman-sweep-" + hex.EncodeToString(nonce[:]) + "-"
	return func(label string) string { return prefix + label }
}

// captureArgs captures each pane, then prints its marker.
func captureArgs(names []string, marker func(string) string) []string {
	args := make([]string, 0, len(names)*8)
	for index, name := range names {
		if index > 0 {
			args = append(args, ";")
		}
		args = append(args, "capture-pane", "-t", name, "-p", ";", "display-message", "-p", marker(strconv.Itoa(index)))
	}
	return args
}

// parseCaptures splits captureArgs's output into panes, as far as it got.
func parseCaptures(out string, names []string, marker func(string) string) map[string]string {
	panes := make(map[string]string, len(names))
	for index, name := range names {
		mark := marker(strconv.Itoa(index))
		end := strings.Index(out, mark+"\n")
		if end < 0 {
			break
		}
		panes[name] = out[:end]
		out = out[end+len(mark)+1:]
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
