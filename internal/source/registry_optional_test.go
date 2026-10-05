package source

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// plainSource implements only Source, the floor every adapter meets.
type plainSource struct{ kind protocol.Kind }

func (s plainSource) Kind() protocol.Kind                                { return s.kind }
func (plainSource) Discover(context.Context) ([]protocol.Session, error) { return nil, nil }
func (plainSource) Page(_ context.Context, id, _ string, _ int) (protocol.Page, error) {
	return protocol.NewPage(id, nil, "", false), nil
}
func (plainSource) Follow(context.Context, string, chan<- []protocol.Message) error { return nil }

// fullSource implements every optional interface and records what it was asked.
type fullSource struct {
	plainSource
	dir string

	reviewed    string
	approved    bool
	comment     string
	injected    []string
	declineInto bool
}

func (s *fullSource) Artifacts(_ context.Context, sessionID string) ([]protocol.Artifact, error) {
	return []protocol.Artifact{{Name: "plan.md", Kind: "plan"}}, nil
}

func (s *fullSource) OpenArtifact(_ context.Context, _, name string) (*os.File, os.FileInfo, error) {
	file, err := os.Open(filepath.Join(s.dir, name))
	if err != nil {
		return nil, nil, err
	}
	info, err := file.Stat()
	return file, info, err
}

func (s *fullSource) ReviewArtifact(_ context.Context, _, name string, approve bool, comment string) error {
	s.reviewed, s.approved, s.comment = name, approve, comment
	return nil
}

func (s *fullSource) PlaceAttachment(_ context.Context, _, path string) (string, error) {
	return filepath.Join(s.dir, ".user_uploaded", filepath.Base(path)), nil
}

func (s *fullSource) InjectWithAttachments(
	_ context.Context, _, text string, paths []string,
) (protocol.InjectMode, error) {
	if s.declineInto {
		return protocol.InjectNone, ErrAttachmentsAsPaths
	}
	s.injected = append([]string{text}, paths...)
	return protocol.InjectAPI, nil
}

func (s *fullSource) ResumedSession(native, defaultPane string) (string, string) {
	return defaultPane, string(s.kind) + ":tmux-" + defaultPane
}

func optionalRegistry(t *testing.T) (*Registry, *fullSource) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "plan.md"), []byte("# Plan"), 0o600); err != nil {
		t.Fatal(err)
	}
	full := &fullSource{plainSource: plainSource{kind: protocol.KindAntigravity}, dir: dir}
	registry := NewRegistry()
	registry.Add(full)
	registry.Add(plainSource{kind: protocol.KindCodex})
	return registry, full
}

func TestRegistryRoutesArtifactsToTheOwningAdapter(t *testing.T) {
	registry, full := optionalRegistry(t)
	ctx := context.Background()

	list, err := registry.Artifacts(ctx, "antigravity:c1")
	if err != nil || len(list) != 1 || list[0].Name != "plan.md" {
		t.Fatalf("artifacts = %+v, %v", list, err)
	}
	file, info, err := registry.OpenArtifact(ctx, "antigravity:c1", "plan.md")
	if err != nil {
		t.Fatal(err)
	}
	file.Close()
	if info.Size() != int64(len("# Plan")) {
		t.Errorf("opened %d bytes, want the plan", info.Size())
	}
	if err := registry.ReviewArtifact(ctx, "antigravity:c1", "plan.md", false, "split step 2"); err != nil {
		t.Fatal(err)
	}
	if full.reviewed != "plan.md" || full.approved || full.comment != "split step 2" {
		t.Errorf("review reached the adapter as %q approve=%v comment=%q", full.reviewed, full.approved, full.comment)
	}
}

// An agent that writes no artifacts has none, which is an answer rather than
// a failure; reading or reviewing one is the failure.
func TestRegistryArtifactsOfAnAdapterWithout(t *testing.T) {
	registry, _ := optionalRegistry(t)
	ctx := context.Background()

	list, err := registry.Artifacts(ctx, "codex:x")
	if err != nil || list == nil || len(list) != 0 {
		t.Fatalf("artifacts of an adapter without any = %#v, %v; want an empty list", list, err)
	}
	if _, _, err := registry.OpenArtifact(ctx, "codex:x", "plan.md"); err == nil {
		t.Error("an adapter without artifacts opened one")
	}
	if err := registry.ReviewArtifact(ctx, "codex:x", "plan.md", true, ""); err == nil {
		t.Error("an adapter without artifacts accepted a review")
	}
	if _, err := registry.Artifacts(ctx, "gemini:x"); err == nil {
		t.Error("a kind with no adapter listed artifacts")
	}
}

func TestRegistryPlacesAttachmentsOnlyWhereTheAdapterAsks(t *testing.T) {
	registry, full := optionalRegistry(t)
	ctx := context.Background()

	placed, err := registry.PlaceAttachment(ctx, "antigravity:c1", "/saved/shot.png")
	if err != nil || placed != filepath.Join(full.dir, ".user_uploaded", "shot.png") {
		t.Fatalf("placed = %q, %v", placed, err)
	}
	placed, err = registry.PlaceAttachment(ctx, "codex:x", "/saved/shot.png")
	if err != nil || placed != "/saved/shot.png" {
		t.Fatalf("an adapter with no preference moved the image to %q, %v", placed, err)
	}
}

func TestRegistryInjectsAttachmentsStructuredOnlyWhenTheAdapterCan(t *testing.T) {
	registry, full := optionalRegistry(t)
	ctx := context.Background()

	mode, handled, err := registry.InjectWithAttachments(ctx, "antigravity:c1", "look", []string{"/a.png"})
	if err != nil || !handled || mode != protocol.InjectAPI {
		t.Fatalf("structured injection = %q handled=%v %v", mode, handled, err)
	}
	if len(full.injected) != 2 || full.injected[0] != "look" || full.injected[1] != "/a.png" {
		t.Errorf("adapter received %q", full.injected)
	}

	full.declineInto = true
	if _, handled, err := registry.InjectWithAttachments(ctx, "antigravity:c1", "look", []string{"/a.png"}); handled || err != nil {
		t.Errorf("a session declined with ErrAttachmentsAsPaths was reported handled=%v %v", handled, err)
	}
	if _, handled, err := registry.InjectWithAttachments(ctx, "codex:x", "look", []string{"/a.png"}); handled || err != nil {
		t.Errorf("an adapter without structured images was reported handled=%v %v", handled, err)
	}
}

func TestRegistryAsksTheOwningAdapterToNameAResume(t *testing.T) {
	registry, _ := optionalRegistry(t)

	pane, id := registry.ResumedSession("antigravity:conv-1", "conv-1", "agentman-antigravity-1-a")
	if pane != "agentman-antigravity-1-a" || id != "antigravity:tmux-agentman-antigravity-1-a" {
		t.Fatalf("resume named %q / %q", pane, id)
	}
	if pane, id := registry.ResumedSession("codex:x", "x", "agentman-codex-1-a"); pane != "" || id != "" {
		t.Errorf("an adapter without a namer named the resume %q / %q", pane, id)
	}
	if pane, id := registry.ResumedSession("malformed", "x", "p"); pane != "" || id != "" {
		t.Errorf("a malformed id named the resume %q / %q", pane, id)
	}
}
