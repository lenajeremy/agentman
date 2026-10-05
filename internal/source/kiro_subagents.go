package source

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lenajeremy/agentman/internal/jsonl"
	"github.com/lenajeremy/agentman/internal/parser"
	"github.com/lenajeremy/agentman/internal/protocol"
)

// A Kiro subagent works in a session of its own: its own metadata, its own
// transcript, in the same session directory as the conversation that started
// it. The parent records only the call and, at the end, the pipeline's
// result; what the subagent read, ran and said is in the child's transcript.
// Claude's feed shows that work as sidechain rows behind a chip, and so does
// this one.
//
// Nothing in either session names the other. A child is bound to its parent
// call by what can be checked — the same working directory, created during
// the call's turn, titled with exactly the prompt the call gave that stage —
// and only when that leaves no doubt: a stage two children could belong to,
// or a child two stages could, shows nothing rather than the wrong work.

// kiroSubagentSlack widens a turn's window for the second-resolution prompt
// time it starts from.
const kiroSubagentSlack = 2 * time.Second

// Bounds on the work done for subagents: children shown per follow, messages
// shown per child, and the size of a child transcript that is read at all.
const (
	maxKiroChildTails       = 32
	maxKiroSidechainRows    = 2000
	maxKiroChildTranscript  = 8 << 20
	kiroChildScanInterval   = time.Second
	kiroSidechainStageWidth = 2
)

// kiroBinding is one stage of a subagent call bound to the session that ran
// it.
type kiroBinding struct {
	callID     string
	stage      int
	ts         int64 // the call row's timestamp
	transcript string
}

// kiroIsSubagentSession reports whether a session file family was written for
// a subagent: no agent name — every session a person talked to records one
// with its first turn — and no input history, which Kiro's interface keeps for
// every session someone typed into.
func kiroIsSubagentSession(base string, meta kiroMeta) bool {
	if meta.SessionState.AgentName != "" {
		return false
	}
	_, err := os.Stat(base + ".history")
	return os.IsNotExist(err)
}

// bindSubagents finds the child sessions of the given calls, made from the
// session whose transcript is parent.
func (s *KiroSource) bindSubagents(parent string, calls []parser.KiroSubagentCall, now time.Time) []kiroBinding {
	if len(calls) == 0 {
		return nil
	}
	parentBase := strings.TrimSuffix(parent, ".jsonl")
	parentMeta, ok := s.readMeta(parentBase + ".json")
	if !ok || parentMeta.Cwd == "" {
		return nil
	}
	cwd := filepath.Clean(parentMeta.Cwd)
	earliest := calls[0].TurnStart
	for _, call := range calls {
		earliest = min(earliest, call.TurnStart)
	}
	earliest -= kiroSubagentSlack.Milliseconds()

	type stageKey struct{ call, stage int }
	matches := map[stageKey][]string{}
	claims := map[string][]stageKey{}
	// Newest first, so the walk stops at the first session older than every
	// turn in question: a child is written after its parent's prompt.
	for _, entry := range newestTranscripts(s.sessionsDir(), ".json", 0) {
		if entry.modTime < earliest {
			break
		}
		base := strings.TrimSuffix(entry.path, ".json")
		if base == parentBase {
			continue
		}
		meta, ok := s.readMeta(entry.path)
		if !ok || filepath.Clean(meta.Cwd) != cwd || !kiroIsSubagentSession(base, meta) {
			continue
		}
		title := strings.TrimSpace(meta.Title)
		created := kiroTime(meta.CreatedAt)
		if title == "" || created == 0 {
			continue
		}
		for i, call := range calls {
			end := call.TurnEnd
			if end == 0 {
				end = now.UnixMilli()
			}
			slack := kiroSubagentSlack.Milliseconds()
			if created < call.TurnStart-slack || created > end+slack {
				continue
			}
			for stage, prompt := range call.Prompts {
				if prompt != "" && prompt == title {
					key := stageKey{i, stage}
					matches[key] = append(matches[key], base)
					claims[base] = append(claims[base], key)
				}
			}
		}
	}

	var bound []kiroBinding
	for i, call := range calls {
		for stage := range call.Prompts {
			children := matches[stageKey{i, stage}]
			if len(children) != 1 || len(claims[children[0]]) != 1 {
				continue // none, or not one beyond doubt
			}
			bound = append(bound, kiroBinding{
				callID: call.CallID, stage: stage, ts: call.Ts, transcript: children[0] + ".jsonl",
			})
		}
	}
	return bound
}

// kiroSidechain parses a child transcript into rows of its parent's feed.
//
// The child's rows take the parent session's id, the parent call's
// timestamp, and ids of their own: "<call id>/sub<stage>/<n>", n counting
// the child's rows in the order they first appear. The app orders rows by
// time and then by id, so a call row's children sort straight after it —
// the call's id is a prefix of theirs — and in their own order; and
// re-reading the same transcript names every row the same way, so a page and
// a follow agree.
type kiroSidechain struct {
	p         *parser.KiroParser
	sessionID string
	prefix    string
	ts        int64
	seq       map[string]int
}

func newKiroSidechain(sessionID string, binding kiroBinding) *kiroSidechain {
	return &kiroSidechain{
		p:         parser.NewKiroParser(sessionID),
		sessionID: sessionID,
		prefix:    fmt.Sprintf("%s/sub%0*d", binding.callID, kiroSidechainStageWidth, binding.stage+1),
		ts:        binding.ts,
		seq:       map[string]int{},
	}
}

func (c *kiroSidechain) parse(line string, offset int64) []protocol.Message {
	var out []protocol.Message
	for _, message := range c.p.Parse(line, offset) {
		// The child's prompt is the task the call gave it, which the call's
		// row already shows. As a user row it would read as something the
		// user typed: the app draws every user row as a sent message.
		if message.Role == protocol.RoleUser {
			continue
		}
		n, seen := c.seq[message.ID]
		if !seen {
			if len(c.seq) >= maxKiroSidechainRows {
				continue
			}
			n = len(c.seq) + 1
			c.seq[message.ID] = n
		}
		message.ID = fmt.Sprintf("%s/%06d", c.prefix, n)
		message.SessionID = c.sessionID
		message.Ts = c.ts
		message.IsSidechain = true
		out = append(out, message)
	}
	return out
}

// readSidechain reads a whole child transcript as sidechain rows, a settled
// row replacing its running one in place.
func readSidechain(ctx context.Context, sessionID string, binding kiroBinding) []protocol.Message {
	info, err := os.Stat(binding.transcript)
	if err != nil || info.Size() > maxKiroChildTranscript {
		return nil
	}
	side := newKiroSidechain(sessionID, binding)
	tail := jsonl.NewTail(binding.transcript)
	var rows []protocol.Message
	at := map[string]int{}
	for ctx.Err() == nil {
		lines, err := tail.Read()
		if err != nil || len(lines) == 0 {
			break
		}
		for _, line := range lines {
			for _, message := range side.parse(line.Text, line.Offset) {
				if index, seen := at[message.ID]; seen {
					rows[index] = message
					continue
				}
				at[message.ID] = len(rows)
				rows = append(rows, message)
			}
		}
	}
	return rows
}

// withSubagents places each bound child's rows straight after its call's row.
func (s *KiroSource) withSubagents(
	ctx context.Context, sessionID, transcript string, messages []protocol.Message, calls []parser.KiroSubagentCall,
) []protocol.Message {
	bindings := s.bindSubagents(transcript, calls, time.Now())
	if len(bindings) == 0 {
		return messages
	}
	children := map[string][]protocol.Message{}
	for _, binding := range bindings {
		children[binding.callID] = append(children[binding.callID], readSidechain(ctx, sessionID, binding)...)
	}
	out := make([]protocol.Message, 0, len(messages))
	for _, message := range messages {
		out = append(out, message)
		if rows := children[message.ID]; message.Tool != nil && len(rows) > 0 {
			out = append(out, rows...)
			delete(children, message.ID)
		}
	}
	return out
}

// kiroChildFollow streams the work of subagents running under a followed
// session, by tailing their transcripts while their call is in progress.
//
// Only calls still running when the follow began, or made after it, are
// followed; the work of finished ones belongs to Page. A child's transcript
// is read from its start, so rows a page already showed are sent again under
// the same ids, which the app merges. Children are looked for at most once a
// second while a call runs, and once more as it finishes, in case a quick
// subagent began and ended between two looks.
type kiroChildFollow struct {
	sessionID string
	parent    string
	finished  map[string]bool
	tails     map[string]*kiroChildTail
	lastScan  time.Time
}

type kiroChildTail struct {
	tail   *jsonl.Tail
	side   *kiroSidechain
	callID string
}

// newKiroChildFollow starts following the subagents of a parent transcript
// whose existing calls are calls.
func newKiroChildFollow(sessionID, parent string, calls []parser.KiroSubagentCall) *kiroChildFollow {
	f := &kiroChildFollow{
		sessionID: sessionID, parent: parent,
		finished: map[string]bool{}, tails: map[string]*kiroChildTail{},
	}
	for _, call := range calls {
		if call.Done {
			f.finished[call.CallID] = true
		}
	}
	return f
}

// read returns the rows the followed subagents have written since the last
// read.
func (f *kiroChildFollow) read(ctx context.Context, s *KiroSource, calls []parser.KiroSubagentCall, now time.Time) []protocol.Message {
	var active []parser.KiroSubagentCall
	running, finishing := false, false
	for _, call := range calls {
		if f.finished[call.CallID] {
			continue
		}
		active = append(active, call)
		running = running || !call.Done
		finishing = finishing || call.Done
	}
	if len(active) == 0 {
		return nil
	}
	if finishing || (running && now.Sub(f.lastScan) >= kiroChildScanInterval) {
		f.lastScan = now
		for _, binding := range s.bindSubagents(f.parent, active, now) {
			if _, ok := f.tails[binding.transcript]; ok || len(f.tails) >= maxKiroChildTails {
				continue
			}
			if info, err := os.Stat(binding.transcript); err == nil && info.Size() > maxKiroChildTranscript {
				continue
			}
			f.tails[binding.transcript] = &kiroChildTail{
				tail: jsonl.NewTail(binding.transcript), side: newKiroSidechain(f.sessionID, binding),
				callID: binding.callID,
			}
		}
	}

	var out []protocol.Message
	for path, child := range f.tails {
		if ctx.Err() != nil {
			break
		}
		lines, err := child.tail.Read()
		if err != nil && !os.IsNotExist(err) {
			delete(f.tails, path)
			continue
		}
		for _, line := range lines {
			out = append(out, child.side.parse(line.Text, line.Offset)...)
		}
	}
	// A finished call's subagents have finished too: what was just read was
	// the last of them.
	for _, call := range active {
		if !call.Done {
			continue
		}
		f.finished[call.CallID] = true
		for path, child := range f.tails {
			if child.callID == call.CallID {
				delete(f.tails, path)
			}
		}
	}
	return out
}
