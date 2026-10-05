package daemon

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/source"
)

// artifactAgent stands in for an adapter whose agent writes artifacts into one
// directory, confined the way the interface asks: a top-level regular file,
// opened only after Lstat says it is one.
type artifactAgent struct {
	streamingSource
	dir  string
	list []protocol.Artifact

	mu      sync.Mutex
	reviews []string
}

func (*artifactAgent) Kind() protocol.Kind { return protocol.KindAntigravity }

func (a *artifactAgent) Artifacts(context.Context, string) ([]protocol.Artifact, error) {
	return a.list, nil
}

func (a *artifactAgent) OpenArtifact(_ context.Context, _, name string) (*os.File, os.FileInfo, error) {
	path := filepath.Join(a.dir, name)
	info, err := os.Lstat(path)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, errors.New("not an artifact")
	}
	file, err := os.Open(path)
	return file, info, err
}

func (a *artifactAgent) ReviewArtifact(_ context.Context, _, name string, approve bool, comment string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	verdict := "[Rejected] "
	if approve {
		verdict = "[Approved] "
	}
	a.reviews = append(a.reviews, verdict+name+"|"+comment)
	return nil
}

func (a *artifactAgent) reviewed() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.reviews...)
}

func artifactDaemon(t *testing.T) (*Daemon, *artifactAgent) {
	t.Helper()
	dir := t.TempDir()
	write(t, dir, "implementation_plan.md", "# Plan\n\n1. Read the code\n")
	shot, err := os.Create(filepath.Join(dir, "screenshot.png"))
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(shot, image.NewRGBA(image.Rect(0, 0, 4, 3))); err != nil {
		t.Fatal(err)
	}
	shot.Close()
	if err := os.WriteFile(filepath.Join(dir, "recording.webm"), []byte{0x1a, 0x45, 0xdf, 0xa3, 0, 0, 0, 1}, 0o600); err != nil {
		t.Fatal(err)
	}
	agent := &artifactAgent{dir: dir, list: []protocol.Artifact{
		{Name: "implementation_plan.md", Kind: "plan", Summary: "Three steps", UpdatedAt: 2, Size: 30, Review: true},
		{Name: "screenshot.png", Kind: "image", MIME: "image/png", UpdatedAt: 1},
	}}
	d, _ := agentWithSource(t, agent)
	return d, agent
}

func TestArtifactsAreListedForTheSession(t *testing.T) {
	d, _ := artifactDaemon(t)
	event := d.Handle(context.Background(), protocol.Request{
		Type: protocol.ReqListArtifacts, SessionID: "antigravity:c1",
	})
	if event.Type != protocol.EvtArtifacts || event.SessionID != "antigravity:c1" {
		t.Fatalf("list = %+v", event)
	}
	if len(event.Artifacts) != 2 || event.Artifacts[0].Name != "implementation_plan.md" ||
		!event.Artifacts[0].Review {
		t.Fatalf("artifacts = %+v", event.Artifacts)
	}
}

// "None" is an answer, and has to arrive as one: [] rather than a missing field.
func TestASessionWithoutArtifactsListsAnEmptyArray(t *testing.T) {
	d, agent := artifactDaemon(t)
	agent.list = nil
	event := d.Handle(context.Background(), protocol.Request{
		Type: protocol.ReqListArtifacts, SessionID: "antigravity:c1",
	})
	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"artifacts":[]`) {
		t.Fatalf("an empty list went out as %s", encoded)
	}

	// An agent that has no artifacts at all answers the same way.
	plain, _ := agentWithSource(t, &injectingSource{})
	event = plain.Handle(context.Background(), protocol.Request{
		Type: protocol.ReqListArtifacts, SessionID: "claude:s1",
	})
	if event.Type != protocol.EvtArtifacts || event.Artifacts == nil || len(event.Artifacts) != 0 {
		t.Fatalf("an agent without artifacts answered %+v", event)
	}
}

// An adapter's list is held to what the phone accepts. A name the phone could
// not send back is dropped rather than renamed, because a renamed one would
// open something else.
func TestArtifactListIsBoundedForThePhone(t *testing.T) {
	d, agent := artifactDaemon(t)
	agent.list = []protocol.Artifact{
		{Name: "../escape.md", Kind: "plan"},
		{Name: "nested/plan.md", Kind: "plan"},
		{Name: "a..b.md", Kind: "plan"},
		{Name: "bell\a.md", Kind: "plan"},
		{Name: strings.Repeat("n", 256), Kind: "plan"},
		{Name: "kept.md", Kind: "  ", Summary: strings.Repeat("s", 10_000),
			Title: strings.Repeat("t", 5_000), MIME: strings.Repeat("m", 200), UpdatedAt: -5, Size: -1},
	}
	for i := range 300 {
		agent.list = append(agent.list, protocol.Artifact{Name: "many-" + strings.Repeat("x", i%3) + ".md"})
	}
	event := d.Handle(context.Background(), protocol.Request{
		Type: protocol.ReqListArtifacts, SessionID: "antigravity:c1",
	})
	if len(event.Artifacts) != maxWireArtifacts {
		t.Fatalf("listed %d artifacts, want the cap of %d", len(event.Artifacts), maxWireArtifacts)
	}
	kept := event.Artifacts[0]
	if kept.Name != "kept.md" {
		t.Fatalf("an unsafe name survived: %q", kept.Name)
	}
	if kept.Kind != "file" || len(kept.Summary) > maxWireArtifactSummary ||
		len(kept.Title) > maxWireArtifactTitle || kept.MIME != "" || kept.UpdatedAt != 0 || kept.Size != 0 {
		t.Errorf("artifact was not bounded: kind=%q summary=%d title=%d mime=%q updated=%d size=%d",
			kept.Kind, len(kept.Summary), len(kept.Title), kept.MIME, kept.UpdatedAt, kept.Size)
	}
}

func TestReadingAMarkdownArtifactSaysItIsMarkdown(t *testing.T) {
	d, _ := artifactDaemon(t)
	event := d.Handle(context.Background(), protocol.Request{
		Type: protocol.ReqReadArtifact, SessionID: "antigravity:c1", Path: "implementation_plan.md",
	})
	if event.Type != protocol.EvtWorkspace || event.Workspace == nil {
		t.Fatalf("read = %+v", event)
	}
	result := event.Workspace
	if result.Kind != "artifact" || result.Path != "implementation_plan.md" ||
		result.MIME != "text/markdown" || !strings.HasPrefix(result.Text, "# Plan") {
		t.Fatalf("artifact read as %+v", result)
	}
}

// Images reuse the workspace preview, so they arrive described and sized for
// a phone exactly as a workspace image does.
func TestReadingAnImageArtifactSendsAPreview(t *testing.T) {
	d, _ := artifactDaemon(t)
	event := d.Handle(context.Background(), protocol.Request{
		Type: protocol.ReqReadArtifact, SessionID: "antigravity:c1", Path: "screenshot.png",
	})
	if event.Type != protocol.EvtWorkspace {
		t.Fatalf("read = %+v", event)
	}
	result := event.Workspace
	if result.Kind != "artifact" || result.MIME != "image/png" || result.Text != "" || result.Source == nil ||
		result.Source.Width != 4 || result.Source.Height != 3 {
		t.Fatalf("image artifact read as %+v", result)
	}
	if _, err := base64.StdEncoding.DecodeString(result.Image); err != nil {
		t.Fatal(err)
	}
}

func TestBinaryArtifactsAreRefusedLikeWorkspaceFiles(t *testing.T) {
	d, _ := artifactDaemon(t)
	event := d.Handle(context.Background(), protocol.Request{
		Type: protocol.ReqReadArtifact, SessionID: "antigravity:c1", Path: "recording.webm",
	})
	if event.Type != protocol.EvtError || !strings.Contains(event.Error, "binary") {
		t.Fatalf("a recording was read as %+v", event)
	}
}

// The name comes back from the phone; nothing that could name another place
// reaches the adapter.
func TestArtifactNamesThatCouldLeaveTheirFolderNeverReachTheAdapter(t *testing.T) {
	d, agent := artifactDaemon(t)
	outside := filepath.Join(filepath.Dir(agent.dir), "outside.md")
	write(t, filepath.Dir(agent.dir), "outside.md", "secret")
	t.Cleanup(func() { os.Remove(outside) })

	for _, name := range []string{
		"", "../outside.md", "..", "a/b.md", `a\b.md`, "/etc/passwd", "plan.md\x00", "plan\n.md",
		"x..md", strings.Repeat("p", 256),
	} {
		event := d.Handle(context.Background(), protocol.Request{
			Type: protocol.ReqReadArtifact, SessionID: "antigravity:c1", Path: name,
		})
		if event.Type != protocol.EvtError {
			t.Errorf("read of %q answered %+v", name, event)
		}
		event = d.Handle(context.Background(), protocol.Request{
			Type: protocol.ReqReviewArtifact, SessionID: "antigravity:c1", Path: name, Approve: true, ClientID: "r",
		})
		if event.Type != protocol.EvtSendResult || event.Status != protocol.StatusFailed || event.ClientID != "r" {
			t.Errorf("review of %q answered %+v", name, event)
		}
	}
	if got := agent.reviewed(); len(got) != 0 {
		t.Fatalf("unsafe names reached the adapter: %q", got)
	}
}

func TestReviewingAnArtifactReachesTheAgentAndAnswersLikeASend(t *testing.T) {
	d, agent := artifactDaemon(t)
	approve := d.Handle(context.Background(), protocol.Request{
		Type: protocol.ReqReviewArtifact, SessionID: "antigravity:c1", ClientID: "review-1",
		Path: "implementation_plan.md", Approve: true,
	})
	if approve.Type != protocol.EvtSendResult || approve.Status != protocol.StatusDelivered ||
		approve.ClientID != "review-1" {
		t.Fatalf("approve = %+v", approve)
	}
	changes := d.Handle(context.Background(), protocol.Request{
		Type: protocol.ReqReviewArtifact, SessionID: "antigravity:c1", ClientID: "review-2",
		Path: "implementation_plan.md", Text: "  Split step 2.\nKeep the tests.  ",
	})
	if changes.Status != protocol.StatusDelivered {
		t.Fatalf("request changes = %+v", changes)
	}
	got := agent.reviewed()
	want := []string{"[Approved] implementation_plan.md|", "[Rejected] implementation_plan.md|Split step 2.\nKeep the tests."}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("reviews reached the agent as %q, want %q", got, want)
	}
}

// A review is typed into the session, so a pending question would receive it
// instead of the prompt, and escape sequences in a comment must never reach
// a terminal.
func TestReviewIsHeldToTheRulesOfASend(t *testing.T) {
	d, agent := artifactDaemon(t)
	if !requestMutatesSession(protocol.ReqReviewArtifact) {
		t.Error("a review does not take the session's action lock")
	}

	bad := d.Handle(context.Background(), protocol.Request{
		Type: protocol.ReqReviewArtifact, SessionID: "antigravity:c1", ClientID: "r",
		Path: "implementation_plan.md", Text: "fine\x1b[2J",
	})
	if bad.Status != protocol.StatusFailed {
		t.Errorf("a comment with a terminal escape was accepted: %+v", bad)
	}

	d.mu.Lock()
	d.sessions["antigravity:c1"] = protocol.Session{
		ID: "antigravity:c1", Kind: protocol.KindAntigravity,
		Question: &protocol.Question{ID: "q", Prompt: "Run it?", Options: []protocol.QuestionOption{{Key: "1", Label: "Yes"}}},
	}
	d.mu.Unlock()
	blocked := d.Handle(context.Background(), protocol.Request{
		Type: protocol.ReqReviewArtifact, SessionID: "antigravity:c1", ClientID: "r",
		Path: "implementation_plan.md", Approve: true,
	})
	if blocked.Status != protocol.StatusFailed || !strings.Contains(blocked.Error, "pending question") {
		t.Errorf("a review during a question answered %+v", blocked)
	}
	if got := agent.reviewed(); len(got) != 0 {
		t.Fatalf("reviews reached the agent anyway: %q", got)
	}
}

func TestReadingAnArtifactOfAnAgentWithoutAnyFails(t *testing.T) {
	d, _ := agentWithSource(t, &injectingSource{})
	event := d.Handle(context.Background(), protocol.Request{
		Type: protocol.ReqReadArtifact, SessionID: "claude:s1", Path: "plan.md",
	})
	if event.Type != protocol.EvtError {
		t.Fatalf("read = %+v", event)
	}
}

var _ source.ArtifactSource = (*artifactAgent)(nil)
