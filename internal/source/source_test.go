package source

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/tmux"
)

type obj = map[string]any

func writeJSONL(t *testing.T, path string, records []any) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, rec := range records {
		line, err := json.Marshal(rec)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

/* --------------------------------- Claude -------------------------------- */

// fakeClaudeHome builds a ~/.claude tree with one session registered against
// the current process, so the pid liveness check passes.
func fakeClaudeHome(t *testing.T, cwd, sessionID, status string) string {
	t.Helper()
	home := t.TempDir()

	reg := obj{
		"pid": os.Getpid(), "sessionId": sessionID, "cwd": cwd,
		"startedAt": time.Now().Add(-time.Hour).UnixMilli(),
		"name":      "test-session", "kind": "interactive", "status": status,
		"updatedAt": time.Now().UnixMilli(),
	}
	raw, err := json.Marshal(reg)
	if err != nil {
		t.Fatal(err)
	}
	sessionsDir := filepath.Join(home, ".claude", "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	regPath := filepath.Join(sessionsDir, fmt.Sprintf("%d.json", os.Getpid()))
	if err := os.WriteFile(regPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	slug := strings.NewReplacer("/", "-", ".", "-").Replace(cwd)
	transcript := filepath.Join(home, ".claude", "projects", slug, sessionID+".jsonl")
	writeJSONL(t, transcript, []any{
		obj{"type": "user", "uuid": "u1", "timestamp": "2026-08-10T10:00:00.000Z",
			"message": obj{"role": "user", "content": "hello"}},
		obj{"type": "assistant", "uuid": "a1", "timestamp": "2026-08-10T10:00:01.000Z",
			"message": obj{"role": "assistant", "content": []any{
				obj{"type": "text", "text": "hi back"}}}},
	})
	return home
}

func TestClaudeDiscoverReportsLiveSession(t *testing.T) {
	home := fakeClaudeHome(t, "/Users/me/work/proj", "sess-1", "busy")
	src, err := NewClaudeSource(home)
	if err != nil {
		t.Fatal(err)
	}

	sessions, err := src.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want 1", len(sessions))
	}
	s := sessions[0]
	if s.ID != "claude:sess-1" {
		t.Errorf("ID = %q, want the kind-prefixed composite", s.ID)
	}
	if s.State != protocol.StateBusy {
		t.Errorf("State = %q, want busy from the registry file", s.State)
	}
	if s.Cwd != "/Users/me/work/proj" || s.Name != "test-session" {
		t.Errorf("unexpected metadata: %+v", s)
	}
}

func TestClaudeIgnoresSessionsWhoseProcessIsGone(t *testing.T) {
	home := fakeClaudeHome(t, "/Users/me/work/proj", "sess-1", "idle")

	// Re-register the session against a pid that cannot be running. A registry
	// file outlives a crashed session, so this is the common real-world case.
	dead := obj{
		"pid": 0x7FFFFFF, "sessionId": "sess-dead", "cwd": "/Users/me/gone",
		"startedAt": time.Now().UnixMilli(), "name": "dead", "status": "idle",
	}
	raw, _ := json.Marshal(dead)
	if err := os.WriteFile(filepath.Join(home, ".claude", "sessions", "9999999.json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}

	src, _ := NewClaudeSource(home)
	sessions, err := src.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sessions {
		if s.NativeID == "sess-dead" {
			t.Fatal("a session whose process has exited must not be listed")
		}
	}
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want only the live one", len(sessions))
	}
}

func TestClaudeSessionIDCannotEscapeTranscriptDirectory(t *testing.T) {
	for _, id := range []string{"../secret", "a/b", `a\b`, "", strings.Repeat("a", 257)} {
		if validClaudeSessionID(id) {
			t.Errorf("unsafe Claude session id %q was accepted", id)
		}
	}
	for _, id := range []string{"550e8400-e29b-41d4-a716-446655440000", "session_123"} {
		if !validClaudeSessionID(id) {
			t.Errorf("normal Claude session id %q was rejected", id)
		}
	}
}

func TestClaudePageReadsTranscript(t *testing.T) {
	home := fakeClaudeHome(t, "/Users/me/work/proj", "sess-1", "idle")
	src, _ := NewClaudeSource(home)
	ctx := context.Background()
	if _, err := src.Discover(ctx); err != nil {
		t.Fatal(err)
	}

	page, err := src.Page(ctx, "claude:sess-1", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Messages) != 2 {
		t.Fatalf("got %d messages, want 2", len(page.Messages))
	}
	if page.Messages[0].Text != "hello" || page.Messages[1].Text != "hi back" {
		t.Errorf("unexpected messages: %+v", page.Messages)
	}
	if page.HasMore {
		t.Error("a two-message transcript has no further history")
	}
}

func TestClaudeTranscriptPathHandlesDotsInCwd(t *testing.T) {
	// /Users/me/.config/app resolves to -Users-me--config-app: both separators
	// and dots collapse to dashes. Getting this wrong shows an empty feed.
	const cwd = "/Users/me/.config/app"
	home := fakeClaudeHome(t, cwd, "sess-dot", "idle")
	src, _ := NewClaudeSource(home)
	ctx := context.Background()
	if _, err := src.Discover(ctx); err != nil {
		t.Fatal(err)
	}

	page, err := src.Page(ctx, "claude:sess-dot", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Messages) != 2 {
		t.Fatalf("transcript not found for a cwd containing a dot: got %d messages", len(page.Messages))
	}
}

func TestClaudeFindsTranscriptWhenSlugRuleFails(t *testing.T) {
	// The slug rule is undocumented and could change. Discovery must still
	// locate the transcript by session id rather than silently showing nothing.
	home := fakeClaudeHome(t, "/Users/me/work/proj", "sess-1", "idle")
	projects := filepath.Join(home, ".claude", "projects")
	if err := os.Rename(
		filepath.Join(projects, "-Users-me-work-proj"),
		filepath.Join(projects, "totally-unexpected-layout"),
	); err != nil {
		t.Fatal(err)
	}

	src, _ := NewClaudeSource(home)
	ctx := context.Background()
	if _, err := src.Discover(ctx); err != nil {
		t.Fatal(err)
	}
	page, err := src.Page(ctx, "claude:sess-1", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Messages) != 2 {
		t.Fatalf("fallback lookup failed: got %d messages", len(page.Messages))
	}
}

func TestClaudeDiscoverOnMachineWithoutClaude(t *testing.T) {
	src, _ := NewClaudeSource(t.TempDir())
	sessions, err := src.Discover(context.Background())
	if err != nil {
		t.Fatalf("a missing install must not be an error: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("got %d sessions, want none", len(sessions))
	}
}

/* --------------------------------- Codex --------------------------------- */

// alwaysRunning stands in for the pgrep probe so discovery logic is exercised
// regardless of what happens to be running on the test machine.
func alwaysRunning(context.Context) bool { return true }

// noPanes stands in for the tmux listing. tmux is a machine-wide server, so
// without this a test would discover whatever panes the developer happens to
// have open rather than only what its own fixture set up.
func noPanes(context.Context) ([]tmux.Session, error) { return nil, nil }

func fakeCodexHome(t *testing.T, sessionID, cwd string, age time.Duration) string {
	t.Helper()
	home := t.TempDir()
	when := time.Now().Add(-age)

	path := filepath.Join(home, ".codex", "sessions",
		when.Format("2006"), when.Format("01"), when.Format("02"),
		"rollout-"+when.Format("2006-01-02T15-04-05")+"-"+sessionID+".jsonl")

	writeJSONL(t, path, []any{
		obj{"timestamp": when.Format(time.RFC3339Nano), "type": "session_meta",
			"payload": obj{"session_id": sessionID, "cwd": cwd, "cli_version": "0.147.0",
				"timestamp": when.Format(time.RFC3339Nano)}},
		obj{"timestamp": when.Format(time.RFC3339Nano), "type": "event_msg",
			"payload": obj{"type": "task_started"}},
		obj{"timestamp": when.Format(time.RFC3339Nano), "type": "event_msg",
			"payload": obj{"type": "item_completed", "item": obj{
				"type": "UserMessage", "id": "um-1",
				"content": []any{obj{"type": "text", "text": "fix the build"}}}}},
		obj{"timestamp": when.Format(time.RFC3339Nano), "type": "event_msg",
			"payload": obj{"type": "item_completed", "item": obj{
				"type": "AgentMessage", "id": "am-1",
				"content": []any{obj{"type": "Text", "text": "on it"}}}}},
		obj{"timestamp": when.Format(time.RFC3339Nano), "type": "event_msg",
			"payload": obj{"type": "task_complete"}},
	})

	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestCodexDiscoverAndPage(t *testing.T) {
	home := fakeCodexHome(t, "01900000-aaaa-bbbb-cccc-000000000001", "/Users/me/repo", time.Minute)
	src, err := NewCodexSource(home)
	if err != nil {
		t.Fatal(err)
	}
	src.processCheck = alwaysRunning
	src.listPanes = noPanes
	ctx := context.Background()

	sessions, err := src.Discover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want 1", len(sessions))
	}
	if sessions[0].Cwd != "/Users/me/repo" || sessions[0].Name != "repo" {
		t.Errorf("unexpected metadata: %+v", sessions[0])
	}
	// The last turn boundary in the fixture is task_complete.
	if sessions[0].State != protocol.StateIdle {
		t.Errorf("State = %q, want idle from the final turn boundary", sessions[0].State)
	}

	page, err := src.Page(ctx, sessions[0].ID, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Messages) != 2 {
		t.Fatalf("got %d messages, want the user turn and the agent reply", len(page.Messages))
	}
	if page.Messages[0].Text != "fix the build" || page.Messages[1].Text != "on it" {
		t.Errorf("unexpected messages: %+v", page.Messages)
	}
}

func TestCodexIgnoresStaleRollouts(t *testing.T) {
	// Older than codexLiveWindow: the session ended long ago.
	home := fakeCodexHome(t, "01900000-aaaa-bbbb-cccc-000000000002", "/Users/me/old", 2*time.Hour)
	src, _ := NewCodexSource(home)
	src.processCheck = alwaysRunning
	src.listPanes = noPanes

	sessions, err := src.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 0 {
		t.Fatalf("got %d sessions, want none for a stale rollout", len(sessions))
	}
}

func TestCodexDiscoverOnMachineWithoutCodex(t *testing.T) {
	src, _ := NewCodexSource(t.TempDir())
	src.processCheck = alwaysRunning
	src.listPanes = noPanes
	sessions, err := src.Discover(context.Background())
	if err != nil {
		t.Fatalf("a missing install must not be an error: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("got %d sessions, want none", len(sessions))
	}
}

/* -------------------------------- Registry -------------------------------- */

type flakySource struct{ fail bool }

func (s *flakySource) Kind() protocol.Kind { return protocol.KindOpenCode }
func (s *flakySource) Discover(context.Context) ([]protocol.Session, error) {
	if s.fail {
		return nil, fmt.Errorf("temporary API failure")
	}
	return []protocol.Session{{ID: "opencode:s1", Kind: protocol.KindOpenCode}}, nil
}
func (s *flakySource) Page(context.Context, string, string, int) (protocol.Page, error) {
	return protocol.Page{}, nil
}
func (s *flakySource) Follow(context.Context, string, chan<- []protocol.Message) error { return nil }

func TestRegistryPreservesLastSnapshotAcrossTransientAdapterFailure(t *testing.T) {
	adapter := &flakySource{}
	registry := NewRegistry()
	registry.Add(adapter)
	if sessions, err := registry.Discover(context.Background()); err != nil || len(sessions) != 1 {
		t.Fatalf("initial discovery = %+v, %v", sessions, err)
	}
	adapter.fail = true
	sessions, err := registry.Discover(context.Background())
	if err == nil {
		t.Fatal("adapter failure was not reported")
	}
	if len(sessions) != 1 || sessions[0].ID != "opencode:s1" {
		t.Fatalf("transient failure erased live sessions: %+v", sessions)
	}
}

func TestRegistryRoutesAndOrders(t *testing.T) {
	home := fakeClaudeHome(t, "/Users/me/work/proj", "sess-1", "busy")
	claude, _ := NewClaudeSource(home)

	reg := NewRegistry()
	reg.Add(claude)

	ctx := context.Background()
	sessions, err := reg.Discover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want 1", len(sessions))
	}

	page, err := reg.Page(ctx, "claude:sess-1", "", 10)
	if err != nil {
		t.Fatalf("registry failed to route to the claude adapter: %v", err)
	}
	if len(page.Messages) != 2 {
		t.Errorf("got %d messages through the registry, want 2", len(page.Messages))
	}
}

func TestRegistryRejectsUnknownSessions(t *testing.T) {
	reg := NewRegistry()
	claude, _ := NewClaudeSource(t.TempDir())
	reg.Add(claude)

	if _, err := reg.Page(context.Background(), "gemini:whatever", "", 10); err == nil {
		t.Error("expected an error for a kind with no adapter")
	}
	if _, err := reg.Page(context.Background(), "malformed", "", 10); err == nil {
		t.Error("expected an error for a session id with no kind prefix")
	}
}

func TestRegistryOrdersWaitingInputFirst(t *testing.T) {
	sessions := []protocol.Session{
		{ID: "a", State: protocol.StateIdle, LastActivityAt: 300},
		{ID: "b", State: protocol.StateBusy, LastActivityAt: 100},
		{ID: "c", State: protocol.StateWaitingInput, LastActivityAt: 50},
		{ID: "d", State: protocol.StateBusy, LastActivityAt: 200},
	}
	// Mirrors the comparator Discover applies.
	SortSessions(sessions)

	got := []string{sessions[0].ID, sessions[1].ID, sessions[2].ID, sessions[3].ID}
	want := []string{"c", "d", "b", "a"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v (blocked sessions must come first)", got, want)
		}
	}
}

// addCodexRollout writes an extra rollout into an existing fake home, so a
// directory can hold more than one — which is what real use produces, since
// Codex files a new rollout on every run.
func addCodexRollout(t *testing.T, home, sessionID, cwd string, age time.Duration) {
	t.Helper()
	when := time.Now().Add(-age)
	path := filepath.Join(home, ".codex", "sessions",
		when.Format("2006"), when.Format("01"), when.Format("02"),
		"rollout-"+when.Format("2006-01-02T15-04-05")+"-"+sessionID+".jsonl")

	writeJSONL(t, path, []any{
		obj{"timestamp": when.Format(time.RFC3339Nano), "type": "session_meta",
			"payload": obj{"session_id": sessionID, "cwd": cwd, "cli_version": "0.147.0",
				"timestamp": when.Format(time.RFC3339Nano)}},
		obj{"timestamp": when.Format(time.RFC3339Nano), "type": "event_msg",
			"payload": obj{"type": "task_complete"}},
	})
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
}

// TestCodexPaneIsClaimedByOneRolloutOnly reproduces a session that became
// unreachable from the phone.
//
// A tmux-backed session is keyed on its pane rather than its rollout, because
// the pane exists from launch and the rollout does not. But a directory
// accumulates a rollout per Codex run, and every one of them matched the same
// pane by working directory — so the same session was reported twice under one
// id, with whichever state each rollout happened to imply.
//
// The app keys its rows by session id. Two rows under one id meant duplicate
// React keys, conflicting states, and a session that could not be opened.
func TestCodexPaneIsClaimedByOneRolloutOnly(t *testing.T) {
	const cwd = "/Users/me/repo"
	// Older run first, so the newest is not simply the one read first.
	home := fakeCodexHome(t, "01900000-aaaa-bbbb-cccc-00000000000a", cwd, 20*time.Minute)
	addCodexRollout(t, home, "01900000-aaaa-bbbb-cccc-00000000000b", cwd, time.Minute)

	src, err := NewCodexSource(home)
	if err != nil {
		t.Fatal(err)
	}
	src.processCheck = alwaysRunning
	src.listPanes = func(context.Context) ([]tmux.Session, error) {
		return []tmux.Session{{
			Name: tmux.Prefix + "codex-1", PanePID: 4242, Cwd: cwd, Command: "codex",
			Created: time.Now().Add(-30 * time.Minute),
		}}, nil
	}

	sessions, err := src.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	seen := map[string]protocol.Session{}
	for _, session := range sessions {
		if previous, clash := seen[session.ID]; clash {
			t.Fatalf("id %q reported twice (states %q and %q) — the app keys rows by id, "+
				"so this session renders twice and cannot be opened",
				session.ID, previous.State, session.State)
		}
		seen[session.ID] = session
	}

	// Exactly one session may own the pane, and it must be the newest rollout:
	// that is the one the running Codex process is actually writing to, and the
	// only one where typing into the pane reaches the right conversation.
	var tmuxBacked []protocol.Session
	for _, session := range sessions {
		if session.Inject == protocol.InjectTmux {
			tmuxBacked = append(tmuxBacked, session)
		}
	}
	if len(tmuxBacked) != 1 {
		t.Fatalf("got %d tmux-backed sessions, want exactly 1: %+v", len(tmuxBacked), tmuxBacked)
	}
	if tmuxBacked[0].NativeID != "01900000-aaaa-bbbb-cccc-00000000000b" {
		t.Errorf("the pane was claimed by rollout %q, want the newest one — "+
			"messages typed into the pane would land in a stale conversation",
			tmuxBacked[0].NativeID)
	}
}
