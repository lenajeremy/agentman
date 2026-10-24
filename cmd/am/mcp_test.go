package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/daemon"
	"github.com/lenajeremy/agentman/internal/protocol"
)

// mcpSession runs the server over lines, with a daemon that answers every
// request with answer, and returns each response by its id.
func mcpSession(t *testing.T, lines []string, answer daemonCall) (map[string]rpcMessage, []protocol.Request) {
	t.Helper()
	var asked []protocol.Request
	call := func(ctx context.Context, req protocol.Request) (protocol.Event, error) {
		asked = append(asked, req)
		return answer(ctx, req)
	}
	var out strings.Builder
	if err := serveMCP(context.Background(), strings.NewReader(strings.Join(lines, "\n")+"\n"), &out, call, "/w/site", 4242); err != nil {
		t.Fatal(err)
	}
	responses := map[string]rpcMessage{}
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		var message struct {
			ID     json.RawMessage `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *rpcError       `json:"error"`
		}
		if err := json.Unmarshal([]byte(line), &message); err != nil {
			t.Fatalf("not JSON: %s", line)
		}
		responses[string(message.ID)] = rpcMessage{Result: message.Result, Error: message.Error}
	}
	return responses, asked
}

func resultOf(t *testing.T, message rpcMessage, into any) {
	t.Helper()
	raw, _ := message.Result.(json.RawMessage)
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatalf("result %s: %v", raw, err)
	}
}

// An agent CLI connects, lists the tools and calls one; each call reaches
// the daemon with the agent's folder and process, and the answer comes
// back as text.
func TestAnAgentUsesAgentmansTools(t *testing.T) {
	responses, asked := mcpSession(t, []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"claude-code"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"search_notes","arguments":{"query":"commit style"}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"request_key","arguments":{"name":"ELEVENLABS_API_KEY","reason":"narration"}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"tell_agentman","arguments":{"message":"Which region?","needs_user":true}}}`,
		`{"jsonrpc":"2.0","id":6,"method":"resources/list"}`,
		`not json`,
	}, func(_ context.Context, req protocol.Request) (protocol.Event, error) {
		if req.Type == protocol.ReqAgentRequestKey {
			return protocol.Event{Type: protocol.EvtError, Error: "daemon: there is no key called ELEVENLABS_API_KEY"}, nil
		}
		return protocol.Event{Type: protocol.EvtAgentReply, Preview: "answer to " + string(req.Type)}, nil
	})

	var initialized struct {
		ProtocolVersion string `json:"protocolVersion"`
		ServerInfo      struct{ Name string }
		Instructions    string
	}
	resultOf(t, responses["1"], &initialized)
	if initialized.ProtocolVersion != "2025-06-18" || initialized.ServerInfo.Name != "agentman" || initialized.Instructions == "" {
		t.Fatalf("initialize = %+v", initialized)
	}
	if len(responses) != 7 {
		t.Fatalf("%d responses; a notification must not be answered", len(responses))
	}

	var listed struct{ Tools []mcpTool }
	resultOf(t, responses["2"], &listed)
	var names []string
	for _, tool := range listed.Tools {
		names = append(names, tool.Name)
		if tool.InputSchema["type"] != "object" {
			t.Errorf("%s has no object schema", tool.Name)
		}
	}
	if strings.Join(names, ",") != "search_notes,read_note,list_keys,request_key,tell_agentman" {
		t.Fatalf("tools = %v", names)
	}

	type callResult struct {
		Content []struct{ Type, Text string }
		IsError bool
	}
	var searched, keyed callResult
	resultOf(t, responses["3"], &searched)
	resultOf(t, responses["4"], &keyed)
	if searched.IsError || searched.Content[0].Text != "answer to agent_knowledge_search" {
		t.Fatalf("search = %+v", searched)
	}
	if !keyed.IsError || keyed.Content[0].Text != "there is no key called ELEVENLABS_API_KEY" {
		t.Fatalf("request_key = %+v", keyed)
	}
	if len(asked) != 3 || asked[0].Text != "commit style" || asked[0].Path != "/w/site" || asked[0].Port != 4242 ||
		asked[1].AnswerText != "narration" || !asked[2].Approve {
		t.Fatalf("asked = %+v", asked)
	}
	if responses["6"].Error == nil || responses["6"].Error.Code != -32601 {
		t.Fatalf("an unknown method = %+v", responses["6"])
	}
	if responses["null"].Error == nil || responses["null"].Error.Code != -32700 {
		t.Fatal("a line that isn't JSON wasn't refused")
	}
}

// With am serve down, the agent is told plainly.
func TestAnAgentIsToldWhenTheDaemonIsDown(t *testing.T) {
	responses, _ := mcpSession(t, []string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_keys","arguments":{}}}`,
	}, func(context.Context, protocol.Request) (protocol.Event, error) {
		return protocol.Event{}, errors.New("connection refused")
	})
	var result struct {
		Content []struct{ Text string }
		IsError bool
	}
	resultOf(t, responses["1"], &result)
	if !result.IsError || !strings.Contains(result.Content[0].Text, "Is am serve running?") {
		t.Fatalf("result = %+v", result)
	}
}

// A call goes over the daemon's real socket and finds its own answer among
// the events every app is sent.
func TestACallReachesTheDaemonOverItsSocket(t *testing.T) {
	dir, err := os.MkdirTemp("", "am")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "am.sock")
	server := daemon.NewLocalServer(path)
	if err := server.Listen(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		_ = server.Serve(ctx, func(_ context.Context, _ string, req protocol.Request) protocol.Event {
			_ = server.Send(protocol.Event{Type: protocol.EvtSessions})
			return protocol.Event{Type: protocol.EvtAgentReply, Preview: "notes for " + req.Text}
		}, func(string) {})
	}()

	event, err := socketCall(path)(ctx, protocol.Request{Type: protocol.ReqAgentKnowledgeSearch, Text: "deploys"})
	if err != nil || event.Preview != "notes for deploys" {
		t.Fatalf("event = %+v, %v", event, err)
	}
}

func TestCodexConfigGainsAndLosesOnlyAgentmansTable(t *testing.T) {
	before := "model = \"gpt-5\"\n\n[mcp_servers.github]\ncommand = \"gh-mcp\"\n"
	added := codexWithAgentman(before, "/opt/homebrew/bin/am", false)
	if !strings.HasPrefix(added, before) || !strings.Contains(added, "[mcp_servers.agentman]\ncommand = \"/opt/homebrew/bin/am\"\nargs = [\"mcp\"]") {
		t.Fatalf("added =\n%s", added)
	}
	if again := codexWithAgentman(added, "/opt/homebrew/bin/am", false); strings.Count(again, "[mcp_servers.agentman]") != 1 {
		t.Fatalf("adding twice =\n%s", again)
	}
	withEnv := added + "\n[mcp_servers.agentman.env]\nX = \"1\"\n\n[profiles.fast]\nmodel = \"o4\"\n"
	removed := codexWithAgentman(withEnv, "", true)
	if strings.Contains(removed, "agentman") || !strings.Contains(removed, "[mcp_servers.github]") || !strings.Contains(removed, "[profiles.fast]") {
		t.Fatalf("removed =\n%s", removed)
	}
}

func TestCursorConfigKeepsOtherServers(t *testing.T) {
	added, err := cursorWithAgentman(`{"mcpServers":{"linear":{"url":"https://mcp.linear.app"}},"other":1}`, "/usr/local/bin/am", false)
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		MCPServers map[string]map[string]any `json:"mcpServers"`
		Other      int                       `json:"other"`
	}
	_ = json.Unmarshal([]byte(added), &config)
	if config.MCPServers["agentman"]["command"] != "/usr/local/bin/am" || config.MCPServers["linear"] == nil || config.Other != 1 {
		t.Fatalf("added = %s", added)
	}
	removed, _ := cursorWithAgentman(added, "", true)
	if strings.Contains(removed, "agentman") || !strings.Contains(removed, "linear") {
		t.Fatalf("removed = %s", removed)
	}
	if _, err := cursorWithAgentman("{broken", "am", false); err == nil {
		t.Fatal("a broken config was overwritten")
	}
}
