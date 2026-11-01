package search

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// fakeAgents is conversations held in memory, paged backward as adapters
// page them.
type fakeAgents struct {
	mu       sync.Mutex
	sessions map[string]protocol.Session
	messages map[string][]protocol.Message
	live     []protocol.Session
	pages    int
}

func newFakeAgents() *fakeAgents {
	return &fakeAgents{sessions: map[string]protocol.Session{}, messages: map[string][]protocol.Message{}}
}

func (f *fakeAgents) say(id, cwd string, at int64, role protocol.Role, text string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	session := f.sessions[id]
	session.ID, session.Kind, session.NativeID = id, protocol.KindClaude, id[len("claude:"):]
	session.Cwd, session.Name, session.State = cwd, "talk "+id, protocol.StateEnded
	session.LastActivityAt = at
	f.sessions[id] = session
	n := len(f.messages[id])
	f.messages[id] = append(f.messages[id], protocol.Message{
		ID: fmt.Sprintf("m%d", n), SessionID: id, Role: role, Ts: at, Text: text,
	})
}

func (f *fakeAgents) Folders(context.Context) ([]protocol.Folder, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	latest := map[string]int64{}
	for _, s := range f.sessions {
		latest[s.Cwd] = max(latest[s.Cwd], s.LastActivityAt)
	}
	var folders []protocol.Folder
	for path, at := range latest {
		folders = append(folders, protocol.Folder{Path: path, LastActivityAt: at})
	}
	return folders, nil
}

func (f *fakeAgents) Past(_ context.Context, dir string, _ int) ([]protocol.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var found []protocol.Session
	for _, s := range f.sessions {
		if under(s.Cwd, dir) {
			found = append(found, s)
		}
	}
	return found, nil
}

func (f *fakeAgents) Live() []protocol.Session {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]protocol.Session(nil), f.live...)
}

func (f *fakeAgents) Page(_ context.Context, id, before string, limit int) (protocol.Page, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pages++
	all := f.messages[id]
	end := len(all)
	if before != "" {
		end, _ = strconv.Atoi(before)
	}
	start := max(0, end-limit)
	page := protocol.Page{SessionID: id, Messages: append([]protocol.Message(nil), all[start:end]...)}
	if start > 0 {
		page.HasMore, page.NextCursor = true, strconv.Itoa(start)
	}
	return page, nil
}

func newIndexer(t *testing.T, agents *fakeAgents) *Indexer {
	t.Helper()
	index, err := Open(filepath.Join(t.TempDir(), "search.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { index.Close() })
	return NewIndexer(index, agents, t.Logf)
}

func TestSearchFindsWhatWasSaidAndWhere(t *testing.T) {
	ctx := context.Background()
	agents := newFakeAgents()
	now := time.Now().UnixMilli()
	agents.say("claude:a", "/src/app", now-5000, protocol.RoleUser, "Can you notarize the build?")
	agents.say("claude:a", "/src/app", now-4000, protocol.RoleAssistant, "Notarizing the DMG now.")
	agents.say("claude:b", "/src/app/mobile", now-3000, protocol.RoleUser, "Why does tmux keep dying?")
	agents.say("claude:c", "/elsewhere", now-2000, protocol.RoleAssistant, "Ran tmux attach -t work.")
	// A tool's output is not conversation.
	agents.messages["claude:c"] = append(agents.messages["claude:c"], protocol.Message{
		ID: "tool", Role: protocol.RoleTool, Ts: now, Text: "notarize notarize", Tool: &protocol.Tool{Name: "Bash"},
	})
	x := newIndexer(t, agents)
	read, err := x.Sweep(ctx)
	if err != nil || read != 3 {
		t.Fatalf("read %d, %v", read, err)
	}

	hits, err := x.Search(ctx, Query{Text: "tmux"})
	if err != nil || len(hits) != 2 {
		t.Fatalf("tmux: %+v, %v", hits, err)
	}
	if hits[0].Session.ID != "claude:c" || hits[0].Message.ID != "m0" || hits[0].Session.Cwd != "/elsewhere" {
		t.Fatalf("the newer conversation first: %+v", hits[0])
	}

	hits, _ = x.Search(ctx, Query{Text: "notariz"})
	if len(hits) != 1 || hits[0].Matches != 2 || hits[0].Message.Role != protocol.RoleUser {
		t.Fatalf("one conversation, both messages, the typed one shown: %+v", hits)
	}
	if hits[0].Session.State != protocol.StateEnded || hits[0].Snippet != "Can you notarize the build?" {
		t.Fatalf("hit %+v", hits[0])
	}

	inOne, _ := x.Search(ctx, Query{Text: "notariz", Key: "claude:a"})
	if len(inOne) != 2 || inOne[0].Message.ID != "m0" || inOne[1].Message.ID != "m1" {
		t.Fatalf("every match in one conversation, oldest first: %+v", inOne)
	}
	if hits, _ := x.Search(ctx, Query{Text: "to"}); hits != nil {
		t.Fatalf("too short to look up: %+v", hits)
	}
}

// After the first sweep only what was added is read, and a conversation
// running now is searched up to its newest message and returned as it runs.
func TestLaterSweepsReadOnlyWhatChanged(t *testing.T) {
	ctx := context.Background()
	agents := newFakeAgents()
	now := time.Now().UnixMilli()
	for i := range 250 {
		agents.say("claude:long", "/src", now-int64(1000-i), protocol.RoleAssistant, fmt.Sprintf("step %d done", i))
	}
	agents.say("claude:other", "/src", now-500, protocol.RoleUser, "unrelated")
	x := newIndexer(t, agents)
	if _, err := x.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if _, messages, _ := x.Index().Counts(ctx); messages != 251 {
		t.Fatalf("messages %d", messages)
	}

	agents.pages = 0
	if read, _ := x.Sweep(ctx); read != 0 || agents.pages != 0 {
		t.Fatalf("nothing changed: read %d with %d pages", read, agents.pages)
	}

	agents.say("claude:long", "/src", now, protocol.RoleUser, "now deploy the relay")
	live := agents.sessions["claude:long"]
	live.State = protocol.StateBusy
	agents.live = []protocol.Session{live}
	hits, err := x.Search(ctx, Query{Text: "deploy relay"})
	if err != nil || len(hits) != 1 || hits[0].Message.ID != "m250" {
		t.Fatalf("the newest message, read just before searching: %+v, %v", hits, err)
	}
	if hits[0].Session.State != protocol.StateBusy {
		t.Fatalf("returned as it runs: %+v", hits[0].Session)
	}
	if agents.pages != 1 {
		t.Fatalf("one page read to catch up, not %d", agents.pages)
	}
}

// A conversation whose first reading was cut short is read again to its
// start, not just its newest end.
func TestAnInterruptedReadIsFinished(t *testing.T) {
	ctx := context.Background()
	agents := newFakeAgents()
	now := time.Now().UnixMilli()
	for i := range 150 {
		agents.say("claude:x", "/src", now-int64(1000-i), protocol.RoleUser, fmt.Sprintf("line %d", i))
	}
	x := newIndexer(t, agents)
	page, _ := agents.Page(ctx, "claude:x", "", pageSize)
	if err := x.Index().add(ctx, agents.sessions["claude:x"], page.Messages); err != nil {
		t.Fatal(err)
	}
	if _, err := x.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if _, messages, _ := x.Index().Counts(ctx); messages != 150 {
		t.Fatalf("messages %d", messages)
	}
}

func TestOnlyTheOutermostFoldersAreListed(t *testing.T) {
	roots := rootFolders([]protocol.Folder{
		{Path: "/src/app/mobile", LastActivityAt: 9},
		{Path: "/src/app", LastActivityAt: 3},
		{Path: "/src/app-old", LastActivityAt: 1},
		{Path: "/tmp/x/", LastActivityAt: 2},
	})
	want := []protocol.Folder{{Path: "/src/app", LastActivityAt: 9}, {Path: "/src/app-old", LastActivityAt: 1}, {Path: "/tmp/x", LastActivityAt: 2}}
	if fmt.Sprint(roots) != fmt.Sprint(want) {
		t.Fatalf("roots %v", roots)
	}
}
