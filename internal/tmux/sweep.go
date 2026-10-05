package tmux

import (
	"context"
	"slices"
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
