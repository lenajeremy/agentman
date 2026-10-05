package source

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/tmux"
)

func TestIsClaudeInfraReadsOnlyTheSubcommand(t *testing.T) {
	cases := []struct {
		args string
		want bool
	}{
		{"/Users/me/.local/bin/claude bg-spare", true},
		{"claude bg-pty-host --fd 3", true},
		{"/opt/homebrew/bin/claude daemon run", true},
		// An npm install runs through node, with the script's path as the
		// first argument.
		{"node /usr/local/bin/claude bg-spare", true},
		// A path with a space in it splits, and the executable is still found.
		{"/Users/me/Application Support/claude bg-spare", true},

		{"/Users/me/.local/bin/claude", false},
		{"claude --resume 2f290b2d-57e4-4d58-a65e-8f5710d9a8fb", false},
		{"claude daemon status", false},
		{"claude daemon", false},
		// The case that rules out matching anywhere: a prompt that mentions a
		// worker is a conversation, not a worker.
		{"claude -p why is bg-spare using so much memory", false},
		{"claude --session-id a0838c8c -- explain claude bg-spare", false},
		{"/usr/bin/vim bg-spare", false},
		{"", false},
	}
	for _, c := range cases {
		if got := isClaudeInfra(c.args); got != c.want {
			t.Errorf("isClaudeInfra(%q) = %v, want %v", c.args, got, c.want)
		}
	}
}

func claudeCandidateFor(sessionID string, pid int, updated int64, status, pane string) claudeCandidate {
	return claudeCandidate{
		file: claudeSessionFile{
			PID: pid, SessionID: sessionID, StartedAt: 1_000, UpdatedAt: updated, Status: status,
		},
		tmuxName: pane,
	}
}

// Resumed in a second terminal while the first is still open: two processes,
// one conversation. The row leads to the one a message can be typed into, and
// still says it is working when only the other one is.
func TestMergeClaudeCandidatesKeepsThePaneAndTheBusiestState(t *testing.T) {
	merged := mergeClaudeCandidates([]claudeCandidate{
		claudeCandidateFor("a", 64549, 100, "idle", "agentman-claude-a"),
		claudeCandidateFor("a", 24392, 900, "busy", ""),
	})
	if len(merged) != 1 {
		t.Fatalf("got %d rows for one conversation, want 1", len(merged))
	}
	got := merged[0]
	if got.file.PID != 64549 || got.tmuxName != "agentman-claude-a" {
		t.Errorf("kept pid %d in %q; the pane-backed process should speak for the row",
			got.file.PID, got.tmuxName)
	}
	if got.file.Status != "busy" {
		t.Errorf("status = %q; one of its processes is working", got.file.Status)
	}
	if got.file.UpdatedAt != 900 {
		t.Errorf("updatedAt = %d; the row should report the latest activity, 900", got.file.UpdatedAt)
	}
}

func TestMergeClaudeCandidatesPrefersTheMostRecentWithoutAPane(t *testing.T) {
	merged := mergeClaudeCandidates([]claudeCandidate{
		claudeCandidateFor("a", 10, 100, "idle", ""),
		claudeCandidateFor("a", 20, 300, "idle", ""),
		claudeCandidateFor("a", 30, 200, "idle", ""),
	})
	if len(merged) != 1 || merged[0].file.PID != 20 {
		t.Fatalf("merged = %+v, want pid 20, the most recently active", merged)
	}
}

// Equal on everything that matters, the choice must still not flip between
// sweeps, or the row's pid — and what it reports as running under it — would.
func TestMergeClaudeCandidatesBreaksATieTheSameWayInEitherOrder(t *testing.T) {
	x := claudeCandidateFor("a", 10, 100, "idle", "")
	y := claudeCandidateFor("a", 20, 100, "idle", "")
	for _, order := range [][]claudeCandidate{{x, y}, {y, x}} {
		if merged := mergeClaudeCandidates(order); merged[0].file.PID != 20 {
			t.Errorf("order %d,%d kept pid %d, want 20",
				order[0].file.PID, order[1].file.PID, merged[0].file.PID)
		}
	}
}

func TestMergeClaudeCandidatesLeavesSeparateConversationsAlone(t *testing.T) {
	merged := mergeClaudeCandidates([]claudeCandidate{
		claudeCandidateFor("a", 10, 100, "idle", ""),
		claudeCandidateFor("b", 20, 100, "busy", ""),
		claudeCandidateFor("c", 30, 100, "idle", ""),
	})
	var ids []string
	for _, c := range merged {
		ids = append(ids, c.file.SessionID)
	}
	if !slices.Equal(ids, []string{"a", "b", "c"}) {
		t.Fatalf("sessions = %v, want a b c in order", ids)
	}
	if merged[0].file.Status != "idle" || merged[1].file.Status != "busy" {
		t.Error("one conversation's state leaked into another's")
	}
}

// Discovery runs every second; asking ps about the same handful of pids every
// time would put back the per-sweep process the snapshot was built to remove.
func TestWithoutInfraAsksAboutEachProcessOnce(t *testing.T) {
	source := &ClaudeSource{infra: map[claudeProcessKey]bool{}}
	var asked [][]int
	commandLines := map[int]string{
		10: "/Users/me/.local/bin/claude",
		20: "/Users/me/.local/bin/claude bg-spare",
		30: "/Users/me/.local/bin/claude daemon run",
	}
	source.processArgs = func(_ context.Context, pids []int) map[int]string {
		asked = append(asked, slices.Clone(pids))
		out := map[int]string{}
		for _, pid := range pids {
			if line, ok := commandLines[pid]; ok {
				out[pid] = line
			}
		}
		return out
	}
	candidates := []claudeCandidate{
		claudeCandidateFor("a", 10, 1, "idle", ""),
		claudeCandidateFor("b", 20, 1, "idle", ""),
		claudeCandidateFor("c", 30, 1, "idle", ""),
		// Gone between the registry read and ps: not reported.
		claudeCandidateFor("d", 40, 1, "idle", ""),
	}

	pids := func(cs []claudeCandidate) []int {
		var out []int
		for _, c := range cs {
			out = append(out, c.file.PID)
		}
		return out
	}

	kept := source.withoutInfra(context.Background(), candidates)
	if !slices.Equal(pids(kept), []int{10, 40}) {
		t.Fatalf("kept %v; want the conversation, and the pid ps could not report", pids(kept))
	}
	if len(asked) != 1 || !slices.Equal(asked[0], []int{10, 20, 30, 40}) {
		t.Fatalf("asked %v; want every pid in one call", asked)
	}

	asked = nil
	kept = source.withoutInfra(context.Background(), candidates)
	if !slices.Equal(pids(kept), []int{10, 40}) {
		t.Fatalf("second sweep kept %v", pids(kept))
	}
	if len(asked) != 1 || !slices.Equal(asked[0], []int{40}) {
		t.Fatalf("second sweep asked %v; only the unanswered pid should be asked again", asked)
	}

	// Once 40 is gone for good and nothing new appears, a sweep spawns nothing.
	asked = nil
	source.withoutInfra(context.Background(), candidates[:3])
	if len(asked) != 0 {
		t.Fatalf("asked %v about processes already judged", asked)
	}
}

// A pid is reused. The conversation that gets a worker's old number must not
// inherit its verdict, and a verdict about a process that has gone is dropped.
func TestWithoutInfraDoesNotCarryAVerdictToAReusedPID(t *testing.T) {
	source := &ClaudeSource{infra: map[claudeProcessKey]bool{}}
	lines := map[int]string{20: "claude bg-spare"}
	source.processArgs = func(_ context.Context, pids []int) map[int]string {
		out := map[int]string{}
		for _, pid := range pids {
			out[pid] = lines[pid]
		}
		return out
	}

	worker := claudeCandidateFor("w", 20, 1, "idle", "")
	if kept := source.withoutInfra(context.Background(), []claudeCandidate{worker}); len(kept) != 0 {
		t.Fatal("the worker was shown")
	}

	lines[20] = "claude --resume r"
	conversation := claudeCandidateFor("r", 20, 1, "idle", "")
	conversation.file.StartedAt = worker.file.StartedAt + 60_000
	kept := source.withoutInfra(context.Background(), []claudeCandidate{conversation})
	if len(kept) != 1 {
		t.Fatal("a conversation on a reused pid was hidden as the worker it replaced")
	}
	if len(source.infra) != 1 {
		t.Errorf("cache holds %d verdicts, want 1: the dead worker's should be gone", len(source.infra))
	}
}

// End to end through Discover, against the registry as Claude Code writes it.
func TestClaudeDiscoverShowsEachConversationOnceAndNoWorkers(t *testing.T) {
	home := t.TempDir()
	sessionsDir := filepath.Join(home, ".claude", "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const conversation = "a5a4ad08-4099-4fc1-bcb7-21715ae61205"
	const spare = "0e0c5a1e-5f0b-4f6f-9a53-0c1d2e3f4a5b"
	// Three processes that are really alive, so the liveness check passes.
	first, second, worker := os.Getpid(), os.Getppid(), 1
	now := time.Now().UnixMilli()
	write := func(name string, pid int, sessionID, status string, updated int64) {
		raw, err := json.Marshal(map[string]any{
			"pid": pid, "sessionId": sessionID, "cwd": "/Users/me/agentman",
			"startedAt": now - 60_000, "updatedAt": updated, "status": status,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(sessionsDir, name), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("first.json", first, conversation, "idle", now-5_000)
	write("second.json", second, conversation, "busy", now)
	write("spare.json", worker, spare, "idle", now)

	source, err := NewClaudeSource(home)
	if err != nil {
		t.Fatal(err)
	}
	source.listPanes = func(context.Context) ([]tmux.Session, error) { return nil, nil }
	source.snapshotProcesses = func(context.Context) (*tmux.ProcessTree, error) { return nil, nil }
	source.processArgs = func(_ context.Context, pids []int) map[int]string {
		out := map[int]string{}
		for _, pid := range pids {
			out[pid] = "/Users/me/.local/bin/claude"
			if pid == worker {
				out[pid] = "/Users/me/.local/bin/claude bg-spare"
			}
		}
		return out
	}

	sessions, err := source.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("discovered %d sessions, want 1: %+v", len(sessions), sessions)
	}
	got := sessions[0]
	if got.NativeID != conversation {
		t.Fatalf("discovered %q; the worker's session should not be listed", got.NativeID)
	}
	if got.State != protocol.StateBusy || got.AgentPID != second {
		t.Errorf("state %s pid %d; want busy on pid %d, the most recent process",
			got.State, got.AgentPID, second)
	}
}

// Claude Code's registry says what kind of process each entry is. Its daemon
// processes are infrastructure whatever their command line says, and are
// dropped without asking ps. Every other entry still goes by the command line.
func TestClaudeRegistryKindDropsDaemonsWithoutAskingPs(t *testing.T) {
	source, err := NewClaudeSource(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var asked []int
	source.processArgs = func(_ context.Context, pids []int) map[int]string {
		asked = append(asked, pids...)
		out := map[int]string{}
		for _, pid := range pids {
			out[pid] = "/Users/me/.local/bin/claude"
		}
		return out
	}
	candidates := []claudeCandidate{
		claudeCandidateFor("daemon", 1, 1, "idle", ""),
		claudeCandidateFor("worker", 2, 1, "idle", ""),
		claudeCandidateFor("chat", 3, 1, "idle", ""),
		claudeCandidateFor("old", 4, 1, "idle", ""),
	}
	candidates[0].file.Kind = "daemon"
	candidates[1].file.Kind = "daemon-worker"
	candidates[2].file.Kind = "interactive"

	kept := source.withoutInfra(context.Background(), candidates)
	if len(kept) != 2 || kept[0].file.SessionID != "chat" || kept[1].file.SessionID != "old" {
		t.Fatalf("kept %+v, want the conversation and the kindless entry", kept)
	}
	for _, pid := range asked {
		if pid == 1 || pid == 2 {
			t.Errorf("asked ps about pid %d, which the registry already called a daemon", pid)
		}
	}
}
