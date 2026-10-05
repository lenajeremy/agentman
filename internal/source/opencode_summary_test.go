package source

import (
	"encoding/json"
	"testing"
)

// OpenCode leaves a tool's title empty for some calls — 15 of 22 globs and a
// handful of reads, edits and fetches in one real store — and the row then
// said only "glob" with nothing after it. The call's own input says what it
// was doing.
func TestAnUntitledOpenCodeToolIsSummarisedFromItsInput(t *testing.T) {
	for _, tc := range []struct{ tool, input, want string }{
		{"glob", `{"pattern":"**/*.go","path":"/work"}`, "**/*.go"},
		{"read", `{"filePath":"/work/main.go"}`, "/work/main.go"},
		{"bash", `{"command":"go test ./...","description":"run tests"}`, "go test ./..."},
		{"webfetch", `{"url":"https://example.com","format":"markdown"}`, "https://example.com"},
		{"edit", `{"filePath":"/work/a.go","oldString":"x","newString":"y"}`, "/work/a.go"},
		{"mcp_search", `{"query":"onboarding","limit":5}`, "onboarding"},
	} {
		var part ocPart
		raw := `{"type":"tool","tool":"` + tc.tool + `","state":{"status":"completed","title":"","input":` + tc.input + `}}`
		if err := json.Unmarshal([]byte(raw), &part); err != nil {
			t.Fatal(err)
		}
		rows := openCodeMessages("opencode:s", ocMessage{Info: ocMessageInfo{ID: "m"}, Parts: []ocPart{part}})
		if len(rows) != 1 || rows[0].Tool == nil || rows[0].Tool.Summary != tc.want {
			t.Errorf("%s: summary %+v, want %q", tc.tool, rows, tc.want)
		}
	}
}

// A title OpenCode did write still wins.
func TestATitledOpenCodeToolKeepsItsTitle(t *testing.T) {
	var part ocPart
	_ = json.Unmarshal([]byte(`{"type":"tool","tool":"read","state":{"status":"completed","title":"main.go","input":{"filePath":"/work/main.go"}}}`), &part)
	rows := openCodeMessages("opencode:s", ocMessage{Info: ocMessageInfo{ID: "m"}, Parts: []ocPart{part}})
	if rows[0].Tool.Summary != "main.go" {
		t.Errorf("summary %q", rows[0].Tool.Summary)
	}
}
