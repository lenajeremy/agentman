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

// Cursor's Agent CLI runs lifecycle hooks from ~/.cursor/hooks.json:
//
//	{"version": 1, "hooks": {"stop": [{"command": "…", "timeout": 5}], …}}
//
// Verified against the installed CLI: each hook gets a JSON payload on stdin
// whose session_id is the chat id, and "stop" carries how the turn ended
// (status: completed, aborted or error). A stop hook may answer
// {"followup_message": "…"} to continue the chat with that message, and
// Cursor accepts Claude's {"decision":"block","reason":"…"} as the same
// thing, so the daemon's queued-message reply works unchanged.
//
// Event names are Cursor's own; the command passes the normalized name the
// daemon already understands. "command" is a shell command line, not an
// argv, so the binary is quoted.
var cursorHookEvents = []struct {
	event string
	name  Name
}{
	{"sessionStart", NameSessionStart},
	{"beforeSubmitPrompt", NameUserPromptSubmit},
	{"stop", NameStop},
	{"sessionEnd", NameSessionEnd},
}

// cursorHookName reports whether a normalized event is one a Cursor hook
// delivers.
func cursorHookName(name Name) bool {
	for _, event := range cursorHookEvents {
		if event.name == name {
			return true
		}
	}
	return false
}

func (in Installer) planCursor(home string, remove bool) Plan {
	path := filepath.Join(home, ".cursor", "hooks.json")
	plan := Plan{
		Kind: protocol.KindCursorCLI,
		Path: path,
		Note: "Cursor Agent CLI reads this when a chat starts; chats already open keep their old hooks",
	}
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		plan.Err = err
		return plan
	}
	plan.Before = string(raw)
	plan.beforeExists = err == nil
	plan.beforeDigest = sha256.Sum256(raw)
	if remove && !plan.beforeExists {
		// Nothing installed, and nothing to create for an uninstall.
		plan.After = plan.Before
		return plan
	}

	config := map[string]any{}
	if strings.TrimSpace(string(raw)) != "" {
		if err := json.Unmarshal(raw, &config); err != nil {
			// Cursor accepts comments in this file; refuse rather than
			// rewrite something that cannot be read back faithfully.
			plan.Err = fmt.Errorf("%s is not plain JSON (%w) — fix or move it, then re-run", path, err)
			return plan
		}
	}
	if _, ok := config["version"]; !ok {
		config["version"] = 1
	}
	hooks := map[string]any{}
	if existing, ok := config["hooks"].(map[string]any); ok {
		hooks = existing
	} else if config["hooks"] != nil {
		plan.Err = fmt.Errorf("%s has a \"hooks\" value that is not an object; refusing to replace it", path)
		return plan
	}
	for _, event := range cursorHookEvents {
		entries, _ := hooks[event.event].([]any)
		kept := make([]any, 0, len(entries)+1)
		for _, entry := range entries {
			if !isOurCursorHook(entry) {
				kept = append(kept, entry)
			}
		}
		if !remove {
			kept = append(kept, map[string]any{
				"command": cursorHookCommand(in.Binary, event.name),
				"timeout": hookTimeoutSeconds,
			})
		}
		if len(kept) == 0 {
			delete(hooks, event.event)
			continue
		}
		hooks[event.event] = kept
	}
	if len(hooks) == 0 && remove {
		delete(config, "hooks")
	} else {
		config["hooks"] = hooks
	}
	after, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		plan.Err = err
		return plan
	}
	plan.After = string(after) + "\n"
	plan.Changed = normalizeJSON(plan.Before) != normalizeJSON(plan.After)
	return plan
}

// cursorHookCommand is the shell line Cursor runs. The token is never in it:
// `am hook` reads it from the private config file.
func cursorHookCommand(binary string, name Name) string {
	return shellQuote(binary) + " hook " + string(protocol.KindCursorCLI) + " " + string(name)
}

func shellQuote(value string) string {
	if value != "" && strings.Trim(value, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789/._-+") == "" {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// isOurCursorHook recognises an entry this installer wrote, by the binary's
// name and our arguments, so a moved or upgraded binary is still replaced and
// nobody else's hook is touched.
func isOurCursorHook(value any) bool {
	entry, ok := value.(map[string]any)
	if !ok {
		return false
	}
	command, _ := entry["command"].(string)
	marker := " hook " + string(protocol.KindCursorCLI) + " "
	at := strings.Index(command, marker)
	if at <= 0 {
		return false
	}
	binary := strings.Trim(strings.TrimSpace(command[:at]), "'")
	base := filepath.Base(binary)
	return base == "am" || base == "agentman"
}
