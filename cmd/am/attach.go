package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/tmux"
)

// runAttach shows an agent's terminal again: the window that ran `am claude`
// can close while the agent works on, and this opens a new one onto it.
func runAttach(ctx context.Context, args []string) error {
	if len(args) > 1 {
		return errors.New("attach: one agent at a time (am attach [name, number or id])")
	}
	want := ""
	if len(args) == 1 {
		want = args[0]
	}
	path, err := socketPath()
	if err != nil {
		return err
	}
	call := socketCall(path)
	ask, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	listed, err := call(ask, protocol.Request{Type: protocol.ReqListSessions})
	if err != nil {
		// No am serve: tmux's own names still work.
		if want == "" {
			return errors.New("attach: am serve isn't running; name the tmux session (tmux -L agentman ls)")
		}
		return tmux.Attach(ctx, want)
	}
	agents := attachable(listed.Sessions)
	if len(agents) == 0 {
		return errors.New("attach: no agent is running in a terminal Agentman started")
	}
	cwd, _ := os.Getwd()
	chosen, err := pickAgent(agents, want, cwd)
	if err != nil {
		printAgents(agents)
		return err
	}
	answer, err := call(ask, protocol.Request{Type: protocol.ReqSessionTerminal, SessionID: chosen.ID})
	if err != nil {
		return err
	}
	if answer.Type == protocol.EvtError || answer.Terminal == nil {
		return fmt.Errorf("attach: %s", strings.TrimPrefix(answer.Error, "daemon: "))
	}
	binary, err := exec.LookPath(answer.Terminal.Argv[0])
	if err != nil {
		return err
	}
	return syscallExec(binary, answer.Terminal.Argv, os.Environ())
}

// attachable are the running sessions Agentman can type into, newest first.
func attachable(sessions []protocol.Session) []protocol.Session {
	var agents []protocol.Session
	for _, session := range sessions {
		if session.Inject == protocol.InjectTmux && session.State != protocol.StateEnded {
			agents = append(agents, session)
		}
	}
	slices.SortStableFunc(agents, func(a, b protocol.Session) int {
		return int(b.LastActivityAt - a.LastActivityAt)
	})
	return agents
}

// pickAgent finds the one meant: by number in the list, by name, or by id
// or the start of one; with nothing asked for, the only one in this folder.
func pickAgent(agents []protocol.Session, want, cwd string) (protocol.Session, error) {
	if want == "" {
		var here []protocol.Session
		for _, agent := range agents {
			// Working in this folder, in one inside it, or around it.
			if within(agent.Cwd, cwd) || within(cwd, agent.Cwd) {
				here = append(here, agent)
			}
		}
		if len(here) == 1 {
			return here[0], nil
		}
		if len(agents) == 1 {
			return agents[0], nil
		}
		return protocol.Session{}, errors.New("attach: which one? am attach <number or name>")
	}
	if number, err := strconv.Atoi(want); err == nil && number >= 1 && number <= len(agents) {
		return agents[number-1], nil
	}
	for _, agent := range agents {
		if strings.EqualFold(agent.Name, want) || agent.ID == want {
			return agent, nil
		}
	}
	var matches []protocol.Session
	for _, agent := range agents {
		if strings.HasPrefix(agent.ID, want) || strings.HasPrefix(strings.ToLower(agent.Name), strings.ToLower(want)) {
			matches = append(matches, agent)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	return protocol.Session{}, fmt.Errorf("attach: no single agent matches %q", want)
}

func within(path, folder string) bool {
	path, folder = filepath.Clean(path), filepath.Clean(folder)
	return path == folder || strings.HasPrefix(path, folder+string(filepath.Separator))
}

func printAgents(agents []protocol.Session) {
	for index, agent := range agents {
		fmt.Fprintf(os.Stderr, "  %2d  %-28s %-8s %s\n", index+1, agent.Name, agent.State, collapseHome(agent.Cwd))
	}
}
