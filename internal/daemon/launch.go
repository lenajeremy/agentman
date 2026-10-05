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
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lenajeremy/agentman/internal/hook"
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

// createLaunchDirectory makes one new folder for a session to start in.
//
// Held to exactly the rule a launch path is, because that is what it becomes:
// beneath the Mac user's home, no dot-directories, no traversal, and every
// parent a real directory rather than a symlink out. The only addition is
// that the last segment must not exist yet — creating a folder that is
// already there would quietly hand back someone else's.
func createLaunchDirectory(raw string) (string, error) {
	if err := validateLaunchPath(raw, false); err != nil {
		return "", err
	}
	parent, leaf := path.Split(raw)
	parent = strings.TrimSuffix(parent, "/")
	if leaf == "" {
		return "", errors.New("daemon: name the folder to create")
	}
	base, err := launchDirectory(parent, true)
	if err != nil {
		return "", err
	}
	target := filepath.Join(base, leaf)
	if _, err := os.Lstat(target); err == nil {
		return "", fmt.Errorf("daemon: %s already exists", leaf)
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("daemon: cannot create that folder: %w", err)
	}
	// 0o700 rather than 0o755: this is a folder an agent is about to be given
	// a directory's worth of authority over, created by a request from a
	// phone. Nothing else on the machine needs to read it.
	if err := os.Mkdir(target, 0o700); err != nil {
		return "", fmt.Errorf("daemon: cannot create that folder: %w", err)
	}
	return raw, nil
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
	if req.Kind == protocol.KindCursorCLI {
		id, err = d.registry.LaunchCursor(ctx, dir, req.Text)
	} else if req.Kind == protocol.KindOpenCode {
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

// agentCommand maps a kind to the program that runs it and the word its tmux
// panes are named after.
func agentCommand(kind protocol.Kind) (command, nameKind string) {
	command = string(kind)
	nameKind = command
	switch kind {
	case protocol.KindCursorCLI:
		return "agent", "cursor"
	case protocol.KindKiro:
		return "kiro-cli", nameKind
	case protocol.KindAntigravity:
		return "agy", nameKind
	}
	return command, nameKind
}

// resumeNaming lets the adapter that owns a session name its resume: the pane
// it opens in and the id discovery will publish for it (see
// source.ResumeNamer). Nil, or an empty id, keeps the daemon's own naming.
type resumeNaming func(native, defaultPane string) (pane, sessionID string)

// startResumedSession reopens an existing session in a pane of its own.
//
// Unlike a launch, nothing is being created: the agent is pointed at a
// transcript it already wrote, in the directory it wrote it in. The prompt is
// the user's to type once it is up, which is why none is sent here.
func startResumedSession(
	ctx context.Context, kind protocol.Kind, dir string, resume []string, naming resumeNaming,
) (string, error) {
	command, nameKind := agentCommand(kind)
	binary, err := exec.LookPath(command)
	if err != nil {
		return "", fmt.Errorf("daemon: %s is not installed on this Mac", command)
	}
	if !tmux.Available() {
		return "", errors.New("daemon: tmux is required to reopen a session")
	}

	name := tmux.NewName(nameKind)
	argv := []string{binary}
	// Kiro reads a bare first word as a subcommand, so its flags must follow
	// `chat` exactly as they do in the wrapper.
	if kind == protocol.KindKiro {
		argv = append(argv, "chat")
	}
	argv = append(argv, resume...)

	// The id discovery will give the reopened session. Claude, Kiro,
	// Antigravity and Cursor all continue writing the transcript they were
	// pointed at, so the session keeps the id it already had. Codex opens a
	// new rollout with an id of its own, so its pane is the only name that
	// exists until that rollout appears — the same rule a fresh launch uses.
	id := ""
	native := resume[len(resume)-1]
	// The adapter is asked first, because an adapter that keys live sessions
	// on their pane is the only place that knows what discovery will call
	// this one. The switch is what an adapter that does not say gets.
	namedPane, namedID := "", ""
	if naming != nil {
		namedPane, namedID = naming(native, name)
	}
	switch kind {
	case protocol.KindClaude:
		// The UUID in the pane name lets discovery bind the pane before
		// Claude has rewritten its process registry.
		name = tmux.Prefix + "claude-" + native
		id = "claude:" + native
	case protocol.KindCodex:
		id = "codex:tmux-" + name
	case protocol.KindCursorCLI:
		id = "cursor-cli:" + native
	case protocol.KindKiro:
		id = "kiro:" + native
	case protocol.KindAntigravity:
		id = "antigravity:" + native
	default:
		return "", errors.New("daemon: this agent cannot be reopened by id")
	}
	if validResumeName(kind, namedPane, namedID) {
		name, id = namedPane, namedID
	}

	if err := startResumedPane(ctx, name, dir, argv, command); err != nil {
		return "", err
	}
	return id, nil
}

// startResumedPane opens the pane a resume runs in and checks the agent stayed
// in it. A variable so a test can see what a resume would open without
// starting tmux.
var startResumedPane = func(ctx context.Context, name, dir string, argv []string, command string) error {
	if err := tmux.Launch(ctx, name, dir, argv); err != nil {
		return err
	}
	return paneSurvivedLaunch(ctx, name, command)
}

// validResumeName accepts an adapter's naming of a resume only when the pane
// is one of ours and plain enough to be a tmux target as written — a "." or
// ":" would make tmux read part of it as a window or pane — and the id is one
// of this agent's.
func validResumeName(kind protocol.Kind, pane, id string) bool {
	if id == "" || len(id) > maxSessionIDBytes || !strings.HasPrefix(id, string(kind)+":") ||
		len(pane) <= len(tmux.Prefix) || len(pane) > 256 || !strings.HasPrefix(pane, tmux.Prefix) {
		return false
	}
	for _, character := range pane {
		if !(character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || character == '-' || character == '_') {
			return false
		}
	}
	return true
}

// paneSurvivedLaunch reports whether the pane is still there a moment later.
//
// A missing login or a rejected argument makes the CLI exit at once, and tmux
// takes the pane with it. Reporting success then sends the phone to a session
// that never existed.
func paneSurvivedLaunch(ctx context.Context, name, command string) error {
	time.Sleep(250 * time.Millisecond)
	panes, err := tmux.List(ctx)
	if err != nil {
		return nil // tmux is unreadable, not necessarily broken
	}
	for _, pane := range panes {
		if pane.Name == name {
			return nil
		}
	}
	return fmt.Errorf("daemon: %s exited before its session started; check its login on the Mac", command)
}

func startTerminalSession(ctx context.Context, kind protocol.Kind, dir, prompt string) (string, error) {
	command, nameKind := agentCommand(kind)
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
	case protocol.KindKiro:
		// Kiro picks its own session id once it starts, so the pane is the
		// only name that exists yet; the adapter keys the session on it.
		argv = append(argv, "chat")
		// Agentman's own agent, when the user opted in to Kiro's hooks
		// (`am install-hooks --kiro`); kiro_default otherwise.
		if home, err := os.UserHomeDir(); err == nil {
			argv = append(argv, hook.KiroAgentArgs(home, nil)...)
		}
		id = "kiro:tmux-" + name
	case protocol.KindAntigravity:
		id = "antigravity:tmux-" + name
	default:
		return "", errors.New("daemon: unsupported launch agent")
	}
	// Kiro and Antigravity are started without their first message, which is
	// typed in once each is ready (see deliverFirstPrompt). Both accept one on
	// the command line, and both mishandle it there, each found by launching:
	//
	//   - kiro-cli drops "--" when it hands arguments to its inner chat
	//     process, so a message beginning with a dash — a bullet, "-v" — is
	//     read as a flag, and Kiro exits with a usage error after the launch
	//     has already reported success.
	//   - agy, in a folder it has not been told to trust, files a conversation
	//     started that way under its own scratch directory, so `ls` listed an
	//     empty scratch folder instead of the project.
	//
	// Typed at the prompt, the message is text, and it runs where it was sent.
	ready := firstPromptReady(kind)
	if ready == nil {
		argv = append(argv, "--", prompt)
	}
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
				if ready != nil {
					go deliverFirstPrompt(name, prompt, ready)
				}
				return id, nil
			}
		}
	}
	return "", fmt.Errorf("daemon: %s exited before its session started; check its login on the Mac", command)
}

// firstPromptReady returns how to tell that an agent launched without its
// first message is ready to have it typed in, or nil for agents that take it
// on the command line.
func firstPromptReady(kind protocol.Kind) func(pane string) bool {
	switch kind {
	case protocol.KindKiro:
		return source.KiroReadyForInput
	case protocol.KindAntigravity:
		return source.AntigravityReadyForInput
	}
	return nil
}

// firstPromptWait bounds how long a launch holds its first message — long
// enough for the user to answer a folder trust prompt from the phone.
const firstPromptWait = 30 * time.Minute

// deliverFirstPrompt types a launch's first message in once ready says the
// agent is idle at its prompt, leaving any open menu for the user to answer.
//
// It runs apart from the request because that answer can take minutes. The
// pane disappearing — the agent exiting, or the user declining to trust the
// folder — ends it, and so does the time limit, rather than typing into
// whatever the pane shows much later.
func deliverFirstPrompt(name, prompt string, ready func(pane string) bool) {
	ctx, cancel := context.WithTimeout(context.Background(), firstPromptWait)
	defer cancel()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		pane, err := tmux.Capture(ctx, name)
		if err != nil {
			return
		}
		if !ready(pane) {
			continue
		}
		_ = tmux.Send(ctx, name, prompt)
		return
	}
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
