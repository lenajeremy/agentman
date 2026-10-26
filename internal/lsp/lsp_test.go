package lsp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// The test binary doubles as a language server: with AGENTMAN_FAKE_LSP set
// it speaks LSP on stdin and stdout, and writes down what it was sent.
func TestMain(m *testing.M) {
	if os.Getenv("AGENTMAN_FAKE_LSP") == "1" {
		fakeServer()
		os.Exit(0)
	}
	os.Setenv("AGENTMAN_FAKE_LSP", "1")
	os.Exit(m.Run())
}

func fakeServer() {
	logFile, _ := os.OpenFile(filepath.Join(mustCwd(), "fake.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	record := func(line string) {
		fmt.Fprintln(logFile, line)
		logFile.Sync()
	}
	out := bufio.NewWriter(os.Stdout)
	send := func(msg map[string]any) {
		msg["jsonrpc"] = "2.0"
		body, _ := json.Marshal(msg)
		writeMessage(out, body)
		out.Flush()
	}
	in := bufio.NewReader(os.Stdin)
	for {
		body, err := readMessage(in)
		if err != nil {
			return
		}
		var msg message
		json.Unmarshal(body, &msg)
		if msg.Method == "" {
			record("response " + string(msg.ID) + " " + string(msg.Result))
			continue
		}
		record(msg.Method + " " + string(msg.ID) + " " + documentURI(msg.Params) + " " + string(msg.Params))
		switch msg.Method {
		case "initialize":
			send(map[string]any{"id": msg.ID, "result": map[string]any{"capabilities": map[string]any{"hoverProvider": true}}})
		case "initialized":
			send(map[string]any{"id": "watch", "method": "client/registerCapability", "params": map[string]any{
				"registrations": []any{map[string]any{"id": "w", "method": "workspace/didChangeWatchedFiles",
					"registerOptions": map[string]any{"watchers": []any{map[string]any{"globPattern": "**/*.go"}}}}},
			}})
		case "textDocument/hover":
			send(map[string]any{"id": msg.ID, "result": map[string]any{"contents": "hover " + string(msg.ID)}})
		case "fake/edit":
			send(map[string]any{"id": "edit", "method": "workspace/applyEdit", "params": map[string]any{"edit": map[string]any{}}})
		case "fake/crash":
			os.Exit(3)
		case "shutdown":
			send(map[string]any{"id": msg.ID, "result": nil})
		case "exit":
			os.Exit(0)
		}
	}
}

func mustCwd() string {
	dir, _ := os.Getwd()
	return dir
}

// MARK: - Pieces

func TestMessagesRoundTripInTheBaseProtocol(t *testing.T) {
	var buf bytes.Buffer
	writeMessage(&buf, []byte(`{"a":"é"}`))
	writeMessage(&buf, []byte(`{}`))
	r := bufio.NewReader(&buf)
	first, err := readMessage(r)
	if err != nil || string(first) != `{"a":"é"}` {
		t.Fatalf("first = %q, %v", first, err)
	}
	if second, err := readMessage(r); err != nil || string(second) != `{}` {
		t.Fatalf("second = %q, %v", second, err)
	}
	if _, err := readMessage(bufio.NewReader(strings.NewReader("X-Other: 1\r\n\r\n{}"))); err == nil {
		t.Fatal("a message without a length should be refused")
	}
	if _, err := readMessage(bufio.NewReader(strings.NewReader(fmt.Sprintf("Content-Length: %d\r\n\r\n", maxMessage+1)))); err == nil {
		t.Fatal("a huge message should be refused before it is read")
	}
}

func TestGlobsMatchAsServersMeanThem(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"**/*.go", "/Users/me/proj/internal/a.go", true},
		{"**/*.go", "/Users/me/proj/a.gox", false},
		{"**/*.{ts,tsx}", "/p/src/App.tsx", true},
		{"**/*.{ts,tsx}", "/p/src/App.js", false},
		{"**/go.mod", "/p/go.mod", true},
		{"*.py", "/p/deep/x.py", true},
		{"src/*.py", "src/x.py", true},
		{"src/*.py", "src/deep/x.py", false},
		{"src/[a-c]*.rs", "src/b.rs", true},
		{"src/[!a-c]*.rs", "src/b.rs", false},
	}
	for _, c := range cases {
		glob, err := CompileGlob(c.pattern)
		if err != nil {
			t.Fatalf("%s: %v", c.pattern, err)
		}
		if got := glob.Match(c.path); got != c.want {
			t.Errorf("%s ~ %s = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}

func TestTheRootIsTheNearestFolderWithAMarker(t *testing.T) {
	folder := t.TempDir()
	module := filepath.Join(folder, "service")
	os.MkdirAll(filepath.Join(module, "internal", "x"), 0o755)
	os.WriteFile(filepath.Join(module, "go.mod"), []byte("module x\n"), 0o644)
	if got := Root(filepath.Join(module, "internal", "x", "a.go"), folder, []string{"go.mod"}); got != module {
		t.Errorf("root = %s, want %s", got, module)
	}
	if got := Root(filepath.Join(folder, "loose.go"), folder, []string{"go.mod"}); got != folder {
		t.Errorf("a file with no marker above it belongs to the folder: %s", got)
	}
}

func TestEveryLanguageHasOneServer(t *testing.T) {
	seen := map[string]string{}
	for _, server := range catalog {
		for _, language := range server.Languages {
			if other, ok := seen[language]; ok {
				t.Errorf("%s is served by both %s and %s", language, other, server.ID)
			}
			seen[language] = server.ID
		}
		if server.Missing == "" || server.find == nil {
			t.Errorf("%s needs a way to be found and words for when it isn't", server.ID)
		}
	}
	if ServerFor("typescriptreact").ID != "typescript" || ServerFor("cobol") != nil {
		t.Error("languages map to the wrong servers")
	}
}

// MARK: - The host

var fakeServerSpec = &Server{ID: "fake", Name: "Fake", Language: "Fake", Languages: []string{"fake"}, Markers: []string{"fake.root"}, Missing: "Fake isn't installed"}

type harness struct {
	t    *testing.T
	host *Host
	url  string
}

func newHarness(t *testing.T, opts Options) *harness {
	t.Helper()
	if opts.Servers == nil {
		opts.Servers = []*Server{fakeServerSpec}
	}
	if opts.Find == nil {
		self, _ := os.Executable()
		opts.Find = func(*Server) string { return self }
	}
	host := NewHost(opts)
	host.backoff = func(int) time.Duration { return 10 * time.Millisecond }
	url, token, err := host.Endpoint()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(host.Close)
	return &harness{t: t, host: host, url: url + "?token=" + token}
}

// project is a folder with a marker, so it is a root of its own.
func project(t *testing.T) string {
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	os.WriteFile(filepath.Join(dir, "fake.root"), nil, 0o644)
	return dir
}

type window struct {
	t      *testing.T
	conn   *websocket.Conn
	frames chan frame
}

func (h *harness) window() *window {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, h.url, &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {Origin}}})
	if err != nil {
		h.t.Fatal(err)
	}
	conn.SetReadLimit(maxMessage)
	w := &window{t: h.t, conn: conn, frames: make(chan frame, 256)}
	go func() {
		for {
			_, data, err := conn.Read(context.Background())
			if err != nil {
				close(w.frames)
				return
			}
			var f frame
			json.Unmarshal(data, &f)
			w.frames <- f
		}
	}()
	h.t.Cleanup(func() { conn.Close(websocket.StatusNormalClosure, "") })
	return w
}

func (w *window) send(f frame) {
	w.t.Helper()
	data, _ := json.Marshal(f)
	if err := w.conn.Write(context.Background(), websocket.MessageText, data); err != nil {
		w.t.Fatal(err)
	}
}

func (w *window) rpc(session string, msg string) {
	w.send(frame{T: "rpc", Session: session, Msg: json.RawMessage(msg)})
}

// until waits for a frame the test is looking for, passing over the rest.
func (w *window) until(what string, match func(frame) bool) frame {
	w.t.Helper()
	deadline := time.After(15 * time.Second)
	for {
		select {
		case f, ok := <-w.frames:
			if !ok {
				w.t.Fatalf("connection closed waiting for %s", what)
			}
			if match(f) {
				return f
			}
		case <-deadline:
			w.t.Fatalf("no %s", what)
		}
	}
}

func (w *window) resolve(id int, file, folder string) string {
	w.t.Helper()
	w.send(frame{T: "resolve", ID: id, File: file, Folder: folder, Language: "fake"})
	f := w.until("resolved", func(f frame) bool { return f.T == "resolved" && f.ID == id })
	if f.Status == nil {
		w.t.Fatal("no server for the file")
	}
	return f.Status.Session
}

func (w *window) ready(session string) {
	w.t.Helper()
	w.until(session+" ready", func(f frame) bool {
		return f.T == "status" && f.Status.Session == session && f.Status.State == StateReady && len(f.Capabilities) > 0
	})
}

// sent is what the fake server in root was sent.
func sent(root string) []string {
	data, _ := os.ReadFile(filepath.Join(root, "fake.log"))
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

func count(lines []string, prefix string) int {
	n := 0
	for _, line := range lines {
		if strings.HasPrefix(line, prefix) {
			n++
		}
	}
	return n
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatalf("never: %s", what)
}

func TestTheEndpointWantsTheTokenAndTheEditorsOrigin(t *testing.T) {
	h := newHarness(t, Options{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	base, _, _ := strings.Cut(h.url, "?")
	for name, try := range map[string]struct {
		url    string
		origin string
	}{
		"no token":     {base, Origin},
		"wrong token":  {base + "?token=nope", Origin},
		"a web page":   {h.url, "https://example.com"},
		"no origin at": {h.url, ""},
	} {
		header := http.Header{}
		if try.origin != "" {
			header.Set("Origin", try.origin)
		}
		if conn, _, err := websocket.Dial(ctx, try.url, &websocket.DialOptions{HTTPHeader: header}); err == nil {
			conn.CloseNow()
			t.Errorf("%s: connected", name)
		}
	}
	if !strings.HasPrefix(base, "ws://127.0.0.1:") {
		t.Errorf("the endpoint must be on loopback: %s", base)
	}
}

func TestTwoWindowsShareOneServer(t *testing.T) {
	h := newHarness(t, Options{})
	root := project(t)
	file := filepath.Join(root, "a.fake")
	uri := fileURI(file)
	first, second := h.window(), h.window()
	session := first.resolve(1, file, root)
	if again := second.resolve(1, file, root); again != session {
		t.Fatalf("second window got %s, want %s", again, session)
	}
	first.ready(session)
	second.ready(session)

	open := fmt.Sprintf(`{"jsonrpc":"2.0","method":"textDocument/didOpen","params":{"textDocument":{"uri":%q,"languageId":"fake","version":1,"text":"x"}}}`, uri)
	first.rpc(session, open)
	second.rpc(session, open)
	// Both ask with id 7; each gets its own answer back under 7.
	hover := fmt.Sprintf(`{"jsonrpc":"2.0","id":7,"method":"textDocument/hover","params":{"textDocument":{"uri":%q},"position":{"line":0,"character":0}}}`, uri)
	first.rpc(session, hover)
	second.rpc(session, hover)
	for _, w := range []*window{first, second} {
		f := w.until("hover answer", func(f frame) bool { return f.T == "rpc" && strings.Contains(string(f.Msg), `"id":7`) })
		if !strings.Contains(string(f.Msg), "hover") {
			t.Errorf("answer = %s", f.Msg)
		}
	}
	lines := sent(root)
	if n := count(lines, "initialize "); n != 1 {
		t.Errorf("initialized %d times", n)
	}
	if n := count(lines, "textDocument/didOpen"); n != 1 {
		t.Errorf("the file was opened %d times; once is enough", n)
	}
	if n := count(lines, "textDocument/hover"); n != 2 {
		t.Errorf("%d hovers reached the server", n)
	}

	close := fmt.Sprintf(`{"jsonrpc":"2.0","method":"textDocument/didClose","params":{"textDocument":{"uri":%q}}}`, uri)
	first.rpc(session, close)
	time.Sleep(100 * time.Millisecond)
	if n := count(sent(root), "textDocument/didClose"); n != 0 {
		t.Error("closed while the other window still has it open")
	}
	second.conn.Close(websocket.StatusNormalClosure, "")
	eventually(t, "the file closes with the last window", func() bool { return count(sent(root), "textDocument/didClose") == 1 })
}

func TestAServerNobodyUsesStops(t *testing.T) {
	h := newHarness(t, Options{Idle: 150 * time.Millisecond})
	root := project(t)
	w := h.window()
	session := w.resolve(1, filepath.Join(root, "a.fake"), root)
	w.ready(session)
	w.send(frame{T: "release", Session: session})
	eventually(t, "the idle server is asked to shut down", func() bool {
		lines := sent(root)
		return count(lines, "shutdown") == 1 && count(lines, "exit") == 1
	})
	eventually(t, "the host forgets it", func() bool { return len(h.host.Statuses()) == 0 })
	// The next file starts it again.
	again := w.resolve(2, filepath.Join(root, "a.fake"), root)
	w.ready(again)
	if n := count(sent(root), "initialize "); n != 2 {
		t.Errorf("initialized %d times", n)
	}
}

func TestAServerInUseIsNotStoppedForBeingIdle(t *testing.T) {
	h := newHarness(t, Options{Idle: 100 * time.Millisecond})
	root := project(t)
	w := h.window()
	session := w.resolve(1, filepath.Join(root, "a.fake"), root)
	w.ready(session)
	time.Sleep(400 * time.Millisecond)
	if count(sent(root), "shutdown") != 0 {
		t.Error("stopped while a window uses it")
	}
}

func TestAFullHostMakesRoomWithTheServerUnusedLongest(t *testing.T) {
	h := newHarness(t, Options{MaxServers: 1})
	a, b := project(t), project(t)
	w := h.window()
	first := w.resolve(1, filepath.Join(a, "x.fake"), a)
	w.ready(first)
	w.send(frame{T: "release", Session: first})
	second := w.resolve(2, filepath.Join(b, "x.fake"), b)
	w.ready(second)
	eventually(t, "the unused server stops", func() bool { return count(sent(a), "shutdown") == 1 })
	if statuses := h.host.Statuses(); len(statuses) != 1 || statuses[0].Session != second {
		t.Errorf("running: %+v", statuses)
	}
}

func TestAServerThatStopsOnItsOwnStartsAgain(t *testing.T) {
	h := newHarness(t, Options{})
	root := project(t)
	w := h.window()
	session := w.resolve(1, filepath.Join(root, "a.fake"), root)
	w.ready(session)
	w.rpc(session, `{"jsonrpc":"2.0","method":"fake/crash"}`)
	w.until("restarting", func(f frame) bool { return f.T == "status" && f.Status.State == StateRestarting })
	w.ready(session)
	if n := count(sent(root), "initialize "); n != 2 {
		t.Errorf("initialized %d times", n)
	}
}

func TestAServerThatKeepsStoppingIsLeftForTheUser(t *testing.T) {
	h := newHarness(t, Options{})
	root := project(t)
	w := h.window()
	session := w.resolve(1, filepath.Join(root, "a.fake"), root)
	for range 4 {
		w.ready(session)
		w.rpc(session, `{"jsonrpc":"2.0","method":"fake/crash"}`)
	}
	f := w.until("failed", func(f frame) bool { return f.T == "status" && f.Status.State == StateFailed })
	if !strings.Contains(f.Status.Detail, "keeps stopping") {
		t.Errorf("detail = %q", f.Status.Detail)
	}
	w.send(frame{T: "restart", Session: session})
	w.ready(session)
}

func TestAMissingServerSaysWhatToDo(t *testing.T) {
	h := newHarness(t, Options{Find: func(*Server) string { return "" }})
	root := project(t)
	w := h.window()
	w.resolve(1, filepath.Join(root, "a.fake"), root)
	f := w.until("failed", func(f frame) bool { return f.T == "status" && f.Status.State == StateFailed })
	if f.Status.Detail != "Fake isn't installed" {
		t.Errorf("detail = %q", f.Status.Detail)
	}
	w.send(frame{T: "resolve", ID: 2, File: filepath.Join(root, "a.txt"), Folder: root, Language: "plaintext"})
	if r := w.until("resolved", func(f frame) bool { return f.T == "resolved" && f.ID == 2 }); r.Status != nil {
		t.Error("a language with no server resolved to one")
	}
}

func TestChangedFilesReachTheServerAsItAsked(t *testing.T) {
	h := newHarness(t, Options{})
	root := project(t)
	w := h.window()
	session := w.resolve(1, filepath.Join(root, "a.fake"), root)
	w.ready(session)
	eventually(t, "the watcher is registered", func() bool { return count(sent(root), "response \"watch\"") == 1 })
	w.send(frame{T: "files", Changes: []FileChange{
		{Path: filepath.Join(root, "pkg", "b.go"), Type: 2},
		{Path: filepath.Join(root, "notes.txt"), Type: 2},
		{Path: "/elsewhere/c.go", Type: 1},
	}})
	eventually(t, "the change is passed on", func() bool { return count(sent(root), "workspace/didChangeWatchedFiles") == 1 })
	for _, line := range sent(root) {
		if strings.HasPrefix(line, "workspace/didChangeWatchedFiles") {
			if !strings.Contains(line, "b.go") || strings.Contains(line, "notes.txt") || strings.Contains(line, "elsewhere") {
				t.Errorf("passed on: %s", line)
			}
		}
	}
}

func TestAServerCanAskTheWindowToEdit(t *testing.T) {
	h := newHarness(t, Options{})
	root := project(t)
	w := h.window()
	session := w.resolve(1, filepath.Join(root, "a.fake"), root)
	w.ready(session)
	w.rpc(session, `{"jsonrpc":"2.0","id":1,"method":"textDocument/hover","params":{}}`)
	w.until("hover", func(f frame) bool { return f.T == "rpc" && strings.Contains(string(f.Msg), "hover") })
	w.rpc(session, `{"jsonrpc":"2.0","method":"fake/edit"}`)
	f := w.until("applyEdit", func(f frame) bool { return f.T == "rpc" && strings.Contains(string(f.Msg), "workspace/applyEdit") })
	var msg message
	json.Unmarshal(f.Msg, &msg)
	w.rpc(session, fmt.Sprintf(`{"jsonrpc":"2.0","id":%s,"result":{"applied":true}}`, msg.ID))
	eventually(t, "the answer reaches the server", func() bool {
		return count(sent(root), `response "edit" {"applied":true}`) == 1
	})
}

func TestRequestsBeforeTheServerIsReadyAreAnswered(t *testing.T) {
	h := newHarness(t, Options{Find: func(*Server) string { return "" }})
	root := project(t)
	w := h.window()
	session := w.resolve(1, filepath.Join(root, "a.fake"), root)
	w.rpc(session, `{"jsonrpc":"2.0","id":3,"method":"textDocument/hover","params":{}}`)
	f := w.until("error", func(f frame) bool { return f.T == "rpc" && strings.Contains(string(f.Msg), `"id":3`) })
	if !strings.Contains(string(f.Msg), "isn't ready") {
		t.Errorf("answer = %s", f.Msg)
	}
}

func TestServersAreAnsweredWithTheirSettings(t *testing.T) {
	gopls := ServerFor("go")
	if settings, _ := gopls.setting("gopls").(map[string]any); settings["semanticTokens"] != true {
		t.Errorf("gopls isn't asked for semantic tokens: %v", settings)
	}
	typescript := ServerFor("typescript")
	if got := typescript.setting("typescript.inlayHints.parameterNames.enabled"); got != "literals" {
		t.Errorf("TypeScript's parameter hints = %v", got)
	}
	if got := typescript.setting("typescript.nothing.here"); got != nil {
		t.Errorf("a missing section = %v", got)
	}
	if got := (&Server{}).setting(""); got != nil {
		t.Errorf("no settings = %v", got)
	}
}

func TestTurningAServerOffStopsItAndLetsItsWindowsGo(t *testing.T) {
	h := newHarness(t, Options{})
	root := project(t)
	w := h.window()
	session := w.resolve(1, filepath.Join(root, "a.fake"), root)
	w.ready(session)
	h.host.Configure(false, nil, map[string]bool{"fake": true})
	w.until("stopped", func(f frame) bool { return f.T == "status" && f.Status.State == StateStopped })
	w.send(frame{T: "resolve", ID: 2, File: filepath.Join(root, "a.fake"), Folder: root, Language: "fake"})
	if f := w.until("resolved", func(f frame) bool { return f.T == "resolved" && f.ID == 2 }); f.Status != nil {
		t.Error("a server turned off resolved")
	}
	eventually(t, "it shuts down", func() bool { return count(sent(root), "exit") == 1 })
}

func TestAnotherProgramForAServerStartsItAgain(t *testing.T) {
	self, _ := os.Executable()
	h := newHarness(t, Options{})
	root := project(t)
	w := h.window()
	w.ready(w.resolve(1, filepath.Join(root, "a.fake"), root))
	h.host.Configure(false, map[string]string{"fake": self}, nil)
	w.until("stopped", func(f frame) bool { return f.T == "status" && f.Status.State == StateStopped })
	w.ready(w.resolve(2, filepath.Join(root, "a.fake"), root))
	if n := count(sent(root), "initialize "); n != 2 {
		t.Errorf("initialized %d times", n)
	}
	if _, where := h.host.Where(fakeServerSpec); where != "yours" {
		t.Errorf("runs from %q", where)
	}
}

func TestTheReportSaysWhereEachServerComesFrom(t *testing.T) {
	dir := t.TempDir()
	found := &Server{ID: "found", Name: "Found", Languages: []string{"found"}}
	downloadable := &Server{ID: "dl", Name: "DL", Languages: []string{"dl"}, Install: &Install{Version: "1", Release: map[string]Asset{}}}
	downloaded := &Server{ID: "have", Name: "Have", Languages: []string{"have"}, Install: &Install{Version: "1", Release: map[string]Asset{}}}
	missing := &Server{ID: "none", Name: "None", Languages: []string{"none"}}
	off := &Server{ID: "off", Name: "Off", Languages: []string{"off"}}
	os.MkdirAll(filepath.Join(dir, "have", "1"), 0o755)
	os.WriteFile(filepath.Join(dir, "have", "1", "have"), make([]byte, 1000), 0o755)
	os.WriteFile(filepath.Join(dir, "have", "1", okMarker), []byte("1\n"), 0o644)
	host := NewHost(Options{Dir: dir, Servers: []*Server{found, downloadable, downloaded, missing, off},
		Disabled: map[string]bool{"off": true},
		Find: func(s *Server) string {
			if s.ID == "found" {
				return "/usr/bin/true"
			}
			return ""
		}})
	defer host.Close()
	want := map[string]string{"found": "mac", "dl": "download", "have": "downloaded", "none": "missing", "off": "off"}
	for _, item := range host.Report().Servers {
		if item.Source != want[item.Server.ID] {
			t.Errorf("%s: %s, want %s", item.Server.ID, item.Source, want[item.Server.ID])
		}
		if item.Server.ID == "have" && item.Size < 1000 {
			t.Errorf("downloaded size = %d", item.Size)
		}
	}
	if err := host.Remove("have"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "have")); err == nil {
		t.Error("the download is still there")
	}
}
