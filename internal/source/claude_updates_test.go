package source

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// These are sanitized excerpts of Claude Code 2.1.311 session
// 270133dc-7216-4c41-b1bc-3fb4639301b7. Each API message was written as
// separate records: an empty signed block, a visible update, then a tool
// call, all sharing message.id but with distinct record UUIDs. The updates
// appeared between tool pills in Claude's terminal and vanished in Agentman.
func TestClaudeVisibleUpdatesPageAndFollow(t *testing.T) {
	data, err := os.ReadFile("../parser/testdata/claude-visible-updates.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	wantUpdates := []struct{ id, prefix, call string }{
		{"f2759273-916b-4d98-a9d2-0a3862d86dfd:0", "Confirmed the service is on 0.23.0, the tmux server is untouched", "toolu_017KKfU1qJWcxCz7Gd5eA5qe"},
		{"4021d0dd-5a30-429f-8284-c1fa37016a5a:0", "The connection's request limiter can get fully filled", "toolu_01DmipQfz8RiJxo3DwjfXRU7"},
		{"b70b73b1-24f5-4964-9d93-0a3506c76c9e:0", "Your Mac restarted at 07:47:44", "toolu_01DWpK33W6mvVkS9FxWw1fCC"},
	}
	const id = "claude:s1"
	check := func(t *testing.T, messages []protocol.Message, live bool) {
		t.Helper()
		stride := 2
		if live {
			stride = 3 // The follow emits both the running and settled call.
		}
		if len(messages) != stride*len(wantUpdates) {
			t.Fatalf("got %d messages, want %d: %+v", len(messages), stride*len(wantUpdates), messages)
		}
		for i, want := range wantUpdates {
			update := messages[stride*i]
			if update.ID != want.id || update.SessionID != id || update.Role != protocol.RoleAssistant ||
				!strings.HasPrefix(update.Text, want.prefix) || strings.Contains(update.Text, "omitted") {
				t.Errorf("update %d = %+v, want %s: %q", i, update, want.id, want.prefix)
			}
			for j := 1; j < stride; j++ {
				call := messages[stride*i+j]
				status := protocol.ToolOK
				if live && j == 1 {
					status = protocol.ToolRunning
				}
				if call.ID != want.call || call.Role != protocol.RoleTool || call.Tool == nil || call.Tool.Status != status {
					t.Errorf("tool after update %d = %+v, want %s %s", i, call, want.call, status)
				}
			}
		}
	}

	for _, limit := range []int{1, 2, 20} {
		t.Run("page/"+strconv.Itoa(limit), func(t *testing.T) {
			home := t.TempDir()
			src, err := NewClaudeSource(home)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(home, "t.jsonl")
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			src.sessions[id] = claudeSession{transcript: path}
			var messages []protocol.Message
			before := ""
			for pages := 0; ; pages++ {
				if pages > len(wantUpdates)*2 {
					t.Fatal("paging did not reach the start")
				}
				page, err := src.Page(context.Background(), id, before, limit)
				if err != nil {
					t.Fatal(err)
				}
				messages = append(page.Messages, messages...)
				if !page.HasMore {
					break
				}
				if page.NextCursor == "" || page.NextCursor == before {
					t.Fatal("page cursor did not advance")
				}
				before = page.NextCursor
			}
			check(t, messages, false)
		})
	}

	t.Run("follow", func(t *testing.T) {
		home := t.TempDir()
		src, err := NewClaudeSource(home)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(home, "t.jsonl")
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		src.sessions[id] = claudeSession{transcript: path}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		out := make(chan []protocol.Message, 4)
		go func() { _ = src.Follow(ctx, id, out) }()
		time.Sleep(2 * followInterval)
		appendLine(t, path, string(data))
		check(t, nextBatch(t, ctx, out), true)
	})
}
