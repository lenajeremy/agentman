package daemon

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/source"
	"github.com/lenajeremy/agentman/internal/tmux"
)

// A launch path is relative to the Mac user's home. Never accept an absolute
// path from the phone, a hidden directory, or a symlink that can escape home.
func validateLaunchPath(raw string, allowHome bool) error {
	if raw == "" && allowHome {
		return nil
	}
	if raw == "" || len(raw) > 4096 || strings.ContainsAny(raw, "\\\x00") ||
		strings.HasPrefix(raw, "/") {
		return errors.New("daemon: invalid launch directory")
	}
	if len(strings.Split(raw, "/")) > 16 {
		return errors.New("daemon: launch directory is too deep")
	}
	for _, part := range strings.Split(raw, "/") {
		if part == "" || part == "." || part == ".." || strings.HasPrefix(part, ".") || privatePart(part) {
			return errors.New("daemon: that directory is not available for launching")
		}
	}
	return nil
}

func launchDirectory(raw string, allowHome bool) (string, error) {
	if err := validateLaunchPath(raw, allowHome); err != nil {
		return "", err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	current := home
	for _, part := range strings.Split(raw, "/") {
		if part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return "", fmt.Errorf("daemon: directory is unavailable: %w", err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("daemon: select a real directory on this Mac")
		}
	}
	return current, nil
}

func listLaunchDirectories(raw string) ([]string, error) {
	dir, err := launchDirectory(raw, true)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("daemon: cannot browse directory: %w", err)
	}
	names := make([]string, 0, min(len(entries), 200))
	for _, entry := range entries {
		if len(names) == 200 {
			break
		}
		if entry.IsDir() && !strings.HasPrefix(entry.Name(), ".") && !privatePart(entry.Name()) {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

func (d *Daemon) startLocalSession(ctx context.Context, req protocol.Request) (string, error) {
	d.mu.Lock()
	if id := d.launches[req.ClientID]; id != "" {
		d.mu.Unlock()
		return id, nil
	}
	d.mu.Unlock()
	dir, err := launchDirectory(req.Path, false)
	if err != nil {
		return "", err
	}
	var id string
	if req.Kind == protocol.KindOpenCode {
		id, err = startOpenCodeSession(ctx, dir, req.Text)
	} else {
		id, err = startTerminalSession(ctx, req.Kind, dir, req.Text)
	}
	if err != nil {
		return "", err
	}
	d.mu.Lock()
	d.launches[req.ClientID] = id
	d.mu.Unlock()
	return id, nil
}

func startTerminalSession(ctx context.Context, kind protocol.Kind, dir, prompt string) (string, error) {
	command := string(kind)
	nameKind := command
	if kind == protocol.KindCursorCLI {
		command, nameKind = "agent", "cursor"
	}
	binary, err := exec.LookPath(command)
	if err != nil {
		return "", fmt.Errorf("daemon: %s is not installed on this Mac", command)
	}
	if !tmux.Available() {
		return "", errors.New("daemon: tmux is required to start a remote agent")
	}
	if kind == protocol.KindCodex {
		panes, err := tmux.List(ctx)
		if err != nil {
			return "", err
		}
		for _, pane := range panes {
			if pane.Cwd == dir && pane.Command == "codex" {
				return "", errors.New("daemon: Codex already has a managed session in this directory; its current transcript matching cannot safely separate two")
			}
		}
	}
	name := tmux.NewName(nameKind)
	argv := []string{binary}
	var id string
	switch kind {
	case protocol.KindClaude:
		uuid, err := newClaudeSessionID()
		if err != nil {
			return "", err
		}
		argv = append(argv, "--session-id", uuid)
		// The UUID in the pane name lets discovery show this session before
		// Claude has written its process registry or first transcript.
		name = tmux.Prefix + "claude-" + uuid
		id = "claude:" + uuid
	case protocol.KindCodex:
		id = "codex:tmux-" + name
	case protocol.KindCursorCLI:
		id = "cursor-cli:pane:" + name
	default:
		return "", errors.New("daemon: unsupported launch agent")
	}
	argv = append(argv, "--", prompt)
	if err := tmux.Launch(ctx, name, dir, argv); err != nil {
		return "", err
	}
	// A missing login or an invalid CLI argument can make tmux exit at once.
	// Do not report success when the pane is already gone.
	time.Sleep(250 * time.Millisecond)
	panes, err := tmux.List(ctx)
	if err == nil {
		for _, pane := range panes {
			if pane.Name == name {
				return id, nil
			}
		}
	}
	return "", fmt.Errorf("daemon: %s exited before its session started; check its login on the Mac", command)
}

func newClaudeSessionID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", raw[:4], raw[4:6], raw[6:8], raw[8:10], raw[10:]), nil
}

func startOpenCodeSession(ctx context.Context, dir, prompt string) (string, error) {
	base := strings.TrimRight(os.Getenv("AGENTMAN_OPENCODE_URL"), "/")
	if base == "" {
		base = managedOpenCodeBase(ctx, dir)
	}
	if base == "" {
		binary, err := exec.LookPath("opencode")
		if err != nil {
			return "", errors.New("daemon: opencode is not installed on this Mac")
		}
		if !tmux.Available() {
			return "", errors.New("daemon: tmux is required to keep a new OpenCode server running")
		}
		port := 0
		for candidate := source.OpenCodeDefaultPort; candidate < source.OpenCodeDefaultPort+source.OpenCodePortSpan; candidate++ {
			listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", candidate))
			if err == nil {
				_ = listener.Close()
				port = candidate
				break
			}
		}
		if port == 0 {
			return "", errors.New("daemon: no OpenCode API port is available")
		}
		base = fmt.Sprintf("http://127.0.0.1:%d", port)
		name := tmux.NewName(fmt.Sprintf("opencode-server-%d", port))
		if err := tmux.Launch(ctx, name, dir, []string{binary, "serve", "--hostname", "127.0.0.1", "--port", fmt.Sprint(port)}); err != nil {
			return "", err
		}
		keepServer := false
		defer func() {
			if !keepServer {
				_ = tmux.Kill(context.Background(), name)
			}
		}()
		ready := false
		for attempts := 0; attempts < 40; attempts++ {
			var health struct {
				Healthy bool `json:"healthy"`
			}
			if openCodeLaunchRequest(ctx, base, http.MethodGet, "/global/health", nil, &health) == nil && health.Healthy {
				ready = true
				break
			}
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(200 * time.Millisecond):
			}
		}
		if !ready {
			return "", errors.New("daemon: OpenCode server did not start")
		}
		// A second process could claim the port in the instant after our free-port
		// probe. Only use the healthy endpoint if our own tmux pane survived.
		panes, err := tmux.List(ctx)
		if err != nil {
			return "", err
		}
		ownPane := false
		for _, pane := range panes {
			if pane.Name == name {
				ownPane = true
				break
			}
		}
		if !ownPane {
			return "", errors.New("daemon: OpenCode could not claim its API port")
		}
		// A healthy server is useful even if session creation fails; the user
		// can reuse it on the Mac, and a retry can select another free port.
		keepServer = true
	}
	query := "?directory=" + url.QueryEscape(dir)
	var created struct {
		ID string `json:"id"`
	}
	if err := openCodeLaunchRequest(ctx, base, http.MethodPost, "/session"+query, map[string]any{}, &created); err != nil {
		return "", err
	}
	if created.ID == "" {
		return "", errors.New("daemon: OpenCode returned no session ID")
	}
	path := "/session/" + url.PathEscape(created.ID) + "/prompt_async" + query
	if err := openCodeLaunchRequest(ctx, base, http.MethodPost, path,
		map[string]any{"parts": []map[string]string{{"type": "text", "text": prompt}}}, nil); err != nil {
		return "", err
	}
	return "opencode:" + created.ID, nil
}

func managedOpenCodeBase(ctx context.Context, dir string) string {
	panes, err := tmux.List(ctx)
	if err != nil {
		return ""
	}
	for _, pane := range panes {
		if pane.Cwd != dir {
			continue
		}
		port := managedOpenCodePort(pane.Name)
		if port == 0 {
			continue
		}
		base := fmt.Sprintf("http://127.0.0.1:%d", port)
		var health struct {
			Healthy bool `json:"healthy"`
		}
		if openCodeLaunchRequest(ctx, base, http.MethodGet, "/global/health", nil, &health) == nil && health.Healthy {
			return base
		}
	}
	return ""
}

func managedOpenCodePort(name string) int {
	const prefix = tmux.Prefix + "opencode-server-"
	if !strings.HasPrefix(name, prefix) {
		return 0
	}
	part, suffix, ok := strings.Cut(strings.TrimPrefix(name, prefix), "-")
	if !ok || suffix == "" {
		return 0
	}
	port, err := strconv.Atoi(part)
	if err != nil ||
		port < source.OpenCodeDefaultPort || port >= source.OpenCodeDefaultPort+source.OpenCodePortSpan {
		return 0
	}
	return port
}

func openCodeLaunchRequest(ctx context.Context, base, method, path string, body any, out any) error {
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, method, base+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if password := os.Getenv("OPENCODE_SERVER_PASSWORD"); password != "" {
		username := os.Getenv("OPENCODE_SERVER_USERNAME")
		if username == "" {
			username = "opencode"
		}
		req.SetBasicAuth(username, password)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode >= 300 {
		return fmt.Errorf("daemon: OpenCode %s failed: %s", method, response.Status)
	}
	if out != nil {
		return json.NewDecoder(response.Body).Decode(out)
	}
	return nil
}
