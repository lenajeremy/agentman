package source

import (
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/tmux"
)

// kiroStatus is what Kiro's status line says about a session: the agent it
// runs as, its model, and how full its context window is.
type kiroStatus struct {
	agent   string
	model   string
	context float64 // percent; meaningful only when hasContext
	// hasContext separates "0%" from "not known".
	hasContext bool
}

// kiroStatusLine is the line above Kiro's prompt:
//
//	kiro_default · auto · ◔ 4%             /work
//	Plan · claude-haiku-4.5 · ◔ 4%         /work
//
// The glyph before the percentage fills as the context does, so any single
// character is accepted there.
var kiroStatusLine = regexp.MustCompile(`^\s*(\S[^·]*?)\s+·\s+(\S+)\s+·\s+\S\s*(\d{1,3})%`)

// apply shows the status on a session: its model, the agent it runs as —
// the session's mode, as the phone calls it — and how full its context is.
func (status kiroStatus) apply(session *protocol.Session) {
	session.Model = status.model
	session.Mode = kiroMode(status.agent)
	if status.hasContext {
		session.ContextPercent = int(math.Round(status.context))
	}
}

// kiroPaneStatus reads the status line from the bottom of a pane. It is the
// live truth — a /model switch shows there at once, while the metadata file
// changes only with the next turn — but a picker or panel can cover it.
func kiroPaneStatus(pane string) (kiroStatus, bool) {
	lines := strings.Split(strings.TrimRight(pane, "\n"), "\n")
	for i := len(lines) - 1; i >= 0 && i >= len(lines)-8; i-- {
		match := kiroStatusLine.FindStringSubmatch(lines[i])
		if match == nil {
			continue
		}
		percent, err := strconv.Atoi(match[3])
		if err != nil || percent > 100 {
			return kiroStatus{}, false
		}
		return kiroStatus{agent: match[1], model: match[2], context: float64(percent), hasContext: true}, true
	}
	return kiroStatus{}, false
}

// kiroMetaStatus reads the same facts from the session's metadata, as of its
// last completed turn.
func kiroMetaStatus(meta kiroMeta) kiroStatus {
	state := meta.SessionState
	status := kiroStatus{agent: state.AgentName, model: state.RTSModelState.ModelInfo.ModelID}
	if percent := state.RTSModelState.ContextUsagePercentage; percent != nil && *percent >= 0 && *percent <= 100 {
		status.context, status.hasContext = *percent, true
	}
	return status
}

// kiroMode names the agent a session runs as, for the phone to show beside its
// model — or "" for Kiro's default agent, which is nothing worth a label.
// The status line already says "Plan" for kiro_planner; the metadata says
// kiro_planner, and reads the same way here.
func kiroMode(agent string) string {
	switch agent {
	case "", "kiro_default":
		return ""
	case "kiro_planner":
		return "Plan"
	}
	return agent
}

// kiroTurnMarkersDir is where Kiro's interface marks a turn in progress: one
// file, "<pid>-<turn start ms>.json", from the moment a turn starts until it
// ends, whatever ended it. The pid is the interface's own process, which the
// session's lock process descends from.
func (s *KiroSource) kiroTurnMarkersDir() string {
	return filepath.Join(s.home, "Library", "Application Support", "kiro-cli", "run", "turn-markers")
}

// kiroTurnMarkers lists the pids with a turn in progress, and whether Kiro
// keeps markers at all on this machine.
func (s *KiroSource) kiroTurnMarkers() (map[int]bool, bool) {
	entries, err := os.ReadDir(s.kiroTurnMarkersDir())
	if err != nil {
		return nil, false
	}
	pids := map[int]bool{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		head, _, ok := strings.Cut(name, "-")
		if !ok {
			continue
		}
		if pid, err := strconv.Atoi(head); err == nil && pid > 1 {
			pids[pid] = true
		}
	}
	return pids, true
}

// kiroMarkedBusy reports whether a marker shows a turn in progress for the
// session whose lock names lockPID: the marker's process is still running
// and is the lock process or one of its ancestors.
func kiroMarkedBusy(markers map[int]bool, processes *tmux.ProcessTree, lockPID int) bool {
	for pid := range markers {
		if processes.Command(pid) != "" && processes.OwnsPID(pid, lockPID) {
			return true
		}
	}
	return false
}

// kiroTurnGrace covers the moment between Kiro removing a turn's marker and
// writing the reply that ended it, so a finished turn is not announced before
// its reply is on disk.
const kiroTurnGrace = 3 * time.Second

// kiroMarkedState decides busy or idle for a session whose turns are known to
// leave markers.
//
// The transcript alone is wrong in one direction for good: a turn that ends
// in an error writes nothing more, so a transcript ending in a prompt or a
// tool result says "busy" until the next turn. The marker is gone by then.
func kiroMarkedState(fromTranscript protocol.State, busy bool, lastWrite, now time.Time) protocol.State {
	if busy {
		return protocol.StateBusy
	}
	if fromTranscript == protocol.StateBusy && now.Sub(lastWrite) < kiroTurnGrace {
		return protocol.StateBusy
	}
	return protocol.StateIdle
}
