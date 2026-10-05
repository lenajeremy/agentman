package source

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func pastName(t *testing.T, lines ...string) string {
	t.Helper()
	home := t.TempDir()
	const work = "/tmp/work" // the cwd every fixture line records
	writeClaudeTranscript(t, home, "33333333-3333-4333-8333-333333333333", work, lines...)
	past, err := hermeticClaude(t, home).Past(context.Background(), work, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(past) != 1 {
		t.Fatalf("Past found %d sessions", len(past))
	}
	return past[0].Name
}

func titleLine(kind, field, value string) string {
	return fmt.Sprintf(`{"type":%q,%q:%q,"sessionId":"33333333-3333-4333-8333-333333333333"}`, kind, field, value)
}

// A session renamed with /rename, or named by Claude, carries that name in
// its transcript. History ignored it and named the row after the first prompt,
// so a session listed live as "agentman-2d" was renamed the moment it ended.
func TestClaudeHistoryUsesTheNameTheSessionWasGiven(t *testing.T) {
	work := "/tmp/work"
	prompt := claudeUserLine(work, "look at the failing build")
	for _, tc := range []struct {
		name  string
		lines []string
		want  string
	}{
		{"renamed", []string{prompt, titleLine("ai-title", "aiTitle", "Build failure"),
			titleLine("custom-title", "customTitle", "release blocker")}, "release blocker"},
		{"agent name over Claude's own title", []string{titleLine("agent-name", "agentName", "agentman-2d"),
			prompt, titleLine("ai-title", "aiTitle", "Build failure")}, "agentman-2d"},
		{"Claude's own title", []string{prompt, titleLine("ai-title", "aiTitle", "Build failure")}, "Build failure"},
		{"the first prompt otherwise", []string{prompt}, "look at the failing build"},
	} {
		if got := pastName(t, tc.lines...); got != tc.want {
			t.Errorf("%s: named %q, want %q", tc.name, got, tc.want)
		}
	}
}

// The newest title wins, even when the transcript is long enough that it is
// nowhere near the start.
func TestClaudeHistoryFindsARenameAtTheEndOfALongSession(t *testing.T) {
	work := "/tmp/work"
	lines := []string{claudeUserLine(work, "first prompt")}
	filler := `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"` +
		strings.Repeat("x", 4000) + `"}]}}`
	for range 200 {
		lines = append(lines, filler)
	}
	lines = append(lines, titleLine("custom-title", "customTitle", "renamed late"))
	if got := pastName(t, lines...); got != "renamed late" {
		t.Errorf("named %q", got)
	}
}

// A scheduled task's whole first prompt is the task's own block, which the
// prompt cleaner strips to nothing, so twenty sessions in one folder were all
// named after the folder. The task's name is the useful one.
func TestClaudeHistoryNamesAScheduledTaskAfterTheTask(t *testing.T) {
	work := "/tmp/work"
	task := `<scheduled-task name=\"growth-daily\" file=\"/Users/me/.claude/scheduled-tasks/growth-daily/SKILL.md\">run the report</scheduled-task>`
	line := `{"type":"user","isSidechain":false,"cwd":"` + work + `","timestamp":"2026-03-05T22:52:36.211Z",` +
		`"message":{"role":"user","content":"` + task + `"}}`
	if got := pastName(t, line); got != "growth-daily" {
		t.Errorf("named %q", got)
	}
}
