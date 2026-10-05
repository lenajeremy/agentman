package source

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/tmux"
)

// cursorGroupFixture builds the group the way buildRegistry does, over a
// temporary home holding one terminal chat and one ACP record in work.
func cursorGroupFixture(t *testing.T) (*CursorCLIGroup, *Registry, string) {
	t.Helper()
	home := t.TempDir()
	work := filepath.Join(home, "code", "api")
	old := time.Now().Add(-30 * 24 * time.Hour).UnixMilli()
	writeCursorCLIChat(t, home, "hash1", "chat-1", work, "Terminal chat", old)

	terminal, err := NewCursorCLISource(home)
	if err != nil {
		t.Fatal(err)
	}
	terminal.listPanes = func(context.Context) ([]tmux.Session, error) { return nil, nil }
	acp, err := NewCursorACPSource(filepath.Join(home, "state"))
	if err != nil {
		t.Fatal(err)
	}
	native := "aaaaaaaa-1111-2222-3333-444444444444"
	st := &cursorACPState{record: cursorACPRecord{
		NativeID: native, Cwd: work, Name: "Phone chat",
		StartedAt: old - 1000, LastActivityAt: old + 1000,
	}}
	acp.sessions[cursorACPPrefix+native] = st
	if err := acp.save(st); err != nil {
		t.Fatal(err)
	}
	group := NewCursorCLIGroup(terminal, acp)
	registry := NewRegistry()
	registry.Add(group)
	return group, registry, work
}

// The registry holds the group, so history has to be found on the group.
// It once lived only on CursorCLISource, and no Cursor chat ever appeared in
// a folder.
func TestCursorCLIGroupListsBothStoresInAFolder(t *testing.T) {
	_, registry, work := cursorGroupFixture(t)
	sessions, err := registry.InDirectory(context.Background(), work, 0)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]protocol.Session{}
	for _, session := range sessions {
		byID[session.ID] = session
	}
	terminal, ok := byID[cursorCLIChatPrefix+"chat-1"]
	if !ok {
		t.Fatalf("terminal chat missing from the folder: %+v", sessions)
	}
	if terminal.State != protocol.StateEnded || terminal.Inject != protocol.InjectNone {
		t.Errorf("terminal chat = %s/%s, want ended and read-only until resumed", terminal.State, terminal.Inject)
	}
	phone, ok := byID[cursorACPPrefix+"aaaaaaaa-1111-2222-3333-444444444444"]
	if !ok {
		t.Fatalf("a month-old ACP chat vanished from its folder: %+v", sessions)
	}
	// ACP chats take a new turn by loading the session; they must never be
	// sent to the terminal CLI to reopen, which would start an empty chat.
	if phone.Inject != protocol.InjectAPI || phone.Name != "Phone chat" {
		t.Errorf("ACP chat = %+v, want InjectAPI under its own name", phone)
	}

	index, err := registry.Folders(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := index.Under(work).Agents; got != 2 {
		t.Errorf("folder count = %d, want both chats", got)
	}
}

// Ending a pane goes through Registry.EndSession, which asks the owning
// source for its pane. Without the group forwarding it, End always failed.
func TestCursorCLIGroupReportsTheManagedPaneToClose(t *testing.T) {
	group, registry, _ := cursorGroupFixture(t)
	id := cursorCLIPaneIDPrefix + "agentman-cursor-test"
	group.terminal.mu.Lock()
	group.terminal.sessions[id] = cursorCLISession{pane: "agentman-cursor-test"}
	group.terminal.mu.Unlock()

	if pane, ok := registry.PaneFor(id); !ok || pane != "agentman-cursor-test" {
		t.Fatalf("PaneFor = %q, %v; want the managed pane", pane, ok)
	}
	if _, ok := registry.PaneFor(cursorACPPrefix + "aaaaaaaa-1111-2222-3333-444444444444"); ok {
		t.Fatal("an ACP chat claimed a pane; it runs in a child process")
	}
}

// A chat a managed pane has open is published under the pane's id. Past must
// not list it a second time as ended under the chat's id.
func TestCursorCLIPastLeavesOutChatsThatAreLive(t *testing.T) {
	group, _, work := cursorGroupFixture(t)
	past, err := group.terminal.Past(context.Background(), work, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(past) != 1 || past[0].ID != cursorCLIChatPrefix+"chat-1" {
		t.Fatalf("Past = %+v, want the chat under the id Discover gives it", past)
	}

	group.terminal.mu.Lock()
	group.terminal.sessions[cursorCLIPaneIDPrefix+"agentman-cursor-x"] = cursorCLISession{
		meta: protocol.Session{NativeID: "chat-1"}, pane: "agentman-cursor-x",
	}
	group.terminal.mu.Unlock()
	past, err = group.terminal.Past(context.Background(), work, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(past) != 0 {
		t.Fatalf("a live chat was also listed as ended: %+v", past)
	}
}

// A turn in progress is a live session; history is only what is not running.
func TestCursorACPPastLeavesOutARunningTurn(t *testing.T) {
	group, _, work := cursorGroupFixture(t)
	for _, st := range group.acp.states() {
		st.mu.Lock()
		st.busy = true
		st.mu.Unlock()
	}
	past, err := group.acp.Past(context.Background(), work, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(past) != 0 {
		t.Fatalf("a running ACP turn was reported as ended: %+v", past)
	}
}
