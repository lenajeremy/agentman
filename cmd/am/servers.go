package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"strconv"

	"github.com/lenajeremy/agentman/internal/hook"
	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/servers"
)

// runServers lists the web servers each agent session has started.
//
// It is the terminal view of what the phone's servers page shows, and the
// quickest way to see why a server is or is not being picked up.
func runServers(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("servers", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return err
	}

	registry, err := buildRegistry()
	if err != nil {
		return err
	}
	sessions, discoverErr := registry.Discover(ctx)
	watcher := servers.NewWatcher()
	ignoreHookPort(watcher)
	found, err := watcher.Snapshot(ctx, servers.OwnersOf(sessions))
	if err != nil {
		return err
	}

	if *asJSON {
		out := map[string][]protocol.Server{}
		for id, list := range found {
			out[id] = list
		}
		if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
			return err
		}
		return discoverErr
	}

	shown := 0
	for _, session := range sessions {
		list := found[session.ID]
		if len(list) == 0 {
			continue
		}
		shown++
		fmt.Printf("  %s %s %s\n", stateDot(session.State), session.Name, dim(string(session.Kind)))
		for _, server := range list {
			label := server.Title
			if label == "" {
				label = server.Command
			}
			fmt.Printf("     localhost:%-6d %s\n", server.Port, dim(label))
		}
		fmt.Println()
	}
	if shown == 0 {
		fmt.Println("No agent has started a web server.")
		fmt.Println("Servers an agent starts show up here, and on your phone, within a few seconds.")
	}
	return discoverErr
}

// ignoreHookPort hides the daemon's hook listener, which answers HTTP and runs
// from the project directory, so it would otherwise look like a dev server.
func ignoreHookPort(watcher *servers.Watcher) {
	cfg, err := hook.LoadConfig("")
	if err != nil {
		return
	}
	if _, portText, err := net.SplitHostPort(cfg.ListenAddr()); err == nil {
		if port, err := strconv.Atoi(portText); err == nil {
			watcher.Ignore(port)
		}
	}
}
