package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/push"
	"github.com/lenajeremy/agentman/internal/source"
)

// expoStub stands in for Expo's push service so the daemon's real sending path
// runs end to end without reaching the network.
type expoStub struct {
	mu       sync.Mutex
	received []map[string]any
	server   *httptest.Server
}

func newExpoStub(t *testing.T) *expoStub {
	t.Helper()
	stub := &expoStub{}
	stub.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var batch []map[string]any
		if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
			t.Error(err)
		}
		stub.mu.Lock()
		stub.received = append(stub.received, batch...)
		count := len(batch)
		stub.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[`))
		for i := 0; i < count; i++ {
			if i > 0 {
				w.Write([]byte(","))
			}
			w.Write([]byte(`{"status":"ok"}`))
		}
		w.Write([]byte(`]}`))
	}))
	t.Cleanup(stub.server.Close)
	return stub
}

func (s *expoStub) wait(t *testing.T, want int) []map[string]any {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		got := len(s.received)
		s.mu.Unlock()
		if got >= want {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]map[string]any(nil), s.received...)
}

func pushedDaemon(t *testing.T, stub *expoStub, cfg push.Config) (*Daemon, *source.Registry, *scriptedSource) {
	t.Helper()
	registry := source.NewRegistry()
	scripted := &scriptedSource{state: protocol.StateBusy, reply: "All 42 tests passed."}
	registry.Add(scripted)

	agent := New(registry, &recordingSink{})
	store := push.NewStore(t.TempDir())
	sender := push.NewSender(store, cfg)
	sender.Endpoint = stub.server.URL
	agent.SetPush(sender)
	return agent, registry, scripted
}

// The whole point of push: a phone whose websocket is gone still hears that its
// agent finished. Everything short of Expo's own delivery runs here — the app's
// registration request, the token store, the turn detection, and the payload.
func TestAFinishedTurnReachesTheDeviceThroughExpo(t *testing.T) {
	stub := newExpoStub(t)
	agent, _, scripted := pushedDaemon(t, stub, push.Config{})
	ctx := context.Background()

	// The app registers exactly as it does over the relay.
	const token = "ExponentPushToken[xxxxxxxxxxxxxxxxxxxxxx]"
	if event := agent.HandleFrom(ctx, "device-1", protocol.Request{
		Type: protocol.ReqRegisterPush, PushToken: token,
	}); event.Type == protocol.EvtError {
		t.Fatalf("registration refused: %s", event.Error)
	}

	agent.refresh(ctx, true) // busy
	scripted.set(protocol.StateIdle)
	agent.refresh(ctx, false) // busy -> idle: the turn ended

	sent := stub.wait(t, 1)
	if len(sent) != 1 {
		t.Fatalf("Expo received %d pushes, want 1", len(sent))
	}
	if sent[0]["to"] != token {
		t.Errorf("addressed to %v, want the registered device", sent[0]["to"])
	}
	if sent[0]["title"] != "checkout" {
		t.Errorf("title = %v, want the session name", sent[0]["title"])
	}
	// Tapping the alert has to open the right session.
	data, _ := sent[0]["data"].(map[string]any)
	if data["sessionId"] != "opencode:s1" {
		t.Errorf("data.sessionId = %v, want the finished session", data["sessionId"])
	}
}

// Transcript text passes through Expo and Apple, so it must not ride along
// unless the user has asked for it.
func TestTheDefaultPushCarriesNoTranscriptText(t *testing.T) {
	stub := newExpoStub(t)
	agent, _, scripted := pushedDaemon(t, stub, push.Config{})
	ctx := context.Background()
	agent.HandleFrom(ctx, "d", protocol.Request{
		Type: protocol.ReqRegisterPush, PushToken: "ExponentPushToken[yyyyyyyyyyyyyyyyyyyyyy]",
	})

	agent.refresh(ctx, true)
	scripted.set(protocol.StateIdle)
	agent.refresh(ctx, false)

	sent := stub.wait(t, 1)
	if len(sent) != 1 {
		t.Fatalf("got %d pushes", len(sent))
	}
	if body, _ := sent[0]["body"].(string); body != "Finished its turn." {
		t.Errorf("body = %q — the agent's words left the machine by default", body)
	}
}

// A blocked agent is the most actionable thing the daemon knows, and the one
// most likely to be missed: the phone is asleep because the user walked away.
func TestABlockedAgentPushesOncePerQuestion(t *testing.T) {
	stub := newExpoStub(t)
	agent, _, scripted := pushedDaemon(t, stub, push.Config{})
	ctx := context.Background()
	agent.HandleFrom(ctx, "d", protocol.Request{
		Type: protocol.ReqRegisterPush, PushToken: "ExponentPushToken[zzzzzzzzzzzzzzzzzzzzzz]",
	})

	scripted.setQuestion(protocol.StateWaitingInput, &protocol.Question{
		ID: "q1", Prompt: "Allow this action?",
		Options: []protocol.QuestionOption{{Key: "1", Label: "Yes"}},
	})
	agent.refresh(ctx, true)
	agent.refresh(ctx, false)
	agent.refresh(ctx, false) // the prompt sits there unanswered

	sent := stub.wait(t, 1)
	if len(sent) != 1 {
		t.Fatalf("got %d pushes, want exactly 1 — an unanswered prompt rang repeatedly", len(sent))
	}
	if title, _ := sent[0]["title"].(string); title != "checkout needs your answer" {
		t.Errorf("title = %q", title)
	}
}
