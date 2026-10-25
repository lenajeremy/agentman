package hook

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Agentman's tools for agents, am mcp, are registered in each agent's own
// MCP config, beside its hooks: a server named "agentman" that runs
// `am mcp`. Like the hooks, each registration is a Plan, shown before it is
// applied, backed up when it is, and left alone when it is already there.

// MCPServerName is what the server is called in every agent's config.
const MCPServerName = "agentman"

// MCPPlans computes Agentman's MCP server registration for every agent that
// is installed, without applying it.
func (in Installer) MCPPlans(remove bool) ([]Plan, error) {
	home := in.Home
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return nil, err
		}
	}
	installed := func(dir string) bool {
		info, err := os.Stat(filepath.Join(home, dir))
		return err == nil && info.IsDir()
	}
	stdio := map[string]any{"command": in.Binary, "args": []any{"mcp"}}

	var plans []Plan
	if plan, ok := in.planClaudeMCP(home, remove); ok {
		plans = append(plans, plan)
	}
	if installed(".codex") {
		plans = append(plans, in.planCodexMCP(home, remove))
	}
	if installed(".cursor") {
		plans = append(plans, planJSONMCP(protocol.KindCursor, filepath.Join(home, ".cursor", "mcp.json"), "mcpServers", stdio, remove))
	}
	if installed(filepath.Join(".config", "opencode")) {
		plans = append(plans, planJSONMCP(protocol.KindOpenCode, openCodeConfig(home), "mcp", map[string]any{
			"type": "local", "command": []any{in.Binary, "mcp"}, "enabled": true,
		}, remove))
	}
	if installed(".kiro") {
		plans = append(plans, planJSONMCP(protocol.KindKiro, filepath.Join(home, ".kiro", "settings", "mcp.json"), "mcpServers", stdio, remove))
	}
	// Every Antigravity app reads ~/.gemini/config, the CLI and the IDE alike
	// (the IDE's ~/.gemini/antigravity/mcp_config.json links to it).
	if installed(filepath.Join(".gemini", "config")) || antigravityInstalled(home) {
		plans = append(plans, planJSONMCP(protocol.KindAntigravity, filepath.Join(home, ".gemini", "config", "mcp_config.json"), "mcpServers", stdio, remove))
	}
	return plans, nil
}

// MCPRegistered reports whether a plan found Agentman's server in the
// agent's config with a program that is still there, whichever am it is.
func (p Plan) MCPRegistered() bool {
	if p.mcpCommand == "" {
		return false
	}
	info, err := os.Stat(p.mcpCommand)
	return err == nil && !info.IsDir()
}

// planJSONMCP puts Agentman's server into, or takes it out of, a JSON config
// that keeps its servers under key. Whatever else the user set on the entry,
// such as an environment, stays.
func planJSONMCP(kind protocol.Kind, path, key string, entry map[string]any, remove bool) Plan {
	path = followLink(path)
	plan := Plan{Kind: kind, Path: path}
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		plan.Err = err
		return plan
	}
	plan.Before = string(raw)
	plan.beforeExists = err == nil
	plan.beforeDigest = sha256.Sum256(raw)

	config := map[string]any{}
	if strings.TrimSpace(plan.Before) != "" {
		if err := json.Unmarshal(raw, &config); err != nil {
			plan.Err = fmt.Errorf("%s is not valid JSON (%w), so it was left alone; add Agentman's server by hand", path, err)
			return plan
		}
	}
	servers, _ := config[key].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	current, found := servers[MCPServerName].(map[string]any)
	switch command := current["command"].(type) {
	case string:
		plan.mcpCommand = command
	case []any:
		if len(command) > 0 {
			plan.mcpCommand, _ = command[0].(string)
		}
	}
	if remove {
		if !found {
			return plan
		}
		delete(servers, MCPServerName)
	} else {
		next := map[string]any{}
		for name, value := range current {
			next[name] = value
		}
		for name, value := range entry {
			next[name] = value
		}
		if found && sameJSON(current, next) {
			return plan
		}
		servers[MCPServerName] = next
	}
	if len(servers) == 0 {
		delete(config, key)
	} else {
		config[key] = servers
	}
	after, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		plan.Err = err
		return plan
	}
	plan.After = string(after) + "\n"
	plan.Changed = true
	return plan
}

// openCodeConfig is OpenCode's global config: opencode.json, or the
// opencode.jsonc a user keeps instead. A .jsonc with comments in it can't be
// rewritten without losing them, and is refused when planned.
func openCodeConfig(home string) string {
	dir := filepath.Join(home, ".config", "opencode")
	plain := filepath.Join(dir, "opencode.json")
	if _, err := os.Stat(plain); err == nil {
		return plain
	}
	if _, err := os.Stat(filepath.Join(dir, "opencode.jsonc")); err == nil {
		return filepath.Join(dir, "opencode.jsonc")
	}
	return plain
}

// followLink is the file a config path names, so a config kept as a link
// (to dotfiles, say) is written where it lives and stays a link.
func followLink(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}

func sameJSON(a, b any) bool {
	left, errA := json.Marshal(a)
	right, errB := json.Marshal(b)
	return errA == nil && errB == nil && string(left) == string(right)
}

/* -------------------------------- Claude -------------------------------- */

// planClaudeMCP registers the server with Claude Code at user scope. Claude
// Code writes ~/.claude.json all the time, so the change goes through its
// own `claude mcp` commands rather than an edit beside it; the file is only
// read, to see what is there.
func (in Installer) planClaudeMCP(home string, remove bool) (Plan, bool) {
	path := filepath.Join(home, ".claude.json")
	claude, lookErr := exec.LookPath("claude")
	if _, err := os.Stat(filepath.Join(home, ".claude")); err != nil && lookErr != nil {
		return Plan{}, false
	}
	plan := Plan{Kind: protocol.KindClaude, Path: path, Note: "through `claude mcp`, at user scope"}
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		plan.Err = err
		return plan, true
	}
	var config struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if len(raw) > 0 && json.Unmarshal(raw, &config) != nil {
		plan.Err = fmt.Errorf("%s is not valid JSON, so Claude Code's servers can't be read", path)
		return plan, true
	}
	current, found := config.MCPServers[MCPServerName]
	plan.mcpCommand = current.Command
	if remove {
		plan.Changed = found
	} else {
		plan.Changed = !found || current.Command != in.Binary || strings.Join(current.Args, " ") != "mcp"
	}
	if !plan.Changed {
		return plan, true
	}
	if lookErr != nil {
		plan.Err = errors.New("the claude command isn't on PATH, so Claude Code can't be given the server")
		return plan, true
	}
	steps := [][]string{{"mcp", "remove", "--scope", "user", MCPServerName}}
	if !remove {
		steps = append(steps, []string{"mcp", "add", "--scope", "user", MCPServerName, "--", in.Binary, "mcp"})
	}
	plan.run = func() error {
		for i, step := range steps {
			out, err := exec.Command(claude, step...).CombinedOutput()
			// Taking out the server before adding it again may find none,
			// and that's fine.
			if err == nil || (i == 0 && !remove) {
				continue
			}
			return fmt.Errorf("claude %s: %v: %s", strings.Join(step, " "), err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	plan.Before, plan.After = "", "claude "+strings.Join(steps[len(steps)-1], " ")
	return plan, true
}

/* --------------------------------- Codex -------------------------------- */

const codexMCPTable = "[mcp_servers." + MCPServerName + "]"

func (in Installer) planCodexMCP(home string, remove bool) Plan {
	path := followLink(filepath.Join(home, ".codex", "config.toml"))
	plan := Plan{Kind: protocol.KindCodex, Path: path}
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		plan.Err = err
		return plan
	}
	plan.Before = string(raw)
	plan.beforeExists = err == nil
	plan.beforeDigest = sha256.Sum256(raw)
	plan.mcpCommand = codexMCPCommand(plan.Before)
	plan.After = CodexWithMCP(plan.Before, in.Binary, remove)
	plan.Changed = plan.After != plan.Before
	return plan
}

// codexMCPCommand is the command in Agentman's server table, if it has one.
func codexMCPCommand(text string) string {
	inside := false
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			inside = trimmed == codexMCPTable
			continue
		}
		name, value, ok := strings.Cut(trimmed, "=")
		if !inside || !ok || strings.TrimSpace(name) != "command" {
			continue
		}
		var command string
		if json.Unmarshal([]byte(strings.TrimSpace(value)), &command) == nil {
			return command
		}
	}
	return ""
}

// CodexWithMCP is Codex's config.toml with Agentman's server table in or
// out. Everything else in the file stays as it was, and a table that is
// already right is left exactly where it is.
func CodexWithMCP(text, binary string, remove bool) string {
	lines := strings.Split(text, "\n")
	var kept, ours []string
	inside := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			inside = trimmed == codexMCPTable || strings.HasPrefix(trimmed, "[mcp_servers."+MCPServerName+".")
		}
		if inside {
			ours = append(ours, line)
		} else {
			kept = append(kept, line)
		}
	}
	quoted, _ := json.Marshal(binary)
	table := codexMCPTable + "\ncommand = " + string(quoted) + "\nargs = [\"mcp\"]"
	if !remove && strings.TrimSpace(strings.Join(ours, "\n")) == table {
		return text
	}
	if remove && len(ours) == 0 {
		return text
	}
	rest := strings.TrimRight(strings.Join(kept, "\n"), "\n")
	if remove {
		if rest == "" {
			return ""
		}
		return rest + "\n"
	}
	if rest == "" {
		return table + "\n"
	}
	return rest + "\n\n" + table + "\n"
}
