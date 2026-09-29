package source

import (
	"context"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Two things the Claude registry gets wrong for our purposes, both of which
// showed up as rows that should not exist.
//
// Claude Code runs background workers of its own — `claude bg-spare`, kept
// warm so the next session starts fast, `claude bg-pty-host`, and
// `claude daemon run` — and they write the same registry file a conversation
// does: a pid, a session id, a working directory, status idle. Nothing in the
// file tells them apart. Read as sessions they appeared as agents with no
// transcript, and since opening a session now resumes it, tapping one would
// try to reopen a conversation that never happened.
//
// And two live processes can hold one conversation — a session resumed in a
// second terminal while the first is still open. Each wrote its own registry
// file with the same session id, and each became a row, so one conversation
// was listed twice with no way to tell which was which.

// claudeCandidate is one live registry entry, before workers are dropped and
// duplicates merged.
type claudeCandidate struct {
	file claudeSessionFile
	// tmuxName is the pane this process runs in, if any.
	tmuxName string
}

// claudeProcessKey identifies a process for long enough to remember a verdict
// about it. The pid alone is not enough: pids are reused, and a new
// conversation must not inherit the verdict of the worker that last had its
// number. The start time is what separates them.
type claudeProcessKey struct {
	pid     int
	started int64
}

func (c claudeCandidate) key() claudeProcessKey {
	return claudeProcessKey{pid: c.file.PID, started: c.file.StartedAt}
}

// withoutInfra drops Claude Code's own workers.
//
// Their command line is the only thing that tells them apart, and the process
// snapshot discovery already takes records only each executable's name. So
// this asks `ps` for the arguments of every registry pid it has not judged
// yet, in one call, and remembers the answer. A process's command line does
// not change, so once the registry's handful of pids are judged a sweep costs
// no extra process at all — which matters, because this runs every second and
// the code around it was rewritten once already to stop spawning per session.
//
// A pid ps cannot report is left in and left unjudged: it has probably just
// exited, and showing a session for one more sweep is the cheaper mistake.
func (s *ClaudeSource) withoutInfra(ctx context.Context, candidates []claudeCandidate) []claudeCandidate {
	if s.processArgs == nil || len(candidates) == 0 {
		return candidates
	}

	s.infraMu.Lock()
	var unknown []int
	asked := map[int]bool{}
	for _, c := range candidates {
		if _, judged := s.infra[c.key()]; !judged && !asked[c.file.PID] {
			asked[c.file.PID] = true
			unknown = append(unknown, c.file.PID)
		}
	}
	s.infraMu.Unlock()

	var args map[int]string
	if len(unknown) > 0 {
		args = s.processArgs(ctx, unknown)
	}

	s.infraMu.Lock()
	defer s.infraMu.Unlock()
	seen := make(map[claudeProcessKey]bool, len(candidates))
	kept := make([]claudeCandidate, 0, len(candidates))
	for _, c := range candidates {
		key := c.key()
		seen[key] = true
		if _, judged := s.infra[key]; !judged {
			if line, reported := args[c.file.PID]; reported {
				s.infra[key] = isClaudeInfra(line)
			}
		}
		if !s.infra[key] {
			kept = append(kept, c)
		}
	}
	// Forget processes that are gone, so the cache is only ever as large as
	// the registry is.
	for key := range s.infra {
		if !seen[key] {
			delete(s.infra, key)
		}
	}
	return kept
}

// isClaudeInfra reports whether a command line is one of Claude Code's own
// workers.
//
// Only the argument right after the `claude` executable is read. `ps` prints
// arguments joined by spaces with their quoting gone, so a prompt passed on
// the command line arrives as loose words, and matching anywhere would hide
// the conversation whose prompt happened to mention "bg-spare". The one case
// this still misreads is a prompt whose very first word is a worker's name,
// which costs that session its row rather than anything worse.
func isClaudeInfra(args string) bool {
	fields := strings.Fields(args)
	for i, field := range fields {
		if filepath.Base(field) != "claude" {
			continue
		}
		rest := fields[i+1:]
		if len(rest) == 0 {
			return false
		}
		switch rest[0] {
		case "bg-spare", "bg-pty-host":
			return true
		case "daemon":
			return len(rest) > 1 && rest[1] == "run"
		}
		return false
	}
	return false
}

// claudeProcessArgs reads the full command lines of the given pids in one
// `ps` call.
func claudeProcessArgs(ctx context.Context, pids []int) map[int]string {
	ctx, cancel := context.WithTimeout(ctx, processCheckTimeout)
	defer cancel()
	list := make([]string, len(pids))
	for i, pid := range pids {
		list[i] = strconv.Itoa(pid)
	}
	// ps exits non-zero when any pid has gone, but still prints the ones it
	// found, so the error is not a reason to discard the output.
	out, _ := exec.CommandContext(ctx, "ps", "-o", "pid=,args=", "-p", strings.Join(list, ",")).Output()
	result := make(map[int]string, len(pids))
	for _, line := range strings.Split(string(out), "\n") {
		pidText, args, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		if pid, err := strconv.Atoi(pidText); err == nil {
			result[pid] = strings.TrimSpace(args)
		}
	}
	return result
}

// mergeClaudeCandidates keeps one entry per conversation.
//
// The one kept is the one in a pane when there is one — that is the process a
// message can be typed into, so it is the one a row should lead to — and
// otherwise the most recently active. The row still reports the busiest of
// them: if either process is working, something is happening in that
// conversation, and showing it idle would be the wrong half of the truth.
func mergeClaudeCandidates(candidates []claudeCandidate) []claudeCandidate {
	order := make([]string, 0, len(candidates))
	groups := make(map[string][]claudeCandidate, len(candidates))
	for _, c := range candidates {
		id := c.file.SessionID
		if _, seen := groups[id]; !seen {
			order = append(order, id)
		}
		groups[id] = append(groups[id], c)
	}

	merged := make([]claudeCandidate, 0, len(order))
	for _, id := range order {
		group := groups[id]
		winner := group[0]
		for _, c := range group[1:] {
			if preferClaudeCandidate(c, winner) {
				winner = c
			}
		}
		for _, c := range group {
			if c.file.Status == "busy" {
				winner.file.Status = "busy"
			}
			winner.file.UpdatedAt = max(winner.file.UpdatedAt, c.file.UpdatedAt)
		}
		merged = append(merged, winner)
	}
	return merged
}

// preferClaudeCandidate reports whether a should speak for a conversation
// over b. The pid breaks a tie only so the choice is the same every sweep.
func preferClaudeCandidate(a, b claudeCandidate) bool {
	if (a.tmuxName != "") != (b.tmuxName != "") {
		return a.tmuxName != ""
	}
	if a.file.UpdatedAt != b.file.UpdatedAt {
		return a.file.UpdatedAt > b.file.UpdatedAt
	}
	return a.file.PID > b.file.PID
}
