package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// Daemon tells the screens where am serve is. am serve writes its app
// endpoint (URL, terminal URL and token) to ~/.agentman/app.json, readable
// only by this user; a web page cannot read files, so it asks here.
type Daemon struct{}

// Endpoint is what ~/.agentman/app.json holds.
type Endpoint struct {
	URL      string `json:"url"`
	Terminal string `json:"terminal"`
	Token    string `json:"token"`
}

// ErrNoDaemon means am serve is not running, or is too old to serve apps
// over WebSocket.
var ErrNoDaemon = errors.New("am serve isn't running")

// Endpoint reads the running daemon's endpoint. It is read again at every
// call: the token changes each time am serve starts.
func (d *Daemon) Endpoint() (Endpoint, error) {
	path, err := endpointFile()
	if err != nil {
		return Endpoint{}, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Endpoint{}, ErrNoDaemon
	}
	if err != nil {
		return Endpoint{}, err
	}
	var endpoint Endpoint
	if err := json.Unmarshal(data, &endpoint); err != nil || endpoint.URL == "" || endpoint.Token == "" {
		return Endpoint{}, fmt.Errorf("%s is not an endpoint am serve wrote", path)
	}
	return endpoint, nil
}

// Platform names the system the shell runs on, for what the screens show
// differently: ⌘ or Ctrl, and room for macOS's window buttons.
func (d *Daemon) Platform() string { return runtime.GOOS }

// endpointFile is ~/.agentman/app.json, or the one under AGENTMAN_HOME, the
// home a test daemon was given with -config-home.
//
// On Windows, am serve runs inside WSL; finding its file through
// \\wsl.localhost is the next step for Windows.
func endpointFile() (string, error) {
	home := os.Getenv("AGENTMAN_HOME")
	if home == "" {
		var err error
		if home, err = os.UserHomeDir(); err != nil {
			return "", err
		}
	}
	return filepath.Join(home, ".agentman", "app.json"), nil
}
