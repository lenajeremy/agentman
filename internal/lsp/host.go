package lsp

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// Origin is where the editor's page comes from; the endpoint answers it
// alone.
const Origin = "agentman-editor://app"

// Options shape a Host; the zero value is am serve's.
type Options struct {
	// Idle is how long a server nobody uses stays running. Ten minutes.
	Idle time.Duration
	// MaxServers is how many may run at once before idle ones make room.
	MaxServers int
	// Dir is where am puts the servers it downloads: ~/.agentman/lsp.
	// Without it, only servers already on the Mac run.
	Dir string
	// NoDownloads keeps am from downloading servers.
	NoDownloads bool
	// Paths run a server from a program of the user's choosing, by id.
	Paths map[string]string
	// Disabled servers don't run, by id.
	Disabled map[string]bool
	// Servers replaces the catalog, for tests.
	Servers []*Server
	// Find replaces looking for a server on the Mac, for tests.
	Find func(*Server) string
}

// Host runs the language servers for every editor window on this Mac.
type Host struct {
	tools        Tools
	idleAfter    time.Duration
	maxServers   int
	servers      []*Server
	findCommand  func(*Server) string
	installer    *Installer
	noDownloads  bool
	paths        map[string]string
	disabled     map[string]bool
	startTimeout time.Duration
	backoff      func(tries int) time.Duration

	ctx    context.Context
	cancel context.CancelFunc

	mu       sync.Mutex
	sessions map[string]*Session
	listener net.Listener
	url      string
	token    string
	closed   bool
}

// NewHost returns a host. Nothing runs until the editor asks for the
// endpoint.
func NewHost(opts Options) *Host {
	h := &Host{
		idleAfter:    opts.Idle,
		maxServers:   opts.MaxServers,
		servers:      opts.Servers,
		findCommand:  opts.Find,
		noDownloads:  opts.NoDownloads,
		paths:        opts.Paths,
		disabled:     opts.Disabled,
		startTimeout: time.Minute,
		backoff:      func(tries int) time.Duration { return time.Duration(tries*tries) * time.Second },
		sessions:     map[string]*Session{},
	}
	if h.idleAfter == 0 {
		h.idleAfter = 10 * time.Minute
	}
	if h.maxServers == 0 {
		h.maxServers = 6
	}
	if h.servers == nil {
		h.servers = catalog
	}
	if opts.Dir != "" {
		h.installer = &Installer{Dir: opts.Dir, Tools: &h.tools}
	}
	h.ctx, h.cancel = context.WithCancel(context.Background())
	return h
}

// Endpoint is where the editor's page connects, and the token it must
// bring. The listener starts on the first call, on loopback only.
func (h *Host) Endpoint() (url, token string, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return "", "", fmt.Errorf("lsp: am serve is stopping")
	}
	if h.listener != nil {
		return h.url, h.token, nil
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return "", "", err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", "", err
	}
	h.listener = listener
	h.token = hex.EncodeToString(secret)
	h.url = "ws://" + listener.Addr().String() + "/lsp"
	server := &http.Server{Handler: http.HandlerFunc(h.serveHTTP), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = server.Serve(listener) }()
	go func() {
		<-h.ctx.Done()
		_ = server.Close()
	}()
	go h.sweep()
	return h.url, h.token, nil
}

// allowed says whether a connection may use the endpoint: the token, and
// the editor page's origin, so a web page in a browser can't, even on this
// Mac.
func (h *Host) allowed(r *http.Request) bool {
	h.mu.Lock()
	token := h.token
	h.mu.Unlock()
	given := r.URL.Query().Get("token")
	if token == "" || subtle.ConstantTimeCompare([]byte(given), []byte(token)) != 1 {
		return false
	}
	return r.Header.Get("Origin") == Origin
}

// Close stops every server.
func (h *Host) Close() {
	h.mu.Lock()
	h.closed = true
	sessions := make([]*Session, 0, len(h.sessions))
	for _, s := range h.sessions {
		sessions = append(sessions, s)
	}
	h.sessions = map[string]*Session{}
	h.mu.Unlock()
	h.cancel()
	var wg sync.WaitGroup
	for _, s := range sessions {
		wg.Go(s.stop)
	}
	wg.Wait()
}

// Statuses are the servers running now, for `am lsp status`.
func (h *Host) Statuses() []Status {
	h.mu.Lock()
	sessions := make([]*Session, 0, len(h.sessions))
	for _, s := range h.sessions {
		sessions = append(sessions, s)
	}
	h.mu.Unlock()
	statuses := make([]Status, 0, len(sessions))
	for _, s := range sessions {
		statuses = append(statuses, s.Status())
	}
	sort.Slice(statuses, func(i, j int) bool { return statuses[i].Session < statuses[j].Session })
	return statuses
}

// serverFor is the server for an LSP language id, from this host's catalog.
func (h *Host) serverFor(language string) *Server {
	for _, server := range h.servers {
		if slices.Contains(server.Languages, language) {
			return server
		}
	}
	return nil
}

// resolve finds or starts the session for a file, and attaches the window
// to it. Without a server for the language it returns nil.
func (h *Host) resolve(c *Client, file, folder, language string) *Session {
	server := h.serverFor(language)
	if server == nil || file == "" || h.off(server) {
		return nil
	}
	root := Root(file, folder, server.Markers)
	key := server.ID + ":" + root
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil
	}
	s := h.sessions[key]
	fresh := s == nil
	var evict *Session
	if fresh {
		s = newSession(h, server, root)
		h.sessions[key] = s
		evict = h.evictLocked(s)
	}
	h.mu.Unlock()
	if evict != nil {
		go h.stopSession(evict)
	}
	s.attach(c)
	if fresh {
		go s.start()
	}
	return s
}

// evictLocked picks a server to make room for a new one: the one unused
// longest, among those no window is using.
func (h *Host) evictLocked(fresh *Session) *Session {
	if len(h.sessions) <= h.maxServers {
		return nil
	}
	var oldest *Session
	for _, s := range h.sessions {
		if s == fresh || !s.unused() {
			continue
		}
		if oldest == nil || s.lastUse().Before(oldest.lastUse()) {
			oldest = s
		}
	}
	if oldest != nil {
		delete(h.sessions, oldest.Key)
	}
	return oldest
}

func (h *Host) stopSession(s *Session) {
	h.mu.Lock()
	if h.sessions[s.Key] == s {
		delete(h.sessions, s.Key)
	}
	h.mu.Unlock()
	s.stop()
}

// sweep stops servers nobody has used for a while.
func (h *Host) sweep() {
	ticker := time.NewTicker(h.sweepEvery())
	defer ticker.Stop()
	for {
		select {
		case <-h.ctx.Done():
			return
		case now := <-ticker.C:
			h.stopIdle(now)
		}
	}
}

func (h *Host) sweepEvery() time.Duration {
	if every := h.idleAfter / 4; every < 30*time.Second {
		return max(every, 10*time.Millisecond)
	}
	return 30 * time.Second
}

func (h *Host) stopIdle(now time.Time) {
	h.mu.Lock()
	var idle []*Session
	for key, s := range h.sessions {
		if s.idle(now, h.idleAfter) {
			idle = append(idle, s)
			delete(h.sessions, key)
		}
	}
	h.mu.Unlock()
	for _, s := range idle {
		go s.stop()
	}
}

// filesChanged passes files changed on disk to every server.
func (h *Host) filesChanged(changes []FileChange) {
	h.mu.Lock()
	sessions := make([]*Session, 0, len(h.sessions))
	for _, s := range h.sessions {
		sessions = append(sessions, s)
	}
	h.mu.Unlock()
	for _, s := range sessions {
		s.filesChanged(changes)
	}
}

// locate is how to run a server without downloading it: the user's own
// program for it, a copy on the Mac, or the one am downloaded before.
func (h *Host) locate(server *Server) (Launch, bool) {
	if path := h.ownPath(server); path != "" {
		return Launch{Path: path, Args: server.Args}, true
	}
	found := ""
	if h.findCommand != nil {
		found = h.findCommand(server)
	} else if server.find != nil {
		found = server.find(&h.tools)
	}
	if found != "" {
		return Launch{Path: found, Args: server.Args}, true
	}
	if h.installer != nil {
		return h.installer.Installed(server)
	}
	return Launch{}, false
}

// Where says how a server would run, for am lsp status: from the user's own
// program, a copy on the Mac, one am downloaded, or not yet ("").
func (h *Host) Where(server *Server) (Launch, string) {
	launch, ok := h.locate(server)
	switch {
	case !ok:
		return Launch{}, ""
	case h.ownPath(server) != "":
		return launch, "yours"
	case h.installer != nil && strings.HasPrefix(launch.Args0OrPath(), h.installer.Dir+string(os.PathSeparator)):
		return launch, "downloaded"
	default:
		return launch, "mac"
	}
}

// Catalog is every server am knows how to run.
func Catalog() []*Server { return catalog }

// canInstall says whether am may download a server.
func (h *Host) canInstall(server *Server) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return server.Install != nil && h.installer != nil && !h.noDownloads
}

func (h *Host) ownPath(server *Server) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.paths[server.ID]
}

func (h *Host) off(server *Server) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.disabled[server.ID]
}

// Configure changes the settings while servers run. A server turned off or
// given another program stops; windows using it open its files again,
// with the new one, or none.
func (h *Host) Configure(noDownloads bool, paths map[string]string, disabled map[string]bool) {
	h.mu.Lock()
	changed := map[string]bool{}
	for _, server := range h.servers {
		if paths[server.ID] != h.paths[server.ID] || disabled[server.ID] != h.disabled[server.ID] {
			changed[server.ID] = true
		}
	}
	h.noDownloads, h.paths, h.disabled = noDownloads, paths, disabled
	var stopping []*Session
	for key, s := range h.sessions {
		if changed[s.server.ID] {
			stopping = append(stopping, s)
			delete(h.sessions, key)
		}
	}
	h.mu.Unlock()
	for _, s := range stopping {
		go s.stop()
	}
}

// Remove deletes what am downloaded for a server, or Node.js ("node"),
// stopping what runs from it first.
func (h *Host) Remove(id string) error {
	if h.installer == nil {
		return nil
	}
	h.mu.Lock()
	var stopping []*Session
	for key, s := range h.sessions {
		npm := s.server.Install != nil && s.server.Install.NPM != ""
		if s.server.ID == id || (id == "node" && npm) {
			stopping = append(stopping, s)
			delete(h.sessions, key)
		}
	}
	h.mu.Unlock()
	for _, s := range stopping {
		s.stop()
	}
	return os.RemoveAll(filepath.Join(h.installer.Dir, id))
}

// Report is the servers and where each comes from, for Settings.
type Report struct {
	NoDownloads bool
	Servers     []ServerReport
	// NodeSize is the Node.js am downloaded, in bytes, or 0.
	NodeSize int64
}

// ServerReport is one server, for Settings.
type ServerReport struct {
	Server *Server
	// Source is "yours", "mac", "downloaded", "download", "missing" or "off".
	Source  string
	Path    string
	Size    int64
	Running []string
}

// Report says what the servers are now.
func (h *Host) Report() Report {
	h.mu.Lock()
	report := Report{NoDownloads: h.noDownloads}
	running := map[string][]string{}
	for _, s := range h.sessions {
		running[s.server.ID] = append(running[s.server.ID], s.root)
	}
	h.mu.Unlock()
	for _, server := range h.servers {
		item := ServerReport{Server: server, Running: running[server.ID]}
		sort.Strings(item.Running)
		launch, where := h.Where(server)
		switch {
		case h.off(server):
			item.Source = "off"
		case where != "":
			item.Source, item.Path = where, launch.Args0OrPath()
			if where == "downloaded" {
				item.Size = folderSize(filepath.Join(h.installer.Dir, server.ID))
			}
		case h.canInstall(server):
			item.Source = "download"
		default:
			item.Source = "missing"
		}
		report.Servers = append(report.Servers, item)
	}
	if h.installer != nil {
		report.NodeSize = folderSize(filepath.Join(h.installer.Dir, "node"))
	}
	return report
}

// folderSize is what a folder's files take, in bytes.
func folderSize(dir string) int64 {
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, entry os.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			if info, err := entry.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total
}

// spawn starts a server in its project's folder.
func (h *Host) spawn(launch Launch, root string) (*process, error) {
	cmd := exec.Command(launch.Path, launch.Args...)
	cmd.Dir = root
	cmd.Env = withPath(h.tools.Env(), launch.Dirs)
	inGroup(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr := &tail{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &process{cmd: cmd, stdin: stdin, stdout: stdout, stderr: stderr, done: make(chan struct{})}, nil
}

// withPath puts folders at the front of an environment's PATH.
func withPath(env, dirs []string) []string {
	if len(dirs) == 0 {
		return env
	}
	front := strings.Join(dirs, string(os.PathListSeparator))
	out := make([]string, 0, len(env))
	for _, kv := range env {
		if rest, ok := strings.CutPrefix(kv, "PATH="); ok {
			kv = "PATH=" + front + string(os.PathListSeparator) + rest
		}
		out = append(out, kv)
	}
	return out
}
