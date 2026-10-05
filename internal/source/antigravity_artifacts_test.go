package source

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/tmux"
)

// agyBrain lays out a conversation's artifacts the way agy 1.2.17 wrote them
// for a /plan request, plus the things that sit beside artifacts without
// being any.
func agyBrain(t *testing.T, home string) string {
	t.Helper()
	brain := filepath.Join(home, ".gemini", "antigravity-cli", "brain", agConversation)
	files := map[string]string{
		"implementation_plan.md": "# Implementation Plan: Add README.md\n\nAdd a `README.md` containing one line.\n",
		"implementation_plan.md.metadata.json": `{
  "summary":  "Implementation plan and task list to add README.md containing 'research workspace'.",
  "updatedAt":  "2026-10-05T09:37:41.038806Z",
  "requestFeedback":  true,
  "userFacing":  true
}`,
		"walkthrough.md": "# Walkthrough\n\nCreated README.md.\n",
		"walkthrough.md.metadata.json": `{
  "summary":  "Walkthrough of creating and verifying README.md",
  "updatedAt":  "2026-10-05T09:37:47.185966Z",
  "userFacing":  true
}`,
		// The IDE's copy of an earlier version, and a note the agent wrote
		// for itself: neither is shown.
		"task.md.resolved.1":               "- [x] old\n",
		"notes.md":                         "scratch\n",
		"internal.md":                      "# Internal\n",
		"internal.md.metadata.json":        `{"summary": "agent-only", "userFacing": false}`,
		"diagram_1772015899190.png":        "\x89PNG fake",
		".tempmediaStorage/media_1_a1.png": "\x89PNG fake",
	}
	for name, body := range files {
		path := filepath.Join(brain, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A link to a file outside the conversation, which exists.
	outside := filepath.Join(home, "outside.md")
	if err := os.WriteFile(outside, []byte("# not an artifact\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(brain, "link.md")); err != nil {
		t.Fatal(err)
	}
	return brain
}

const agReviewPrompt = `{"step_index": 48, "source": "USER_EXPLICIT", "type": "USER_INPUT", "status": "DONE", "created_at": "2026-10-05T09:37:24Z", "content": "<USER_REQUEST>\n[Approved] implementation_plan.md\n</USER_REQUEST>\n<ADDITIONAL_METADATA>\nThe current local time is: 2026-10-05T10:37:24+01:00.\n</ADDITIONAL_METADATA>"}`

func agyArtifactSession(t *testing.T, lines ...string) (*AntigravitySource, *agyKeys, string, string) {
	t.Helper()
	home := t.TempDir()
	writeAntigravityConversation(t, home, agConversation, lines...)
	brain := agyBrain(t, home)
	pane := tmux.Session{Name: "agentman-antigravity-1-a", PanePID: 900, Cwd: "/work/ws1"}
	s := newTestAntigravity(t, home, 900, map[int]antigravityProcess{
		900: {cwd: "/work/ws1", conversations: []string{agConversation}},
	}, pane)
	keys := &agyKeys{panes: []string{"? for shortcuts                       plan · Gemini 3.8 Flash · high\n"}}
	s.capturePane = keys.capture
	s.keys = keys.keys(false)
	return s, keys, "antigravity:tmux-agentman-antigravity-1-a", brain
}

func TestAntigravityListsItsArtifacts(t *testing.T) {
	// The plan was approved, then revised: agy asks for review again.
	s, _, id, _ := agyArtifactSession(t, agLinePrompt, agReviewPrompt, agLineReply)
	discovered := discoverAntigravity(t, s)[id]
	if discovered.Artifacts != 3 || discovered.ArtifactsToReview != 1 || discovered.Mode != "plan" {
		t.Errorf("session: artifacts %d to review %d mode %q", discovered.Artifacts,
			discovered.ArtifactsToReview, discovered.Mode)
	}

	artifacts, err := s.Artifacts(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(artifacts))
	for i, artifact := range artifacts {
		names[i] = artifact.Name
	}
	// Newest first; the image has no metadata and dates from its file.
	if strings.Join(names[1:], ",") != "walkthrough.md,implementation_plan.md" || names[0] != "diagram_1772015899190.png" {
		t.Fatalf("names = %q", names)
	}
	plan := artifacts[2]
	if plan.Kind != "plan" || plan.Title != "Implementation Plan: Add README.md" || !plan.Review ||
		plan.MIME != "text/markdown" || plan.UpdatedAt != 1791193061038 ||
		!strings.HasPrefix(plan.Summary, "Implementation plan and task list") {
		t.Errorf("plan = %+v", plan)
	}
	if walk := artifacts[1]; walk.Kind != "walkthrough" || walk.Review {
		t.Errorf("walkthrough = %+v", walk)
	}
	if image := artifacts[0]; image.Kind != "image" || image.MIME != "image/png" {
		t.Errorf("image = %+v", image)
	}
}

// An answer newer than the artifact settles its review.
func TestAntigravityAnsweredArtifactNeedsNoReview(t *testing.T) {
	approved := strings.Replace(agReviewPrompt, "2026-10-05T09:37:24Z", "2026-10-05T09:38:00Z", 1)
	s, _, id, _ := agyArtifactSession(t, agLinePrompt, approved)
	if discovered := discoverAntigravity(t, s)[id]; discovered.ArtifactsToReview != 0 {
		t.Errorf("to review = %d", discovered.ArtifactsToReview)
	}
}

func TestAntigravityOpensOnlyItsOwnArtifacts(t *testing.T) {
	s, _, id, _ := agyArtifactSession(t, agLinePrompt)
	discoverAntigravity(t, s)
	file, info, err := s.OpenArtifact(context.Background(), id, "implementation_plan.md")
	if err != nil || info.Size() == 0 {
		t.Fatalf("open: %v", err)
	}
	file.Close()
	for _, name := range []string{"link.md", "../../settings.json", ".tempmediaStorage", "missing.md",
		"implementation_plan.md.metadata.json", "task.md.resolved.1"} {
		if file, _, err := s.OpenArtifact(context.Background(), id, name); err == nil {
			file.Close()
			t.Errorf("opened %q", name)
		}
	}
}

// A review is the message agy's own review panel sends.
func TestAntigravityReviewsAnArtifactAsItsPanelDoes(t *testing.T) {
	s, keys, id, _ := agyArtifactSession(t, agLinePrompt, agLineReply)
	discoverAntigravity(t, s)
	if err := s.ReviewArtifact(context.Background(), id, "implementation_plan.md", true, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.ReviewArtifact(context.Background(), id, "implementation_plan.md", false, "  use docs/ instead  "); err != nil {
		t.Fatal(err)
	}
	want := "type [Approved] implementation_plan.md;type [Rejected] implementation_plan.md\n\nuse docs/ instead"
	if got := strings.Join(keys.events, ";"); got != want {
		t.Errorf("typed %q", got)
	}
	if err := s.ReviewArtifact(context.Background(), id, "nothing.md", true, ""); err == nil {
		t.Error("reviewed an artifact that is not there")
	}
}

// An ended session from the folder list still has its artifacts.
func TestAntigravityPastSessionHasItsArtifacts(t *testing.T) {
	s, _, _, _ := agyArtifactSession(t, agLinePrompt, agLineReply)
	id := "antigravity:" + agConversation
	s.past.remember(id, s.transcriptPath(agConversation))
	if artifacts, err := s.Artifacts(context.Background(), id); err != nil || len(artifacts) != 3 {
		t.Errorf("artifacts %+v, %v", artifacts, err)
	}
}

// How full the context is, from the newest response's token counts.
func TestAntigravityContextPercent(t *testing.T) {
	usage := `{"step_index": 77, "source": "MODEL", "type": "PLANNER_RESPONSE", "status": "DONE", "created_at": "2026-10-05T09:44:22Z", "input_tokens": 3236, "cache_read_tokens": 32589, "output_tokens": 40, "content": "alpha"}`
	big := strings.Replace(usage, `"input_tokens": 3236`, `"input_tokens": 520000`, 1)
	home := t.TempDir()
	path := writeAntigravityConversation(t, home, agConversation, agLinePrompt, usage, agLinePrompt)
	s, err := NewAntigravitySource(home)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := s.contextPercent(path, "Gemini 3.8 Flash (high)"); !ok || got != 3 {
		t.Errorf("percent = %d %v", got, ok)
	}
	if _, ok := s.contextPercent(path, "Claude Opus 4.6 (Thinking)"); ok {
		t.Error("claimed a window for a model agy never showed one for")
	}
	path = writeAntigravityConversation(t, home, agConversation, agLinePrompt, big)
	if got, _ := s.contextPercent(path, "Gemini 3.8 Flash (High)"); got != 53 {
		t.Errorf("percent = %d", got)
	}
}
