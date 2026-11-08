package manager

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Shipping several finished agents together: each one's changes are
// committed, pushed and opened as a pull request, one agent after another.
// It only ever runs when the user asks for these agents, and it never pushes
// a default branch or commits work whose tests fail.

const shipStepTimeout = 5 * time.Minute

// shipState is the latest run, for apps that connect during one.
type shipState struct {
	mu  sync.Mutex
	run *protocol.ShipRun
}

// current is a copy of the run, if there has been one.
func (s *shipState) current() *protocol.ShipRun {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.run == nil {
		return nil
	}
	copied := *s.run
	copied.Items = append([]protocol.ShipItem(nil), s.run.Items...)
	return &copied
}

// begin takes the one run at a time.
func (s *shipState) begin(run protocol.ShipRun) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.run != nil && s.run.State == protocol.ShipRunning {
		return false
	}
	s.run = &run
	return true
}

func (s *shipState) change(edit func(*protocol.ShipRun)) protocol.ShipRun {
	s.mu.Lock()
	defer s.mu.Unlock()
	edit(s.run)
	copied := *s.run
	copied.Items = append([]protocol.ShipItem(nil), s.run.Items...)
	return copied
}

func (m *Manager) startShip(ids []string) protocol.Event {
	if len(ids) == 0 {
		return protocol.Event{Type: protocol.EvtError, Error: "Choose the agents to ship."}
	}
	run := protocol.ShipRun{ID: newID(), State: protocol.ShipRunning, StartedAt: m.now().UnixMilli()}
	for _, id := range ids {
		name := id
		if session, ok := m.session(id); ok {
			name = session.Name
		}
		run.Items = append(run.Items, protocol.ShipItem{SessionID: id, Name: name, State: protocol.ShipWaiting})
	}
	if !m.shipping.begin(run) {
		return protocol.Event{Type: protocol.EvtError, Error: "Agentman is already shipping. Wait for it to finish."}
	}
	go m.runShip(context.Background(), len(ids))
	return protocol.Event{Type: protocol.EvtManagerShip, Ship: m.shipping.current()}
}

func (m *Manager) setShipItem(index int, state, note, url string) {
	run := m.shipping.change(func(run *protocol.ShipRun) {
		run.Items[index].State, run.Items[index].Note = state, note
		if url != "" {
			run.Items[index].URL = url
		}
	})
	m.emit(protocol.Event{Type: protocol.EvtManagerShip, Ship: &run})
}

func (m *Manager) runShip(ctx context.Context, count int) {
	shipped := 0
	var problems []string
	for i := 0; i < count; i++ {
		item := m.shipping.current().Items[i]
		m.setShipItem(i, protocol.ShipWorking, "", "")
		stepCtx, cancel := context.WithTimeout(ctx, shipStepTimeout)
		note, url, skipped, err := m.shipOne(stepCtx, item.SessionID)
		cancel()
		switch {
		case err != nil:
			m.setShipItem(i, protocol.ShipFailed, err.Error(), "")
			problems = append(problems, item.Name+": "+err.Error())
		case skipped:
			m.setShipItem(i, protocol.ShipSkipped, note, "")
			problems = append(problems, item.Name+": "+note)
		default:
			m.setShipItem(i, protocol.ShipShipped, note, url)
			shipped++
		}
	}
	run := m.shipping.change(func(run *protocol.ShipRun) { run.State = protocol.ShipDone })
	m.emit(protocol.Event{Type: protocol.EvtManagerShip, Ship: &run})
	text := fmt.Sprintf("Shipped %d of %d.", shipped, count)
	if len(problems) > 0 {
		text += " " + strings.Join(problems, " ")
	}
	m.addEntry(protocol.ManagerEntry{Role: "brief", Text: text})
	m.note(text)
}

// shipOne takes one agent's work as far as a pull request. skipped says it
// was left alone, and why; err that it went wrong part way.
func (m *Manager) shipOne(ctx context.Context, sessionID string) (note, url string, skipped bool, err error) {
	session, live := m.session(sessionID)
	if !live {
		return "It has ended", "", true, nil
	}
	if session.State != protocol.StateIdle || session.Question != nil {
		return "It's still working", "", true, nil
	}
	if check, ok := m.verifies.get(sessionID); ok {
		switch check.State {
		case protocol.VerifyFailed:
			return "Its tests fail", "", true, nil
		case protocol.VerifyChecking:
			return "It's still being checked", "", true, nil
		}
	}
	git := m.agents.Request(ctx, protocol.Request{Type: protocol.ReqGitStatus, SessionID: sessionID}).Git
	switch {
	case git == nil:
		return "It isn't in a git repository", "", true, nil
	case git.Branch == "":
		return "No branch is checked out", "", true, nil
	case isProtectedBranch(git.Branch, git.DefaultBranch):
		return "It's on " + git.Branch + ", which is yours to push", "", true, nil
	case isProtectedBranch(upstreamBranch(git.Upstream), git.DefaultBranch):
		return "It tracks " + git.Upstream + ", which is yours to push", "", true, nil
	case git.Behind > 0:
		return "Its branch is behind its upstream", "", true, nil
	case git.Upstream == "" && git.Remote == "":
		return "The repository has no remote", "", true, nil
	case git.Changes == 0 && git.Ahead == 0 && git.AheadOfDefault == 0:
		return "There's nothing to ship", "", true, nil
	}
	if git.Changes > 0 {
		listed := m.agents.Request(ctx, protocol.Request{Type: protocol.ReqListChanges, SessionID: sessionID})
		if listed.Type == protocol.EvtError || listed.Workspace == nil {
			return "", "", false, errors.New("couldn't list its changes")
		}
		// Only what's listed: private files are left out of the list.
		paths := make([]string, 0, len(listed.Workspace.Changes))
		for _, change := range listed.Workspace.Changes {
			paths = append(paths, change.Path)
		}
		if len(paths) > 0 {
			if err := m.gitStep(ctx, protocol.Request{Type: protocol.ReqGitCommit, SessionID: sessionID, Paths: paths}, nil); err != nil {
				return "", "", false, fmt.Errorf("commit failed: %w", err)
			}
		}
	}
	if err := m.gitStep(ctx, protocol.Request{Type: protocol.ReqGitPush, SessionID: sessionID}, nil); err != nil {
		return "", "", false, fmt.Errorf("push failed: %w", err)
	}
	var after protocol.GitState
	if err := m.gitStep(ctx, protocol.Request{Type: protocol.ReqGitCreatePR, SessionID: sessionID}, &after); err != nil {
		return "Pushed, but opening the pull request failed: " + err.Error(), "", false, nil
	}
	return "Pull request open", after.URL, false, nil
}

// isProtectedBranch reports the default branch, and the names a default
// branch goes by in a repository that can't say which is its own.
func isProtectedBranch(branch, defaultBranch string) bool {
	return branch != "" && (branch == defaultBranch || slices.Contains([]string{"main", "master", "trunk", "develop", "production"}, branch))
}

// upstreamBranch is origin/name without its remote.
func upstreamBranch(upstream string) string {
	_, branch, _ := strings.Cut(upstream, "/")
	return branch
}

func (m *Manager) gitStep(ctx context.Context, req protocol.Request, into *protocol.GitState) error {
	event := m.agents.Request(ctx, req)
	if event.Type == protocol.EvtError {
		return errors.New(plainError(event.Error))
	}
	if into != nil && event.Git != nil {
		*into = *event.Git
	}
	return nil
}
