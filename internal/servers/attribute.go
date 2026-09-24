package servers

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	// Ports below 1024 need root and are never a dev server an agent started.
	minServerPort = 1024
	// The kernel hands out ports from here up to programs that ask for "any
	// free port" — which is exactly what test suites do for throwaway servers
	// (Go's httptest, supertest and friends). Dev servers pick fixed, low
	// numbers, so ignoring this range filters out a test run's dozens of
	// short-lived listeners without hiding anything a person would open.
	firstEphemeralPort = 32768
)

// Owner is an agent session a server can belong to.
type Owner struct {
	SessionID string
	Cwd       string
	// PID is the agent's own process, or zero when the adapter cannot tell.
	PID int
	// LastActivity breaks ties between sessions sharing a directory: the one
	// that was working most recently most likely started the server.
	LastActivity int64
}

// Ancestry answers whether one process descends from another.
type Ancestry interface {
	OwnsPID(ancestor, pid int) bool
}

// Attribute assigns each listener to at most one session.
//
// Ancestry is tried first because it is exact: whatever an agent's tools start
// runs as its descendant. The working directory is the fallback for agents
// whose pid is unknown, and for servers that detached from the agent's
// process tree (nohup, a daemonizing server), which ancestry cannot see.
func Attribute(owners []Owner, scan Scan, tree Ancestry, selfPID int) map[string][]Listener {
	agentPIDs := map[int]bool{}
	for _, owner := range owners {
		if owner.PID > 0 {
			agentPIDs[owner.PID] = true
		}
	}

	result := map[string][]Listener{}
	for _, listener := range scan.Listeners {
		// The agent's own sockets (IDE bridges, its OAuth callback) and the
		// daemon's hook listener are infrastructure, not something to open.
		if !listener.Loopback || listener.PID == selfPID || agentPIDs[listener.PID] ||
			listener.Port < minServerPort || listener.Port >= firstEphemeralPort {
			continue
		}
		if owner, ok := ownerByAncestry(owners, listener.PID, tree); ok {
			result[owner] = append(result[owner], listener)
			continue
		}
		if owner, ok := ownerByDirectory(owners, scan.Cwds[listener.PID]); ok {
			result[owner] = append(result[owner], listener)
		}
	}
	return result
}

func ownerByAncestry(owners []Owner, pid int, tree Ancestry) (string, bool) {
	if tree == nil {
		return "", false
	}
	for _, owner := range owners {
		if owner.PID > 0 && tree.OwnsPID(owner.PID, pid) {
			return owner.SessionID, true
		}
	}
	return "", false
}

// ownerByDirectory picks the session whose directory is the deepest one
// containing cwd, so a server started in ~/code/app/web belongs to a session
// in ~/code/app/web rather than one in ~/code/app.
func ownerByDirectory(owners []Owner, cwd string) (string, bool) {
	if cwd == "" {
		return "", false
	}
	cwd = filepath.Clean(cwd)
	best, bestDepth, bestActivity := "", -1, int64(0)
	for _, owner := range owners {
		if owner.Cwd == "" {
			continue
		}
		root := filepath.Clean(owner.Cwd)
		// A session rooted at / or the home directory would claim every
		// server on the machine, which is a guess, not an attribution.
		if root == "/" || root == homeDir() {
			continue
		}
		if cwd != root && !strings.HasPrefix(cwd, root+string(filepath.Separator)) {
			continue
		}
		depth := len(root)
		if depth > bestDepth || (depth == bestDepth && owner.LastActivity > bestActivity) {
			best, bestDepth, bestActivity = owner.SessionID, depth, owner.LastActivity
		}
	}
	return best, best != ""
}

// homeDir is a variable so tests can pin it.
var homeDir = func() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Clean(home)
}
