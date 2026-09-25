package servers

import (
	"context"
	"errors"
	"fmt"
	"syscall"
	"time"
)

const (
	// How long a server gets to exit on its own before it is killed. Dev
	// servers handle SIGTERM and use the time to close sockets and flush; a
	// hung one should not leave the port occupied forever.
	stopGrace = 3 * time.Second
	stopPoll  = 100 * time.Millisecond
)

// Stop asks whatever is listening on port to exit.
//
// The port is resolved to a process here, from a fresh scan, rather than from
// anything the phone sent: a pid that travelled to a device and back could
// name any process on the machine by the time it returned. The listener must
// still belong to the session making the request, so one session can never
// stop another's server — nor the agent itself, nor this daemon, both of which
// Attribute already excludes.
func (w *Watcher) Stop(ctx context.Context, owners []Owner, sessionID string, port int) error {
	scan, err := w.scan(ctx)
	if err != nil {
		return fmt.Errorf("servers: could not list listening ports: %w", err)
	}
	tree, _ := w.tree(ctx)

	var pid int
	for _, listener := range Attribute(owners, scan, tree, w.selfPID)[sessionID] {
		if listener.Port == port {
			pid = listener.PID
			break
		}
	}
	if pid == 0 {
		return errors.New("that server is no longer running")
	}

	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return nil // it exited between the scan and the signal
		}
		return fmt.Errorf("servers: could not stop the server: %w", err)
	}

	deadline := time.Now().Add(stopGrace)
	for time.Now().Before(deadline) {
		if !alive(pid) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(stopPoll):
		}
	}

	// Still holding the port after the grace period. SIGKILL cannot be
	// declined, and leaving a wedged process on the port would make the
	// button look broken.
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("servers: could not stop the server: %w", err)
	}
	return nil
}

// alive reports whether a pid still exists. Signal 0 performs the permission
// and existence checks without delivering anything.
func alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
