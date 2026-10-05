package source

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lenajeremy/agentman/internal/jsonl"
	"github.com/lenajeremy/agentman/internal/parser"
	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/tmux"
)

// Which chats are running, and what they are doing, without a pane.
//
// Discovery used to call any chat updated in the last twelve hours live.
// A chat run in a plain terminal and closed an hour ago therefore sat on the
// phone as an idle session that could not be typed into, could not be
// reopened (it was not ended), and whose real state nobody knew. A chat is
// now live exactly when a Cursor agent process has its store open — the same
// lsof evidence that binds managed panes, widened to every Cursor process —
// and the rest are history.
//
// Cursor also writes an IDE-style transcript for every CLI chat
// (~/.cursor/projects/<slug>/agent-transcripts/<id>/<id>.jsonl, the path its
// hooks report). Its records end each turn with turn_ended, so it gives the
// busy/idle state of a chat with no pane to read, and the error a turn ended
// with, which the chat store never records.

// cursorCLILiveGrace is how long the last successful scan stands in for one
// that failed. lsof can time out under load; dropping every chat it found a
// second ago, then bringing them back, would make sessions flicker.
const cursorCLILiveGrace = 30 * time.Second

// cursorCLIMaxAgentPIDs bounds one lsof call.
const cursorCLIMaxAgentPIDs = 64

type cursorCLILiveness struct {
	mu     sync.Mutex
	stores map[string]int
	at     time.Time
}

// cursorCLIAgentProcess reports whether a process is Cursor's agent: its
// launcher ("…/.local/bin/agent", "cursor-agent") or the node runtime inside
// a versioned install (".../cursor-agent/versions/<v>/node").
func cursorCLIAgentProcess(command string) bool {
	base := filepath.Base(command)
	return base == "agent" || base == "cursor-agent" || strings.Contains(command, "/cursor-agent/versions/")
}

func cursorCLIAgentPIDs(tree *tmux.ProcessTree) []int {
	var pids []int
	for _, pid := range tree.PIDs() {
		if cursorCLIAgentProcess(tree.Command(pid)) {
			pids = append(pids, pid)
			if len(pids) == cursorCLIMaxAgentPIDs {
				break
			}
		}
	}
	return pids
}

// observeStores reads which chat store each process has open: the managed
// panes' processes, for binding, and every Cursor agent process, for
// liveness. live maps an open store to the process holding it; known is
// false when there is no trustworthy answer, and the caller falls back to
// recency.
func (s *CursorCLISource) observeStores(ctx context.Context, panePIDs []int) (open map[int]string, live map[string]int, known bool) {
	pids := append([]int(nil), panePIDs...)
	scanned := false
	if s.processes != nil {
		if tree, err := s.processes(ctx); err == nil {
			pids = append(pids, cursorCLIAgentPIDs(tree)...)
			scanned = true
		}
	}
	open = map[int]string{}
	lsofOK := false
	if s.openStores != nil {
		open, lsofOK = s.openStores(ctx, pids)
		if open == nil {
			open = map[int]string{}
		}
	}
	live = map[string]int{}
	for pid, store := range open {
		if store != "ambiguous" {
			live[store] = pid
		}
	}
	s.live.mu.Lock()
	defer s.live.mu.Unlock()
	if scanned && lsofOK {
		s.live.stores, s.live.at = live, time.Now()
		return open, live, true
	}
	if s.live.stores != nil && time.Since(s.live.at) < cursorCLILiveGrace {
		for store, pid := range s.live.stores {
			if _, ok := live[store]; !ok {
				live[store] = pid
			}
		}
		return open, live, true
	}
	return open, live, false
}

// cursorCLITranscript finds the IDE-style transcript Cursor keeps for a chat.
// The project directory encodes the working directory lossily, so it is
// found by the chat id rather than computed, and remembered once found.
func (s *CursorCLISource) cursorCLITranscript(chatID string) string {
	if !cursorCLIValidChatID(chatID) {
		return ""
	}
	s.turnMu.Lock()
	path, ok := s.transcripts[chatID]
	s.turnMu.Unlock()
	if ok {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	matches, _ := filepath.Glob(filepath.Join(s.home, ".cursor", "projects", "*", "agent-transcripts", chatID, chatID+".jsonl"))
	if len(matches) != 1 {
		return ""
	}
	s.turnMu.Lock()
	if s.transcripts == nil {
		s.transcripts = map[string]string{}
	}
	s.transcripts[chatID] = matches[0]
	s.turnMu.Unlock()
	return matches[0]
}

// cursorCLIValidChatID keeps a chat id from becoming a glob or a path.
func cursorCLIValidChatID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, r := range id {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' && r != '_' {
			return false
		}
	}
	return true
}

// cursorCLITurn is the latest decisive record of a chat's transcript.
type cursorCLITurn struct {
	state protocol.State
	// failure is the error the last turn ended with, and offset where its
	// record sits; empty when the turn succeeded or is still running.
	failure string
	offset  int64
}

type cursorCLITurnEntry struct {
	size  int64
	mod   time.Time
	turn  cursorCLITurn
	valid bool
}

// cursorCLITurnState reads the latest turn boundary of a chat's transcript,
// once per change to the file.
func (s *CursorCLISource) cursorCLITurnState(ctx context.Context, chatID string) (cursorCLITurn, bool) {
	path := s.cursorCLITranscript(chatID)
	if path == "" {
		return cursorCLITurn{}, false
	}
	info, err := os.Stat(path)
	if err != nil {
		return cursorCLITurn{}, false
	}
	s.turnMu.Lock()
	cached, ok := s.turns[path]
	s.turnMu.Unlock()
	if ok && cached.size == info.Size() && cached.mod.Equal(info.ModTime()) {
		return cached.turn, cached.valid
	}
	turn, valid := readCursorCLITurn(ctx, path)
	s.turnMu.Lock()
	if s.turns == nil {
		s.turns = map[string]cursorCLITurnEntry{}
	}
	s.turns[path] = cursorCLITurnEntry{size: info.Size(), mod: info.ModTime(), turn: turn, valid: valid}
	s.turnMu.Unlock()
	return turn, valid
}

func readCursorCLITurn(ctx context.Context, path string) (cursorCLITurn, bool) {
	result, err := jsonl.CollectBackwardContext(ctx, path, jsonl.BackwardOptions{
		Want:         1,
		ChunkSize:    32 * 1024,
		MaxScanBytes: cursorActivityScanBytes,
		Map: func(line string, offset int64) []protocol.Message {
			state, ok := parser.CursorStateFromLine(line)
			if !ok {
				return nil
			}
			var record struct {
				Status string `json:"status"`
				Error  string `json:"error"`
			}
			_ = json.Unmarshal([]byte(line), &record)
			failure := ""
			if state == protocol.StateIdle && record.Status != "" && record.Status != "success" {
				failure = strings.TrimSpace(record.Error)
				if failure == "" {
					failure = record.Status
				}
			}
			// Smuggled through a throwaway message: the latest decisive line
			// is the only one wanted.
			return []protocol.Message{{ID: string(state), Text: failure, Ts: offset}}
		},
	})
	if err != nil || len(result.Messages) != 1 {
		return cursorCLITurn{}, false
	}
	found := result.Messages[0]
	return cursorCLITurn{state: protocol.State(found.ID), failure: found.Text, offset: found.Ts}, true
}

// cursorCLIFailureNotice turns a failed last turn into a system row for the
// end of the newest page. The transcript keeps only the latest turn's ending,
// so only the newest page can carry it; it is emitted once, live, where it
// belongs, and an older failure is not reconstructed later.
func cursorCLIFailureNotice(sessionID string, turn cursorCLITurn, after int64) protocol.Message {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d\x00%s", turn.offset, turn.failure)))
	return protocol.Message{
		ID: "cursor-cli:turn-error:" + hex.EncodeToString(sum[:8]), SessionID: sessionID,
		Role: protocol.RoleSystem, Ts: after + 1,
		Text: clipRunes("Cursor stopped: "+turn.failure, parser.PreviewChars),
	}
}

// cursorCLITranscriptVersion folds a chat's transcript into the follow
// loop's change check: a turn can end in an error with nothing new in the
// store.
func (s *CursorCLISource) cursorCLITranscriptVersion(chatID string) string {
	path := s.cursorCLITranscript(chatID)
	if path == "" {
		return ""
	}
	if info, err := os.Stat(path); err == nil {
		return fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano())
	}
	return ""
}

// SetPending gives the source the queue that hook delivery drains.
func (s *CursorCLISource) SetPending(queue *PendingQueue) { s.pending = queue }

// cursorCLIHookKey is the queue key Cursor's own stop hook is delivered
// under: the hook names the chat by its id, not by the phone's session id.
func cursorCLIHookKey(chatID string) string {
	return string(protocol.KindCursorCLI) + ":" + chatID
}

// cursorCLIHooksInstalled reports whether `am install-hooks` has put
// Agentman's stop hook in ~/.cursor/hooks.json. Only then can a chat running
// in an ordinary terminal be handed a message: its stop hook answers with
// the queued text and Cursor continues the chat with it. Without the hook a
// queued message would never arrive, so the session stays read-only.
func (s *CursorCLISource) cursorCLIHooksInstalled() bool {
	path := filepath.Join(s.home, ".cursor", "hooks.json")
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	s.turnMu.Lock()
	defer s.turnMu.Unlock()
	if s.hooksChecked.size == info.Size() && s.hooksChecked.mod.Equal(info.ModTime()) {
		return s.hooksChecked.installed
	}
	installed := false
	if data, err := os.ReadFile(path); err == nil && len(data) < 1<<20 {
		var config struct {
			Hooks map[string][]struct {
				Command string `json:"command"`
			} `json:"hooks"`
		}
		if json.Unmarshal(data, &config) == nil {
			for _, entry := range config.Hooks["stop"] {
				if strings.HasSuffix(strings.TrimSpace(entry.Command), " hook cursor-cli Stop") {
					installed = true
				}
			}
		}
	}
	s.hooksChecked = cursorCLIHooksEntry{size: info.Size(), mod: info.ModTime(), installed: installed}
	return installed
}

type cursorCLIHooksEntry struct {
	size      int64
	mod       time.Time
	installed bool
}

type cursorCLIModeEntry struct {
	version string
	mode    string
}

// cursorCLIModes maps the mode names a chat store records to the ones the
// CLI shows. Its ACP server maps "ask" to the same "search".
var cursorCLIModes = map[string]string{"default": "agent", "search": "ask"}

// cursorCLIStoreMode reads the mode a chat store records, once per change to
// the store. The meta row also holds the store's encryption key, so only the
// mode is ever taken from it.
func (s *CursorCLISource) cursorCLIStoreMode(ctx context.Context, store string) string {
	version := cursorCLIStoreVersion(store)
	s.turnMu.Lock()
	cached, ok := s.storeModes[store]
	s.turnMu.Unlock()
	if ok && cached.version == version {
		return cached.mode
	}
	mode := queryCursorCLIStoreMode(ctx, store)
	s.turnMu.Lock()
	if s.storeModes == nil {
		s.storeModes = map[string]cursorCLIModeEntry{}
	}
	s.storeModes[store] = cursorCLIModeEntry{version: version, mode: mode}
	s.turnMu.Unlock()
	return mode
}

func queryCursorCLIStoreMode(ctx context.Context, store string) string {
	bin, err := exec.LookPath("sqlite3")
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, cursorCLIDBTimeout)
	defer cancel()
	output, err := exec.CommandContext(ctx, bin, "-readonly", "-cmd", cursorCLITimeoutCommand,
		"file:"+store+"?mode=ro", "SELECT value FROM meta WHERE key='0'").Output()
	if err != nil || len(output) > 64*1024 {
		return ""
	}
	raw, err := hex.DecodeString(strings.TrimSpace(string(output)))
	if err != nil {
		return ""
	}
	var meta struct {
		Mode string `json:"mode"`
	}
	if json.Unmarshal(raw, &meta) != nil {
		return ""
	}
	mode := strings.ToLower(strings.TrimSpace(meta.Mode))
	if mapped, ok := cursorCLIModes[mode]; ok {
		mode = mapped
	}
	if len(mode) > 40 {
		return ""
	}
	return mode
}
