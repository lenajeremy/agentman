package manager

import (
	"context"
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
)

func titleRequest() protocol.Request {
	return protocol.Request{
		Type: protocol.ReqToolsTitle, SessionID: "claude:a",
		Text: "Now the phone check.",
		Steps: []protocol.Tool{
			{Name: "Bash", Summary: "go build -o am-dev ./cmd/am", Title: "Rebuilt the daemon"},
			{Name: "Bash", Summary: "am pair -relay http://localhost:8099", Title: "Paired the phone with the test daemon"},
		},
	}
}

// A run gets one plain line, written once however often it is asked for.
func TestARunOfStepsGetsOnePlainTitle(t *testing.T) {
	model := &scriptedModel{replies: []string{textReply("\"Started a test daemon and paired the phone with it.\"\n")}}
	h := newHarness(t, model, true)

	first := h.m.Handle(context.Background(), titleRequest())
	if first.Type != protocol.EvtToolsTitle || first.Preview != "Started a test daemon and paired the phone with it" {
		t.Fatalf("first = %+v", first)
	}
	again := h.m.Handle(context.Background(), titleRequest())
	if again.Preview != first.Preview {
		t.Fatalf("again = %+v", again)
	}
	if len(model.requests) != 1 {
		t.Fatalf("the model was asked %d times", len(model.requests))
	}
	sent := model.requests[0].Messages[0].Content[0].OfText.Text
	for _, want := range []string{"Now the phone check.", "Rebuilt the daemon", "go build -o am-dev ./cmd/am"} {
		if !strings.Contains(sent, want) {
			t.Errorf("the model was not told %q:\n%s", want, sent)
		}
	}
	if string(model.requests[0].Model) != titleModel {
		t.Errorf("model = %s", model.requests[0].Model)
	}
}

func TestOneStepNeedsNoTitle(t *testing.T) {
	h := newHarness(t, &scriptedModel{}, true)
	req := titleRequest()
	req.Steps = req.Steps[:1]
	if event := h.m.Handle(context.Background(), req); event.Type != protocol.EvtError {
		t.Fatalf("event = %+v", event)
	}
}
