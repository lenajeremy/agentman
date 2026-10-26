package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/lenajeremy/agentman/internal/hook"
	"github.com/lenajeremy/agentman/internal/lsp"
)

// languageOptions are the language servers' settings from config.json.
func languageOptions(cfg hook.Config, configHome string) lsp.Options {
	opts := lsp.Options{
		NoDownloads: cfg.LSP.NoDownloads,
		Idle:        time.Duration(cfg.LSP.IdleMinutes) * time.Minute,
		MaxServers:  cfg.LSP.MaxServers,
		Paths:       map[string]string{},
		Disabled:    map[string]bool{},
	}
	if dir, err := hook.ConfigDir(configHome); err == nil {
		opts.Dir = filepath.Join(dir, "lsp")
	}
	for id, server := range cfg.LSP.Servers {
		if server.Path != "" {
			opts.Paths[id] = server.Path
		}
		if server.Disabled {
			opts.Disabled[id] = true
		}
	}
	return opts
}

// runLSP looks after the language servers the editor in Agentman for Mac
// uses: which it has, getting one ahead of time, and clearing out the ones
// am downloaded.
func runLSP(ctx context.Context, args []string) error {
	if len(args) == 0 {
		args = []string{"status"}
	}
	fs := flag.NewFlagSet("lsp "+args[0], flag.ContinueOnError)
	configHome := fs.String("config-home", "", "separate home for Agentman state")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	cfg, err := hook.LoadConfig(*configHome)
	if err != nil {
		return err
	}
	opts := languageOptions(cfg, *configHome)
	host := lsp.NewHost(opts)
	defer host.Close()

	switch args[0] {
	case "status":
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		for _, server := range lsp.Catalog() {
			launch, where := host.Where(server)
			line := ""
			switch {
			case opts.Disabled[server.ID]:
				line = "turned off"
			case where == "yours":
				line = "your own\t" + launch.Path
			case where == "mac":
				line = "on this Mac\t" + launch.Path
			case where == "downloaded":
				line = "downloaded\t" + collapseHome(launch.Args0OrPath())
			case server.Install != nil && !opts.NoDownloads:
				line = "downloads when first used (" + server.Install.Size + ")"
			default:
				line = "missing\t" + server.Missing
			}
			fmt.Fprintf(w, "%s\t%s\t%s\n", server.Language, server.Name, line)
		}
		return w.Flush()
	case "install":
		if fs.NArg() != 1 {
			return errors.New("usage: am lsp install <language>  (go, typescript, python, rust)")
		}
		server := serverNamed(fs.Arg(0))
		if server == nil {
			return fmt.Errorf("am doesn't know a language server for %q", fs.Arg(0))
		}
		if server.Install == nil {
			return errors.New(server.Missing)
		}
		if opts.Dir == "" {
			return errors.New("no folder for language servers")
		}
		in := &lsp.Installer{Dir: opts.Dir, Tools: &lsp.Tools{}}
		launch, err := in.Install(ctx, server, func(detail string) {
			fmt.Printf("\r%-60s", detail)
		})
		fmt.Print("\r" + strings.Repeat(" ", 60) + "\r")
		if err != nil {
			return err
		}
		fmt.Printf("%s %s is ready: %s\n", server.Language, server.Name, collapseHome(launch.Args0OrPath()))
		return nil
	case "clean":
		if opts.Dir == "" {
			return nil
		}
		if err := os.RemoveAll(opts.Dir); err != nil {
			return err
		}
		fmt.Printf("Removed the language servers am downloaded (%s). They download again when needed.\n", collapseHome(opts.Dir))
		return nil
	default:
		return fmt.Errorf("unknown lsp command %q: try status, install or clean", args[0])
	}
}

// serverNamed finds a server by language, id or name: "go", "rust",
// "pyright".
func serverNamed(name string) *lsp.Server {
	name = strings.ToLower(name)
	for _, server := range lsp.Catalog() {
		if strings.ToLower(server.ID) == name || strings.ToLower(server.Language) == name || strings.ToLower(server.Name) == name {
			return server
		}
		for _, language := range server.Languages {
			if language == name {
				return server
			}
		}
	}
	return nil
}
