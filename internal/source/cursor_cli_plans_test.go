package source

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

func writePlan(t *testing.T, home, name, body string, age time.Duration) string {
	t.Helper()
	dir := filepath.Join(home, ".cursor", "plans")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-age)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
	return path
}

// The plans Cursor wrote for a chat, as it writes them: named after the plan
// and the chat, opening with a comment that names the whole chat id.
func TestCursorPlansAreAChatsArtifacts(t *testing.T) {
	group, _, _ := cursorGroupFixture(t)
	home := group.terminal.home
	writePlan(t, home, "Create goodbyetxt-chat-1.plan.md",
		"<!-- chat-1 -->\n# Create goodbye.txt\n\nWrite goodbye.txt with contents: `bye`.\n", time.Hour)
	writePlan(t, home, "Second pass-chat-1.plan.md",
		"<!-- chat-1 -->\n# Second pass\n\n## Steps\n\nTidy up.\n", time.Minute)
	writePlan(t, home, "Other-chat-2.plan.md", "<!-- chat-2 -->\n# Someone else's\n", time.Minute)
	writePlan(t, home, "Loose.plan.md", "# No owner\n", time.Minute)
	if err := os.Symlink("/etc/hosts", filepath.Join(home, ".cursor", "plans", "Sneaky-chat-1.plan.md")); err != nil {
		t.Fatal(err)
	}

	artifacts, err := group.Artifacts(context.Background(), cursorCLIChatPrefix+"chat-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 2 {
		t.Fatalf("artifacts = %+v, want the chat's two plans", artifacts)
	}
	newest := artifacts[0]
	if newest.Name != "Second pass-chat-1.plan.md" || newest.Kind != "plan" || newest.Title != "Second pass" ||
		newest.Summary != "Tidy up." || newest.MIME != "text/markdown" || newest.Review {
		t.Errorf("newest = %+v", newest)
	}
	if artifacts[1].Title != "Create goodbye.txt" || artifacts[1].Summary != "Write goodbye.txt with contents: `bye`." {
		t.Errorf("older = %+v", artifacts[1])
	}

	file, info, err := group.OpenArtifact(context.Background(), cursorCLIChatPrefix+"chat-1", "Create goodbyetxt-chat-1.plan.md")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(file)
	file.Close()
	if info.Size() != int64(len(body)) || string(body[:15]) != "<!-- chat-1 -->" {
		t.Fatalf("opened %q", body)
	}
	for _, name := range []string{"Other-chat-2.plan.md", "Sneaky-chat-1.plan.md", "../plans/Other-chat-2.plan.md", "Loose.plan.md"} {
		if file, _, err := group.OpenArtifact(context.Background(), cursorCLIChatPrefix+"chat-1", name); err == nil {
			file.Close()
			t.Errorf("%s opened for chat-1", name)
		}
	}
	if err := group.ReviewArtifact(context.Background(), cursorCLIChatPrefix+"chat-1", artifacts[0].Name, true, ""); err == nil {
		t.Error("a plan was approved outside Cursor's own question")
	}
}

// Discovery counts each session's plans, for the phone's Artifacts button.
func TestCursorPlansAreCountedInDiscovery(t *testing.T) {
	_, source, _ := cursorCLIFixture(t)
	group := NewCursorCLIGroup(source, mustACP(t))
	writePlan(t, source.home, "A-chat-123.plan.md", "<!-- chat-123 -->\n# A\n", time.Minute)
	sessions, err := group.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Artifacts != 1 {
		t.Fatalf("sessions = %+v", sessions)
	}
	if artifacts, _ := group.Artifacts(context.Background(), sessions[0].ID); len(artifacts) != 1 {
		t.Fatalf("listed %+v", artifacts)
	}
	_ = protocol.KindCursorCLI
}

func mustACP(t *testing.T) *CursorACPSource {
	t.Helper()
	acp, err := NewCursorACPSource(filepath.Join(t.TempDir(), "state"))
	if err != nil {
		t.Fatal(err)
	}
	return acp
}
