package lsp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// State is where a server is in its life, as the editor shows it.
type State string

const (
	StateInstalling State = "installing"
	StateStarting   State = "starting"
	StateReady      State = "ready"
	StateRestarting State = "restarting"
	StateFailed     State = "failed"
	StateStopped    State = "stopped"
)

// Status is a server's state, for the editor's status bar.
type Status struct {
	Session  string `json:"session"`
	Server   string `json:"server"`
	Name     string `json:"name"`
	Language string `json:"language"`
	Root     string `json:"root"`
	State    State  `json:"state"`
	// Detail says what it is doing or what went wrong.
	Detail string `json:"detail,omitempty"`
}

// FileChange is a file created, changed or deleted on disk, as LSP numbers
// them: 1 created, 2 changed, 3 deleted.
type FileChange struct {
	Path string `json:"path"`
	Type int    `json:"type"`
}

// A session is one server process for one project root, shared by every
// editor window with a file in that project.
type Session struct {
	Key    string
	server *Server
	root   string
	host   *Host

	mu       sync.Mutex
	state    State
	detail   string
	caps     json.RawMessage
	proc     *process
	clients  map[*Client]bool
	pending  map[int64]pendingRequest
	own      map[int64]chan message
	nextID   int64
	docs     map[string]*openDoc
	watchers []watcher
	progress map[string]*progressReport
	// notice is what the server last warned of: a toolchain it doesn't
	// support, a project it couldn't load. Shown while nothing is under way.
	notice     string
	lastUsed   time.Time
	lastClient *Client
	crashes    []time.Time
	stopping   bool
}

// pendingRequest is a window's request, under the id am gave it.
type pendingRequest struct {
	client *Client
	id     json.RawMessage
}

// openDoc is a file the server has open, and which windows have it open.
// The first window to open it keeps it in step; the others' copies are the
// same file, and the server only needs it once.
type openDoc struct {
	owner *Client
	refs  map[*Client]bool
}

// watcher is a file watcher a server registered.
type watcher struct {
	id   string
	base string
	glob Glob
	kind int
}

func newSession(host *Host, server *Server, root string) *Session {
	return &Session{
		Key:      server.ID + ":" + root,
		server:   server,
		root:     root,
		host:     host,
		state:    StateStarting,
		clients:  map[*Client]bool{},
		pending:  map[int64]pendingRequest{},
		own:      map[int64]chan message{},
		docs:     map[string]*openDoc{},
		progress: map[string]*progressReport{},
		lastUsed: time.Now(),
	}
}

// Status is the session's state now.
func (s *Session) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.statusLocked()
}

func (s *Session) statusLocked() Status {
	return Status{
		Session: s.Key, Server: s.server.ID, Name: s.server.Name, Language: s.server.Language,
		Root: s.root, State: s.state, Detail: s.detail,
	}
}

func (s *Session) setState(state State, detail string) {
	s.mu.Lock()
	if s.stopping && state != StateStopped {
		s.mu.Unlock()
		return
	}
	s.state, s.detail = state, detail
	status, caps := s.statusLocked(), s.caps
	if state != StateReady {
		caps = nil
	}
	clients := s.clientsLocked()
	s.mu.Unlock()
	for _, c := range clients {
		c.sendStatus(status, caps)
	}
}

func (s *Session) clientsLocked() []*Client {
	clients := make([]*Client, 0, len(s.clients))
	for c := range s.clients {
		clients = append(clients, c)
	}
	return clients
}

// attach adds a window, and tells it where the server is.
func (s *Session) attach(c *Client) {
	s.mu.Lock()
	s.clients[c] = true
	s.lastUsed = time.Now()
	status, caps := s.statusLocked(), s.caps
	if s.state != StateReady {
		caps = nil
	}
	s.mu.Unlock()
	c.sendStatus(status, caps)
}

// detach takes a window away: the files only it had open are closed.
func (s *Session) detach(c *Client) {
	s.mu.Lock()
	delete(s.clients, c)
	var closed []string
	for uri, doc := range s.docs {
		if !doc.refs[c] {
			continue
		}
		delete(doc.refs, c)
		if len(doc.refs) == 0 {
			delete(s.docs, uri)
			closed = append(closed, uri)
		} else if doc.owner == c {
			for other := range doc.refs {
				doc.owner = other
				break
			}
		}
	}
	for id, p := range s.pending {
		if p.client == c {
			delete(s.pending, id)
		}
	}
	if s.lastClient == c {
		s.lastClient = nil
	}
	s.lastUsed = time.Now()
	ready, proc := s.state == StateReady, s.proc
	s.mu.Unlock()
	if !ready || proc == nil {
		return
	}
	for _, uri := range closed {
		_ = proc.notify("textDocument/didClose", map[string]any{"textDocument": map[string]string{"uri": uri}})
	}
}

// idle says whether no window has used the server for d.
func (s *Session) idle(now time.Time, d time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.clients) == 0 && now.Sub(s.lastUsed) >= d
}

func (s *Session) unused() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.clients) == 0
}

func (s *Session) lastUse() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastUsed
}

// MARK: - Starting

// start runs the server and initializes it, telling windows how it goes.
func (s *Session) start() {
	launch, ok := s.host.locate(s.server)
	if !ok {
		if !s.host.canInstall(s.server) {
			s.setState(StateFailed, s.server.Missing)
			return
		}
		// The first file of a language: am gets its server, once.
		s.setState(StateInstalling, "Getting "+s.server.Language+" support…")
		var err error
		launch, err = s.host.installer.Install(s.host.ctx, s.server, func(detail string) {
			s.setState(StateInstalling, detail)
		})
		if err != nil {
			s.setState(StateFailed, err.Error())
			return
		}
	}
	if s.server.PathFirst != nil {
		launch.Dirs = append(s.server.PathFirst(&s.host.tools), launch.Dirs...)
	}
	s.setState(StateStarting, "Starting "+s.server.Name+"…")
	proc, err := s.host.spawn(launch, s.root)
	if err != nil {
		s.setState(StateFailed, fmt.Sprintf("Couldn't start %s: %v", s.server.Name, err))
		return
	}
	s.mu.Lock()
	if s.stopping {
		s.mu.Unlock()
		proc.kill()
		return
	}
	s.proc = proc
	s.mu.Unlock()
	go s.read(proc)

	ctx, cancel := context.WithTimeout(context.Background(), s.host.startTimeout)
	defer cancel()
	result, err := s.call(ctx, "initialize", s.initializeParams())
	if err != nil {
		proc.kill()
		s.setState(StateFailed, fmt.Sprintf("%s didn't start: %v%s", s.server.Name, err, proc.stderr.suffix()))
		return
	}
	var initialized struct {
		Capabilities json.RawMessage `json:"capabilities"`
	}
	_ = json.Unmarshal(result, &initialized)
	if err := proc.notify("initialized", map[string]any{}); err != nil {
		proc.kill()
		s.setState(StateFailed, fmt.Sprintf("%s stopped as it started%s", s.server.Name, proc.stderr.suffix()))
		return
	}
	if s.server.Settings != nil {
		_ = proc.notify("workspace/didChangeConfiguration", map[string]any{"settings": s.server.Settings})
	}
	s.mu.Lock()
	s.caps = initialized.Capabilities
	if len(s.caps) == 0 {
		s.caps = json.RawMessage("{}")
	}
	s.mu.Unlock()
	s.setState(StateReady, "")
}

func (s *Session) initializeParams() map[string]any {
	root := fileURI(s.root)
	return map[string]any{
		"processId":             os.Getpid(),
		"clientInfo":            map[string]string{"name": "Agentman"},
		"rootUri":               root,
		"rootPath":              s.root,
		"workspaceFolders":      []map[string]string{{"uri": root, "name": filepath.Base(s.root)}},
		"capabilities":          clientCapabilities,
		"initializationOptions": s.server.Options,
	}
}

// read handles everything the server sends, until it exits.
func (s *Session) read(proc *process) {
	reader := bufio.NewReaderSize(proc.stdout, 64<<10)
	for {
		body, err := readMessage(reader)
		if err != nil {
			break
		}
		var msg message
		if json.Unmarshal(body, &msg) != nil {
			continue
		}
		s.fromServer(proc, msg)
	}
	proc.wait()
	s.exited(proc)
}

// exited: a server that stops on its own is started again, up to three
// times in three minutes; after that the editor offers to restart it.
func (s *Session) exited(proc *process) {
	s.mu.Lock()
	if s.proc != proc {
		s.mu.Unlock()
		return
	}
	s.proc = nil
	s.docs = map[string]*openDoc{}
	s.watchers = nil
	s.progress = map[string]*progressReport{}
	s.notice = ""
	failed := s.pending
	s.pending = map[int64]pendingRequest{}
	for id, ch := range s.own {
		close(ch)
		delete(s.own, id)
	}
	if s.stopping {
		s.mu.Unlock()
		return
	}
	now := time.Now()
	recent := s.crashes[:0]
	for _, at := range s.crashes {
		if now.Sub(at) < 3*time.Minute {
			recent = append(recent, at)
		}
	}
	s.crashes = append(recent, now)
	tries := len(s.crashes)
	s.mu.Unlock()

	for _, p := range failed {
		p.client.sendRPC(s.Key, errorResponse(p.id, -32803, s.server.Name+" stopped"))
	}
	if tries > 3 {
		s.setState(StateFailed, s.server.Name+" keeps stopping"+proc.stderr.suffix())
		return
	}
	s.setState(StateRestarting, s.server.Name+" stopped; starting it again…")
	go func() {
		time.Sleep(s.host.backoff(tries))
		s.start()
	}()
}

// restart starts a server that failed, on the user's say.
func (s *Session) restart() {
	s.mu.Lock()
	if s.state != StateFailed || s.stopping {
		s.mu.Unlock()
		return
	}
	s.crashes = nil
	s.mu.Unlock()
	go s.start()
}

// MARK: - Stopping

// stop asks the server to shut down, and ends it if it doesn't.
func (s *Session) stop() {
	s.mu.Lock()
	if s.stopping {
		s.mu.Unlock()
		return
	}
	s.stopping = true
	proc, ready := s.proc, s.state == StateReady
	s.mu.Unlock()
	if proc != nil {
		if ready {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			_, _ = s.call(ctx, "shutdown", nil)
			cancel()
			_ = proc.notify("exit", nil)
		} else {
			proc.kill()
		}
		select {
		case <-proc.done:
		case <-time.After(5 * time.Second):
		}
		proc.kill()
	}
	s.setState(StateStopped, "")
}

// MARK: - Messages from windows

// fromClient passes a window's message to the server, with requests under
// ids of am's own so windows sharing the server can't clash.
func (s *Session) fromClient(c *Client, msg message) {
	s.mu.Lock()
	if s.state != StateReady || s.proc == nil {
		s.mu.Unlock()
		if msg.isRequest() {
			c.sendRPC(s.Key, errorResponse(msg.ID, -32002, s.server.Name+" isn't ready"))
		}
		return
	}
	s.lastUsed = time.Now()
	switch {
	case msg.isRequest():
		s.nextID++
		s.pending[s.nextID] = pendingRequest{client: c, id: msg.ID}
		s.lastClient = c
		msg.ID = json.RawMessage(strconv.FormatInt(s.nextID, 10))
	case msg.Method == "$/cancelRequest":
		var params struct {
			ID json.RawMessage `json:"id"`
		}
		_ = json.Unmarshal(msg.Params, &params)
		found := false
		for id, p := range s.pending {
			if p.client == c && bytes.Equal(p.id, params.ID) {
				msg.Params, _ = json.Marshal(map[string]int64{"id": id})
				found = true
				break
			}
		}
		if !found {
			s.mu.Unlock()
			return
		}
	case msg.Method == "textDocument/didOpen":
		uri := documentURI(msg.Params)
		if doc := s.docs[uri]; doc != nil {
			doc.refs[c] = true
			s.mu.Unlock()
			return
		}
		s.docs[uri] = &openDoc{owner: c, refs: map[*Client]bool{c: true}}
	case msg.Method == "textDocument/didChange" || msg.Method == "textDocument/didSave" || msg.Method == "textDocument/willSave":
		if doc := s.docs[documentURI(msg.Params)]; doc == nil || doc.owner != c {
			s.mu.Unlock()
			return
		}
	case msg.Method == "textDocument/didClose":
		uri := documentURI(msg.Params)
		doc := s.docs[uri]
		if doc == nil || !doc.refs[c] {
			s.mu.Unlock()
			return
		}
		delete(doc.refs, c)
		if len(doc.refs) > 0 {
			if doc.owner == c {
				for other := range doc.refs {
					doc.owner = other
					break
				}
			}
			s.mu.Unlock()
			return
		}
		delete(s.docs, uri)
	}
	proc := s.proc
	s.mu.Unlock()
	_ = proc.send(msg)
}

// filesChanged tells the server about files changed on disk, as far as
// it asked to be told.
func (s *Session) filesChanged(changes []FileChange) {
	s.mu.Lock()
	watchers, proc, ready := s.watchers, s.proc, s.state == StateReady
	s.mu.Unlock()
	if !ready || proc == nil || len(watchers) == 0 {
		return
	}
	var events []map[string]any
	for _, change := range changes {
		if !within(change.Path, s.root) || change.Type < 1 || change.Type > 3 {
			continue
		}
		for _, w := range watchers {
			if w.kind&(1<<(change.Type-1)) == 0 {
				continue
			}
			path := change.Path
			if w.base != "" {
				rel, err := filepath.Rel(w.base, path)
				if err != nil || strings.HasPrefix(rel, "..") {
					continue
				}
				path = filepath.ToSlash(rel)
			}
			if w.glob.Match(path) {
				events = append(events, map[string]any{"uri": fileURI(change.Path), "type": change.Type})
				break
			}
		}
	}
	if len(events) > 0 {
		_ = proc.notify("workspace/didChangeWatchedFiles", map[string]any{"changes": events})
	}
}

// MARK: - Messages from the server

func (s *Session) fromServer(proc *process, msg message) {
	switch {
	case msg.isResponse():
		id, err := strconv.ParseInt(string(msg.ID), 10, 64)
		if err != nil {
			return
		}
		s.mu.Lock()
		if ch, ok := s.own[id]; ok {
			delete(s.own, id)
			s.mu.Unlock()
			ch <- msg
			return
		}
		p, ok := s.pending[id]
		delete(s.pending, id)
		s.mu.Unlock()
		if ok {
			msg.ID = p.id
			p.client.sendRPC(s.Key, msg)
		}
	case msg.isRequest():
		s.answer(proc, msg)
	case msg.Method == "window/logMessage" || msg.Method == "telemetry/event":
	case msg.Method == "$/progress":
		s.showProgress(msg.Params)
	case msg.Method == "experimental/serverStatus":
		s.serverStatus(msg.Params)
	case msg.Method == "window/showMessage":
		var p struct {
			Type    int    `json:"type"`
			Message string `json:"message"`
		}
		if json.Unmarshal(msg.Params, &p) == nil && (p.Type == 1 || p.Type == 2) {
			s.notify(p.Message)
		}
	default:
		s.mu.Lock()
		clients := s.clientsLocked()
		s.mu.Unlock()
		for _, c := range clients {
			c.sendRPC(s.Key, msg)
		}
	}
}

// answer replies to what a server asks of its editor: am answers most of
// it, and passes on what only the window can answer.
func (s *Session) answer(proc *process, msg message) {
	reply := func(result any) {
		raw, _ := json.Marshal(result)
		_ = proc.send(message{JSONRPC: "2.0", ID: msg.ID, Result: raw})
	}
	switch msg.Method {
	case "workspace/configuration":
		var params struct {
			Items []struct {
				Section string `json:"section"`
			} `json:"items"`
		}
		_ = json.Unmarshal(msg.Params, &params)
		values := make([]any, len(params.Items))
		for i, item := range params.Items {
			values[i] = s.server.setting(item.Section)
		}
		reply(values)
	case "client/registerCapability":
		s.register(msg.Params)
		reply(nil)
	case "client/unregisterCapability":
		s.unregister(msg.Params)
		reply(nil)
	case "window/workDoneProgress/create":
		reply(nil)
	case "workspace/semanticTokens/refresh", "workspace/inlayHint/refresh":
		// Every window asks again; the server needn't know how many there are.
		reply(nil)
		s.mu.Lock()
		clients := s.clientsLocked()
		s.mu.Unlock()
		for _, c := range clients {
			c.sendRPC(s.Key, message{Method: msg.Method})
		}
	case "workspace/workspaceFolders":
		reply([]map[string]string{{"uri": fileURI(s.root), "name": filepath.Base(s.root)}})
	case "workspace/applyEdit", "window/showDocument", "window/showMessageRequest":
		s.mu.Lock()
		target := s.lastClient
		if target == nil || !s.clients[target] {
			target = nil
			for c := range s.clients {
				target = c
				break
			}
		}
		s.mu.Unlock()
		if target == nil {
			switch msg.Method {
			case "workspace/applyEdit":
				reply(map[string]bool{"applied": false})
			case "window/showDocument":
				reply(map[string]bool{"success": false})
			default:
				reply(nil)
			}
			return
		}
		target.sendRPC(s.Key, msg)
	default:
		raw, _ := json.Marshal(map[string]any{"code": -32601, "message": "Agentman doesn't handle " + msg.Method})
		_ = proc.send(message{JSONRPC: "2.0", ID: msg.ID, Error: raw})
	}
}

// fromClientResponse passes a window's answer to a request the server sent.
func (s *Session) fromClientResponse(msg message) {
	s.mu.Lock()
	proc := s.proc
	s.mu.Unlock()
	if proc != nil {
		_ = proc.send(msg)
	}
}

func (s *Session) register(params json.RawMessage) {
	var p struct {
		Registrations []struct {
			ID              string `json:"id"`
			Method          string `json:"method"`
			RegisterOptions struct {
				Watchers []struct {
					GlobPattern json.RawMessage `json:"globPattern"`
					Kind        int             `json:"kind"`
				} `json:"watchers"`
			} `json:"registerOptions"`
		} `json:"registrations"`
	}
	if json.Unmarshal(params, &p) != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range p.Registrations {
		if r.Method != "workspace/didChangeWatchedFiles" {
			continue
		}
		for _, w := range r.RegisterOptions.Watchers {
			pattern, base := globPattern(w.GlobPattern)
			glob, err := CompileGlob(pattern)
			if err != nil {
				continue
			}
			kind := w.Kind
			if kind == 0 {
				kind = 7
			}
			s.watchers = append(s.watchers, watcher{id: r.ID, base: base, glob: glob, kind: kind})
		}
	}
}

func (s *Session) unregister(params json.RawMessage) {
	var p struct {
		// LSP spells it this way.
		Unregisterations []struct {
			ID string `json:"id"`
		} `json:"unregisterations"`
	}
	if json.Unmarshal(params, &p) != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range p.Unregisterations {
		kept := s.watchers[:0]
		for _, w := range s.watchers {
			if w.id != u.ID {
				kept = append(kept, w)
			}
		}
		s.watchers = kept
	}
}

// progressReport is a piece of work a server reports, as it last said.
type progressReport struct {
	title, message string
	percentage     *int
}

func (r *progressReport) text() string {
	text := r.title
	if r.message != "" {
		text += " (" + r.message + ")"
	}
	if r.percentage != nil {
		text += fmt.Sprintf(" %d%%", *r.percentage)
	}
	return strings.TrimSpace(text)
}

// showProgress turns the server's progress reports ("Loading packages…")
// into the status the editor shows. A report says only what changed: its
// title comes with the first.
func (s *Session) showProgress(params json.RawMessage) {
	var p struct {
		Token json.RawMessage `json:"token"`
		Value struct {
			Kind       string  `json:"kind"`
			Title      string  `json:"title"`
			Message    *string `json:"message"`
			Percentage *int    `json:"percentage"`
		} `json:"value"`
	}
	if json.Unmarshal(params, &p) != nil || p.Value.Kind == "" {
		return
	}
	token := string(p.Token)
	s.mu.Lock()
	detail := ""
	switch p.Value.Kind {
	case "begin", "report":
		report := s.progress[token]
		if report == nil || p.Value.Kind == "begin" {
			report = &progressReport{}
			s.progress[token] = report
		}
		if p.Value.Title != "" {
			report.title = p.Value.Title
		}
		if p.Value.Message != nil {
			report.message = *p.Value.Message
		}
		if p.Value.Percentage != nil {
			report.percentage = p.Value.Percentage
		}
		detail = report.text()
	case "end":
		delete(s.progress, token)
		detail = s.underWayLocked()
	}
	state := s.state
	s.mu.Unlock()
	if state == StateReady {
		s.setState(StateReady, detail)
	}
}

// underWayLocked is what to show when a piece of work ends: another under
// way, or what the server last warned of.
func (s *Session) underWayLocked() string {
	tokens := make([]string, 0, len(s.progress))
	for token := range s.progress {
		tokens = append(tokens, token)
	}
	sort.Strings(tokens)
	for _, token := range tokens {
		if text := s.progress[token].text(); text != "" {
			return text
		}
	}
	return s.notice
}

// notify keeps what a server warns of, shown in the editor's status bar
// until it says all is well: its first line, as the bar has room for one.
func (s *Session) notify(message string) {
	message, _, _ = strings.Cut(strings.TrimSpace(message), "\n")
	s.mu.Lock()
	s.notice = strings.TrimSpace(message)
	busy := len(s.progress) > 0
	state := s.state
	s.mu.Unlock()
	if state == StateReady && !busy {
		s.setState(StateReady, message)
	}
}

// serverStatus is rust-analyzer's word on how it is: a workspace it
// couldn't load, or all well again.
func (s *Session) serverStatus(params json.RawMessage) {
	var p struct {
		Health  string `json:"health"`
		Message string `json:"message"`
	}
	if json.Unmarshal(params, &p) != nil {
		return
	}
	if p.Health == "ok" {
		s.mu.Lock()
		cleared := s.notice != ""
		s.notice = ""
		busy := len(s.progress) > 0
		state := s.state
		s.mu.Unlock()
		if cleared && !busy && state == StateReady {
			s.setState(StateReady, "")
		}
		return
	}
	if p.Message != "" {
		s.notify(p.Message)
	}
}

// call sends a request of am's own and waits for the answer.
func (s *Session) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	s.mu.Lock()
	proc := s.proc
	if proc == nil {
		s.mu.Unlock()
		return nil, errors.New("not running")
	}
	s.nextID++
	id := s.nextID
	ch := make(chan message, 1)
	s.own[id] = ch
	s.mu.Unlock()
	raw, _ := json.Marshal(params)
	if params == nil {
		raw = nil
	}
	if err := proc.send(message{JSONRPC: "2.0", ID: json.RawMessage(strconv.FormatInt(id, 10)), Method: method, Params: raw}); err != nil {
		s.mu.Lock()
		delete(s.own, id)
		s.mu.Unlock()
		return nil, err
	}
	select {
	case msg, ok := <-ch:
		if !ok {
			return nil, errors.New("it stopped")
		}
		if len(msg.Error) > 0 {
			var e struct {
				Message string `json:"message"`
			}
			_ = json.Unmarshal(msg.Error, &e)
			return nil, errors.New(e.Message)
		}
		return msg.Result, nil
	case <-ctx.Done():
		s.mu.Lock()
		delete(s.own, id)
		s.mu.Unlock()
		return nil, errors.New("no answer in time")
	}
}

// MARK: - The process

type process struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stdout  io.Reader
	stderr  *tail
	writeMu sync.Mutex
	done    chan struct{}
	once    sync.Once
}

func (p *process) send(msg message) error {
	msg.JSONRPC = "2.0"
	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	return writeMessage(p.stdin, body)
}

func (p *process) notify(method string, params any) error {
	var raw json.RawMessage
	if params != nil {
		raw, _ = json.Marshal(params)
	}
	return p.send(message{Method: method, Params: raw})
}

func (p *process) wait() {
	p.once.Do(func() {
		_ = p.cmd.Wait()
		close(p.done)
	})
}

func (p *process) kill() {
	killGroup(p.cmd)
	_ = p.stdin.Close()
}

// tail keeps the end of what a server wrote to stderr, to say why it failed.
type tail struct {
	mu  sync.Mutex
	buf []byte
}

func (t *tail) Write(b []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, b...)
	if len(t.buf) > 4096 {
		t.buf = t.buf[len(t.buf)-4096:]
	}
	return len(b), nil
}

// suffix is the last line it wrote, as ": …", or nothing.
func (t *tail) suffix() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	lines := strings.Split(strings.TrimSpace(string(t.buf)), "\n")
	last := strings.TrimSpace(lines[len(lines)-1])
	if last == "" {
		return ""
	}
	if len(last) > 200 {
		last = last[:200] + "…"
	}
	return ": " + last
}

// MARK: - Helpers

func errorResponse(id json.RawMessage, code int, text string) message {
	raw, _ := json.Marshal(map[string]any{"code": code, "message": text})
	return message{JSONRPC: "2.0", ID: id, Error: raw}
}

func documentURI(params json.RawMessage) string {
	var p struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
	}
	_ = json.Unmarshal(params, &p)
	return p.TextDocument.URI
}

// fileURI is a path as LSP names files: file:///Users/me/a%20b.go.
func fileURI(path string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
}

// within says whether path is root or inside it.
func within(path, root string) bool {
	return path == root || strings.HasPrefix(path, strings.TrimSuffix(root, "/")+"/")
}

// globPattern reads a watcher's pattern: a string, or a pattern relative to
// a base folder.
func globPattern(raw json.RawMessage) (pattern, base string) {
	if json.Unmarshal(raw, &pattern) == nil {
		return pattern, ""
	}
	var relative struct {
		BaseURI json.RawMessage `json:"baseUri"`
		Pattern string          `json:"pattern"`
	}
	_ = json.Unmarshal(raw, &relative)
	var uri string
	if json.Unmarshal(relative.BaseURI, &uri) != nil {
		var folder struct {
			URI string `json:"uri"`
		}
		_ = json.Unmarshal(relative.BaseURI, &folder)
		uri = folder.URI
	}
	if u, err := url.Parse(uri); err == nil && u.Scheme == "file" {
		base = u.Path
	}
	return relative.Pattern, base
}

// clientCapabilities are what the editor's page handles.
var clientCapabilities = map[string]any{
	"general": map[string]any{"positionEncodings": []string{"utf-16"}},
	"workspace": map[string]any{
		"applyEdit":              true,
		"workspaceEdit":          map[string]any{"documentChanges": true},
		"didChangeConfiguration": map[string]any{"dynamicRegistration": false},
		"didChangeWatchedFiles":  map[string]any{"dynamicRegistration": true, "relativePatternSupport": true},
		"workspaceFolders":       true,
		"configuration":          true,
		"executeCommand":         map[string]any{},
		"semanticTokens":         map[string]any{"refreshSupport": true},
		"inlayHint":              map[string]any{"refreshSupport": true},
	},
	// rust-analyzer says how it is (a workspace it couldn't load) when asked.
	"experimental": map[string]any{"serverStatusNotification": true},
	"window": map[string]any{
		"workDoneProgress": true,
		"showMessage":      map[string]any{},
		"showDocument":     map[string]any{"support": true},
	},
	"textDocument": map[string]any{
		"synchronization": map[string]any{"didSave": true},
		"completion": map[string]any{
			"contextSupport": true,
			"completionItem": map[string]any{
				"snippetSupport":          true,
				"commitCharactersSupport": true,
				"documentationFormat":     []string{"markdown", "plaintext"},
				"deprecatedSupport":       true,
				"preselectSupport":        true,
				"insertReplaceSupport":    true,
				"labelDetailsSupport":     true,
				"resolveSupport":          map[string]any{"properties": []string{"documentation", "detail", "additionalTextEdits"}},
			},
		},
		"hover": map[string]any{"contentFormat": []string{"markdown", "plaintext"}},
		"signatureHelp": map[string]any{
			"contextSupport": true,
			"signatureInformation": map[string]any{
				"documentationFormat":    []string{"markdown", "plaintext"},
				"parameterInformation":   map[string]any{"labelOffsetSupport": true},
				"activeParameterSupport": true,
			},
		},
		"definition":        map[string]any{"linkSupport": true},
		"typeDefinition":    map[string]any{"linkSupport": true},
		"implementation":    map[string]any{"linkSupport": true},
		"references":        map[string]any{},
		"documentHighlight": map[string]any{},
		"documentSymbol":    map[string]any{"hierarchicalDocumentSymbolSupport": true},
		"codeAction": map[string]any{
			"isPreferredSupport": true,
			"codeActionLiteralSupport": map[string]any{"codeActionKind": map[string]any{"valueSet": []string{
				"", "quickfix", "refactor", "refactor.extract", "refactor.inline", "refactor.rewrite",
				"source", "source.organizeImports", "source.fixAll",
			}}},
		},
		"formatting":      map[string]any{},
		"rangeFormatting": map[string]any{},
		"semanticTokens": map[string]any{
			"requests":                map[string]any{"full": true},
			"tokenTypes":              semanticTokenTypes,
			"tokenModifiers":          semanticTokenModifiers,
			"formats":                 []string{"relative"},
			"multilineTokenSupport":   false,
			"overlappingTokenSupport": false,
		},
		"inlayHint": map[string]any{},
		"rename":    map[string]any{"prepareSupport": true},
		"publishDiagnostics": map[string]any{
			"relatedInformation":     true,
			"tagSupport":             map[string]any{"valueSet": []int{1, 2}},
			"codeDescriptionSupport": true,
		},
	},
}

// The semantic token kinds the editor colours, as LSP names them.
var semanticTokenTypes = []string{
	"namespace", "type", "class", "enum", "interface", "struct", "typeParameter", "parameter", "variable",
	"property", "enumMember", "event", "function", "method", "macro", "keyword", "modifier", "comment",
	"string", "number", "regexp", "operator", "decorator",
}

var semanticTokenModifiers = []string{
	"declaration", "definition", "readonly", "static", "deprecated", "abstract", "async", "modification",
	"documentation", "defaultLibrary",
}
