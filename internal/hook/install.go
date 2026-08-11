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
	if err := os.MkdirAll(filepath.Dir(p.Path), 0o700); err != nil {
		return err
	}
	// Back up whatever is there now. These are the user's real agent configs;
	// a one-command undo matters more than tidiness.
	if p.Before != "" {
		if err := writeFileAtomic(p.Path+".agentman.bak", []byte(p.Before), 0o600); err != nil {
			return err
		}
	}
	return writeFileAtomic(p.Path, []byte(p.After), 0o600)
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
	path := filepath.Join(home, ".codex", "config.toml")
	plan := Plan{
		Kind: protocol.KindCodex,
		Path: path,
		Note: "uses Codex's supported `notify` command for turn-complete events",
	}
	_ = token // read by the invoked am process from its private config file

	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		plan.Err = err
		return plan
	}
	plan.Before = string(raw)

	base, _ := stripCodexNotifyBlock(plan.Before)
	if remove {
		plan.After = base
		plan.Changed = plan.After != plan.Before
		return plan
	}
	if hasTopLevelTOMLKey(base, "notify") {
		plan.Err = fmt.Errorf(
			"%s already defines `notify`; refusing to replace the user's command", path)
		return plan
	}

	quotedBinary, _ := json.Marshal(in.Binary)
	block := codexNotifyBegin + "\n" +
		"notify = [" + string(quotedBinary) + ", \"hook\", \"codex\", \"Stop\"]\n" +
		codexNotifyEnd + "\n"
	plan.After = block + strings.TrimLeft(base, "\n")
	plan.Changed = plan.After != plan.Before
	return plan
}

const (
	codexNotifyBegin = "# agentman: managed Codex turn notification"
	codexNotifyEnd   = "# agentman: end managed Codex turn notification"
)

func stripCodexNotifyBlock(text string) (string, bool) {
	start := strings.Index(text, codexNotifyBegin)
	if start < 0 {
		return text, false
	}
	relativeEnd := strings.Index(text[start:], codexNotifyEnd)
	if relativeEnd < 0 {
		return text, false
	}
	end := start + relativeEnd + len(codexNotifyEnd)
	if end < len(text) && text[end] == '\r' {
		end++
	}
	if end < len(text) && text[end] == '\n' {
		end++
	}
	return text[:start] + text[end:], true
}

// hasTopLevelTOMLKey looks only before the first table header. TOML never
// returns to the document root after entering a table, so this avoids
// mistaking `[profile.work] notify = ...` for the global Codex notifier.
func hasTopLevelTOMLKey(text, wanted string) bool {
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "[") {
			return false
		}
		key, _, ok := strings.Cut(trimmed, "=")
		if ok && strings.TrimSpace(key) == wanted {
			return true
		}
	}
	return false
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
	return base == "am" || base == "agentman"
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
