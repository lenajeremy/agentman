package daemon

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/servers"
	"github.com/lenajeremy/agentman/internal/source"
)

type fakeWatcher struct {
	mu     sync.Mutex
	found  map[string][]protocol.Server
	owners []servers.Owner
}

func (w *fakeWatcher) Update(_ context.Context, owners []servers.Owner) (map[string][]protocol.Server, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.owners = owners
	out := map[string][]protocol.Server{}
	for id, list := range w.found {
		out[id] = append([]protocol.Server(nil), list...)
	}
	return out, nil
}

func (w *fakeWatcher) set(found map[string][]protocol.Server) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.found = found
}

type fakeSharer struct {
	mu       sync.Mutex
	links    map[int]string
	opened   []int
	closed   []int
	retained map[int]bool
	fail     error
}

func (s *fakeSharer) Open(_ context.Context, port int) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.fail != nil {
		return "", s.fail
	}
	s.opened = append(s.opened, port)
	link := "https://link-" + string(rune('a'+len(s.opened))) + ".agentman.online"
	s.links[port] = link
	return link, nil
}

func (s *fakeSharer) Close(port int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = append(s.closed, port)
	delete(s.links, port)
}

func (s *fakeSharer) Links() map[int]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[int]string{}
	for port, link := range s.links {
		out[port] = link
	}
	return out
}

func (s *fakeSharer) Retain(live map[int]bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.retained = live
	for port := range s.links {
		if !live[port] {
			delete(s.links, port)
		}
	}
}

func (s *fakeSharer) CloseAll() { s.Retain(nil) }

func newServerDaemon(t *testing.T) (*Daemon, *recordingSink, *fakeWatcher, *fakeSharer) {
	t.Helper()
	adapter := &bulkDiscoverySource{sessions: []protocol.Session{{
		ID: "claude:s1", Kind: protocol.KindClaude, NativeID: "s1", Name: "app",
		Cwd: "/src/app", State: protocol.StateBusy, AgentPID: 4242,
	}}}
	registry := source.NewRegistry()
	registry.Add(adapter)
	sink := &recordingSink{}
	agent := New(registry, sink)
	watcher := &fakeWatcher{}
	sharer := &fakeSharer{links: map[int]string{}}
	agent.SetServers(watcher, sharer)
	agent.refresh(context.Background(), true)
	return agent, sink, watcher, sharer
}

func lastUpdate(t *testing.T, sink *recordingSink) protocol.Session {
	t.Helper()
	sink.mu.Lock()
	defer sink.mu.Unlock()
	for i := len(sink.events) - 1; i >= 0; i-- {
		if event := sink.events[i]; event.Type == protocol.EvtSessionUpdate && event.Session != nil {
			return *event.Session
		}
	}
	t.Fatal("no session_update was sent")
	return protocol.Session{}
}

func TestServerScanPublishesChangesOnly(t *testing.T) {
	agent, sink, watcher, _ := newServerDaemon(t)
	watcher.set(map[string][]protocol.Server{"claude:s1": {{Port: 5173, Command: "node", Title: "Vite App"}}})

	agent.scanServers(context.Background())
	got := lastUpdate(t, sink)
	if len(got.Servers) != 1 || got.Servers[0].Port != 5173 {
		t.Fatalf("update carried %+v", got.Servers)
	}
	// The watcher learned who owns what, including the agent pid.
	if owners := watcher.owners; len(owners) != 1 || owners[0].PID != 4242 || owners[0].Cwd != "/src/app" {
		t.Fatalf("watcher owners = %+v", owners)
	}

	sink.mu.Lock()
	before := len(sink.events)
	sink.mu.Unlock()
	agent.scanServers(context.Background())
	sink.mu.Lock()
	after := len(sink.events)
	sink.mu.Unlock()
	if after != before {
		t.Fatal("an unchanged scan sent another update")
	}

	// A discovery sweep must keep the servers rather than erase them.
	agent.refresh(context.Background(), false)
	if servers := agent.snapshot()[0].Servers; len(servers) != 1 {
		t.Fatalf("refresh dropped servers: %+v", servers)
	}
}

func TestOpenServerSharesOnlyDetectedPorts(t *testing.T) {
	agent, sink, watcher, sharer := newServerDaemon(t)
	watcher.set(map[string][]protocol.Server{"claude:s1": {{Port: 5173}}})
	agent.scanServers(context.Background())

	refused := agent.Handle(context.Background(), protocol.Request{
		Type: protocol.ReqOpenServer, SessionID: "claude:s1", Port: 22,
	})
	if refused.Type != protocol.EvtError || len(sharer.opened) != 0 {
		t.Fatalf("an undetected port was shared: %+v", refused)
	}

	opened := agent.Handle(context.Background(), protocol.Request{
		Type: protocol.ReqOpenServer, SessionID: "claude:s1", Port: 5173,
	})
	if opened.Type != protocol.EvtServerOpened || opened.Port != 5173 || opened.Link == "" {
		t.Fatalf("open answered %+v", opened)
	}
	if got := lastUpdate(t, sink); got.Servers[0].Link != opened.Link {
		t.Fatalf("other devices were not told about the link: %+v", got.Servers)
	}

	agent.Handle(context.Background(), protocol.Request{
		Type: protocol.ReqCloseServer, SessionID: "claude:s1", Port: 5173,
	})
	if len(sharer.closed) != 1 || lastUpdate(t, sink).Servers[0].Link != "" {
		t.Fatal("closing did not stop sharing and clear the link")
	}
}

func TestLinksDieWithTheirServer(t *testing.T) {
	agent, sink, watcher, sharer := newServerDaemon(t)
	watcher.set(map[string][]protocol.Server{"claude:s1": {{Port: 5173}}})
	agent.scanServers(context.Background())
	agent.Handle(context.Background(), protocol.Request{
		Type: protocol.ReqOpenServer, SessionID: "claude:s1", Port: 5173,
	})

	watcher.set(nil)
	agent.scanServers(context.Background())
	if sharer.retained[5173] || len(sharer.Links()) != 0 {
		t.Fatal("the link outlived its server")
	}
	if got := lastUpdate(t, sink); len(got.Servers) != 0 {
		t.Fatalf("stopped server still listed: %+v", got.Servers)
	}
}

func TestOpenServerReportsSharingFailures(t *testing.T) {
	agent, _, watcher, sharer := newServerDaemon(t)
	watcher.set(map[string][]protocol.Server{"claude:s1": {{Port: 5173}}})
	agent.scanServers(context.Background())
	sharer.fail = errors.New("this relay does not offer preview links")

	event := agent.Handle(context.Background(), protocol.Request{
		Type: protocol.ReqOpenServer, SessionID: "claude:s1", Port: 5173,
	})
	if event.Type != protocol.EvtError || event.Error != "this relay does not offer preview links" {
		t.Fatalf("got %+v", event)
	}
}

func TestOpenServerWithoutRelay(t *testing.T) {
	agent, _, watcher, _ := newServerDaemon(t)
	agent.SetServers(watcher, nil)
	watcher.set(map[string][]protocol.Server{"claude:s1": {{Port: 5173}}})
	agent.scanServers(context.Background())

	event := agent.Handle(context.Background(), protocol.Request{
		Type: protocol.ReqOpenServer, SessionID: "claude:s1", Port: 5173,
	})
	if event.Type != protocol.EvtError {
		t.Fatalf("got %+v", event)
	}
}

func TestServerRequestsValidatePort(t *testing.T) {
	for _, port := range []int{0, -1, 70000} {
		if err := validateRequest(protocol.Request{
			Type: protocol.ReqOpenServer, SessionID: "claude:s1", Port: port,
		}); err == nil {
			t.Errorf("port %d accepted", port)
		}
	}
}
