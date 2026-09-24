package servers

import (
	"context"
	"os"
	"sort"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/tmux"
)

const (
	// A server has to be seen on consecutive scans before it is shown, so a
	// listener that exists for one test and disappears never flickers onto
	// the phone.
	confirmSightings = 2
	// A port that did not answer HTTP is asked again after this long, since a
	// dev server can listen well before it is ready to serve.
	negativeProbeTTL = 30 * time.Second
	// Probes per scan, so a machine full of new listeners cannot turn one
	// scan into a long series of timeouts.
	maxProbesPerScan = 8
	// More than any real project runs, and a bound on what one session can
	// put on the wire.
	maxServersPerSession = 16
)

// Watcher turns successive scans into each session's list of servers.
// It is not safe for concurrent use; the daemon calls it from one loop.
type Watcher struct {
	scan    func(context.Context) (Scan, error)
	tree    func(context.Context) (Ancestry, error)
	probe   func(context.Context, int) (string, bool)
	now     func() time.Time
	selfPID int

	sightings map[listenerKey]int
	probes    map[listenerKey]probeResult
	ignored   map[int]bool
}

type listenerKey struct{ pid, port int }

type probeResult struct {
	ok    bool
	title string
	at    time.Time
}

// NewWatcher builds a watcher for this machine.
func NewWatcher() *Watcher {
	return &Watcher{
		scan: ScanListeners,
		tree: func(ctx context.Context) (Ancestry, error) {
			tree, err := tmux.SnapshotProcessTree(ctx)
			if err != nil {
				// A nil *ProcessTree in an interface is not a nil interface.
				return nil, err
			}
			return tree, nil
		},
		probe:     Probe,
		now:       time.Now,
		selfPID:   os.Getpid(),
		sightings: map[listenerKey]int{},
		probes:    map[listenerKey]probeResult{},
	}
}

// Ignore hides ports that belong to agentman itself, such as the hook
// listener of a daemon running in another process.
func (w *Watcher) Ignore(ports ...int) {
	if w.ignored == nil {
		w.ignored = map[int]bool{}
	}
	for _, port := range ports {
		w.ignored[port] = true
	}
}

// Update scans once and returns the confirmed servers of each session.
// Sessions with no servers are absent from the map.
func (w *Watcher) Update(ctx context.Context, owners []Owner) (map[string][]protocol.Server, error) {
	return w.update(ctx, owners, confirmSightings)
}

// Snapshot is Update without the wait for a second sighting, for a one-off
// listing such as `am servers`.
func (w *Watcher) Snapshot(ctx context.Context, owners []Owner) (map[string][]protocol.Server, error) {
	return w.update(ctx, owners, 1)
}

// OwnersOf describes discovered sessions as the owners servers can belong to.
func OwnersOf(sessions []protocol.Session) []Owner {
	owners := make([]Owner, 0, len(sessions))
	for _, session := range sessions {
		owners = append(owners, Owner{
			SessionID: session.ID, Cwd: session.Cwd, PID: session.AgentPID,
			LastActivity: session.LastActivityAt,
		})
	}
	return owners
}

func (w *Watcher) update(ctx context.Context, owners []Owner, confirm int) (map[string][]protocol.Server, error) {
	scan, err := w.scan(ctx)
	if err != nil {
		return nil, err
	}
	// Without a process tree, attribution falls back to directories alone.
	tree, _ := w.tree(ctx)
	attributed := Attribute(owners, scan, tree, w.selfPID)

	now := w.now()
	sightings := map[listenerKey]int{}
	result := map[string][]protocol.Server{}
	probes := 0
	for sessionID, listeners := range attributed {
		for _, listener := range listeners {
			if w.ignored[listener.Port] {
				continue
			}
			key := listenerKey{listener.PID, listener.Port}
			sightings[key] = w.sightings[key] + 1
			if sightings[key] < confirm {
				continue
			}
			probe, known := w.probes[key]
			stale := !known || (!probe.ok && now.Sub(probe.at) >= negativeProbeTTL)
			if stale && probes < maxProbesPerScan {
				probes++
				title, ok := w.probe(ctx, listener.Port)
				probe = probeResult{ok: ok, title: title, at: now}
				w.probes[key] = probe
			}
			if !probe.ok || len(result[sessionID]) >= maxServersPerSession {
				continue
			}
			result[sessionID] = append(result[sessionID], protocol.Server{
				Port: listener.Port, Command: listener.Command, Title: probe.title,
			})
		}
	}
	// Forget everything no longer listening, so a restarted server on the
	// same port is probed afresh and memory stays bounded.
	for key := range w.probes {
		if _, alive := sightings[key]; !alive {
			delete(w.probes, key)
		}
	}
	w.sightings = sightings

	for _, servers := range result {
		sort.Slice(servers, func(i, j int) bool { return servers[i].Port < servers[j].Port })
	}
	return result, nil
}
