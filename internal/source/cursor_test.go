package source

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// fakeCursorHome builds a ~/.cursor/projects tree with one session whose
// transcript holds a complete turn: user prompt, assistant reply with a tool
// call, and a successful turn end.
func fakeCursorHome(t *testing.T, projectPath, sessionID string) (home, transcript string) {
	t.Helper()
	home = t.TempDir()

	enc := strings.TrimPrefix(projectPath, "/")
	enc = strings.ReplaceAll(enc, "/", "-")
	transcript = filepath.Join(home, ".cursor", "projects", enc,
		"agent-transcripts", sessionID, sessionID+".jsonl")
	writeJSONL(t, transcript, []any{
		obj{"role": "user", "message": obj{"content": []any{
			obj{"type": "text", "text": "<timestamp>Monday, Aug 3, 2026, 12:50 PM (UTC+1)</timestamp>\n<user_query>\nwhich directory are you in?\n</user_query>"}}}},
		obj{"role": "assistant", "message": obj{"content": []any{
			obj{"type": "text", "text": "Checking now."},
			obj{"type": "tool_use", "name": "Shell", "input": obj{"command": "pwd"}}}}},
		obj{"type": "turn_ended", "status": "success"},
	})
	return home, transcript
}

func TestCursorDiscoverReportsLiveSession(t *testing.T) {
	home, _ := fakeCursorHome(t, "/Users/me/work/proj", "sess-1")
	src, err := NewCursorSource(home)
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := src.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	s := sessions[0]
	if s.ID != "cursor:sess-1" || s.Kind != protocol.KindCursor || s.NativeID != "sess-1" {
		t.Errorf("unexpected identity: %+v", s)
	}
	if s.Cwd != "/Users/me/work/proj" {
		t.Errorf("unexpected cwd: %q", s.Cwd)
	}
	if s.Name != "which directory are you in?" {
		t.Errorf("name should come from the opening query, got %q", s.Name)
	}
	if s.State != protocol.StateIdle {
		t.Errorf("trailing turn_ended should read idle, got %q", s.State)
	}
	if s.Inject != protocol.InjectNone {
		t.Errorf("cursor sessions are read-only, got inject %q", s.Inject)
	}
	if s.LastActivityAt <= 0 || s.StartedAt <= 0 {
		t.Errorf("timestamps should be set: %+v", s)
	}
}

func TestCursorDiscoverDoesNotPresentCLITranscriptAsIDEChat(t *testing.T) {
	home, _ := fakeCursorHome(t, "/Users/me/work/proj", "cli-chat-1")
	meta := filepath.Join(home, ".cursor", "chats", "workspace", "cli-chat-1", "meta.json")
	if err := os.MkdirAll(filepath.Dir(meta), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(meta, []byte(`{"hasConversation":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	src, err := NewCursorSource(home)
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := src.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 0 {
		t.Fatalf("CLI transcript was duplicated as an IDE session: %+v", sessions)
	}
}

func TestCursorDiscoverBusyWhenMidTurn(t *testing.T) {
	home := t.TempDir()
	enc := "test-proj"
	transcript := filepath.Join(home, ".cursor", "projects", enc,
		"agent-transcripts", "sess-9", "sess-9.jsonl")
	writeJSONL(t, transcript, []any{
		obj{"role": "user", "message": obj{"content": []any{
			obj{"type": "text", "text": "do the thing"}}}},
		obj{"role": "assistant", "message": obj{"content": []any{
			obj{"type": "text", "text": "working on it"}}}},
	})
	src, err := NewCursorSource(home)
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := src.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].State != protocol.StateBusy {
		t.Errorf("no trailing turn_ended should read busy, got %q", sessions[0].State)
	}
}

func TestCursorDiscoverSkipsStaleAndJunk(t *testing.T) {
	home, _ := fakeCursorHome(t, "/live/proj", "live-1")

	// A transcript older than the live window is history, not a session.
	stale := filepath.Join(home, ".cursor", "projects", "stale-proj",
		"agent-transcripts", "old-1", "old-1.jsonl")
	writeJSONL(t, stale, []any{
		obj{"role": "user", "message": obj{"content": []any{
			obj{"type": "text", "text": "old"}}}},
	})
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}

	// Junk the scanner must ignore: stray files, a session directory whose
	// file does not match its name, non-agent project content, hidden dirs.
	projects := filepath.Join(home, ".cursor", "projects")
	agentTranscripts := filepath.Join(projects, "live-proj", "agent-transcripts")
	if err := os.WriteFile(filepath.Join(agentTranscripts, "loose.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mismatch := filepath.Join(agentTranscripts, "sess-x")
	if err := os.MkdirAll(mismatch, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mismatch, "other.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(projects, "live-proj", "terminals"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(projects, ".hidden", "agent-transcripts"), 0o755); err != nil {
		t.Fatal(err)
	}

	src, err := NewCursorSource(home)
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := src.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].NativeID != "live-1" {
		t.Fatalf("expected only the live session, got %+v", sessions)
	}
}

func TestCursorDiscoverEmptyWhenNotInstalled(t *testing.T) {
	src, err := NewCursorSource(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := src.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 0 {
		t.Errorf("expected no sessions, got %+v", sessions)
	}
}

func TestDecodeCursorProjectDir(t *testing.T) {
	if got := decodeCursorProjectDir("Users-mac-Desktop-opal"); got != "/Users/mac/Desktop/opal" {
		t.Errorf("unexpected decode: %q", got)
	}
}

func TestCursorPage(t *testing.T) {
	home, _ := fakeCursorHome(t, "/Users/me/work/proj", "sess-1")
	src, err := NewCursorSource(home)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	sessions, err := src.Discover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	page, err := src.Page(ctx, sessions[0].ID, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	// User text, assistant text, one tool row; the successful turn end maps
	// to nothing.
	if len(page.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d: %+v", len(page.Messages), page.Messages)
	}
	if page.Messages[0].Role != protocol.RoleUser || page.Messages[0].Text != "which directory are you in?" {
		t.Errorf("unexpected first message: %+v", page.Messages[0])
	}
	if page.Messages[1].Role != protocol.RoleAssistant {
		t.Errorf("unexpected second message: %+v", page.Messages[1])
	}
	if page.Messages[2].Role != protocol.RoleTool || page.Messages[2].Tool.Name != "Shell" {
		t.Errorf("unexpected third message: %+v", page.Messages[2])
	}
	if page.HasMore {
		t.Errorf("single-turn transcript should have no more pages")
	}
	// Cursor records carry no timestamps; Page stamps the file's mtime.
	for _, m := range page.Messages {
		if m.Ts <= 0 {
			t.Errorf("message %q should carry a timestamp, got %d", m.ID, m.Ts)
		}
	}
	if page.Messages[0].Ts != page.Messages[2].Ts {
		t.Errorf("messages in one read should share the transcript mtime")
	}

	if _, err := src.Page(ctx, "cursor:missing", "", 10); err == nil {
		t.Errorf("expected error for unknown session")
	}
	if _, err := src.Page(ctx, sessions[0].ID, "not-a-cursor", 10); err == nil {
		t.Errorf("expected error for bad cursor")
	}
}

func TestCursorPagePaginates(t *testing.T) {
	home := t.TempDir()
	var records []any
	for i := 0; i < 6; i++ {
		records = append(records, obj{"role": "user", "message": obj{"content": []any{
			obj{"type": "text", "text": "question"}}}})
	}
	transcript := filepath.Join(home, ".cursor", "projects", "p",
		"agent-transcripts", "s", "s.jsonl")
	writeJSONL(t, transcript, records)

	src, err := NewCursorSource(home)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	sessions, err := src.Discover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	first, err := src.Page(ctx, sessions[0].ID, "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Messages) != 2 || !first.HasMore || first.NextCursor == "" {
		t.Fatalf("expected a full first page with more: %+v", first)
	}
	second, err := src.Page(ctx, sessions[0].ID, first.NextCursor, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Messages) != 4 {
		t.Fatalf("expected remaining 4 messages, got %d", len(second.Messages))
	}
	// Pages join up chronologically: every byte offset in the older page
	// precedes every offset in the newer one, and together they cover all
	// six records exactly once.
	joined := append(append([]protocol.Message{}, second.Messages...), first.Messages...)
	seen := map[string]bool{}
	var last int64 = -1
	for _, m := range joined {
		var offset int64
		if _, err := fmt.Sscanf(m.ID, "o%d", &offset); err != nil {
			t.Fatalf("unexpected message id %q", m.ID)
		}
		if offset <= last {
			t.Fatalf("messages out of order at %q", m.ID)
		}
		last = offset
		seen[m.ID] = true
	}
	if len(seen) != 6 {
		t.Errorf("expected 6 distinct messages across both pages, got %d", len(seen))
	}
}

func TestCursorFollowEmitsAppends(t *testing.T) {
	home, transcript := fakeCursorHome(t, "/Users/me/work/proj", "sess-1")
	src, err := NewCursorSource(home)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sessions, err := src.Discover(ctx)
	if err != nil {
		t.Fatal(err)
	}

	out := make(chan []protocol.Message, 4)
	done := make(chan error, 1)
	go func() { done <- src.Follow(ctx, sessions[0].ID, out) }()

	// Let the tail prime at end-of-file so only the append is reported.
	time.Sleep(followInterval + 200*time.Millisecond)
	f, err := os.OpenFile(transcript, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("{\"role\":\"user\",\"message\":{\"content\":[{\"type\":\"text\",\"text\":\"and another thing\"}]}}\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()

	select {
	case batch := <-out:
		if len(batch) != 1 || batch[0].Text != "and another thing" {
			t.Fatalf("unexpected batch: %+v", batch)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("timed out waiting for follow batch")
	}
	cancel()
	if err := <-done; err != context.Canceled && err != context.DeadlineExceeded {
		t.Errorf("unexpected follow error: %v", err)
	}
}
