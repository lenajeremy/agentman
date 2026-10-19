package source

import (
	"encoding/json"
	"slices"
	"testing"
)

// Shaped like OpenCode 1.18's GET /session/{id}/message while a step runs and
// a message sent meanwhile waits for it (texts replaced).
func TestOpenCodeWaitingMessagesAreThoseAfterTheRunningReply(t *testing.T) {
	var messages []ocMessage
	raw := `[
	  {"info":{"id":"msg_u1","role":"user","time":{"created":1}},"parts":[{"id":"prt_1","type":"text","text":"fix the bug"}]},
	  {"info":{"id":"msg_a1","role":"assistant","parentID":"msg_u1","time":{"created":2}},"parts":[{"id":"prt_2","type":"text","text":"looking"}]},
	  {"info":{"id":"msg_u2","role":"user","time":{"created":3}},"parts":[{"id":"prt_3","type":"text","text":"also add a test"},{"id":"prt_4","type":"text","text":"scaffold","synthetic":true}]},
	  {"info":{"id":"msg_u3","role":"user","time":{"created":4}},"parts":[{"id":"prt_5","type":"text","text":"and the docs"}]}
	]`
	if err := json.Unmarshal([]byte(raw), &messages); err != nil {
		t.Fatal(err)
	}
	texts, rows := openCodeWaiting("opencode:s", messages)
	if !slices.Equal(texts, []string{"also add a test", "and the docs"}) {
		t.Fatalf("texts = %q", texts)
	}
	// The same ids the feed gives those rows, so the phone can find them.
	if !slices.Equal(rows, []string{"msg_u2:prt_3", "msg_u3:prt_5"}) {
		t.Fatalf("rows = %q", rows)
	}

	// The step ends and the reply to msg_u3 starts: nothing waits.
	messages[1].Info.Time.Completed = 5
	var reply ocMessage
	if err := json.Unmarshal([]byte(`{"info":{"id":"msg_a2","role":"assistant","parentID":"msg_u3","time":{"created":6}},"parts":[]}`), &reply); err != nil {
		t.Fatal(err)
	}
	texts, rows = openCodeWaiting("opencode:s", append(messages, reply))
	if texts != nil || rows != nil {
		t.Fatalf("after the reply started: texts = %q, rows = %q", texts, rows)
	}

	// Nothing running: nothing waits, whatever came last.
	if texts, _ := openCodeWaiting("opencode:s", messages[:1]); texts != nil {
		t.Fatalf("idle: texts = %q", texts)
	}
}
