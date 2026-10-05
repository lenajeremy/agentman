package source

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/tmux"
)

const cursorCLIProcessTable = `  1     0 /sbin/launchd
777   600 /Users/u/.local/bin/agent
778   777 /Users/u/.local/share/cursor-agent/versions/2026.10.01-e373342/node
779   600 /bin/zsh
`

// livenessFixture is the store fixture with a process table holding one
// Cursor agent (777, plus its node child) and an lsof that reports the store
// open by 777 while open is true.
func livenessFixture(t *testing.T) (*CursorCLISource, string, *bool, *[]int) {
	t.Helper()
	_, source, store := cursorCLIFixture(t)
	open := true
	var asked []int
	source.processes = func(context.Context) (*tmux.ProcessTree, error) {
		return tmux.ProcessTreeFromTable(cursorCLIProcessTable), nil
	}
	source.openStores = func(_ context.Context, pids []int) (map[int]string, bool) {
		asked = append([]int(nil), pids...)
		if open && slices.Contains(pids, 777) {
			return map[int]string{777: store}, true
		}
		return map[int]string{}, true
	}
	return source, store, &open, &asked
}

func TestCursorCLIChatIsLiveOnlyWhileAProcessHoldsIt(t *testing.T) {
	source, _, open, asked := livenessFixture(t)
	sessions, err := source.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].AgentPID != 777 || sessions[0].Inject != protocol.InjectNone {
		t.Fatalf("held chat = %+v, want live with the holding process", sessions)
	}
	if slices.Contains(*asked, 779) {
		t.Errorf("lsof was asked about a shell: %v", *asked)
	}

	// Closed a minute ago, updated a minute ago: no longer running, so it is
	// history rather than an idle session nobody can type into or reopen.
	*open = false
	sessions, err = source.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 0 {
		t.Fatalf("a chat no process holds is still live: %+v", sessions)
	}
	past, err := source.Past(context.Background(), sessions0Cwd(t, source), 0)
	if err != nil || len(past) != 1 || past[0].State != protocol.StateEnded {
		t.Fatalf("closed chat missing from history: %+v, %v", past, err)
	}
}

func sessions0Cwd(t *testing.T, source *CursorCLISource) string {
	t.Helper()
	chats := source.everyCursorCLIChat()
	if len(chats) == 0 {
		t.Fatal("fixture has no chat")
	}
	return chats[0].meta.Cwd
}

// A failed scan right after a good one keeps the last answer, so sessions do
// not vanish and come back because lsof was slow once.
func TestCursorCLILivenessSurvivesOneFailedScan(t *testing.T) {
	source, _, _, _ := livenessFixture(t)
	if sessions, _ := source.Discover(context.Background()); len(sessions) != 1 {
		t.Fatalf("baseline = %+v", sessions)
	}
	source.processes = func(context.Context) (*tmux.ProcessTree, error) { return nil, errors.New("ps timed out") }
	sessions, err := source.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].AgentPID != 777 {
		t.Fatalf("one failed scan dropped the live chat: %+v", sessions)
	}
	source.live.mu.Lock()
	source.live.at = time.Now().Add(-2 * cursorCLILiveGrace)
	source.live.mu.Unlock()
	// Past the grace, recency is all there is; the fixture chat is recent.
	if sessions, _ := source.Discover(context.Background()); len(sessions) != 1 {
		t.Fatalf("fallback to recency = %+v", sessions)
	}
}

func TestCursorCLIAgentProcessesAreRecognised(t *testing.T) {
	for command, want := range map[string]bool{
		"/Users/u/.local/bin/agent":                                           true,
		"/Users/u/.local/bin/cursor-agent":                                    true,
		"/Users/u/.local/share/cursor-agent/versions/2026.10.01-e373342/node": true,
		"/opt/homebrew/bin/node":                                              false,
		"/bin/zsh":                                                            false,
		"/usr/libexec/agentstored":                                            false,
		"/Applications/Cursor.app/x":                                          false,
	} {
		if got := cursorCLIAgentProcess(command); got != want {
			t.Errorf("%s: %v, want %v", command, got, want)
		}
	}
}

func writeCursorCLITranscript(t *testing.T, source *CursorCLISource, chatID string, lines ...string) string {
	t.Helper()
	dir := filepath.Join(source.home, ".cursor", "projects", "Users-u-project", "agent-transcripts", chatID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, chatID+".jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// A chat with no pane gets its state from the transcript Cursor writes for
// it, and a turn that failed says why at the end of the feed.
func TestCursorCLITranscriptGivesStateAndTheFailure(t *testing.T) {
	source, _, _, _ := livenessFixture(t)
	user := `{"role":"user","message":{"content":[{"type":"text","text":"<user_query>\nhi\n</user_query>"}]}}`
	writeCursorCLITranscript(t, source, "chat-123", user)
	sessions, err := source.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].State != protocol.StateBusy {
		t.Fatalf("open turn reads %+v, want busy", sessions)
	}

	writeCursorCLITranscript(t, source, "chat-123", user,
		`{"role":"assistant","message":{"content":[{"type":"text","text":"Working."}]}}`,
		`{"type":"turn_ended","status":"error","error":"You've hit your usage limit Get Cursor Pro for more Agent usage."}`)
	sessions, err = source.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if sessions[0].State != protocol.StateIdle {
		t.Fatalf("ended turn reads %s, want idle", sessions[0].State)
	}
	page, err := source.Page(context.Background(), sessions[0].ID, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	last := page.Messages[len(page.Messages)-1]
	if last.Role != protocol.RoleSystem || !strings.HasPrefix(last.Text, "Cursor stopped: You've hit your usage limit") {
		t.Fatalf("last message = %+v, want the failure", last)
	}
	again, _ := source.Page(context.Background(), sessions[0].ID, "", 10)
	if again.Messages[len(again.Messages)-1].ID != last.ID {
		t.Fatal("the failure notice changed id between reads")
	}

	writeCursorCLITranscript(t, source, "chat-123", user,
		`{"type":"turn_ended","status":"success"}`)
	page, _ = source.Page(context.Background(), sessions[0].ID, "", 10)
	if page.Messages[len(page.Messages)-1].Role == protocol.RoleSystem {
		t.Fatal("a successful turn reported a failure")
	}
}

// A chat in an ordinary terminal can take a message once Agentman's stop
// hook is installed: the hook hands it to Cursor when the turn ends, keyed
// by the chat id the hook reports.
func TestCursorCLITerminalChatTakesMessagesThroughTheStopHook(t *testing.T) {
	source, _, _, _ := livenessFixture(t)
	queue := NewPendingQueue()
	source.SetPending(queue)
	sessions, err := source.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if sessions[0].Inject != protocol.InjectNone {
		t.Fatalf("without the hook the chat claims %s", sessions[0].Inject)
	}
	if _, err := source.Inject(context.Background(), sessions[0].ID, "hello"); err == nil {
		t.Fatal("a message was queued for a hook that is not installed")
	}

	hooks := filepath.Join(source.home, ".cursor", "hooks.json")
	if err := os.MkdirAll(filepath.Dir(hooks), 0o700); err != nil {
		t.Fatal(err)
	}
	config := `{"version":1,"hooks":{"stop":[{"command":"./mine.sh"},{"command":"/usr/local/bin/am hook cursor-cli Stop","timeout":5}]}}`
	if err := os.WriteFile(hooks, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	sessions, err = source.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if sessions[0].Inject != protocol.InjectHook {
		t.Fatalf("inject = %s, want hook", sessions[0].Inject)
	}
	mode, err := source.Inject(context.Background(), sessions[0].ID, "run the tests next")
	if err != nil || mode != protocol.InjectHook {
		t.Fatalf("inject = %s, %v", mode, err)
	}
	if got := queue.Take("cursor-cli:chat-123"); len(got) != 1 || got[0] != "run the tests next" {
		t.Fatalf("queued under the hook's key: %v", got)
	}
}
