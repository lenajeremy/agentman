package source

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
)

type endTestSource struct {
	Source
	sessions []protocol.Session
}

func (s *endTestSource) Kind() protocol.Kind { return protocol.KindMuse }
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
	source.sessions = []protocol.Session{museIdle("a", 11)}
	if got := listedIDs(t, registry); len(got) != 1 {
		t.Fatalf("newer activity should list it again: %v", got)
	}
	// And that's the end of it: it isn't hidden again by what came before.
	source.sessions = []protocol.Session{museIdle("a", 11)}
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
