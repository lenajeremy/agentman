package daemon

import (
	"context"
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/hook"
	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/source"
)

// Not every agent's Stop hook carries the closing words. The notification
// must not then say only that something finished.
func TestAHookWithoutAPreviewTakesOneFromTheTranscript(t *testing.T) {
	registry := source.NewRegistry()
	scripted := &scriptedSource{state: protocol.StateBusy, reply: "Refactored the parser; all 12 tests pass."}
	registry.Add(scripted)
	sink := &recordingSink{}
	agent := New(registry, sink)
	agent.turnDelay = 0
	agent.refresh(context.Background(), true)

	agent.handleHook(hook.Event{Kind: protocol.KindOpenCode, Name: hook.NameStop, SessionID: "opencode:s1"})

	events := sink.turnCompletes()
	if len(events) != 1 {
		t.Fatalf("got %d turn_complete events, want 1", len(events))
	}
	if !strings.Contains(events[0].Preview, "all 12 tests pass") {
		t.Fatalf("Preview = %q, want the agent's closing words from the transcript", events[0].Preview)
	}
}

// A hook that does carry them is believed: it is newer than the transcript,
// which can lag behind it.
func TestAHookPreviewIsKeptOverTheTranscript(t *testing.T) {
	registry := source.NewRegistry()
	registry.Add(&scriptedSource{state: protocol.StateBusy, reply: "an older reply"})
	sink := &recordingSink{}
	agent := New(registry, sink)
	agent.turnDelay = 0
	agent.refresh(context.Background(), true)

	agent.handleHook(hook.Event{
		Kind: protocol.KindOpenCode, Name: hook.NameStop, SessionID: "opencode:s1",
		Payload: hook.Payload{LastAssistantMessage: "Shipped it."},
	})

	events := sink.turnCompletes()
	if len(events) != 1 || events[0].Preview != "Shipped it." {
		t.Fatalf("turn_complete = %+v", events)
	}
}

// A withdrawn stream leaves an assistant row with no text in it; the preview
// skips it rather than ringing the phone with nothing.
func TestTheLatestPreviewSkipsBlankAssistantRows(t *testing.T) {
	registry := source.NewRegistry()
	registry.Add(&scriptedSource{reply: "   \n "})
	agent := New(registry, &recordingSink{})
	if got := agent.latestPreview(context.Background(), "opencode:s1"); got != "" {
		t.Fatalf("preview = %q, want none from a blank reply", got)
	}
}

// The app refuses a whole session list over one session's out-of-range field,
// so an adapter misreading a footer must not be able to blank the board.
func TestSessionStatusIsHeldToWhatTheAppAccepts(t *testing.T) {
	src := &bulkDiscoverySource{sessions: []protocol.Session{
		{ID: "claude:over", Kind: protocol.KindClaude, Mode: "  plan  ", ContextPercent: 104,
			Artifacts: 3, ArtifactsToReview: 9},
		{ID: "claude:under", Kind: protocol.KindClaude, Mode: "plan\x1b[2J", ContextPercent: -2,
			Artifacts: -1, ArtifactsToReview: -1},
		{ID: "claude:long", Kind: protocol.KindClaude, Mode: strings.Repeat("é", 100),
			Artifacts: 1 << 30, ArtifactsToReview: 1},
	}}
	registry := source.NewRegistry()
	registry.Add(src)
	agent := New(registry, &recordingSink{})
	agent.refresh(context.Background(), true)

	agent.mu.Lock()
	over, under, long := agent.sessions["claude:over"], agent.sessions["claude:under"], agent.sessions["claude:long"]
	agent.mu.Unlock()
	if over.Mode != "plan" || over.ContextPercent != 100 || over.Artifacts != 3 || over.ArtifactsToReview != 3 {
		t.Errorf("over = mode %q context %d artifacts %d/%d", over.Mode, over.ContextPercent,
			over.ArtifactsToReview, over.Artifacts)
	}
	if under.Mode != "" || under.ContextPercent != 0 || under.Artifacts != 0 || under.ArtifactsToReview != 0 {
		t.Errorf("under = mode %q context %d artifacts %d/%d", under.Mode, under.ContextPercent,
			under.ArtifactsToReview, under.Artifacts)
	}
	if len(long.Mode) > maxWireModeBytes || !strings.HasPrefix(long.Mode, "é") ||
		long.Artifacts != maxWireArtifactCount {
		t.Errorf("long = mode of %d bytes, %d artifacts", len(long.Mode), long.Artifacts)
	}
}
