package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/lenajeremy/agentman/internal/hook"
	"github.com/lenajeremy/agentman/internal/protocol"
)

// am mcp is Agentman's tools for agents: an MCP server an agent CLI starts
// and talks to over stdin and stdout. Each call goes to am serve over the
// local socket, so an agent reads the user's shared notes, asks for a key,
// or tells Agentman something, without the user in between.
//
// am install-hooks and am mcp install register it with every agent that is
// installed, and am serve with any that lack it once Agentman is set up;
// am mcp uninstall takes it out again, for good.

// mcpInstructions is what the agent is told about the server when it
// connects.
const mcpInstructions = `Agentman is the user's agent manager: it watches all of their coding agents for them. Use these tools:
- search_notes and read_note: the user's shared notes (style guides, conventions, how they like things done). Check them before guessing a preference.
- list_keys and request_key: API keys the user keeps for their agents (OpenAI, Gemini, ElevenLabs...). Ask for one here instead of asking the user to paste it; it is written into your project's .env, or the user is asked first.
- tell_agentman: tell the user's manager something worth knowing (you finished something substantial, you are blocked), or ask for the user with needs_user, and their answer comes back to you as a message.`

// The MCP versions this server speaks, newest first.
var mcpVersions = []string{"2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05"}

type mcpTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	// request turns the call's arguments into a request to the daemon.
	request func(args map[string]any) (protocol.Request, error) `json:"-"`
}

func stringArg(args map[string]any, name string) string {
	value, _ := args[name].(string)
	return strings.TrimSpace(value)
}

func schema(properties map[string]any, required ...string) map[string]any {
	out := map[string]any{"type": "object", "properties": properties}
	if len(required) > 0 {
		out["required"] = required
	}
	return out
}

var mcpTools = []mcpTool{
	{
		Name:        "search_notes",
		Description: "Search the user's shared notes for their agents: style guides, conventions, how they like things done, facts about their projects.",
		InputSchema: schema(map[string]any{"query": map[string]any{"type": "string", "description": "Words to look for."}}, "query"),
		request: func(args map[string]any) (protocol.Request, error) {
			query := stringArg(args, "query")
			if query == "" {
				return protocol.Request{}, errors.New("say what to search for")
			}
			return protocol.Request{Type: protocol.ReqAgentKnowledgeSearch, Text: query}, nil
		},
	},
	{
		Name:        "read_note",
		Description: "Read one of the user's shared notes in full, by the name search_notes gave.",
		InputSchema: schema(map[string]any{"name": map[string]any{"type": "string"}}, "name"),
		request: func(args map[string]any) (protocol.Request, error) {
			return protocol.Request{Type: protocol.ReqAgentKnowledgeRead, Text: stringArg(args, "name")}, nil
		},
	},
	{
		Name:        "list_keys",
		Description: "The API keys the user keeps for their agents, by name, and whether the user is asked before one is given. Values are never shown.",
		InputSchema: schema(map[string]any{}),
		request: func(map[string]any) (protocol.Request, error) {
			return protocol.Request{Type: protocol.ReqAgentKeys}, nil
		},
	},
	{
		Name: "request_key",
		Description: "Ask for one of the user's API keys. It is written into the .env in your project, when git ignores .env, " +
			"or the user is asked first and you are messaged once it is there. Read it from the environment; never print it.",
		InputSchema: schema(map[string]any{
			"name":   map[string]any{"type": "string", "description": "The key, as list_keys names it: ELEVENLABS_API_KEY."},
			"reason": map[string]any{"type": "string", "description": "What you need it for, in a sentence, for the user if they are asked."},
		}, "name", "reason"),
		request: func(args map[string]any) (protocol.Request, error) {
			return protocol.Request{Type: protocol.ReqAgentRequestKey, Text: stringArg(args, "name"), AnswerText: stringArg(args, "reason")}, nil
		},
	},
	{
		Name: "tell_agentman",
		Description: "Tell the user's manager, Agentman, something worth knowing: you finished something substantial, " +
			"you are blocked, something went wrong. With needs_user, the user gets it as a card with a box to reply, " +
			"and their reply comes to you as a message.",
		InputSchema: schema(map[string]any{
			"message":    map[string]any{"type": "string", "description": "What to say, in a sentence or two."},
			"needs_user": map[string]any{"type": "boolean", "description": "Ask the user, and wait for their reply. Default false."},
		}, "message"),
		request: func(args map[string]any) (protocol.Request, error) {
			needs, _ := args["needs_user"].(bool)
			return protocol.Request{Type: protocol.ReqAgentTell, Text: stringArg(args, "message"), Approve: needs}, nil
		},
	},
}

// daemonCall sends one request to am serve and returns its answer.
type daemonCall func(ctx context.Context, req protocol.Request) (protocol.Event, error)

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// serveMCP answers MCP requests from in on out until in ends. Every call
// carries the agent's folder and this process's ID, so the daemon can tell
// which agent is asking.
func serveMCP(ctx context.Context, in io.Reader, out io.Writer, call daemonCall, cwd string, pid int) error {
	reader := bufio.NewReader(in)
	writer := bufio.NewWriter(out)
	send := func(message rpcMessage) error {
		message.JSONRPC = "2.0"
		raw, err := json.Marshal(message)
		if err != nil {
			return err
		}
		if _, err := writer.Write(append(raw, '\n')); err != nil {
			return err
		}
		return writer.Flush()
	}
	for {
		line, err := reader.ReadBytes('\n')
		if len(strings.TrimSpace(string(line))) > 0 {
			var message rpcMessage
			if json.Unmarshal(line, &message) != nil {
				if err := send(rpcMessage{ID: json.RawMessage("null"), Error: &rpcError{Code: -32700, Message: "parse error"}}); err != nil {
					return err
				}
			} else if len(message.ID) > 0 && message.Method != "" {
				// Requests get an answer; notifications, which have no ID,
				// don't.
				result, failure := answerMCP(ctx, message, call, cwd, pid)
				if err := send(rpcMessage{ID: message.ID, Result: result, Error: failure}); err != nil {
					return err
				}
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func answerMCP(ctx context.Context, message rpcMessage, call daemonCall, cwd string, pid int) (any, *rpcError) {
	switch message.Method {
	case "initialize":
		var params struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(message.Params, &params)
		version := mcpVersions[1]
		if slices.Contains(mcpVersions, params.ProtocolVersion) {
			version = params.ProtocolVersion
		}
		return map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "agentman", "title": "Agentman", "version": version},
			"instructions":    mcpInstructions,
		}, nil
	case "ping":
		return map[string]any{}, nil
	case "tools/list":
		return map[string]any{"tools": mcpTools}, nil
	case "tools/call":
		var params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if json.Unmarshal(message.Params, &params) != nil {
			return nil, &rpcError{Code: -32602, Message: "invalid params"}
		}
		for _, tool := range mcpTools {
			if tool.Name != params.Name {
				continue
			}
			text, failed := callTool(ctx, tool, params.Arguments, call, cwd, pid)
			return map[string]any{
				"content": []map[string]any{{"type": "text", "text": text}},
				"isError": failed,
			}, nil
		}
		return nil, &rpcError{Code: -32602, Message: "unknown tool " + params.Name}
	}
	return nil, &rpcError{Code: -32601, Message: "method not found: " + message.Method}
}

// callTool runs one tool, and says what happened in words for the agent.
func callTool(ctx context.Context, tool mcpTool, args map[string]any, call daemonCall, cwd string, pid int) (string, bool) {
	req, err := tool.request(args)
	if err != nil {
		return err.Error(), true
	}
	req.Path, req.Port = cwd, pid
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	event, err := call(ctx, req)
	if err != nil {
		return "Agentman isn't reachable: " + err.Error() + ". Is am serve running?", true
	}
	if event.Type == protocol.EvtError {
		return strings.TrimPrefix(event.Error, "daemon: "), true
	}
	return event.Preview, false
}

// socketCall reaches am serve over its local socket, one connection a call.
func socketCall(path string) daemonCall {
	return func(ctx context.Context, req protocol.Request) (protocol.Event, error) {
		var dialer net.Dialer
		conn, err := dialer.DialContext(ctx, "unix", path)
		if err != nil {
			return protocol.Event{}, err
		}
		defer conn.Close()
		if deadline, ok := ctx.Deadline(); ok {
			_ = conn.SetDeadline(deadline)
		}
		payload, err := json.Marshal(req)
		if err != nil {
			return protocol.Event{}, err
		}
		id := fmt.Sprintf("mcp-%d", time.Now().UnixNano())
		frame, _ := json.Marshal(protocol.Envelope{V: protocol.Version, ID: id, To: protocol.PeerDaemon, Payload: payload})
		if _, err := conn.Write(append(frame, '\n')); err != nil {
			return protocol.Event{}, err
		}
		// Other apps' events come down the same socket; only the answer to
		// this request matters.
		reader := bufio.NewReader(conn)
		for {
			line, err := reader.ReadBytes('\n')
			if err != nil {
				return protocol.Event{}, err
			}
			var envelope protocol.Envelope
			if json.Unmarshal(line, &envelope) != nil || envelope.ReplyTo != id {
				continue
			}
			var event protocol.Event
			if err := json.Unmarshal(envelope.Payload, &event); err != nil {
				return protocol.Event{}, err
			}
			return event, nil
		}
	}
}

func socketPath() (string, error) {
	if path := os.Getenv("AGENTMAN_SOCKET"); path != "" {
		return path, nil
	}
	dir, err := hook.ConfigDir("")
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "am.sock"), nil
}

func runMCP(ctx context.Context, args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "install":
			return runMCPInstall(args[1:], false)
		case "uninstall":
			return runMCPInstall(args[1:], true)
		}
	}
	path, err := socketPath()
	if err != nil {
		return err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	return serveMCP(ctx, os.Stdin, os.Stdout, socketCall(path), cwd, os.Getpid())
}

// runMCPInstall registers am mcp with every agent that is installed, or
// takes it out again. Taking it out also keeps am serve from putting it back.
func runMCPInstall(args []string, remove bool) error {
	fs := flag.NewFlagSet("mcp install", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "show what would change without changing it")
	if err := fs.Parse(args); err != nil {
		return err
	}
	binary, err := stableBinary()
	if err != nil {
		return err
	}
	plans, err := hook.Installer{Binary: binary}.MCPPlans(remove)
	if err != nil {
		return err
	}
	if len(plans) == 0 {
		fmt.Println("No Claude Code, Codex, Cursor, OpenCode, Kiro or Antigravity found to register with.")
	}
	failures := applyMCPPlans(plans, remove, *dryRun)
	if !*dryRun {
		if err := rememberAgentTools(!remove); err != nil {
			return err
		}
	}
	if failures > 0 {
		return fmt.Errorf("%d agent(s) could not be configured", failures)
	}
	return nil
}

// applyMCPPlans applies Agentman's MCP registrations, a line for each agent
// in install-hooks' style, and returns how many failed.
func applyMCPPlans(plans []hook.Plan, remove, dryRun bool) int {
	failures := 0
	for _, plan := range plans {
		where := collapseHome(plan.Path)
		switch {
		case plan.Err != nil:
			failures++
			fmt.Printf("  ✗ %-8s %s\n", plan.Kind, plan.Err)
		case !plan.Changed && remove:
			fmt.Printf("  ✓ %-8s no Agentman tools to remove\n", plan.Kind)
		case !plan.Changed:
			fmt.Printf("  ✓ %-8s already has Agentman's tools %s\n", plan.Kind, dim(where))
		case dryRun && remove:
			fmt.Printf("  · %-8s would remove Agentman's tools from %s\n", plan.Kind, where)
		case dryRun:
			fmt.Printf("  · %-8s would add Agentman's tools to %s\n", plan.Kind, where)
		default:
			if err := plan.Apply(); err != nil {
				failures++
				fmt.Printf("  ✗ %-8s %v\n", plan.Kind, err)
				continue
			}
			if remove {
				fmt.Printf("  ✓ %-8s removed Agentman's tools from %s\n", plan.Kind, where)
			} else {
				fmt.Printf("  ✓ %-8s added Agentman's tools (am mcp) to %s\n", plan.Kind, where)
			}
		}
	}
	return failures
}

// rememberAgentTools keeps the user's choice: am serve gives agents
// Agentman's tools unless they were taken out.
func rememberAgentTools(on bool) error {
	cfg, err := hook.LoadConfig("")
	if err != nil || cfg.NoAgentTools == !on {
		return err
	}
	cfg.NoAgentTools = !on
	return hook.SaveConfig("", cfg)
}

// giveAgentsTools registers am mcp, when am serve starts, with each agent
// that doesn't have it, or has it pointing at an am that is gone. One that
// names another am that still works is left alone: am mcp install is how
// the user picks which.
func giveAgentsTools(cfg hook.Config) {
	if cfg.NoAgentTools {
		return
	}
	binary, err := stableBinary()
	if err != nil {
		return
	}
	plans, err := hook.Installer{Binary: binary}.MCPPlans(false)
	if err != nil {
		return
	}
	for _, plan := range plans {
		if plan.Err != nil || !plan.Changed || plan.MCPRegistered() {
			continue
		}
		if err := plan.Apply(); err != nil {
			fmt.Printf("%s %s\n", stamp(), dim(fmt.Sprintf("agentman   couldn't give %s Agentman's tools: %v", plan.Kind, err)))
			continue
		}
		fmt.Printf("%s %s\n", stamp(), dim(fmt.Sprintf("agentman   gave %s Agentman's tools (am mcp); new sessions have them", plan.Kind)))
	}
}

// stableBinary is the am to register: the one on PATH when it is this
// program, since an upgrade replaces what it points at; this program's own
// path otherwise.
func stableBinary() (string, error) {
	self, err := os.Executable()
	if err != nil {
		return "", err
	}
	if onPath, err := exec.LookPath("am"); err == nil {
		a, errA := os.Stat(onPath)
		b, errB := os.Stat(self)
		if errA == nil && errB == nil && os.SameFile(a, b) {
			return onPath, nil
		}
	}
	return self, nil
}
