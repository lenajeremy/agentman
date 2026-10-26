// Package shellenv reads the environment the user's login shell sets up.
//
// am serve started from a terminal inherits that shell's environment: the
// PATH that finds node, claude and codex, and whatever else the user's
// profile exports. Started by launchd at login, as a service, it gets almost
// nothing, and every agent it launches would get nothing either. Reading the
// login shell's environment once at startup puts back what a terminal would
// have given it.
package shellenv

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"time"
)

// marker separates what the shell's startup files print from the
// environment that follows, so a chatty .zshrc can't be mistaken for it.
const marker = "\x00AGENTMAN_ENV_FOLLOWS\x00"

// Timeout bounds the shell's startup: a profile that waits for input or
// hangs leaves the service with the environment it already has.
const Timeout = 15 * time.Second

// skipped are the variables that describe the shell that printed them, or
// the terminal it isn't in, rather than the user's setup.
var skipped = map[string]bool{
	"PWD": true, "OLDPWD": true, "SHLVL": true, "_": true,
	"TERM": true, "TERM_PROGRAM": true, "TERM_PROGRAM_VERSION": true,
	"TMUX": true, "TMUX_PANE": true,
}

// Load runs the user's shell as an interactive login shell, as a terminal
// would start it, and returns the environment it sets up, as KEY=value.
// The shell is $SHELL, else zsh.
func Load(ctx context.Context) ([]string, error) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/zsh"
	}
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	command := exec.CommandContext(ctx, shell, "-l", "-i", "-c", `printf '\0AGENTMAN_ENV_FOLLOWS\0'; exec /usr/bin/env -0`)
	// No terminal to read from: a profile that asks something gets an end
	// of file rather than waiting forever.
	command.Stdin = nil
	out, err := command.Output()
	if err != nil {
		return nil, err
	}
	return Parse(out)
}

// Parse takes what Load's command printed and returns the environment after
// the marker.
func Parse(out []byte) ([]string, error) {
	at := bytes.LastIndex(out, []byte(marker))
	if at < 0 {
		return nil, errors.New("shellenv: the shell printed no environment")
	}
	var env []string
	for _, entry := range strings.Split(string(out[at+len(marker):]), "\x00") {
		name, _, ok := strings.Cut(entry, "=")
		if !ok || name == "" || skipped[name] {
			continue
		}
		env = append(env, entry)
	}
	if len(env) == 0 {
		return nil, errors.New("shellenv: the shell's environment was empty")
	}
	return env, nil
}

// Apply sets each variable in this process's environment, over what it has.
// It returns the names it set.
func Apply(env []string) []string {
	var set []string
	for _, entry := range env {
		name, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if os.Setenv(name, value) == nil {
			set = append(set, name)
		}
	}
	return set
}
