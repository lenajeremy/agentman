package source

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/tmux"
)

// Kiro's lock file is what says a session is running. Without one the session
// is finished, which is exactly what Discover is built to skip and what the
// folder filter is built to show.
func TestKiroPastFindsSessionsWithNoLock(t *testing.T) {
	home := t.TempDir()
	work := filepath.Join(home, "code", "api")
	writeKiroSession(t, home, "51486a42-f1e6-4d90-bf89-4bcfe78e7fb6", work, 0, kiroLinePrompt)
	writeKiroSession(t, home, "62597b53-a2f7-4e01-ca9a-5cd0fe89b7c8",
		filepath.Join(home, "code", "web"), 0, kiroLinePrompt)

	s, err := NewKiroSource(home)
	if err != nil {
		t.Fatal(err)
	}
	past, err := s.Past(context.Background(), work, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(past) != 1 {
		t.Fatalf("Past found %d sessions, want only the one in %s", len(past), work)
	}
	if past[0].State != protocol.StateEnded {
		t.Errorf("state = %q, want %q", past[0].State, protocol.StateEnded)
	}
	if past[0].Name != "list the files" {
		t.Errorf("name = %q, want the title Kiro wrote", past[0].Name)
	}
	// Opening it has to work, which means Page resolves an id no sweep saw.
	if _, err := s.Page(context.Background(),
		string(protocol.KindKiro)+":51486a42-f1e6-4d90-bf89-4bcfe78e7fb6", "", 10); err != nil {
		t.Errorf("Page on an ended session: %v", err)
	}
}

// Antigravity records no working directory near its transcripts — a live
// session's comes from lsof on the running process, which an ended one does
// not have. history.jsonl is the only thing that connects a finished
// conversation to a folder.
func TestAntigravityPastReadsTheWorkspaceFromThePromptLog(t *testing.T) {
	home := t.TempDir()
	work := filepath.Join(home, "code", "api")
	root := filepath.Join(home, ".gemini", "antigravity-cli")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		fmt.Sprintf(`{"display":"/model","timestamp":1790527908019,"workspace":%q,"type":"slash_command"}`, work),
		fmt.Sprintf(`{"display":"what is this repo","timestamp":1790527941251,"workspace":%q,"conversationId":"16d0a7e5-165d-4de6-a2d4-bd791f805bdf"}`, work),
		fmt.Sprintf(`{"display":"and the relay?","timestamp":1790527999999,"workspace":%q,"conversationId":"16d0a7e5-165d-4de6-a2d4-bd791f805bdf"}`, work),
		fmt.Sprintf(`{"display":"elsewhere","timestamp":1790527950000,"workspace":%q,"conversationId":"99999999-165d-4de6-a2d4-bd791f805bdf"}`,
			filepath.Join(home, "code", "web")),
	}
	body := ""
	for _, line := range lines {
		body += line + "\n"
	}
	if err := os.WriteFile(filepath.Join(root, "history.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := NewAntigravitySource(home)
	if err != nil {
		t.Fatal(err)
	}
	past, err := s.Past(context.Background(), work, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(past) != 1 {
		t.Fatalf("Past found %d conversations, want 1: %+v", len(past), past)
	}
	if past[0].NativeID != "16d0a7e5-165d-4de6-a2d4-bd791f805bdf" {
		t.Errorf("id = %q", past[0].NativeID)
	}
	if past[0].Name != "what is this repo" {
		t.Errorf("name = %q, want the first prompt of the conversation", past[0].Name)
	}
	// The last prompt in the conversation is when it was last active.
	if past[0].LastActivityAt != 1790527999999 {
		t.Errorf("lastActivityAt = %d, want the newest prompt's time", past[0].LastActivityAt)
	}
	if past[0].StartedAt != 1790527941251 {
		t.Errorf("startedAt = %d, want the first prompt's time", past[0].StartedAt)
	}
}

// A slash command typed outside a conversation carries a workspace but no
// conversation, and is not a session anyone can open.
func TestAntigravityPastIgnoresPromptsWithNoConversation(t *testing.T) {
	home := t.TempDir()
	work := filepath.Join(home, "code", "api")
	root := filepath.Join(home, ".gemini", "antigravity-cli")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{"display":"/model","timestamp":1790527908019,"workspace":%q,"type":"slash_command"}`+"\n", work)
	if err := os.WriteFile(filepath.Join(root, "history.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := NewAntigravitySource(home)
	if err != nil {
		t.Fatal(err)
	}
	past, err := s.Past(context.Background(), work, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(past) != 0 {
		t.Errorf("Past found %d sessions, want none: %+v", len(past), past)
	}
}

func writeCursorCLIChat(t *testing.T, home, hash, id, cwd, title string, updatedAt int64) {
	t.Helper()
	dir := filepath.Join(home, ".cursor", "chats", hash, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	meta := fmt.Sprintf(`{"schemaVersion":1,"createdAtMs":%d,"hasConversation":true,"title":%q,"updatedAtMs":%d,"cwd":%q}`,
		updatedAt-1000, title, updatedAt, cwd)
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "store.db"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Discovery drops a Cursor chat older than cursorCLIWindow unless a pane
// still holds its store open. History is what that window throws away.
func TestCursorCLIPastIgnoresTheRecencyWindow(t *testing.T) {
	home := t.TempDir()
	work := filepath.Join(home, "code", "api")
	old := time.Now().Add(-90 * 24 * time.Hour).UnixMilli()
	writeCursorCLIChat(t, home, "hash1", "chat-1", work, "Agentman verify ready", old)
	writeCursorCLIChat(t, home, "hash2", "chat-2", filepath.Join(home, "code", "web"), "other", old)

	s, err := NewCursorCLISource(home)
	if err != nil {
		t.Fatal(err)
	}
	s.listPanes = func(context.Context) ([]tmux.Session, error) { return nil, nil }
	live, err := s.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 0 {
		t.Fatalf("Discover found %d chats; a 90-day-old chat with no pane is not live", len(live))
	}

	past, err := s.Past(context.Background(), work, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(past) != 1 {
		t.Fatalf("Past found %d chats, want 1", len(past))
	}
	if past[0].Name != "Agentman verify ready" {
		t.Errorf("name = %q, want the chat's own title", past[0].Name)
	}
	if past[0].Cwd != work {
		t.Errorf("cwd = %q, want %q", past[0].Cwd, work)
	}
}

// OpenCode's server lists every session it has, from every project. Only
// openCodeIdleWindow hides the old ones, and a folder's history is exactly
// what that window discards.
func TestOpenCodePastIgnoresTheIdleWindow(t *testing.T) {
	work := "/code/api"
	stale := time.Now().Add(-30 * 24 * time.Hour).UnixMilli()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/global/health":
			json.NewEncoder(w).Encode(map[string]any{"healthy": true})
		case "/session":
			json.NewEncoder(w).Encode([]map[string]any{
				{
					"id": "ses_old", "title": "cybersecurity audit", "directory": work,
					"time": map[string]any{"created": stale - 1000, "updated": stale},
				},
				{
					"id": "ses_other", "title": "somewhere else", "directory": "/code/web",
					"time": map[string]any{"created": stale - 1000, "updated": stale},
				},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	s := NewOpenCodeSource(server.URL)
	s.findServers = func(context.Context) []string { return []string{server.URL} }

	live, err := s.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 0 {
		t.Fatalf("Discover found %d sessions; one idle for 30 days is past the window", len(live))
	}

	past, err := s.Past(context.Background(), work, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(past) != 1 {
		t.Fatalf("Past found %d sessions, want the one in %s: %+v", len(past), work, past)
	}
	if past[0].Name != "cybersecurity audit" {
		t.Errorf("name = %q", past[0].Name)
	}
	if past[0].State != protocol.StateEnded {
		t.Errorf("state = %q, want %q", past[0].State, protocol.StateEnded)
	}
	// Its transcript is a server plus an id rather than a path, so opening it
	// has to resolve a route no sweep recorded.
	if _, ok := s.routeFor(string(protocol.KindOpenCode) + ":ses_old"); !ok {
		t.Error("no route kept for the ended session; Page would not find it")
	}
}
