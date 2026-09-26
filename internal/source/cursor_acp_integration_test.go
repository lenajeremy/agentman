package source

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Run with AGENTMAN_TEST_CURSOR_ACP=1 to exercise the installed Cursor CLI.
// The ordinary suite stays offline and does not spend Cursor usage.
func TestCursorACPRealStreamingAndResume(t *testing.T) {
	if os.Getenv("AGENTMAN_TEST_CURSOR_ACP") != "1" {
		t.Skip("requires installed Cursor CLI and logged-in account")
	}
	if _, err := exec.LookPath("agent"); err != nil {
		t.Skip("Cursor Agent CLI is unavailable")
	}
	root := t.TempDir()
	cwd := filepath.Join(root, "project")
	if err := os.Mkdir(cwd, 0o700); err != nil {
		t.Fatal(err)
	}
	source, err := NewCursorACPSource(filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	id, err := source.Launch(ctx, cwd, "Write twelve distinct short facts about the moon, numbered one through twelve. End with STREAM_OK. Do not use tools or edit files.")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(id, cursorACPPrefix) {
		t.Fatalf("unexpected session ID: %s", id)
	}
	updates := make(chan []protocol.Message, 32)
	followCtx, stopFollow := context.WithCancel(ctx)
	defer stopFollow()
	go func() { _ = source.Follow(followCtx, id, updates) }()
	var sawPartial, sawFinal bool
	for !sawFinal {
		select {
		case batch := <-updates:
			for _, message := range batch {
				if message.Role != protocol.RoleAssistant {
					continue
				}
				if strings.Contains(message.Text, "STREAM_OK") {
					sawFinal = true
				}
				if !sawFinal && message.Text != "" {
					sawPartial = true
				}
			}
		case <-ctx.Done():
			t.Fatal("Cursor did not stream a reply")
		}
	}
	if !sawPartial {
		t.Fatal("Cursor reply only appeared after completion")
	}
	for {
		sessions, _ := source.Discover(ctx)
		if len(sessions) == 1 && sessions[0].State == protocol.StateIdle {
			break
		}
		select {
		case <-time.After(100 * time.Millisecond):
		case <-ctx.Done():
			t.Fatal("Cursor never returned to idle")
		}
	}
	reloaded, err := NewCursorACPSource(filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	page, err := reloaded.Page(ctx, id, "", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Messages) < 2 || page.Messages[0].Role != protocol.RoleUser {
		t.Fatalf("missing persisted history: %+v", page)
	}
	if _, err := reloaded.Inject(ctx, id, "Reply exactly RESUME_OK. Do not use tools or edit files."); err != nil {
		t.Fatal(err)
	}
	for {
		page, _ := reloaded.Page(ctx, id, "", 20)
		for _, message := range page.Messages {
			if message.Role == protocol.RoleAssistant && strings.Contains(message.Text, "RESUME_OK") {
				return
			}
		}
		select {
		case <-time.After(100 * time.Millisecond):
		case <-ctx.Done():
			t.Fatal("resumed Cursor session did not answer")
		}
	}
}

func TestCursorACPRealQueuedFollowUp(t *testing.T) {
	if os.Getenv("AGENTMAN_TEST_CURSOR_ACP") != "1" {
		t.Skip("requires installed Cursor CLI and logged-in account")
	}
	root := t.TempDir()
	cwd := filepath.Join(root, "project")
	if err := os.Mkdir(cwd, 0o700); err != nil {
		t.Fatal(err)
	}
	source, err := NewCursorACPSource(filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	source.EnableAsync()
	defer source.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	id, err := source.Launch(ctx, cwd, "Write twelve short facts about the moon. Do not use tools or edit files.")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Inject(ctx, id, "After that, reply exactly QUEUE_OK. Do not use tools or edit files."); err != nil {
		t.Fatal(err)
	}
	st, _ := source.get(id)
	st.mu.Lock()
	queued := len(st.record.Queued)
	st.mu.Unlock()
	if queued != 1 {
		t.Fatalf("follow-up did not enter durable queue: %d", queued)
	}
	for {
		page, err := source.Page(ctx, id, "", 20)
		if err != nil {
			t.Fatal(err)
		}
		for _, message := range page.Messages {
			if message.Role == protocol.RoleAssistant && strings.Contains(message.Text, "QUEUE_OK") {
				return
			}
		}
		select {
		case <-time.After(100 * time.Millisecond):
		case <-ctx.Done():
			t.Fatal("queued Cursor follow-up was not answered")
		}
	}
}

func TestCursorACPRealInterrupt(t *testing.T) {
	if os.Getenv("AGENTMAN_TEST_CURSOR_ACP") != "1" {
		t.Skip("requires installed Cursor CLI and logged-in account")
	}
	root := t.TempDir()
	cwd := filepath.Join(root, "project")
	if err := os.Mkdir(cwd, 0o700); err != nil {
		t.Fatal(err)
	}
	source, err := NewCursorACPSource(filepath.Join(root, "state"))
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	id, err := source.Launch(ctx, cwd, "Write 300 numbered, short facts about the moon. Do not use tools or edit files.")
	if err != nil {
		t.Fatal(err)
	}
	for {
		page, _ := source.Page(ctx, id, "", 20)
		started := false
		for _, message := range page.Messages {
			if message.Role == protocol.RoleAssistant && message.Text != "" {
				started = true
			}
		}
		if started {
			break
		}
		select {
		case <-time.After(50 * time.Millisecond):
		case <-ctx.Done():
			t.Fatal("Cursor never started its reply")
		}
	}
	if err := source.Interrupt(ctx, id); err != nil {
		t.Fatal(err)
	}
	for {
		sessions, _ := source.Discover(ctx)
		if len(sessions) == 1 && sessions[0].State == protocol.StateIdle {
			return
		}
		select {
		case <-time.After(100 * time.Millisecond):
		case <-ctx.Done():
			t.Fatal("Cursor did not stop after cancellation")
		}
	}
}
