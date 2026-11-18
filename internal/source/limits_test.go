package source

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/parser"
	"github.com/lenajeremy/agentman/internal/protocol"
)

const (
	limitPrompt  = `{"type":"user","uuid":"p1","timestamp":"2026-11-18T15:00:00.000Z","message":{"role":"user","content":"carry on"}}`
	limitRefusal = `{"type":"assistant","uuid":"r1","timestamp":"2026-11-18T15:02:00.000Z","isApiErrorMessage":true,"error":"rate_limit",` +
		`"message":{"role":"assistant","content":[{"type":"text","text":"You've hit your session limit · resets 5:20pm (Europe/Dublin)"}]}}`
	limitAfter = `{"type":"last-prompt","lastPrompt":"carry on"}`
)

func writeLines(t *testing.T, path string, lines ...string) {
	t.Helper()
	var text string
	for _, line := range lines {
		text += line + "\n"
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLimitWatchFollowsTheTranscript(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	now := time.Date(2026, 11, 18, 15, 5, 0, 0, time.UTC)
	w := &limitWatch{now: func() time.Time { return now }}
	ctx := context.Background()

	writeLines(t, path, limitPrompt, limitRefusal, limitAfter)
	limit := w.limit(ctx, "claude:s", path, protocol.StateIdle, parser.ClaudeLimitLine)
	if limit == nil || limit.Text != "You've hit your session limit · resets 5:20pm (Europe/Dublin)" {
		t.Fatalf("limit %+v", limit)
	}

	// Working again is not out of usage, whatever the file last said.
	if limit := w.limit(ctx, "claude:s", path, protocol.StateBusy, parser.ClaudeLimitLine); limit != nil {
		t.Errorf("a busy agent is out of usage: %+v", limit)
	}

	// The reset passing ends it with no new line.
	now = time.Date(2026, 11, 18, 17, 20, 0, 0, time.UTC)
	if limit := w.limit(ctx, "claude:s", path, protocol.StateIdle, parser.ClaudeLimitLine); limit != nil {
		t.Errorf("still out after the reset: %+v", limit)
	}

	// So does a new prompt, before the reset.
	now = time.Date(2026, 11, 18, 15, 10, 0, 0, time.UTC)
	writeLines(t, path, limitPrompt, limitRefusal, limitAfter, limitPrompt)
	if limit := w.limit(ctx, "claude:s", path, protocol.StateIdle, parser.ClaudeLimitLine); limit != nil {
		t.Errorf("still out after a new prompt: %+v", limit)
	}

	// No transcript at all, or none yet.
	if limit := w.limit(ctx, "claude:t", filepath.Join(t.TempDir(), "none.jsonl"), protocol.StateIdle, parser.ClaudeLimitLine); limit != nil {
		t.Errorf("a missing file is out of usage: %+v", limit)
	}
	if limit := w.limit(ctx, "claude:t", "", protocol.StateIdle, parser.ClaudeLimitLine); limit != nil {
		t.Errorf("no file is out of usage: %+v", limit)
	}

	w.forget(map[string]bool{})
	if len(w.seen) != 0 {
		t.Errorf("forgot nothing: %v", w.seen)
	}
}

// An unchanged transcript is not read again.
func TestLimitWatchReadsOnlyChanges(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.jsonl")
	writeLines(t, path, limitPrompt, limitRefusal)
	now := time.Date(2026, 11, 18, 15, 5, 0, 0, time.UTC)
	w := &limitWatch{now: func() time.Time { return now }}
	reads := 0
	count := func(line string) (*protocol.Limit, bool) {
		reads++
		return parser.ClaudeLimitLine(line)
	}
	for range 3 {
		if w.limit(context.Background(), "claude:s", path, protocol.StateIdle, count) == nil {
			t.Fatal("not out of usage")
		}
	}
	if reads != 1 {
		t.Errorf("read %d lines for three sweeps of one unchanged file", reads)
	}
}

// A JSON-RPC server may put the reason in the error's data.
func TestACPErrorKeepsItsDetail(t *testing.T) {
	cases := []struct {
		err  acpRPCError
		want string
	}{
		{acpRPCError{Message: "Internal error", Data: []byte(`{"details":"You've hit your usage limit"}`)}, "Internal error: You've hit your usage limit"},
		{acpRPCError{Message: "Internal error", Data: []byte(`"Quota exceeded"`)}, "Internal error: Quota exceeded"},
		{acpRPCError{Message: "You've hit your usage limit", Data: []byte(`{"message":"You've hit your usage limit"}`)}, "You've hit your usage limit"},
		{acpRPCError{Message: "Method not found"}, "Method not found"},
		{acpRPCError{Data: []byte(`{"error":"Out of credits"}`)}, "Out of credits"},
	}
	for _, c := range cases {
		if got := c.err.text(); got != c.want {
			t.Errorf("%+v: %q, want %q", c.err, got, c.want)
		}
	}
}
