package source

import (
	"context"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// historySource is an adapter with a fixed history.
type historySource struct {
	plainSource
	past []protocol.Session
}

func (h historySource) Past(context.Context, string, int) ([]protocol.Session, error) {
	return h.past, nil
}

func (historySource) Directories(context.Context) ([]protocol.Folder, error) { return nil, nil }

// Kiro, Codex and Antigravity key a session running in an Agentman pane on
// the pane, while its transcript on disk carries the agent's own id. Matching
// on id alone listed the one conversation twice: once live, once as history.
func TestAFolderListsAPaneKeyedLiveSessionOnce(t *testing.T) {
	dir := "/work/app"
	registry := NewRegistry()
	registry.Add(historySource{plainSource: plainSource{kind: protocol.KindKiro}, past: []protocol.Session{
		{ID: "kiro:abc", Kind: protocol.KindKiro, NativeID: "abc", Cwd: dir, State: protocol.StateEnded},
		{ID: "kiro:old", Kind: protocol.KindKiro, NativeID: "old", Cwd: dir, State: protocol.StateEnded},
	}})
	registry.Add(historySource{plainSource: plainSource{kind: protocol.KindCodex}, past: []protocol.Session{
		// The same native id under another agent is another conversation.
		{ID: "codex:abc", Kind: protocol.KindCodex, NativeID: "abc", Cwd: dir, State: protocol.StateEnded},
	}})
	registry.mu.Lock()
	registry.last[protocol.KindKiro] = []protocol.Session{{
		ID: "kiro:tmux-agentman-kiro-1-a", Kind: protocol.KindKiro, NativeID: "abc",
		Cwd: dir, State: protocol.StateIdle, Inject: protocol.InjectTmux,
	}}
	registry.mu.Unlock()

	sessions, err := registry.InDirectory(context.Background(), dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, session := range sessions {
		ids[session.ID] = true
	}
	if len(sessions) != 3 || !ids["kiro:tmux-agentman-kiro-1-a"] || !ids["kiro:old"] || !ids["codex:abc"] {
		t.Fatalf("folder listed %v, want the live pane, the other Kiro session and Codex's", ids)
	}
	if ids["kiro:abc"] {
		t.Fatal("the live conversation was listed again as history")
	}
}

// A live session elsewhere is not in this folder's list, so its history here
// is the only row for it and stays.
func TestAFolderKeepsHistoryWhoseLiveSessionIsElsewhere(t *testing.T) {
	registry := NewRegistry()
	registry.Add(historySource{plainSource: plainSource{kind: protocol.KindKiro}, past: []protocol.Session{
		{ID: "kiro:abc", Kind: protocol.KindKiro, NativeID: "abc", Cwd: "/work/app", State: protocol.StateEnded},
	}})
	registry.mu.Lock()
	registry.last[protocol.KindKiro] = []protocol.Session{{
		ID: "kiro:tmux-agentman-kiro-1-a", Kind: protocol.KindKiro, NativeID: "abc", Cwd: "/work/other",
	}}
	registry.mu.Unlock()

	sessions, err := registry.InDirectory(context.Background(), "/work/app", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].ID != "kiro:abc" {
		t.Fatalf("folder listed %+v", sessions)
	}
}
