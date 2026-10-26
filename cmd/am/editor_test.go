package main

import (
	"context"
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/hook"
	"github.com/lenajeremy/agentman/internal/lsp"
	"github.com/lenajeremy/agentman/internal/protocol"
)

func TestOnlyTheLocalSocketHandsOutTheLanguageServers(t *testing.T) {
	languages := lsp.NewHost(lsp.Options{})
	defer languages.Close()
	var passed []protocol.RequestType
	daemon := func(_ context.Context, _ string, req protocol.Request) protocol.Event {
		passed = append(passed, req.Type)
		return protocol.Event{Type: protocol.EvtError, Error: "unknown request"}
	}
	local := appRequests(daemon, languages, t.TempDir(), noPane)

	reply := local(context.Background(), "local:1", protocol.Request{Type: protocol.ReqLSPEndpoint})
	if reply.Type != protocol.EvtLSPEndpoint || reply.LSP == nil || reply.LSP.Token == "" ||
		!strings.HasPrefix(reply.LSP.URL, "ws://127.0.0.1:") {
		t.Fatalf("local socket got %+v", reply)
	}
	again := local(context.Background(), "local:2", protocol.Request{Type: protocol.ReqLSPEndpoint})
	if *again.LSP != *reply.LSP {
		t.Error("the endpoint should stay the same for the daemon's life")
	}
	local(context.Background(), "local:1", protocol.Request{Type: protocol.ReqListSessions})
	if len(passed) != 1 || passed[0] != protocol.ReqListSessions {
		t.Errorf("passed to the daemon: %v", passed)
	}
	// The relay's requests go straight to the daemon, which doesn't know it.
	if relayed := daemon(context.Background(), "phone", protocol.Request{Type: protocol.ReqLSPEndpoint}); relayed.LSP != nil {
		t.Error("the relay's path handed out the endpoint")
	}
}

func TestLanguageSettingsAreSavedAndTakeEffect(t *testing.T) {
	home := t.TempDir()
	cfg, err := hook.LoadConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	languages := lsp.NewHost(languageOptions(cfg, home))
	defer languages.Close()
	local := appRequests(func(context.Context, string, protocol.Request) protocol.Event { return protocol.Event{} }, languages, home, noPane)
	ask := func(req protocol.Request) protocol.Event {
		return local(context.Background(), "local:1", req)
	}

	status := ask(protocol.Request{Type: protocol.ReqLSPStatus})
	if status.Languages == nil || len(status.Languages.Servers) != len(lsp.Catalog()) {
		t.Fatalf("status = %+v", status)
	}
	off, yes := true, true
	reply := ask(protocol.Request{Type: protocol.ReqLSPSet, Language: &protocol.LanguageChange{Server: "pyright", Disabled: &off, NoDownloads: &yes}})
	if reply.Languages == nil || !reply.Languages.NoDownloads {
		t.Fatalf("reply = %+v", reply)
	}
	for _, server := range reply.Languages.Servers {
		if server.ID == "pyright" && server.Source != "off" {
			t.Errorf("pyright is %s", server.Source)
		}
	}
	saved, _ := hook.LoadConfig(home)
	if !saved.LSP.NoDownloads || !saved.LSP.Servers["pyright"].Disabled {
		t.Errorf("not saved: %+v", saved.LSP)
	}
	on := false
	ask(protocol.Request{Type: protocol.ReqLSPSet, Language: &protocol.LanguageChange{Server: "pyright", Disabled: &on}})
	if saved, _ := hook.LoadConfig(home); len(saved.LSP.Servers) != 0 {
		t.Errorf("a server back to usual should leave no entry: %+v", saved.LSP.Servers)
	}
	notAProgram := home
	if bad := ask(protocol.Request{Type: protocol.ReqLSPSet, Language: &protocol.LanguageChange{Server: "gopls", Path: &notAProgram}}); bad.Type != protocol.EvtError {
		t.Error("a folder was taken as a program")
	}
	if bad := ask(protocol.Request{Type: protocol.ReqLSPSet, Language: &protocol.LanguageChange{Server: "cobol", Disabled: &off}}); bad.Type != protocol.EvtError {
		t.Error("an unknown server was changed")
	}
}

func noPane(string) (string, bool) { return "", false }
