package source

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/tmux"
)

// An image from the phone goes into the conversation's .user_uploaded folder,
// which agy reads without asking; from ~/.agentman it asked every time.
func TestAntigravityPlacesAnImageWhereAgyReadsItWithoutAsking(t *testing.T) {
	home := t.TempDir()
	writeAntigravityConversation(t, home, agConversation, agLinePrompt, agLineReply)
	pane := tmux.Session{Name: "agentman-antigravity-1-a", PanePID: 900, Cwd: "/work/api"}
	s := newTestAntigravity(t, home, 900, map[int]antigravityProcess{
		900: {cwd: "/work/api", conversations: []string{agConversation}},
	}, pane)
	id := "antigravity:tmux-agentman-antigravity-1-a"
	discoverAntigravity(t, s)

	saved := filepath.Join(t.TempDir(), "k3x9.png")
	if err := os.WriteFile(saved, []byte("\x89PNG fake"), 0o600); err != nil {
		t.Fatal(err)
	}
	uploads := filepath.Join(home, ".gemini", "antigravity-cli", "brain", agConversation, ".user_uploaded")
	placed, err := s.PlaceAttachment(context.Background(), id, saved)
	if err != nil || placed != filepath.Join(uploads, "k3x9.png") {
		t.Fatalf("placed %q, %v", placed, err)
	}
	if raw, _ := os.ReadFile(placed); string(raw) != "\x89PNG fake" {
		t.Errorf("copied %q", raw)
	}
	// The same name again is a second file, not an overwrite.
	again, err := s.PlaceAttachment(context.Background(), id, saved)
	if err != nil || again != filepath.Join(uploads, "k3x9-1.png") {
		t.Errorf("second copy %q, %v", again, err)
	}
	if strings.ContainsAny(placed, "\n\r\t") || !filepath.IsAbs(placed) {
		t.Errorf("not one absolute path: %q", placed)
	}
}

// Before agy has opened a conversation there is no folder to copy into.
func TestAntigravityLeavesAnImageWhereItIsBeforeAConversation(t *testing.T) {
	pane := tmux.Session{Name: "agentman-antigravity-1-a", PanePID: 900, Cwd: "/work/new"}
	s := newTestAntigravity(t, t.TempDir(), 900, map[int]antigravityProcess{900: {cwd: "/work/new"}}, pane)
	discoverAntigravity(t, s)
	saved := filepath.Join(t.TempDir(), "k3x9.png")
	if err := os.WriteFile(saved, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	if placed, err := s.PlaceAttachment(context.Background(), "antigravity:tmux-agentman-antigravity-1-a", saved); err != nil || placed != saved {
		t.Errorf("placed %q, %v", placed, err)
	}
}

// A link where the copy is going is not written through.
func TestAntigravityAttachmentNeverWritesThroughALink(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(t.TempDir(), "victim")
	if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(dir, "shot.png")); err != nil {
		t.Fatal(err)
	}
	saved := filepath.Join(t.TempDir(), "shot.png")
	if err := os.WriteFile(saved, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	placed, err := copyAntigravityAttachment(saved, dir)
	if err != nil || placed != filepath.Join(dir, "shot-1.png") {
		t.Errorf("placed %q, %v", placed, err)
	}
	if raw, _ := os.ReadFile(victim); string(raw) != "keep" {
		t.Errorf("wrote through the link: %q", raw)
	}
}
