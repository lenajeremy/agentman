package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync/atomic"

	"github.com/coder/websocket"
	"github.com/lenajeremy/agentman/internal/daemon"
	"github.com/lenajeremy/agentman/internal/term"
	"github.com/lenajeremy/agentman/internal/tmux"
)

// appGateway serves the cross-platform app on the hook listener: the app
// protocol at /app and terminals at /app/terminal (see internal/daemon's
// appws.go). The routes are mounted before the hook listener starts, and
// answer 503 until the daemon behind them is ready. The terminal panels'
// shells are kept in a registry: they run on while no panel shows them,
// until a little after the app's last connection closes.
type appGateway struct {
	gate  daemon.AppGate
	ready atomic.Pointer[appTargets]
}

type appTargets struct {
	ctx     context.Context
	local   *daemon.LocalServer
	paneFor func(string) (string, bool)
	shells  *term.Registry
}

func newAppGateway(token string) *appGateway {
	return &appGateway{gate: daemon.AppGate{Token: token}}
}

// serve starts answering, once the local server and the agents exist.
func (g *appGateway) serve(ctx context.Context, local *daemon.LocalServer, paneFor func(string) (string, bool)) {
	shells := term.NewRegistry(ctx)
	shells.AgentArgv = func(ctx context.Context, session string) ([]string, error) {
		name, ok := paneFor(session)
		if !ok || name == "" || !tmux.Has(ctx, name) {
			return nil, term.ErrNoAgentTerminal
		}
		argv := tmux.AttachArgs(ctx, name)
		if _, err := exec.LookPath(argv[0]); err != nil {
			return nil, errors.New("tmux isn't installed")
		}
		return argv, nil
	}
	g.ready.Store(&appTargets{ctx: ctx, local: local, paneFor: paneFor, shells: shells})
}

func (g *appGateway) targets(w http.ResponseWriter, r *http.Request) *appTargets {
	if err := g.gate.Allow(r); err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return nil
	}
	targets := g.ready.Load()
	if targets == nil || targets.local == nil {
		http.Error(w, "am serve is starting", http.StatusServiceUnavailable)
		return nil
	}
	return targets
}

// protocol is /app: the phone's protocol, as on am.sock. While one is
// open, the app is running, and its terminal panels' shells with it.
func (g *appGateway) protocol(w http.ResponseWriter, r *http.Request) {
	if targets := g.targets(w, r); targets != nil {
		if targets.shells != nil {
			targets.shells.Hold()
			defer targets.shells.Release()
		}
		targets.local.ServeWebSocket(w, r)
	}
}

// terminal is /app/terminal. A terminal panel's: ?watch=<dir> follows that
// folder's shells and takes commands for them, and ?id=<shell> shows one
// (internal/term's registry.go). One of its own, ended when it closes:
// ?session=<id> attaches to that agent's tmux session, as the Mac app's
// terminal does; ?folder=<dir> starts the user's login shell there. cols and
// rows give the first size.
func (g *appGateway) terminal(w http.ResponseWriter, r *http.Request) {
	targets := g.targets(w, r)
	if targets == nil {
		return
	}
	query := r.URL.Query()
	size := term.Size{Cols: atoi16(query.Get("cols")), Rows: atoi16(query.Get("rows"))}
	if watch := query.Get("watch"); watch != "" && targets.shells != nil {
		folder := filepath.Clean(watch)
		if info, err := os.Stat(folder); !filepath.IsAbs(watch) || err != nil || !info.IsDir() {
			http.Error(w, "no such folder", http.StatusNotFound)
			return
		}
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		targets.shells.Watch(targets.ctx, c, folder)
		return
	}
	if id := query.Get("id"); id != "" && targets.shells != nil {
		shell := targets.shells.Get(id)
		if shell == nil {
			http.Error(w, "no such terminal", http.StatusNotFound)
			return
		}
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		targets.shells.Attach(targets.ctx, c, shell, size)
		return
	}
	var cmd *exec.Cmd
	if session := query.Get("session"); session != "" {
		name, ok := targets.paneFor(session)
		if !ok || name == "" || !tmux.Has(r.Context(), name) {
			http.Error(w, "this agent isn't running in a terminal Agentman can show", http.StatusNotFound)
			return
		}
		argv := tmux.AttachArgs(r.Context(), name)
		path, err := exec.LookPath(argv[0])
		if err != nil {
			http.Error(w, "tmux isn't installed", http.StatusNotFound)
			return
		}
		cmd = exec.Command(path, argv[1:]...)
	} else {
		folder := filepath.Clean(query.Get("folder"))
		if info, err := os.Stat(folder); !filepath.IsAbs(folder) || err != nil || !info.IsDir() {
			http.Error(w, "no such folder", http.StatusNotFound)
			return
		}
		cmd = exec.Command(term.Shell(), "-l")
		cmd.Dir = folder
	}
	cmd.Env = term.Env(os.Environ())

	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	_ = term.Serve(targets.ctx, c, cmd, size)
}

func atoi16(s string) uint16 {
	n, err := strconv.ParseUint(s, 10, 16)
	if err != nil {
		return 0
	}
	return uint16(n)
}
