package daemon

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/push"
)

// switchedAgentman is Agentman as the daemon sees it, on or off.
type switchedAgentman struct{ on bool }

func (switchedAgentman) Handles(protocol.RequestType) bool { return false }
func (switchedAgentman) Handle(context.Context, protocol.Request) protocol.Event {
	return protocol.Event{}
}
func (switchedAgentman) TurnComplete(protocol.Session)      {}
func (switchedAgentman) NeedsAnswer(protocol.Session)       {}
func (switchedAgentman) SessionsChanged([]protocol.Session) {}
func (a switchedAgentman) On() bool                         { return a.on }

const (
	quietPhone = "ExponentPushToken[quietquietquietquietqu]"
	everyPhone = "ExponentPushToken[everyeveryeveryeveryev]"
)

func yes() *bool { on := true; return &on }

// registerPhones registers a phone that chose nothing new, as the apps out
// there today do, and one that asked to hear from every agent.
func registerPhones(t *testing.T, agent *Daemon) {
	t.Helper()
	ctx := context.Background()
	for token, notify := range map[string]*protocol.NotifyPrefs{
		quietPhone: {Finished: true, NeedsYou: true},
		everyPhone: {Finished: true, NeedsYou: true, Agents: yes()},
	} {
		if event := agent.HandleFrom(ctx, "d", protocol.Request{
			Type: protocol.ReqRegisterPush, PushToken: token, Notify: notify,
		}); event.Type == protocol.EvtError {
			t.Fatalf("registration refused: %s", event.Error)
		}
	}
}

func recipients(sent []map[string]any) map[string]bool {
	to := map[string]bool{}
	for _, message := range sent {
		to[message["to"].(string)] = true
	}
	return to
}

// While Agentman is on it is the one voice: an agent finishing reaches only
// the phone that asked for every agent, and Agentman's own word reaches both.
func TestWhileAgentmanIsOnOnlyItNotifies(t *testing.T) {
	stub := newExpoStub(t)
	agent, _, scripted := pushedDaemon(t, stub, push.Config{})
	agent.SetManager(switchedAgentman{on: true})
	registerPhones(t, agent)
	ctx := context.Background()

	agent.refresh(ctx, true)
	scripted.set(protocol.StateIdle)
	agent.refresh(ctx, false)
	time.Sleep(200 * time.Millisecond)
	sent := stub.wait(t, 1)
	if to := recipients(sent); len(sent) != 1 || !to[everyPhone] {
		t.Fatalf("a finished turn went to %v, want only the phone that asked for every agent", to)
	}

	agent.AlertAgentman(protocol.ManagerEntry{ID: "b1", Role: "brief", Text: "Tests pass.", SessionID: "opencode:s1"})
	sent = stub.wait(t, 3)
	if len(sent) != 3 {
		t.Fatalf("Expo received %d pushes, want Agentman's brief on both phones too", len(sent))
	}
	for _, message := range sent[1:] {
		if message["title"] != "Agentman" || message["body"] != "News about checkout." {
			t.Errorf("Agentman's brief read %q / %q", message["title"], message["body"])
		}
		if data, _ := message["data"].(map[string]any); data["agentman"] != true {
			t.Errorf("data = %v, want it to open Agentman", data)
		}
	}
}

// An agent the user asked to hear from speaks for itself, on every phone,
// and the choice outlives the daemon.
func TestAnAgentTheUserAskedForStillNotifies(t *testing.T) {
	stub := newExpoStub(t)
	agent, _, scripted := pushedDaemon(t, stub, push.Config{})
	agent.SetManager(switchedAgentman{on: true})
	path := filepath.Join(t.TempDir(), "alerts.json")
	agent.SetAlertsFile(path)
	registerPhones(t, agent)
	ctx := context.Background()

	agent.refresh(ctx, true)
	reply := agent.HandleFrom(ctx, "d", protocol.Request{Type: protocol.ReqSetAgentAlerts, SessionID: "opencode:s1", Alerts: yes()})
	if reply.Type != protocol.EvtSessionUpdate || reply.Session == nil || !reply.Session.Alerts {
		t.Fatalf("set_agent_alerts answered %+v", reply)
	}
	scripted.set(protocol.StateIdle)
	agent.refresh(ctx, false)
	if sent := stub.wait(t, 2); len(sent) != 2 {
		t.Fatalf("Expo received %d pushes, want the finished turn on both phones", len(sent))
	}
	if !agent.Sessions()[0].Alerts {
		t.Error("the next sweep forgot the agent's alerts are on")
	}
	if !loadAgentAlerts(path).has("opencode:s1") {
		t.Error("the choice wasn't kept for the next start")
	}
}

// Off, Agentman says nothing and the agents notify as they always have.
func TestWithAgentmanOffAgentsNotifyAsBefore(t *testing.T) {
	stub := newExpoStub(t)
	agent, _, scripted := pushedDaemon(t, stub, push.Config{})
	agent.SetManager(switchedAgentman{on: false})
	registerPhones(t, agent)
	ctx := context.Background()

	agent.refresh(ctx, true)
	scripted.set(protocol.StateIdle)
	agent.refresh(ctx, false)
	if sent := stub.wait(t, 2); len(sent) != 2 {
		t.Fatalf("Expo received %d pushes, want the finished turn on both phones", len(sent))
	}
	if info := agent.sessionsEvent(nil).Daemon; info != nil && info.Agentman {
		t.Error("the apps were told Agentman is on")
	}
}

// A card rings as needing the user, every time; Agentman's other word comes
// once a window, so a run of briefs is one alert.
func TestAgentmanCardsRingAndItsNewsIsCoalesced(t *testing.T) {
	stub := newExpoStub(t)
	agent, _, _ := pushedDaemon(t, stub, push.Config{})
	agent.SetManager(switchedAgentman{on: true})
	agent.alertCoalesce = time.Minute
	ctx := context.Background()
	agent.HandleFrom(ctx, "d", protocol.Request{Type: protocol.ReqRegisterPush, PushToken: quietPhone})
	agent.refresh(ctx, true)

	for _, id := range []string{"c1", "c2"} {
		agent.AlertAgentman(protocol.ManagerEntry{ID: id, Role: "card", SessionID: "opencode:s1", Card: &protocol.ManagerCard{
			ID: id, Kind: protocol.CardQuestion, State: protocol.CardOpen, Title: "wants to run `git push`",
		}})
	}
	agent.AlertAgentman(protocol.ManagerEntry{ID: "b1", Role: "brief", Text: "Done."})
	agent.AlertAgentman(protocol.ManagerEntry{ID: "b2", Role: "brief", Text: "Done again."})
	time.Sleep(200 * time.Millisecond)
	sent := stub.wait(t, 3)
	if len(sent) != 3 {
		t.Fatalf("Expo received %d pushes, want two cards and one brief", len(sent))
	}
	cards := 0
	for _, message := range sent {
		if message["title"] == "checkout needs you" {
			cards++
			if message["sound"] != push.SoundNeedsYou || message["body"] != "Agentman has a card for you." {
				t.Errorf("a card rang as %q with %q", message["sound"], message["body"])
			}
		}
	}
	if cards != 2 {
		t.Errorf("%d cards rang, want both", cards)
	}
	if info := agent.sessionsEvent(nil).Daemon; info == nil || !info.Agentman {
		t.Error("the apps weren't told Agentman is on")
	}
}

// The app has to say on or off; a request that says neither changes nothing.
func TestSetAgentAlertsNeedsAChoice(t *testing.T) {
	if err := validateRequest(protocol.Request{Type: protocol.ReqSetAgentAlerts, SessionID: "claude:x"}); err == nil {
		t.Error("a request without a choice was accepted")
	}
}
