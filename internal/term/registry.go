package term

// The shells of the cross-platform app's terminal panels. A shell belongs to
// a folder, as the Mac app's do (EditorStore.terminals), not to a window: it
// runs on while no panel shows it, two windows can show it at once, and a
// panel opened again finds its screen and scrollback as they were. Shells
// end a little after the app's last connection closes, as the Mac app's end
// when it quits.
//
// Two kinds of socket, both on /app/terminal:
//
//   - ?watch=<folder>: the folder's shells. The server sends their state as
//     JSON ({"type":"state","shells":[…],"active":id,"splits":{left:right}})
//     at once and after every change; the app sends commands as JSON: new,
//     ensure, agent, split, unsplit, close, restart, activate.
//   - ?id=<shell>: one shell's screen. The server sends the scrollback, then
//     the output as it comes, as binary messages; the app sends keystrokes
//     as binary and {"type":"resize"} or {"type":"clear"} as JSON.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"
	"github.com/creack/pty"
)

// Scrollback is how much of a shell's output is kept for a panel that opens
// it later.
const Scrollback = 512 << 10

// clearScreen clears the screen and the scrollback and puts the cursor home.
const clearScreen = "\x1b[2J\x1b[3J\x1b[H"

// ErrNoAgentTerminal is the answer when an agent has no terminal to show.
var ErrNoAgentTerminal = errors.New("this agent isn't running in a terminal Agentman can show")

// Registry holds every shell, by folder.
type Registry struct {
	// ShellPath is the program to start; empty, the user's login shell.
	ShellPath string
	// AgentArgv is the command that shows an agent's own terminal (tmux
	// attach), or why it can't.
	AgentArgv func(ctx context.Context, session string) ([]string, error)
	// Environ is the environment shells start from; nil, os.Environ.
	Environ func() []string
	// Linger is how long shells run on after the app's last connection
	// closes.
	Linger time.Duration

	mu       sync.Mutex
	next     int
	shells   map[string]*PanelShell
	folders  map[string]*folderShells
	watchers map[string]map[*watcher]struct{}
	holds    int
	timer    *time.Timer

	loginOnce sync.Once
	login     string
}

type folderShells struct {
	order  []string
	active string
	splits map[string]string
}

// NewRegistry makes a registry whose shells all end with ctx.
func NewRegistry(ctx context.Context) *Registry {
	r := &Registry{
		Linger:   10 * time.Second,
		shells:   map[string]*PanelShell{},
		folders:  map[string]*folderShells{},
		watchers: map[string]map[*watcher]struct{}{},
	}
	go func() {
		<-ctx.Done()
		r.StopAll()
	}()
	return r
}

// Hold says an app connection is open: shells run on.
func (r *Registry) Hold() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.holds++
	if r.timer != nil {
		r.timer.Stop()
		r.timer = nil
	}
}

// Release says one closed. After the last, shells end once Linger passes
// without another opening.
func (r *Registry) Release() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.holds > 0 {
		r.holds--
	}
	if r.holds > 0 {
		return
	}
	if r.timer != nil {
		r.timer.Stop()
	}
	var timer *time.Timer
	timer = time.AfterFunc(r.Linger, func() {
		r.mu.Lock()
		current := r.timer == timer && r.holds == 0
		if current {
			r.timer = nil
		}
		r.mu.Unlock()
		if current {
			r.StopAll()
		}
	})
	r.timer = timer
}

// StopAll ends every shell.
func (r *Registry) StopAll() {
	r.mu.Lock()
	shells := make([]*PanelShell, 0, len(r.shells))
	for _, s := range r.shells {
		shells = append(shells, s)
	}
	folders := make([]string, 0, len(r.folders))
	for folder := range r.folders {
		folders = append(folders, folder)
	}
	r.shells = map[string]*PanelShell{}
	r.folders = map[string]*folderShells{}
	r.mu.Unlock()
	for _, s := range shells {
		s.close()
	}
	for _, folder := range folders {
		r.notify(folder)
	}
}

// Get is a shell by its id, or nil.
func (r *Registry) Get(id string) *PanelShell {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.shells[id]
}

// ShellInfo is a shell as the folder's state lists it.
type ShellInfo struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Exited  bool   `json:"exited"`
	Code    int    `json:"code"`
	Session string `json:"session,omitempty"`
}

// State is a folder's shells, the one in front and the tabs split in two.
type State struct {
	Type   string            `json:"type"`
	Shells []ShellInfo       `json:"shells"`
	Active string            `json:"active"`
	Splits map[string]string `json:"splits"`
}

// State is a folder's shells as they are now.
func (r *Registry) State(folder string) State {
	r.mu.Lock()
	defer r.mu.Unlock()
	state := State{Type: "state", Shells: []ShellInfo{}, Splits: map[string]string{}}
	f := r.folders[folder]
	if f == nil {
		return state
	}
	state.Active = f.active
	for left, right := range f.splits {
		state.Splits[left] = right
	}
	for _, id := range f.order {
		if s := r.shells[id]; s != nil {
			state.Shells = append(state.Shells, s.info())
		}
	}
	return state
}

func (r *Registry) folder(folder string) *folderShells {
	f := r.folders[folder]
	if f == nil {
		f = &folderShells{splits: map[string]string{}}
		r.folders[folder] = f
	}
	return f
}

// add makes a shell in a folder; the caller holds r.mu. It starts when a
// panel first shows it, at that panel's size.
func (r *Registry) add(folder string, command []string, title, session string) *PanelShell {
	f := r.folder(folder)
	r.next++
	if title == "" {
		title = filepath.Base(r.shellPath())
		if number := len(f.order) + 1; number > 1 {
			title += " " + strconv.Itoa(number)
		}
	}
	s := &PanelShell{
		ID: "t" + strconv.Itoa(r.next), Folder: folder, Session: session,
		reg: r, command: command, title: title, viewers: map[*viewer]struct{}{},
	}
	r.shells[s.ID] = s
	f.order = append(f.order, s.ID)
	return s
}

// New makes a login shell in the folder, in front.
func (r *Registry) New(folder string) *PanelShell {
	r.mu.Lock()
	s := r.add(folder, nil, "", "")
	r.folders[folder].active = s.ID
	r.mu.Unlock()
	r.notify(folder)
	return s
}

// Ensure makes a shell in the folder unless it has one.
func (r *Registry) Ensure(folder string) {
	r.mu.Lock()
	if f := r.folders[folder]; f != nil && len(f.order) > 0 {
		r.mu.Unlock()
		return
	}
	s := r.add(folder, nil, "", "")
	r.folders[folder].active = s.ID
	r.mu.Unlock()
	r.notify(folder)
}

// Agent shows an agent's own terminal in a tab of its own, or brings the
// tab it has to the front.
func (r *Registry) Agent(ctx context.Context, folder, session, title string) error {
	r.mu.Lock()
	if f := r.folders[folder]; f != nil {
		for _, id := range f.order {
			if s := r.shells[id]; s != nil && s.Session == session && !s.exitedNow() {
				f.active = id
				r.mu.Unlock()
				r.notify(folder)
				return nil
			}
		}
	}
	r.mu.Unlock()
	if r.AgentArgv == nil {
		return ErrNoAgentTerminal
	}
	argv, err := r.AgentArgv(ctx, session)
	if err != nil {
		return err
	}
	r.mu.Lock()
	s := r.add(folder, argv, title, session)
	r.folders[folder].active = s.ID
	r.mu.Unlock()
	r.notify(folder)
	return nil
}

// Split puts a new shell beside the one in front, in the same tab.
func (r *Registry) Split(folder string) {
	r.mu.Lock()
	f := r.folders[folder]
	if f == nil || r.shells[f.active] == nil || f.splits[f.active] != "" {
		r.mu.Unlock()
		return
	}
	s := r.add(folder, nil, "", "")
	f.splits[f.active] = s.ID
	r.mu.Unlock()
	r.notify(folder)
}

// Unsplit closes the shell beside the one in front.
func (r *Registry) Unsplit(folder string) {
	r.mu.Lock()
	var right string
	if f := r.folders[folder]; f != nil {
		right = f.splits[f.active]
	}
	r.mu.Unlock()
	if right != "" {
		r.Close(folder, right)
	}
}

// Activate brings a shell's tab to the front.
func (r *Registry) Activate(folder, id string) {
	r.mu.Lock()
	f := r.folders[folder]
	if f == nil || r.shells[id] == nil || r.shells[id].Folder != folder {
		r.mu.Unlock()
		return
	}
	f.active = id
	r.mu.Unlock()
	r.notify(folder)
}

// Close stops a shell and takes it away, as EditorStore.closeTerminal does:
// the right half of a split leaves its tab whole; a left half leaves the
// right one the tab, in front if it was.
func (r *Registry) Close(folder, id string) {
	r.mu.Lock()
	f := r.folders[folder]
	s := r.shells[id]
	if f == nil || s == nil || s.Folder != folder {
		r.mu.Unlock()
		return
	}
	left := ""
	for l, right := range f.splits {
		if right == id {
			left = l
		}
	}
	if left != "" {
		delete(f.splits, left)
	} else if right, ok := f.splits[id]; ok {
		delete(f.splits, id)
		if f.active == id {
			f.active = right
		}
	}
	index := -1
	for i, other := range f.order {
		if other == id {
			index = i
		}
	}
	if index >= 0 {
		f.order = append(f.order[:index:index], f.order[index+1:]...)
	}
	delete(r.shells, id)
	if f.active == id {
		halves := map[string]bool{}
		for _, right := range f.splits {
			halves[right] = true
		}
		var tabs []string
		for _, other := range f.order {
			if !halves[other] {
				tabs = append(tabs, other)
			}
		}
		f.active = ""
		if len(tabs) > 0 {
			f.active = tabs[min(max(index, 0), len(tabs)-1)]
		}
	}
	if len(f.order) == 0 {
		delete(r.folders, folder)
	}
	r.mu.Unlock()
	s.close()
	r.notify(folder)
}

// Restart starts a fresh process in place of one that ended.
func (r *Registry) Restart(folder, id string) {
	s := r.Get(id)
	if s == nil || s.Folder != folder {
		return
	}
	if s.restart() {
		r.notify(folder)
	}
}

func (r *Registry) shellPath() string {
	if r.ShellPath != "" {
		return r.ShellPath
	}
	r.loginOnce.Do(func() { r.login = LoginShell() })
	return r.login
}

func (r *Registry) environ() []string {
	if r.Environ != nil {
		return r.Environ()
	}
	return os.Environ()
}

// command is how a shell starts: the login shell, as Terminal starts it (a
// leading dash in its name), in its folder; an agent's terminal runs in one
// too, so it is found on the user's own PATH.
func (r *Registry) command(s *PanelShell) *exec.Cmd {
	shell := r.shellPath()
	args := []string{"-" + filepath.Base(shell)}
	if s.command != nil {
		quoted := make([]string, len(s.command))
		for i, word := range s.command {
			quoted[i] = Quoted(word)
		}
		args = append(args, "-c", "exec "+strings.Join(quoted, " "))
	}
	cmd := &exec.Cmd{Path: shell, Args: args, Dir: s.Folder, Env: ShellEnv(r.environ())}
	return cmd
}

// Quoted is a word as a shell reads it back unchanged.
func Quoted(word string) string {
	return "'" + strings.ReplaceAll(word, "'", `'\''`) + "'"
}

// ShellEnv is the environment of a panel's shell: what a terminal says about
// itself, and nothing that would confuse a shell started outside tmux or
// make it look like Agentman's own process.
func ShellEnv(base []string) []string {
	env := make([]string, 0, len(base)+4)
	lang := false
	for _, kv := range base {
		key, _, _ := strings.Cut(kv, "=")
		switch key {
		case "TMUX", "TMUX_PANE", "TERM", "TERM_PROGRAM", "TERM_PROGRAM_VERSION", "COLORTERM":
			continue
		case "LANG":
			lang = true
		}
		if strings.HasPrefix(key, "AGENTMAN_") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env, "TERM=xterm-256color", "COLORTERM=truecolor", "TERM_PROGRAM=Agentman")
	if !lang {
		env = append(env, "LANG=en_US.UTF-8")
	}
	return env
}

// LoginShell is the user's shell as the system records it, as the Mac app
// picks it; else $SHELL; else the first of zsh, bash and sh there is.
func LoginShell() string {
	if shell := recordedShell(); executable(shell) {
		return shell
	}
	if shell := os.Getenv("SHELL"); executable(shell) {
		return shell
	}
	for _, shell := range []string{"/bin/zsh", "/bin/bash"} {
		if executable(shell) {
			return shell
		}
	}
	return "/bin/sh"
}

func executable(path string) bool {
	if path == "" || !filepath.IsAbs(path) {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Mode()&0o111 != 0
}

// recordedShell is the shell in the user's record: Directory Services on
// macOS, the passwd database elsewhere.
func recordedShell() string {
	me, err := user.Current()
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if runtime.GOOS == "darwin" {
		out, err := exec.CommandContext(ctx, "/usr/bin/dscl", ".", "-read", "/Users/"+me.Username, "UserShell").Output()
		if err != nil {
			return ""
		}
		_, shell, _ := strings.Cut(strings.TrimSpace(string(out)), ":")
		return strings.TrimSpace(shell)
	}
	if out, err := exec.CommandContext(ctx, "getent", "passwd", me.Uid).Output(); err == nil {
		if shell := passwdShell(string(out), me.Uid); shell != "" {
			return shell
		}
	}
	if data, err := os.ReadFile("/etc/passwd"); err == nil {
		return passwdShell(string(data), me.Uid)
	}
	return ""
}

// passwdShell is the shell of the passwd line for uid.
func passwdShell(passwd, uid string) string {
	scanner := bufio.NewScanner(strings.NewReader(passwd))
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), ":")
		if len(fields) >= 7 && fields[2] == uid {
			return fields[6]
		}
	}
	return ""
}

// PanelShell is one of a folder's shells: its process (started when a panel first
// shows it, again on restart), its scrollback, and the panels showing it.
type PanelShell struct {
	ID      string
	Folder  string
	Session string

	reg     *Registry
	command []string

	mu      sync.Mutex
	title   string
	proc    *process
	started bool
	exited  bool
	code    int
	closed  bool
	size    Size
	ring    []byte
	osc     oscParser
	viewers map[*viewer]struct{}
}

type process struct {
	cmd      *exec.Cmd
	ptmx     *os.File
	done     chan struct{}
	code     int
	finished bool
}

func (s *PanelShell) info() ShellInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	return ShellInfo{ID: s.ID, Title: s.title, Exited: s.exited, Code: s.code, Session: s.Session}
}

func (s *PanelShell) exitedNow() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exited
}

// Title is the shell's name, or the one a program in it set.
func (s *PanelShell) Title() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.title
}

// startLocked starts the process at the shell's size; the caller holds s.mu.
func (s *PanelShell) startLocked() {
	if !s.size.valid() {
		s.size = Size{Cols: 80, Rows: 24}
	}
	cmd := s.reg.command(s)
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: s.size.Cols, Rows: s.size.Rows})
	s.started = true
	s.exited = false
	s.code = 0
	if err != nil {
		s.exited = true
		s.code = -1
		s.outputLocked([]byte("\r\n\x1b[2m[The shell couldn't start: " + err.Error() + "]\x1b[0m\r\n"))
		return
	}
	p := &process{cmd: cmd, ptmx: ptmx, done: make(chan struct{})}
	s.proc = p
	go func() {
		p.code = exitCode(cmd.Wait())
		close(p.done)
	}()
	read := make(chan struct{})
	go s.read(p, read)
	go s.finish(p, read)
}

// read carries the program's output to the scrollback and the panels.
func (s *PanelShell) read(p *process, done chan<- struct{}) {
	defer close(done)
	buf := make([]byte, 32<<10)
	for {
		n, err := p.ptmx.Read(buf)
		if n > 0 {
			s.mu.Lock()
			titled := false
			if !p.finished && !s.closed {
				s.outputLocked(buf[:n])
				if title, ok := s.osc.feed(buf[:n]); ok && title != s.title {
					s.title = title
					titled = true
				}
			}
			s.mu.Unlock()
			if titled {
				s.reg.notify(s.Folder)
			}
		}
		if err != nil {
			return
		}
	}
}

// finish waits for the program to end, then says so on its screen.
func (s *PanelShell) finish(p *process, read <-chan struct{}) {
	<-p.done
	// The rest of its output first, unless a program it left behind keeps
	// the terminal open.
	select {
	case <-read:
	case <-time.After(500 * time.Millisecond):
	}
	_ = p.ptmx.Close()
	s.mu.Lock()
	p.finished = true
	if s.closed || s.proc != p {
		s.mu.Unlock()
		return
	}
	s.exited = true
	s.code = p.code
	with := ""
	if p.code != 0 {
		with = " with code " + strconv.Itoa(p.code)
	}
	s.outputLocked([]byte("\r\n\x1b[2m[The shell ended" + with + ". Restart it from the panel.]\x1b[0m\r\n"))
	s.mu.Unlock()
	s.reg.notify(s.Folder)
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		if status, ok := exit.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			return 128 + int(status.Signal())
		}
		return exit.ExitCode()
	}
	return -1
}

// outputLocked keeps output in the scrollback and sends it to every panel
// showing the shell; the caller holds s.mu.
func (s *PanelShell) outputLocked(data []byte) {
	s.ring = append(s.ring, data...)
	if over := len(s.ring) - Scrollback; over > 0 {
		// Never from the middle of a character.
		for over < len(s.ring) && !utf8.RuneStart(s.ring[over]) {
			over++
		}
		s.ring = append(s.ring[:0:0], s.ring[over:]...)
	}
	chunk := append([]byte(nil), data...)
	for v := range s.viewers {
		v.send(chunk)
	}
}

// resetLocked replaces the scrollback with a cleared screen, on every panel
// too; the caller holds s.mu.
func (s *PanelShell) resetLocked() {
	s.ring = nil
	s.outputLocked([]byte(clearScreen))
}

func (s *PanelShell) restart() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || (s.started && !s.exited) {
		return false
	}
	s.resetLocked()
	s.startLocked()
	return true
}

// Clear clears the screen and the scrollback, and has the shell draw its
// prompt again, as ⌘K does in Terminal.
func (s *PanelShell) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resetLocked()
	if s.proc != nil && !s.exited {
		_, _ = s.proc.ptmx.Write([]byte{0x0c})
	}
}

func (s *PanelShell) resize(size Size) {
	if !size.valid() {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.size = size
	if s.proc != nil && !s.exited {
		_ = pty.Setsize(s.proc.ptmx, &pty.Winsize{Cols: size.Cols, Rows: size.Rows})
	}
}

func (s *PanelShell) write(data []byte) {
	s.mu.Lock()
	p := s.proc
	running := p != nil && !s.exited && !s.closed
	s.mu.Unlock()
	if running {
		_, _ = p.ptmx.Write(data)
	}
}

// close stops the process and lets every panel showing it go.
func (s *PanelShell) close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	p := s.proc
	viewers := s.viewers
	s.viewers = map[*viewer]struct{}{}
	s.mu.Unlock()
	for v := range viewers {
		v.end(websocket.StatusNormalClosure, "closed")
	}
	if p != nil {
		go stop(p.cmd, p.done)
	}
}

// viewer is a panel showing a shell: what is to be sent to it, in order.
type viewer struct {
	conn   *websocket.Conn
	queue  chan []byte
	once   sync.Once
	gone   chan struct{}
	reason string
	status websocket.StatusCode
}

func newViewer(conn *websocket.Conn) *viewer {
	return &viewer{conn: conn, queue: make(chan []byte, 1024), gone: make(chan struct{})}
}

// send queues output; a panel too slow to keep up is let go, and finds the
// scrollback when it opens again.
func (v *viewer) send(data []byte) {
	select {
	case <-v.gone:
	case v.queue <- data:
	default:
		v.end(websocket.StatusTryAgainLater, "behind")
	}
}

func (v *viewer) end(status websocket.StatusCode, reason string) {
	v.once.Do(func() {
		v.status, v.reason = status, reason
		close(v.gone)
	})
}

// Attach shows a shell on a panel's socket until the panel goes, the shell
// is closed or ctx ends. The first panel starts the shell at its size.
func (r *Registry) Attach(ctx context.Context, c *websocket.Conn, s *PanelShell, size Size) {
	c.SetReadLimit(1 << 20)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	v := newViewer(c)
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		_ = c.Close(websocket.StatusNormalClosure, "closed")
		return
	}
	// The scrollback first, then what comes after it.
	v.queue <- append([]byte(nil), s.ring...)
	s.viewers[v] = struct{}{}
	if size.valid() {
		s.size = size
	}
	if !s.started {
		s.startLocked()
	} else if s.proc != nil && !s.exited && size.valid() {
		_ = pty.Setsize(s.proc.ptmx, &pty.Winsize{Cols: size.Cols, Rows: size.Rows})
	}
	s.mu.Unlock()
	// It may have started, or failed to.
	r.notify(s.Folder)
	defer func() {
		s.mu.Lock()
		delete(s.viewers, v)
		s.mu.Unlock()
	}()

	// Keystrokes, resizes and clears from the panel.
	go func() {
		defer v.end(websocket.StatusNormalClosure, "")
		for {
			kind, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			switch kind {
			case websocket.MessageBinary:
				s.write(data)
			case websocket.MessageText:
				var msg command
				if json.Unmarshal(data, &msg) != nil {
					continue
				}
				switch msg.Type {
				case "resize":
					s.resize(msg.Size)
				case "clear":
					s.Clear()
				}
			}
		}
	}()

	for {
		select {
		case data := <-v.queue:
			if len(data) == 0 {
				continue
			}
			if err := c.Write(ctx, websocket.MessageBinary, data); err != nil {
				return
			}
		case <-v.gone:
			if v.reason != "" {
				_ = c.Close(v.status, v.reason)
			}
			return
		case <-ctx.Done():
			_ = c.Close(websocket.StatusGoingAway, "am serve is stopping")
			return
		}
	}
}

// watcher is a panel following a folder's shells.
type watcher struct {
	changed chan struct{}
}

func (r *Registry) notify(folder string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for w := range r.watchers[folder] {
		select {
		case w.changed <- struct{}{}:
		default:
		}
	}
}

// folderCommand is what a panel asks of a folder's shells.
type folderCommand struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	Session string `json:"session"`
	Title   string `json:"title"`
}

// Watch follows a folder's shells on a panel's socket, sending their state
// as it changes and doing what the panel asks.
func (r *Registry) Watch(ctx context.Context, c *websocket.Conn, folder string) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	w := &watcher{changed: make(chan struct{}, 1)}
	w.changed <- struct{}{}
	r.mu.Lock()
	if r.watchers[folder] == nil {
		r.watchers[folder] = map[*watcher]struct{}{}
	}
	r.watchers[folder][w] = struct{}{}
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.watchers[folder], w)
		if len(r.watchers[folder]) == 0 {
			delete(r.watchers, folder)
		}
		r.mu.Unlock()
	}()

	go func() {
		defer cancel()
		for {
			kind, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			var msg folderCommand
			if kind != websocket.MessageText || json.Unmarshal(data, &msg) != nil {
				continue
			}
			if err := r.do(ctx, folder, msg); err != nil {
				reply, _ := json.Marshal(map[string]string{"type": "error", "error": err.Error()})
				_ = c.Write(ctx, websocket.MessageText, reply)
			}
		}
	}()

	var last []byte
	for {
		select {
		case <-w.changed:
			state, _ := json.Marshal(r.State(folder))
			if bytes.Equal(state, last) {
				continue
			}
			last = state
			if err := c.Write(ctx, websocket.MessageText, state); err != nil {
				return
			}
		case <-ctx.Done():
			_ = c.Close(websocket.StatusNormalClosure, "")
			return
		}
	}
}

func (r *Registry) do(ctx context.Context, folder string, msg folderCommand) error {
	switch msg.Type {
	case "new":
		r.New(folder)
	case "ensure":
		r.Ensure(folder)
	case "agent":
		return r.Agent(ctx, folder, msg.Session, msg.Title)
	case "split":
		r.Split(folder)
	case "unsplit":
		r.Unsplit(folder)
	case "close":
		r.Close(folder, msg.ID)
	case "restart":
		r.Restart(folder, msg.ID)
	case "activate":
		r.Activate(folder, msg.ID)
	default:
		return fmt.Errorf("no such command: %s", msg.Type)
	}
	return nil
}

// oscParser finds the titles programs set (OSC 0 and OSC 2) in output, a
// sequence split across reads included.
type oscParser struct {
	state int
	num   []byte
	text  []byte
}

const (
	oscNone = iota
	oscEscape
	oscNumber
	oscText
	oscTextEscape
)

// maxTitle bounds a title, so a sequence never ended can't grow forever.
const maxTitle = 1024

// feed reads output; it answers the last title set in it, if any.
func (p *oscParser) feed(data []byte) (string, bool) {
	title, found := "", false
	for _, b := range data {
		switch p.state {
		case oscNone:
			if b == 0x1b {
				p.state = oscEscape
			}
		case oscEscape:
			if b == ']' {
				p.state, p.num, p.text = oscNumber, p.num[:0], p.text[:0]
			} else if b != 0x1b {
				p.state = oscNone
			}
		case oscNumber:
			switch {
			case b >= '0' && b <= '9' && len(p.num) < 8:
				p.num = append(p.num, b)
			case b == ';':
				p.state = oscText
			case b == 0x07:
				p.state = oscNone
			case b == 0x1b:
				p.state = oscTextEscape
			default:
				p.state = oscText
				p.num = append(p.num[:0], 'x')
			}
		case oscText:
			switch b {
			case 0x07:
				if t, ok := p.done(); ok {
					title, found = t, true
				}
			case 0x1b:
				p.state = oscTextEscape
			default:
				if len(p.text) < maxTitle {
					p.text = append(p.text, b)
				}
			}
		case oscTextEscape:
			if b == '\\' {
				if t, ok := p.done(); ok {
					title, found = t, true
				}
			} else if b == ']' {
				// A new sequence in place of an unended one.
				p.state, p.num, p.text = oscNumber, p.num[:0], p.text[:0]
			} else {
				p.state = oscNone
			}
		}
	}
	return title, found
}

func (p *oscParser) done() (string, bool) {
	p.state = oscNone
	if n := string(p.num); n != "0" && n != "2" {
		return "", false
	}
	title := strings.TrimSpace(string(p.text))
	if title == "" || !utf8.ValidString(title) {
		return "", false
	}
	return title, true
}
