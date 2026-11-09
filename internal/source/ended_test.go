package source

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

type endTestSource struct {
	Source
	sessions []protocol.Session
	kind     protocol.Kind
}

func (s *endTestSource) Kind() protocol.Kind {
	if s.kind != "" {
		return s.kind
	}
	return protocol.KindMuse
}
func (s *endTestSource) Discover(context.Context) ([]protocol.Session, error) {
	return append([]protocol.Session(nil), s.sessions...), nil
}

func listedIDs(t *testing.T, r *Registry) []string {
	t.Helper()
	sessions, err := r.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(sessions))
	for _, session := range sessions {
		ids = append(ids, session.ID)
	}
	return ids
}

func endRegistry(t *testing.T, sessions ...protocol.Session) (*Registry, *endTestSource) {
	t.Helper()
	source := &endTestSource{sessions: sessions}
	registry := NewRegistry()
	registry.Add(source)
	listedIDs(t, registry)
	return registry, source
}

func museIdle(id string, activity int64) protocol.Session {
	return protocol.Session{ID: "muse:" + id, Kind: protocol.KindMuse, State: protocol.StateIdle, LastActivityAt: activity}
}

func TestEndingASessionWithNoPaneTakesItOffTheList(t *testing.T) {
	registry, _ := endRegistry(t, museIdle("a", 10), museIdle("b", 20))
	if err := registry.EndSession(context.Background(), "muse:a"); err != nil {
		t.Fatal(err)
	}
	if got := listedIDs(t, registry); len(got) != 1 || got[0] != "muse:b" {
		t.Fatalf("listed %v", got)
	}
}

func TestAnEndedSessionComesBackWhenItDoesSomething(t *testing.T) {
	registry, source := endRegistry(t, museIdle("a", 10))
	if err := registry.EndSession(context.Background(), "muse:a"); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Hour).UnixMilli()
	source.sessions = []protocol.Session{museIdle("a", later)}
	if got := listedIDs(t, registry); len(got) != 1 {
		t.Fatalf("newer activity should list it again: %v", got)
	}
	// And that's the end of it: it isn't hidden again by what came before.
	source.sessions = []protocol.Session{museIdle("a", later)}
	if got := listedIDs(t, registry); len(got) != 1 {
		t.Fatalf("listed %v", got)
	}
}

func TestPickingAnEndedSessionBackUpListsIt(t *testing.T) {
	registry, _ := endRegistry(t, museIdle("a", 10))
	if err := registry.EndSession(context.Background(), "muse:a"); err != nil {
		t.Fatal(err)
	}
	registry.ForgetEnded("muse:a")
	if got := listedIDs(t, registry); len(got) != 1 {
		t.Fatalf("listed %v", got)
	}
}

func TestASessionStillWorkingCantBeEnded(t *testing.T) {
	for _, state := range []protocol.State{protocol.StateBusy, protocol.StateWaitingInput} {
		session := museIdle("a", 10)
		session.State = state
		registry, _ := endRegistry(t, session)
		if err := registry.EndSession(context.Background(), "muse:a"); err == nil {
			t.Fatalf("%s: ended while working", state)
		}
		if got := listedIDs(t, registry); len(got) != 1 {
			t.Fatalf("%s: listed %v", state, got)
		}
	}
}

func TestEndedSessionsSurviveARestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ended.json")
	registry, _ := endRegistry(t, museIdle("a", 10))
	registry.KeepEnded(path)
	if err := registry.EndSession(context.Background(), "muse:a"); err != nil {
		t.Fatal(err)
	}
	again, _ := endRegistry(t, museIdle("a", 10))
	again.KeepEnded(path)
	if got := listedIDs(t, again); len(got) != 0 {
		t.Fatalf("came back after a restart: %v", got)
	}
}

func TestAClosedTerminalsLastWritesDontBringItBack(t *testing.T) {
	registry, source := endRegistry(t, museIdle("a", 10))
	if err := registry.EndSession(context.Background(), "muse:a"); err != nil {
		t.Fatal(err)
	}
	flushed := museIdle("a", time.Now().UnixMilli()+500)
	flushed.State = protocol.StateBusy
	source.sessions = []protocol.Session{flushed}
	if got := listedIDs(t, registry); len(got) != 0 {
		t.Fatalf("came back from its own last write: %v", got)
	}
}

func TestEndingFollowsTheConversationNotThePane(t *testing.T) {
	inPane := protocol.Session{ID: "codex:pane-1", Kind: protocol.KindCodex, NativeID: "thread-9", State: protocol.StateIdle, LastActivityAt: 10}
	registry := NewRegistry()
	stub := &endTestSource{sessions: []protocol.Session{inPane}}
	stub.kind = protocol.KindCodex
	registry.Add(stub)
	listedIDs(t, registry)
	if err := registry.EndSession(context.Background(), "codex:pane-1"); err != nil {
		t.Fatal(err)
	}
	stub.sessions = []protocol.Session{{ID: "codex:thread-9", Kind: protocol.KindCodex, NativeID: "thread-9", State: protocol.StateIdle, LastActivityAt: 10}}
	if got := listedIDs(t, registry); len(got) != 0 {
		t.Fatalf("listed again under its own id: %v", got)
	}
}

func TestAnAgentsOwnEndedRowStaysSoItCanBePickedUp(t *testing.T) {
	registry, source := endRegistry(t, museIdle("a", 10))
	if err := registry.EndSession(context.Background(), "muse:a"); err != nil {
		t.Fatal(err)
	}
	ended := museIdle("a", 10)
	ended.State = protocol.StateEnded
	source.sessions = []protocol.Session{ended}
	if got := listedIDs(t, registry); len(got) != 1 {
		t.Fatalf("listed %v", got)
	}
}

func TestFoldersDontCountAnEndedSessionAsRunning(t *testing.T) {
	session := museIdle("a", 10)
	session.Cwd = "/w"
	registry, _ := endRegistry(t, session)
	if err := registry.EndSession(context.Background(), "muse:a"); err != nil {
		t.Fatal(err)
	}
	listedIDs(t, registry)
	if got := registry.lastKnown(); len(got) != 0 {
		t.Fatalf("still known as running: %v", got)
	}
}

func TestOldEndedSessionsAreForgotten(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ended.json")
	old := time.Now().Add(-endedKept - time.Hour).UnixMilli()
	raw := []byte(`{"muse:old":` + strconv.FormatInt(old, 10) + `,"muse:new":` + strconv.FormatInt(time.Now().UnixMilli(), 10) + `}`)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	e := newEndedSessions(path)
	if _, kept := e.at["muse:old"]; kept || len(e.at) != 1 {
		t.Fatalf("kept %v", e.at)
	}
}
