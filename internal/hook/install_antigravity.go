package hook

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// antigravityHookName is the key Agentman owns in Antigravity's hooks.json.
//
// The file is a JSON object keyed by hook name, each name holding its event
// handlers (see agy's built-in agy-customizations skill, docs/hooks.md):
//
//	{"agentman": {"PreInvocation": [{"type": "command", "command": "…", "timeout": 5}],
//	              "Stop":          [{"type": "command", "command": "…", "timeout": 5}]}}
//
// So installing and removing ours is one key, and every other hook the user
// has is left exactly as it was.
const antigravityHookName = "agentman"

// planAntigravity registers two of agy's lifecycle hooks in
// ~/.gemini/config/hooks.json, the global customization root:
//
//   - Stop fires the moment a turn ends, and rings the phone.
//   - PreInvocation fires before every model call; delivered as
//     UserPromptSubmit, it marks the session working at once.
//
// Never PreToolUse. A PreToolUse hook must answer with a decision, and one that
// answers with nothing — checked against agy 1.2.17 — blocks every tool call.
// Agentman has no decision to make there.
//
// Agy's hooks answer on stdout, and an empty answer from Stop and
// PreInvocation is accepted, which is what `am hook` gives.
func (in Installer) planAntigravity(home string, remove bool) Plan {
	path := filepath.Join(home, ".gemini", "config", "hooks.json")
	plan := Plan{
		Kind: protocol.KindAntigravity,
		Path: path,
		Note: "Antigravity's IDE reads this file too; Agentman ignores events that are not the CLI's",
	}
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		plan.Err = err
		return plan
	}
	plan.Before = string(raw)
	plan.beforeExists = err == nil
	plan.beforeDigest = sha256.Sum256(raw)

	hooks := map[string]any{}
	if len(strings.TrimSpace(string(raw))) > 0 {
		if err := json.Unmarshal(raw, &hooks); err != nil {
			plan.Err = fmt.Errorf("%s is not valid JSON (%w) — fix or move it, then re-run", path, err)
			return plan
		}
	}
	if remove {
		delete(hooks, antigravityHookName)
	} else {
		handler := func(name Name) []any {
			return []any{map[string]any{
				"type":    "command",
				"command": shellQuote(in.Binary) + " hook " + string(protocol.KindAntigravity) + " " + string(name),
				"timeout": hookTimeoutSeconds,
			}}
		}
		hooks[antigravityHookName] = map[string]any{
			"PreInvocation": handler(NameUserPromptSubmit),
			"Stop":          handler(NameStop),
		}
	}
	if len(hooks) == 0 && !plan.beforeExists {
		return plan // nothing there, nothing to add
	}
	after, err := json.MarshalIndent(hooks, "", "  ")
	if err != nil {
		plan.Err = err
		return plan
	}
	plan.After = string(after) + "\n"
	plan.Changed = normalizeJSON(plan.Before) != normalizeJSON(plan.After)
	return plan
}
