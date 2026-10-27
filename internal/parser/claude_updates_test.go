package parser

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
)

func TestClaudeOnlyVisibleUpdatesBecomeAssistantText(t *testing.T) {
	for _, tt := range []struct {
		name, display, text   string
		user, meta, sidechain bool
		want                  bool
	}{
		{name: "visible update", display: "updates", text: "Checking the service.\n\n", want: true},
		{name: "sidechain update", display: "updates", text: "Checking the service.", sidechain: true, want: true},
		{name: "signature only", display: "updates"},
		{name: "blank update", display: "updates", text: " \n\t"},
		{name: "ordinary thinking", text: "not a visible update"},
		{name: "disabled display", display: "omitted", text: "not a visible update"},
		{name: "unknown display", display: "unknown", text: "not a visible update"},
		{name: "user record", display: "updates", text: "not an assistant update", user: true},
		{name: "meta record", display: "updates", text: "bookkeeping", meta: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rec := claudeAssistant("a1", []any{
				obj{"type": "thinking", "thinking": tt.text, "signature": "opaque signature"},
			})
			if tt.display != "" {
				rec["thinkingDisplay"] = tt.display
			}
			if tt.user {
				rec["type"] = "user"
				rec["message"].(obj)["role"] = "user"
			}
			rec["isMeta"] = tt.meta
			rec["isSidechain"] = tt.sidechain
			raw, err := json.Marshal(rec)
			if err != nil {
				t.Fatal(err)
			}
			got := NewClaudeParser("claude:s").Parse(string(raw), 0)
			if !tt.want {
				if len(got) != 0 {
					t.Fatalf("non-visible content emitted: %+v", got)
				}
				return
			}
			want := []protocol.Message{{
				ID: "a1:0", SessionID: "claude:s", Role: protocol.RoleAssistant,
				Ts: parseTime("2026-08-10T10:00:01.000Z"), Text: "Checking the service.", IsSidechain: tt.sidechain,
			}}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("got %+v, want %+v", got, want)
			}
		})
	}
}

func TestClaudeVisibleUpdatesKeepTheirBlockOrder(t *testing.T) {
	rec := claudeAssistant("a1", []any{
		obj{"type": "thinking", "thinking": "", "signature": "opaque signature"},
		obj{"type": "thinking", "thinking": "First update.\n\n", "signature": "opaque signature"},
		obj{"type": "text", "text": "Ordinary reply."},
		obj{"type": "tool_use", "id": "toolu_1", "name": "Bash", "input": obj{"command": "date"}},
		obj{"type": "thinking", "thinking": "Second update."},
		obj{"type": "redacted_thinking", "data": "opaque payload"},
		obj{"type": "text", "text": "Last reply."},
	})
	rec["thinkingDisplay"] = "updates"
	path := writeFixture(t, []any{rec})
	got := pageAll(t, path, NewClaudeParser("claude:s"), 20)
	var rows []string
	for _, msg := range got {
		rows = append(rows, string(msg.Role)+" "+msg.ID+" "+msg.Text)
	}
	want := []string{
		"assistant a1:1 First update.",
		"assistant a1:2 Ordinary reply.",
		"tool toolu_1 ",
		"assistant a1:4 Second update.",
		"assistant a1:6 Last reply.",
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("block order = %q, want %q", rows, want)
	}
}
