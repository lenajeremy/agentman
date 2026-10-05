package hook

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Kiro CLI runs hooks only from an agent's own configuration. Kiro's own
// documentation (features/hooks.md, read through its introspect tool) names
// no global hook file, and its default agent, kiro_default, is built in. So
// Kiro's hooks are opt-in: `am install-hooks --kiro` writes an agent of
// Agentman's own, ~/.kiro/agents/agentman.json — kiro_default as the installed
// Kiro defines it, plus three hooks — and sessions Agentman starts run as it.
// Nothing about kiro_default or any other agent changes, and the default
// install never touches Kiro.
//
// The hooks, verified against Kiro CLI 2.27.1 with a throwaway agent:
//
//   - agentSpawn: {"hook_event_name","cwd","session_id"} when a session starts;
//   - userPromptSubmit: the same plus "prompt", when a turn starts;
//   - stop: the same plus "assistant_response", when a turn ends normally
//     (not when the user interrupts it).
//
// Kiro adds a hook's stdout to the model's context for agentSpawn and
// userPromptSubmit, so `am hook kiro` prints nothing, ever. A stop hook
// cannot continue a turn, so a message queued for a session outside tmux is
// never delivered through one; Kiro sessions outside tmux take no messages.
// preToolUse fires before every tool, approved or not, so it says nothing
// about the user being needed, and is not installed.

// KiroAgentName is the agent Agentman owns.
const KiroAgentName = "agentman"

// kiroAgentDescription marks the file as Agentman's. A file at that path with
// any other description is the user's, and is never replaced or removed.
const kiroAgentDescription = "Kiro's default agent, with Agentman's hooks (managed by `am install-hooks --kiro`)"

var kiroHookEvents = []struct {
	trigger string
	name    Name
}{
	{"agentSpawn", NameSessionStart},
	{"userPromptSubmit", NameUserPromptSubmit},
	{"stop", NameStop},
}

// kiroHookName reports whether a normalized event is one a Kiro hook delivers.
func kiroHookName(name Name) bool {
	for _, event := range kiroHookEvents {
		if event.name == name {
			return true
		}
	}
	return false
}

// KiroAgentPath is where Agentman's Kiro agent lives.
func KiroAgentPath(home string) string {
	return filepath.Join(home, ".kiro", "agents", KiroAgentName+".json")
}

// kiroAgentIsOurs reports whether an agent file's contents are Agentman's.
func kiroAgentIsOurs(raw []byte) bool {
	var agent struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	return json.Unmarshal(raw, &agent) == nil && agent.Name == KiroAgentName &&
		agent.Description == kiroAgentDescription
}

// KiroAgentInstalled reports whether Agentman's Kiro agent exists and is ours.
func KiroAgentInstalled(home string) bool {
	raw, err := os.ReadFile(KiroAgentPath(home))
	return err == nil && kiroAgentIsOurs(raw)
}

// KiroAgentArgs returns the flag that starts a Kiro session as Agentman's
// agent — only when that agent is installed and ours, and the user's own
// arguments choose no agent of their own.
func KiroAgentArgs(home string, args []string) []string {
	for _, arg := range args {
		if arg == "--agent" || strings.HasPrefix(arg, "--agent=") {
			return nil
		}
	}
	if home == "" || !KiroAgentInstalled(home) {
		return nil
	}
	return []string{"--agent", KiroAgentName}
}

// KiroDefaultAgent returns kiro_default as the installed Kiro defines it, by
// asking Kiro to copy it into a scratch directory. Copying rather than
// keeping one here means a Kiro upgrade's changes to its default agent reach
// Agentman's on the next install.
func KiroDefaultAgent(ctx context.Context) (map[string]any, error) {
	binary, err := exec.LookPath("kiro-cli")
	if err != nil {
		return nil, errors.New("Kiro CLI is not installed (kiro-cli is not on PATH)")
	}
	dir, err := os.MkdirTemp("", "agentman-kiro-agent-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "agent", "create", "agentman-base",
		"--from", "kiro_default", "--directory", dir)
	// `agent create` opens the new file in $EDITOR; there is nothing to edit.
	cmd.Env = append(os.Environ(), "EDITOR=true", "VISUAL=true")
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("kiro-cli could not copy its default agent: %v: %s", err, strings.TrimSpace(string(out)))
	}
	raw, err := os.ReadFile(filepath.Join(dir, "agentman-base.json"))
	if err != nil {
		return nil, fmt.Errorf("kiro-cli wrote no copy of its default agent: %w", err)
	}
	agent := map[string]any{}
	if err := json.Unmarshal(raw, &agent); err != nil {
		return nil, fmt.Errorf("kiro-cli's copy of its default agent is not JSON: %w", err)
	}
	return agent, nil
}

// PlanKiro computes Agentman's Kiro agent, or its removal. base supplies
// kiro_default's configuration (KiroDefaultAgent); it is not called for a
// removal.
func (in Installer) PlanKiro(remove bool, base func() (map[string]any, error)) Plan {
	home := in.Home
	if home == "" {
		var err error
		if home, err = os.UserHomeDir(); err != nil {
			return Plan{Kind: protocol.KindKiro, Err: err}
		}
	}
	path := KiroAgentPath(home)
	plan := Plan{Kind: protocol.KindKiro, Path: path}
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		plan.Err = err
		return plan
	}
	plan.Before = string(raw)
	plan.beforeExists = err == nil
	plan.beforeDigest = sha256.Sum256(raw)
	if plan.beforeExists && !kiroAgentIsOurs(raw) {
		if remove {
			plan.After, plan.Note = plan.Before, "left alone: it is not Agentman's"
			return plan
		}
		plan.Err = fmt.Errorf("%s exists and is not Agentman's — rename or move it, then re-run", path)
		return plan
	}

	if remove {
		plan.Delete = plan.beforeExists
		plan.Changed = plan.beforeExists
		return plan
	}
	agent, err := base()
	if err != nil {
		plan.Err = err
		return plan
	}
	agent["name"] = KiroAgentName
	agent["description"] = kiroAgentDescription
	hooks := map[string]any{}
	for _, event := range kiroHookEvents {
		hooks[event.trigger] = []any{map[string]any{
			"command":    shellQuote(in.Binary) + " hook " + string(protocol.KindKiro) + " " + string(event.name),
			"timeout_ms": hookTimeoutSeconds * 1000,
		}}
	}
	agent["hooks"] = hooks
	after, err := json.MarshalIndent(agent, "", "  ")
	if err != nil {
		plan.Err = err
		return plan
	}
	plan.After = string(after) + "\n"
	plan.Changed = normalizeJSON(plan.Before) != normalizeJSON(plan.After)
	plan.Note = "Kiro sessions started with `am kiro` or from the phone run as the agent \"agentman\"; " +
		"kiro_default is unchanged"
	return plan
}
