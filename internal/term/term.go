// Package term runs a program on a pseudo-terminal and carries it over a
// WebSocket, for the cross-platform app's terminals: the app draws them
// (xterm.js) and am serve owns the processes, so the app needs no terminal
// code of its own and works the same against an am serve inside WSL.
//
// The wire is small. Binary messages are terminal bytes: the program's output
// from am serve, keystrokes from the app. Text messages from the app are
// commands as JSON; the only one is {"type":"resize","cols":N,"rows":N}.
package term

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/coder/websocket"
	"github.com/creack/pty"
)

// Size is a terminal's size in cells.
type Size struct {
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

func (s Size) valid() bool { return s.Cols > 0 && s.Rows > 0 && s.Cols < 2000 && s.Rows < 1000 }

type command struct {
	Type string `json:"type"`
	Size
	// Data is a "reply": the terminal's answer to the program's question.
	Data string `json:"data"`
}

// Env is the environment a terminal's program gets: am serve's own (the
// user's login environment, when it runs as the service) as a terminal app
// would set it, and outside any tmux am serve itself was started in.
func Env(base []string) []string {
	env := make([]string, 0, len(base)+2)
	for _, kv := range base {
		key, _, _ := strings.Cut(kv, "=")
		switch key {
		case "TMUX", "TMUX_PANE", "TERM", "TERM_PROGRAM", "TERM_PROGRAM_VERSION", "COLORTERM":
			continue
		}
		env = append(env, kv)
	}
	return append(env, "TERM=xterm-256color", "COLORTERM=truecolor")
}

// Serve runs cmd on a pty of the given size and joins it to c until the
// program exits, the app hangs up or ctx ends. The program is stopped when
// the app goes: for `tmux attach` that only detaches, and the agent runs on.
func Serve(ctx context.Context, c *websocket.Conn, cmd *exec.Cmd, size Size) error {
	if !size.valid() {
		size = Size{Cols: 80, Rows: 24}
	}
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: size.Cols, Rows: size.Rows})
	if err != nil {
		_ = c.Close(websocket.StatusInternalError, "could not start the terminal")
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	exited := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(exited)
	}()
	defer func() {
		stop(cmd, exited)
		_ = ptmx.Close()
	}()

	// The program's output, in the order it came.
	outputDone := make(chan error, 1)
	go func() {
		buf := make([]byte, 32<<10)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				if werr := c.Write(ctx, websocket.MessageBinary, buf[:n]); werr != nil {
					outputDone <- werr
					return
				}
			}
			if err != nil {
				// EIO is how Linux reports the program's side closing.
				if errors.Is(err, io.EOF) || errors.Is(err, syscall.EIO) {
					err = nil
				}
				outputDone <- err
				return
			}
		}
	}()

	// Keystrokes and resizes from the app.
	inputDone := make(chan error, 1)
	go func() {
		for {
			kind, data, err := c.Read(ctx)
			if err != nil {
				inputDone <- err
				return
			}
			switch kind {
			case websocket.MessageBinary:
				if _, err := ptmx.Write(data); err != nil {
					inputDone <- err
					return
				}
			case websocket.MessageText:
				var msg command
				if json.Unmarshal(data, &msg) != nil {
					continue
				}
				switch {
				case msg.Type == "resize" && msg.valid():
					_ = pty.Setsize(ptmx, &pty.Winsize{Cols: msg.Cols, Rows: msg.Rows})
				case msg.Type == "reply":
					// One terminal shows this program, so its answers are heard.
					if _, err := ptmx.Write([]byte(msg.Data)); err != nil {
						inputDone <- err
						return
					}
				}
			}
		}
	}()

	select {
	case err = <-outputDone:
		// The program ended: say so, so the app can show it rather than
		// guess from a dropped connection.
		_ = c.Close(websocket.StatusNormalClosure, "exited")
	case err = <-inputDone:
		if websocket.CloseStatus(err) != -1 {
			err = nil // the app closed it
		}
	case <-ctx.Done():
		_ = c.Close(websocket.StatusGoingAway, "am serve is stopping")
	}
	return err
}

// stop ends the program: a hangup first, as closing a terminal window sends,
// then a kill if it is still there a moment later.
func stop(cmd *exec.Cmd, exited <-chan struct{}) {
	if cmd.Process == nil {
		return
	}
	select {
	case <-exited:
		return
	default:
	}
	_ = cmd.Process.Signal(syscall.SIGHUP)
	select {
	case <-exited:
	case <-time.After(2 * time.Second):
		_ = cmd.Process.Kill()
		<-exited
	}
}

// Shell is the user's shell, as a terminal app would pick it.
func Shell() string {
	if shell := os.Getenv("SHELL"); shell != "" {
		return shell
	}
	for _, shell := range []string{"/bin/zsh", "/bin/bash"} {
		if _, err := os.Stat(shell); err == nil {
			return shell
		}
	}
	return "/bin/sh"
}
