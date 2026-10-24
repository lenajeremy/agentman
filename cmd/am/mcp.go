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
// am mcp install registers it with Claude Code, Codex and Cursor;
// am mcp uninstall takes it out again.

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

// runMCPInstall registers am mcp with the agent CLIs that are installed,
// or takes it out again.
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
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	did := false
	if claude, err := exec.LookPath("claude"); err == nil {
		did = true
		steps := [][]string{{"mcp", "remove", "--scope", "user", "agentman"}}
		if !remove {
			steps = append(steps, []string{"mcp", "add", "--scope", "user", "agentman", "--", binary, "mcp"})
		}
		for i, step := range steps {
			if *dryRun {
				fmt.Printf("would run: claude %s\n", strings.Join(step, " "))
				continue
			}
			out, err := exec.Command(claude, step...).CombinedOutput()
			// Removing one that isn't there fails, and that's fine.
			if err != nil && i > 0 {
				return fmt.Errorf("claude %s: %v: %s", strings.Join(step, " "), err, strings.TrimSpace(string(out)))
			}
		}
		if !*dryRun {
			fmt.Println(doneLine(remove, "Claude Code"))
		}
	}
	codexConfig := filepath.Join(home, ".codex", "config.toml")
	if _, err := os.Stat(filepath.Dir(codexConfig)); err == nil {
		did = true
		if err := editFile(codexConfig, *dryRun, func(text string) string {
			return codexWithAgentman(text, binary, remove)
		}); err != nil {
			return err
		}
		if !*dryRun {
			fmt.Println(doneLine(remove, "Codex"))
		}
	}
	cursorConfig := filepath.Join(home, ".cursor", "mcp.json")
	if _, err := os.Stat(filepath.Dir(cursorConfig)); err == nil {
		did = true
		var editErr error
		if err := editFile(cursorConfig, *dryRun, func(text string) string {
			next, err := cursorWithAgentman(text, binary, remove)
			if err != nil {
				editErr = err
				return text
			}
			return next
		}); err != nil {
			return err
		}
		if editErr != nil {
			return fmt.Errorf("%s: %w", cursorConfig, editErr)
		}
		if !*dryRun {
			fmt.Println(doneLine(remove, "Cursor"))
		}
	}
	if !did {
		fmt.Println("No Claude Code, Codex or Cursor found to register with.")
	}
	return nil
}

func doneLine(remove bool, agent string) string {
	if remove {
		return "Took Agentman's tools out of " + agent + "."
	}
	return "Gave " + agent + " Agentman's tools. New sessions have them."
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

// editFile rewrites a config file with change, keeping its mode, or prints
// what would change.
func editFile(path string, dryRun bool, change func(string) string) error {
	current, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	next := change(string(current))
	if next == string(current) {
		return nil
	}
	if dryRun {
		fmt.Printf("would write %s:\n%s\n", path, next)
		return nil
	}
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	return os.WriteFile(path, []byte(next), mode)
}

const codexTable = "[mcp_servers.agentman]"

// codexWithAgentman is Codex's config.toml with Agentman's server table
// in or out; everything else in the file stays as it was.
func codexWithAgentman(text, binary string, remove bool) string {
	lines := strings.Split(text, "\n")
	var kept []string
	inside := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			inside = trimmed == codexTable || strings.HasPrefix(trimmed, "[mcp_servers.agentman.")
		}
		if !inside {
			kept = append(kept, line)
		}
	}
	text = strings.TrimRight(strings.Join(kept, "\n"), "\n")
	if remove {
		if text == "" {
			return ""
		}
		return text + "\n"
	}
	quoted, _ := json.Marshal(binary)
	table := codexTable + "\ncommand = " + string(quoted) + "\nargs = [\"mcp\"]\n"
	if text == "" {
		return table
	}
	return text + "\n\n" + table
}

// cursorWithAgentman is Cursor's mcp.json with Agentman's server in or out.
func cursorWithAgentman(text, binary string, remove bool) (string, error) {
	config := map[string]any{}
	if strings.TrimSpace(text) != "" {
		if err := json.Unmarshal([]byte(text), &config); err != nil {
			return "", fmt.Errorf("not valid JSON, so left alone: %w", err)
		}
	}
	servers, _ := config["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	if remove {
		delete(servers, "agentman")
	} else {
		servers["agentman"] = map[string]any{"command": binary, "args": []string{"mcp"}}
	}
	config["mcpServers"] = servers
	raw, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return "", err
	}
	return string(raw) + "\n", nil
}
