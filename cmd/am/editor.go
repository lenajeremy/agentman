package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/lenajeremy/agentman/internal/hook"
	"github.com/lenajeremy/agentman/internal/lsp"
	"github.com/lenajeremy/agentman/internal/protocol"
)

// appRequests answers what only an app on this Mac may ask, then hands the
// rest to the daemon, as the relay does. The phone's requests never come
// this way, so the language servers' endpoint, token and settings stay on
// the Mac.
func appRequests(handle func(context.Context, string, protocol.Request) protocol.Event, languages *lsp.Host, configHome string) func(context.Context, string, protocol.Request) protocol.Event {
	return func(ctx context.Context, from string, req protocol.Request) protocol.Event {
		var err error
		switch req.Type {
		case protocol.ReqLSPEndpoint:
			url, token, err := languages.Endpoint()
			if err != nil {
				return protocol.Event{Type: protocol.EvtError, Error: err.Error()}
			}
			return protocol.Event{Type: protocol.EvtLSPEndpoint, LSP: &protocol.LSPEndpoint{URL: url, Token: token}}
		case protocol.ReqLSPStatus:
		case protocol.ReqLSPSet:
			err = changeLanguages(languages, configHome, req.Language)
		case protocol.ReqLSPRemove:
			if req.Language == nil || lsp.ServerByID(req.Language.Server) == nil && req.Language.Server != "node" {
				err = errors.New("no such language server")
			} else {
				err = languages.Remove(req.Language.Server)
			}
		default:
			return handle(ctx, from, req)
		}
		if err != nil {
			return protocol.Event{Type: protocol.EvtError, Error: err.Error()}
		}
		return protocol.Event{Type: protocol.EvtLSPStatus, Languages: languagesState(languages.Report())}
	}
}

// changeLanguages saves a change to the language servers' settings in
// config.json and gives it to the running host.
func changeLanguages(languages *lsp.Host, configHome string, change *protocol.LanguageChange) error {
	if change == nil {
		return errors.New("nothing to change")
	}
	cfg, err := hook.LoadConfig(configHome)
	if err != nil {
		return err
	}
	if change.NoDownloads != nil {
		cfg.LSP.NoDownloads = *change.NoDownloads
	}
	if change.Server != "" {
		if lsp.ServerByID(change.Server) == nil {
			return fmt.Errorf("no language server called %s", change.Server)
		}
		server := cfg.LSP.Servers[change.Server]
		if change.Path != nil {
			if *change.Path != "" {
				info, err := os.Stat(*change.Path)
				if !filepath.IsAbs(*change.Path) || err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
					return fmt.Errorf("%s isn't a program that can run", *change.Path)
				}
			}
			server.Path = *change.Path
		}
		if change.Disabled != nil {
			server.Disabled = *change.Disabled
		}
		if cfg.LSP.Servers == nil {
			cfg.LSP.Servers = map[string]hook.LSPServer{}
		}
		if server == (hook.LSPServer{}) {
			delete(cfg.LSP.Servers, change.Server)
		} else {
			cfg.LSP.Servers[change.Server] = server
		}
	}
	if err := hook.SaveConfig(configHome, cfg); err != nil {
		return err
	}
	opts := languageOptions(cfg, configHome)
	languages.Configure(opts.NoDownloads, opts.Paths, opts.Disabled)
	return nil
}

func languagesState(report lsp.Report) *protocol.LanguagesState {
	state := &protocol.LanguagesState{NoDownloads: report.NoDownloads, NodeSize: report.NodeSize}
	for _, item := range report.Servers {
		server := protocol.LanguageServer{
			ID: item.Server.ID, Name: item.Server.Name, Language: item.Server.Language,
			Source: item.Source, Path: item.Path, Size: item.Size, Running: item.Running,
		}
		if item.Server.Install != nil {
			server.DownloadSize = item.Server.Install.Size
		}
		if item.Source == "missing" {
			server.Missing = item.Server.Missing
		}
		state.Servers = append(state.Servers, server)
	}
	return state
}
