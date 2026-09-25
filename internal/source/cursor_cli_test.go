package source

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/tmux"
)

func cursorCLIFixture(t *testing.T) (string, *CursorCLISource, string) {
	t.Helper()
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 is not installed")
	}
	home := t.TempDir()
	cwd := filepath.Join(home, "project")
	if err := os.Mkdir(cwd, 0700); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".cursor", "chats", "workspace-hash", "chat-123")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	created := time.Now().Add(-time.Minute).UnixMilli()
	meta := cursorCLIChat{
		CreatedAtMs: created, UpdatedAtMs: time.Now().UnixMilli(),
		HasConversation: true, Title: "CLI conversation", Cwd: cwd,
	}
	data, _ := json.Marshal(meta)
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	store := filepath.Join(dir, "store.db")
	rows := []string{
		`{"role":"system","content":"private system prompt"}`,
		`{"role":"user","content":"<user_info>private workspace context</user_info>"}`,
		`{"role":"user","content":[{"type":"text","text":"<timestamp>today</timestamp>\\n<user_query>First request</user_query>"}]}`,
		`{"role":"assistant","content":[{"type":"reasoning","text":"private thought"},{"type":"text","text":"First reply"}]}`,
		`{"role":"user","content":"Second request"}`,
	}
	var sql strings.Builder
	sql.WriteString("CREATE TABLE blobs (id TEXT PRIMARY KEY, data BLOB);")
	for i, row := range rows {
		fmt.Fprintf(&sql, "INSERT INTO blobs(id,data) VALUES ('blob-%d','%s');",
			i, strings.ReplaceAll(row, "'", "''"))
	}
	cmd := exec.Command("sqlite3", store, sql.String())
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture db: %v: %s", err, output)
	}
	s, err := NewCursorCLISource(home)
	if err != nil {
		t.Fatal(err)
	}
	s.listPanes = func(context.Context) ([]tmux.Session, error) { return nil, nil }
	return cwd, s, store
}

func TestCursorCLIChatHistoryPagesAndExcludesSystemAndReasoning(t *testing.T) {
	_, source, _ := cursorCLIFixture(t)
	ctx := context.Background()
	sessions, err := source.Discover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want 1", len(sessions))
	}
	got := sessions[0]
	if got.Kind != protocol.KindCursorCLI || got.Inject != protocol.InjectNone {
		t.Fatalf("unexpected session: %+v", got)
	}
	page, err := source.Page(ctx, got.ID, "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if !page.HasMore || len(page.Messages) != 2 || page.Messages[0].Text != "First reply" ||
		page.Messages[1].Text != "Second request" {
		t.Fatalf("unexpected latest page: %+v", page)
	}
	older, err := source.Page(ctx, got.ID, page.NextCursor, 2)
	if err != nil {
		t.Fatal(err)
	}
	if older.HasMore || len(older.Messages) != 1 || older.Messages[0].Text != "First request" {
		t.Fatalf("unexpected older page: %+v", older)
	}
}

func TestCursorCLIManagedPaneKeepsStableSessionIDWhenChatAppears(t *testing.T) {
	cwd, source, store := cursorCLIFixture(t)
	ctx := context.Background()
	pane := tmux.Session{
		Name: "agentman-cursor-abc", PanePID: 1234, Cwd: cwd,
		Command: "agent", Created: time.Now().Add(-2 * time.Minute),
	}
	source.listPanes = func(context.Context) ([]tmux.Session, error) { return []tmux.Session{pane}, nil }
	source.capturePane = func(context.Context, string) (string, error) {
		return "→ Add a follow-up     ctrl+c to stop\n", nil
	}
	metaPath := filepath.Join(filepath.Dir(store), "meta.json")
	meta, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(metaPath); err != nil {
		t.Fatal(err)
	}
	before, err := source.Discover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 1 || before[0].ID != "cursor-cli:pane:"+pane.Name ||
		before[0].Inject != protocol.InjectTmux {
		t.Fatalf("unexpected pane before first chat: %+v", before)
	}
	if err := os.WriteFile(metaPath, meta, 0600); err != nil {
		t.Fatal(err)
	}
	sessions, err := source.Discover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].ID != "cursor-cli:pane:"+pane.Name ||
		sessions[0].Inject != protocol.InjectTmux || sessions[0].State != protocol.StateBusy {
		t.Fatalf("unexpected managed session: %+v", sessions)
	}
	if store == "" {
		t.Fatal("fixture store missing")
	}
	if sessions[0].AgentPID != pane.PanePID {
		t.Fatalf("agent pid = %d", sessions[0].AgentPID)
	}
	_, err = source.Inject(ctx, "cursor-cli:chat:chat-123", "wrong session")
	if err == nil {
		t.Fatal("chat ID must not route to a managed pane")
	}
}

func TestCursorCLIAmbiguousPanesNeverClaimTheSameChat(t *testing.T) {
	cwd, source, _ := cursorCLIFixture(t)
	start := time.Now().Add(-2 * time.Minute)
	panes := []tmux.Session{
		{Name: "agentman-cursor-a", Command: "agent", Cwd: cwd, Created: start},
		{Name: "agentman-cursor-b", Command: "agent", Cwd: cwd, Created: start},
	}
	source.listPanes = func(context.Context) ([]tmux.Session, error) { return panes, nil }
	source.capturePane = func(context.Context, string) (string, error) { return "", nil }
	sessions, err := source.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 3 {
		t.Fatalf("got %d sessions, want 2 panes and 1 read-only chat", len(sessions))
	}
	for _, session := range sessions {
		if strings.HasPrefix(session.ID, "cursor-cli:chat:") && session.Inject != protocol.InjectNone {
			t.Fatalf("ambiguous chat was made writable: %+v", session)
		}
	}
}

func TestCursorCLIExactOpenStoreMatchesPanesSharingDirectory(t *testing.T) {
	cwd, source, firstStore := cursorCLIFixture(t)
	firstDir := filepath.Dir(firstStore)
	secondDir := filepath.Join(filepath.Dir(firstDir), "chat-456")
	if err := os.MkdirAll(secondDir, 0700); err != nil {
		t.Fatal(err)
	}
	meta, err := os.ReadFile(filepath.Join(firstDir, "meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secondDir, "meta.json"), meta, 0600); err != nil {
		t.Fatal(err)
	}
	storeData, err := os.ReadFile(firstStore)
	if err != nil {
		t.Fatal(err)
	}
	secondStore := filepath.Join(secondDir, "store.db")
	if err := os.WriteFile(secondStore, storeData, 0600); err != nil {
		t.Fatal(err)
	}
	start := time.Now().Add(-2 * time.Minute)
	panes := []tmux.Session{
		{Name: "agentman-cursor-a", Command: "node", PanePID: 123, Cwd: cwd, Created: start},
		{Name: "agentman-cursor-b", Command: "node", PanePID: 456, Cwd: cwd, Created: start},
	}
	source.listPanes = func(context.Context) ([]tmux.Session, error) { return panes, nil }
	bindings := map[int]string{123: firstStore, 456: secondStore}
	source.openStores = func(context.Context, []int) map[int]string { return bindings }
	source.capturePane = func(context.Context, string) (string, error) { return "", nil }
	sessions, err := source.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("expected two matched sessions, got %+v", sessions)
	}
	want := map[string]string{"chat-123": "agentman-cursor-a", "chat-456": "agentman-cursor-b"}
	for _, session := range sessions {
		if session.ID != "cursor-cli:pane:"+want[session.NativeID] || session.Inject != protocol.InjectTmux {
			t.Fatalf("chat matched to wrong pane: %+v", session)
		}
	}
	// Switching chats in the same terminal must follow the open database,
	// not a stale directory/time association from the previous sweep.
	bindings = map[int]string{123: secondStore, 456: firstStore}
	panes[0].Cwd = filepath.Join(cwd, "other")
	sessions, err = source.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want = map[string]string{"chat-123": "agentman-cursor-b", "chat-456": "agentman-cursor-a"}
	for _, session := range sessions {
		if session.ID != "cursor-cli:pane:"+want[session.NativeID] {
			t.Fatalf("chat switch was not reflected: %+v", sessions)
		}
	}
}

func TestCursorCLIOpenStoreParserRejectsAmbiguousProcess(t *testing.T) {
	home := t.TempDir()
	a := filepath.Join(home, ".cursor", "chats", "workspace", "a", "store.db")
	b := filepath.Join(home, ".cursor", "chats", "workspace", "b", "store.db")
	output := []byte("p123\nn" + a + "\np456\nn" + b + "\np789\nn" + a + "\nn" + b + "\n")
	got := parseCursorCLIOpenStores(home, output)
	if got[123] != a || got[456] != b || got[789] != "ambiguous" {
		t.Fatalf("incorrect open-store mapping: %+v", got)
	}
}

func TestCursorCLIHistoryIncludesToolCallsAndResults(t *testing.T) {
	_, source, store := cursorCLIFixture(t)
	sql := `INSERT INTO blobs(id,data) VALUES
('call', '{"role":"assistant","content":[{"type":"reasoning","text":"hidden"},{"type":"text","text":"Checking."},{"type":"tool-call","toolCallId":"tc1","toolName":"Shell","args":{"command":"pwd"}}]}'),
('result', '{"role":"tool","content":[{"type":"tool-result","toolCallId":"tc1","toolName":"Shell","result":"/work/project"}]}');`
	if output, err := exec.Command("sqlite3", store, sql).CombinedOutput(); err != nil {
		t.Fatalf("insert tool rows: %v: %s", err, output)
	}
	sessions, err := source.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	page, err := source.Page(context.Background(), sessions[0].ID, "", 20)
	if err != nil {
		t.Fatal(err)
	}
	var calls, results int
	for _, message := range page.Messages {
		if strings.Contains(message.Text, "hidden") {
			t.Fatal("reasoning leaked into history")
		}
		if message.Tool == nil {
			continue
		}
		if message.Tool.Summary == "pwd" {
			calls++
		}
		if message.Text == "/work/project" {
			results++
		}
	}
	if calls != 1 || results != 1 {
		t.Fatalf("tool activity missing: %+v", page.Messages)
	}
}

func TestCursorCLIReportsModelWithoutExposingReasoning(t *testing.T) {
	_, source, store := cursorCLIFixture(t)
	sql := `INSERT INTO blobs(id,data) VALUES
('model-reply', '{"role":"assistant","content":[{"type":"reasoning","text":"hidden","providerOptions":{"cursor":{"modelName":"example-model"}}},{"type":"text","text":"Done."}]}');`
	if output, err := exec.Command("sqlite3", store, sql).CombinedOutput(); err != nil {
		t.Fatalf("insert model row: %v: %s", err, output)
	}
	sessions, err := source.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Model != "example-model" {
		t.Fatalf("model not discovered: %+v", sessions)
	}
	page, err := source.Page(context.Background(), sessions[0].ID, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, message := range page.Messages {
		if strings.Contains(message.Text, "hidden") {
			t.Fatal("reasoning leaked into history")
		}
	}
}

func TestCursorCLIShellApprovalIsAnswerableAndBlocksNormalSend(t *testing.T) {
	pane := `  $  sleep 20 in .

 Run this command?
 Not in allowlist: sleep
  → Run (once) (y)
    Add Shell(sleep) to allowlist? (tab)
    Run Everything (shift+tab)
    Skip & tell the agent what to do instead (esc or n)
`
	q := cursorCLIQuestionFromPane(pane)
	if q == nil || q.Detail != "sleep 20 in ." || len(q.Options) != 2 ||
		q.Options[0].Key != "y" || q.Options[1].Key != "n" {
		t.Fatalf("approval menu not parsed: %+v", q)
	}
	s, err := NewCursorCLISource(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s.capturePane = func(context.Context, string) (string, error) { return pane, nil }
	id := "cursor-cli:pane:agentman-cursor-test"
	s.sessions[id] = cursorCLISession{pane: "agentman-cursor-test", meta: protocol.Session{Question: q}}
	if _, err := s.Inject(context.Background(), id, "message"); err == nil {
		t.Fatal("send into approval menu was allowed")
	}
	if current, err := s.CurrentQuestion(context.Background(), id); err != nil ||
		current == nil || current.ID != q.ID {
		t.Fatalf("current approval menu: %+v, %v", current, err)
	}
	if cursorCLIQuestionFromPane("Run this command?\n  → Run (once) (y)") != nil {
		t.Fatal("partial approval menu was accepted")
	}
	if cursorCLIQuestionFromPane(strings.Replace(pane, "  $  sleep 20 in .", "", 1)) != nil {
		t.Fatal("approval without a visible command was accepted")
	}
}

func TestCursorCLIWorkspaceTrustPrompt(t *testing.T) {
	pane := `  │  ▶ [a] Trust this workspace  │
  │    [q] Quit                  │
  │  Use arrow keys to navigate, Enter to select, or press the key shown  │
  │                                                                        │
  ╰────────────────────────────────────────────────────────────────────────╯`
	q := cursorCLIQuestionFromPane(pane)
	if q == nil || q.Prompt != "Trust this workspace?" || len(q.Options) != 2 ||
		q.Options[0].Key != "a" || q.Options[1].Key != "q" {
		t.Fatalf("trust prompt not detected: %+v", q)
	}
	if cursorCLIQuestionFromPane(pane+"\nCursor Agent\n→ Ask anything") != nil {
		t.Fatal("stale trust prompt was still answerable")
	}
}
