package daemon

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/lenajeremy/agentman/internal/hook"
	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/source"
)

// agy's Stop hook, end to end: posted by `am hook antigravity Stop` with agy's
// own payload, it rings the phone once for the pane-backed session the phone
// knows — keyed on the pane, not on the conversation id agy reports.
func TestAntigravityStopHookRingsThePhone(t *testing.T) {
	const conversation = "ef116096-84c3-4723-bbc3-aed0f3cca68b"
	brain := filepath.Join(t.TempDir(), ".gemini", "antigravity-cli", "brain", conversation)
	payload := `{"artifactDirectoryPath":"` + brain + `","conversationId":"` + conversation + `",` +
		`"fullyIdle":true,"modelName":"gemini-3.8-flash-high","terminationReason":"NO_TOOL_CALL",` +
		`"transcriptPath":"` + brain + `/.system_generated/logs/transcript_full.jsonl","workspacePaths":["/work/api"]}`

	server := hook.NewServer("secret")
	req := httptest.NewRequest(http.MethodPost, "/hook/antigravity/Stop", bytes.NewReader([]byte(payload)))
	req.Header.Set("X-Agentman-Token", "secret")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, req)
	if response.Code != http.StatusNoContent || response.Body.Len() != 0 {
		t.Fatalf("agy was answered %d %q", response.Code, response.Body.String())
	}

	sink := &recordingSink{}
	agent := New(source.NewRegistry(), sink)
	agent.turnDelay = 0
	paneID := "antigravity:tmux-agentman-antigravity-1-a"
	agent.sessions[paneID] = protocol.Session{
		ID: paneID, Kind: protocol.KindAntigravity, NativeID: conversation,
		Name: "Create And Edit Notes File", State: protocol.StateBusy, Inject: protocol.InjectTmux,
	}
	agent.handleHook(<-server.Events())

	events := sink.turnCompletes()
	if len(events) != 1 || events[0].SessionID != paneID || events[0].SessionName != "Create And Edit Notes File" {
		t.Fatalf("turn completes = %+v", events)
	}
	if _, invented := agent.sessions["antigravity:"+conversation]; invented {
		t.Error("the hook made a session under the conversation id")
	}
}
