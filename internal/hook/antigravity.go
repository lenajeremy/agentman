package hook

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Antigravity CLI runs hooks from hooks.json with a JSON object on stdin, in
// camelCase (protojson), unlike Claude's snake_case. Its Stop payload, from
// agy 1.2.17:
//
//	{"artifactDirectoryPath": "/Users/me/.gemini/antigravity-cli/brain/<id>",
//	 "conversationId": "<id>", "error": "", "executionNum": 0, "fullyIdle": true,
//	 "modelName": "gemini-3.8-flash-high", "terminationReason": "NO_TOOL_CALL",
//	 "transcriptPath": "/Users/me/.gemini/antigravity-cli/brain/<id>/.system_generated/logs/transcript_full.jsonl",
//	 "workspacePaths": ["/Users/me/work/api"]}
//
// PreInvocation, fired before every model call, carries the same common
// fields.

// antigravityCLIRoot is the part of a transcript path that says the CLI wrote
// it. ~/.gemini/config/hooks.json is read by every Antigravity app — the IDE
// and Antigravity 2.0 keep theirs under antigravity-ide/ and antigravity/ — and
// only the CLI's sessions are ones Agentman shows.
var antigravityCLIRoot = string(filepath.Separator) + filepath.Join(".gemini", "antigravity-cli") + string(filepath.Separator)

// parseAntigravityPayload reads agy's shape. ok is false when the payload is
// not agy's at all, and true with an empty SessionID when it is one to
// acknowledge and drop: another app's, or a subagent's.
func parseAntigravityPayload(raw []byte) (Payload, bool) {
	var agy struct {
		ConversationID string   `json:"conversationId"`
		TranscriptPath string   `json:"transcriptPath"`
		WorkspacePaths []string `json:"workspacePaths"`
	}
	if json.Unmarshal(raw, &agy) != nil || agy.ConversationID == "" {
		return Payload{}, false
	}
	root, _, ok := strings.Cut(agy.TranscriptPath, antigravityCLIRoot)
	if !ok || !validConversationID(agy.ConversationID) {
		return Payload{}, true
	}
	root += antigravityCLIRoot
	if antigravitySubagent(root, agy.ConversationID) {
		return Payload{}, true
	}
	p := Payload{SessionID: agy.ConversationID, TranscriptPath: agy.TranscriptPath}
	if len(agy.WorkspacePaths) > 0 {
		p.Cwd = agy.WorkspacePaths[0]
	}
	return p, true
}

// antigravitySubagent reports whether a conversation is a subagent's. A
// subagent runs hooks like any conversation, but it is its parent's work, not
// a session the phone lists — its Stop would ring the phone for a session
// that does not exist there. The parent records each child it starts under
// brain/<parent>/.system_generated/subagents/<child>.json.
func antigravitySubagent(root, conversation string) bool {
	matches, err := filepath.Glob(filepath.Join(root, "brain", "*", ".system_generated", "subagents", conversation+".json"))
	return err == nil && len(matches) > 0
}

// validConversationID accepts agy's conversation ids, which are UUIDs, so
// nothing from a payload can steer a path built from one.
func validConversationID(id string) bool {
	if len(id) != 36 {
		return false
	}
	for i, r := range id {
		switch {
		case i == 8 || i == 13 || i == 18 || i == 23:
			if r != '-' {
				return false
			}
		case (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f'):
		default:
			return false
		}
	}
	return true
}

// antigravityInstalled reports whether the Antigravity CLI has ever run for
// this user, which is when installing its hooks makes sense.
func antigravityInstalled(home string) bool {
	info, err := os.Stat(filepath.Join(home, ".gemini", "antigravity-cli"))
	return err == nil && info.IsDir()
}
