package hook

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// hookTimeoutSeconds bounds each hook invocation. Hooks run while the agent
// waits, so this is deliberately short: if the daemon is wedged, the user's
// session should stutter briefly, not hang.
const hookTimeoutSeconds = 5

// Installer writes hook registrations into the agent CLIs' config files.
type Installer struct {
	// Home is the user's home directory; empty means the real one.
	Home string
	// Binary is the absolute path to the am executable that hooks will call.
	Binary string
}

// Plan describes what an install or uninstall would change, so the caller can
// show it before touching anything.
type Plan struct {
	Kind protocol.Kind
	Path string
	// Before and After are the rendered config files.
	Before string
	After  string
	// Changed is false when the config already matches the desired state,
	// which makes install idempotent and re-runnable.
	Changed bool
	// Note carries a caveat worth showing the user.
	Note string
	// Err is set when this agent cannot be configured, without preventing
	// other agents from being configured.
	Err error
}

// command is one hook handler entry.
//
// The exec form (command plus args) is used rather than a shell string so
// nothing in a path needs quoting and no shell is spawned per event.
type command struct {
	Type    string   `json:"type"`
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
	Timeout int      `json:"timeout,omitempty"`
}

// matcherGroup is the { matcher, hooks } shape both CLIs use.
type matcherGroup struct {
	Matcher string    `json:"matcher,omitempty"`
	Hooks   []command `json:"hooks"`
}

// Plans computes the changes for every supported agent without applying them.
func (in Installer) Plans(token string, remove bool) ([]Plan, error) {
	home := in.Home
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return nil, err
		}
	}

	return []Plan{
		in.planClaude(home, token, remove),
		in.planCodex(home, token, remove),
	}, nil
}

// Apply writes a plan to disk, keeping a backup of the previous contents.
func (p Plan) Apply() error {
	if p.Err != nil {
		return p.Err
	}
	if !p.Changed {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p.Path), 0o755); err != nil {
		return err
	}
	// Back up whatever is there now. These are the user's real agent configs;
	// a one-command undo matters more than tidiness.
	if p.Before != "" {
		if err := writeFileAtomic(p.Path+".agentman.bak", []byte(p.Before), 0o644); err != nil {
			return err
		}
	}
	return writeFileAtomic(p.Path, []byte(p.After), 0o644)
}

/* --------------------------------- Claude -------------------------------- */

func (in Installer) planClaude(home, token string, remove bool) Plan {
	path := filepath.Join(home, ".claude", "settings.json")
	plan := Plan{Kind: protocol.KindClaude, Path: path}

	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		plan.Err = err
		return plan
	}
	plan.Before = string(raw)

	// Preserve unknown keys and their order as far as Go allows: decode into a
	// generic map so settings we know nothing about survive the round trip.
	settings := map[string]any{}
	if len(strings.TrimSpace(string(raw))) > 0 {
		if err := json.Unmarshal(raw, &settings); err != nil {
			// Refuse rather than guess. This file holds the user's real
			// configuration, and overwriting an unparseable one would destroy
			// settings we cannot read.
			plan.Err = fmt.Errorf(
				"%s is not valid JSON (%w) — fix or move it, then re-run", path, err)
			return plan
		}
	}

	hooks := map[string]any{}
	if existing, ok := settings["hooks"].(map[string]any); ok {
		hooks = existing
	}

	for _, name := range Installed {
		key := ClaudeEventKey(name)
		groups := stripOurGroups(hooks[key])
		if !remove {
			groups = append(groups, map[string]any{
				"hooks": []any{ourCommand(in.Binary, protocol.KindClaude, name, token)},
			})
		}
		if len(groups) == 0 {
			delete(hooks, key)
			continue
		}
		hooks[key] = groups
	}

	if len(hooks) == 0 {
		delete(settings, "hooks")
	} else {
		settings["hooks"] = hooks
	}

	after, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		plan.Err = err
		return plan
	}
	plan.After = string(after) + "\n"
	plan.Changed = normalizeJSON(plan.Before) != normalizeJSON(plan.After)
	return plan
}

/* --------------------------------- Codex --------------------------------- */

func (in Installer) planCodex(home, token string, remove bool) Plan {
	path := filepath.Join(home, ".codex", "hooks.json")
	plan := Plan{
		Kind: protocol.KindCodex,
		Path: path,
		// Stated plainly because we could not confirm it. The Codex binary
		// contains the same hook machinery and snake_case event names, but it
		// ships no schema, `codex doctor` does not validate hooks, and
		// confirming it needs a live authenticated session. `am doctor`
		// reports whether Codex hooks have ever actually fired, so this stays
		// visible instead of being assumed.
		Note: "schema unverified — run `am doctor` after a Codex turn to confirm it fires",
	}

	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		plan.Err = err
		return plan
	}
	plan.Before = string(raw)

	config := map[string]any{}
	if len(strings.TrimSpace(string(raw))) > 0 {
		if err := json.Unmarshal(raw, &config); err != nil {
			plan.Err = fmt.Errorf("%s is not valid JSON (%w) — fix or move it, then re-run", path, err)
			return plan
		}
	}

	for _, name := range Installed {
		key := CodexEventKey(name)
		groups := stripOurGroups(config[key])
		if !remove {
			groups = append(groups, map[string]any{
				"hooks": []any{ourCommand(in.Binary, protocol.KindCodex, name, token)},
			})
		}
		if len(groups) == 0 {
			delete(config, key)
			continue
		}
		config[key] = groups
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

/* --------------------------------- shared -------------------------------- */

// ourCommand builds the handler entry that calls this binary back.
func ourCommand(binary string, kind protocol.Kind, name Name, token string) map[string]any {
	entry := map[string]any{
		"type":    "command",
		"command": binary,
		"args":    []any{"hook", string(kind), string(name)},
		"timeout": hookTimeoutSeconds,
	}
	_ = token // the token is read from ~/.agentman/config.json, never placed
	// in argv where it would be visible to every process on the machine.
	return entry
}

// stripOurGroups removes previously installed agentman entries, leaving every
// other hook the user has configured untouched.
//
// This is what makes install idempotent and uninstall surgical: we identify
// our own entries by their argv shape rather than rewriting the whole key.
func stripOurGroups(value any) []any {
	groups, ok := value.([]any)
	if !ok {
		return nil
	}
	kept := make([]any, 0, len(groups))

	for _, raw := range groups {
		group, ok := raw.(map[string]any)
		if !ok {
			kept = append(kept, raw)
			continue
		}
		commands, ok := group["hooks"].([]any)
		if !ok {
			kept = append(kept, raw)
			continue
		}

		remaining := make([]any, 0, len(commands))
		for _, c := range commands {
			if !isOurCommand(c) {
				remaining = append(remaining, c)
			}
		}
		if len(remaining) == 0 {
			// The whole group was ours; drop it rather than leave an empty
			// shell behind in the user's config.
			continue
		}
		group["hooks"] = remaining
		kept = append(kept, group)
	}
	return kept
}

func isOurCommand(value any) bool {
	entry, ok := value.(map[string]any)
	if !ok {
		return false
	}
	args, ok := entry["args"].([]any)
	if !ok || len(args) == 0 {
		return false
	}
	if first, ok := args[0].(string); !ok || first != "hook" {
		return false
	}
	// Match on the binary's name rather than its full path, so an install that
	// moved (a rebuild into a different directory, or a brew upgrade) is still
	// recognized and replaced instead of accumulating duplicates.
	command, _ := entry["command"].(string)
	base := filepath.Base(command)
	return base == "am" || strings.Contains(base, "agentman")
}

// normalizeJSON compares configs by structure rather than formatting, so
// whitespace differences do not register as a change.
func normalizeJSON(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	var value any
	if json.Unmarshal([]byte(text), &value) != nil {
		return text
	}
	out, err := json.Marshal(value)
	if err != nil {
		return text
	}
	return string(out)
}
