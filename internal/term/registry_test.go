package term

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// testRegistry is a registry whose shells are /bin/sh, in an environment of
// the test's own, served as /app/terminal serves it.
func testRegistry(t *testing.T) (*Registry, string, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	r := NewRegistry(ctx)
	r.ShellPath = "/bin/sh"
	home := t.TempDir()
	r.Environ = func() []string {
		return []string{"PATH=/usr/bin:/bin", "HOME=" + home, "PS1=$ ", "ENV=/dev/null", "HISTFILE=/dev/null",
			"AGENTMAN_HOME=/somewhere", "AGENTMAN_OPEN=agent=x", "TMUX=/tmp/x,1,0", "TMUX_PANE=%1", "TERM_PROGRAM=Apple_Terminal"}
	}
	t.Cleanup(r.StopAll)
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		q := req.URL.Query()
		if folder := q.Get("watch"); folder != "" {
			c, err := websocket.Accept(w, req, nil)
			if err != nil {
				return
			}
			r.Watch(ctx, c, folder)
			return
		}
		s := r.Get(q.Get("id"))
		if s == nil {
			http.Error(w, "no such terminal", http.StatusNotFound)
			return
		}
		c, err := websocket.Accept(w, req, nil)
		if err != nil {
			return
		}
		r.Attach(ctx, c, s, Size{Cols: 100, Rows: 30})
	}))
	t.Cleanup(web.Close)
	return r, "ws" + strings.TrimPrefix(web.URL, "http"), ctx
}

func dial(t *testing.T, ctx context.Context, base string, query url.Values) *websocket.Conn {
	t.Helper()
	c, _, err := websocket.Dial(ctx, base+"?"+query.Encode(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.CloseNow() })
	return c
}

func attach(t *testing.T, ctx context.Context, base, id string) *websocket.Conn {
	return dial(t, ctx, base, url.Values{"id": {id}})
}

func send(t *testing.T, ctx context.Context, c *websocket.Conn, text string) {
	t.Helper()
	if err := c.Write(ctx, websocket.MessageBinary, []byte(text)); err != nil {
		t.Fatal(err)
	}
}

func ask(t *testing.T, ctx context.Context, c *websocket.Conn, cmd any) {
	t.Helper()
	data, _ := json.Marshal(cmd)
	if err := c.Write(ctx, websocket.MessageText, data); err != nil {
		t.Fatal(err)
	}
}

// nextState waits for a state the test accepts.
func nextState(t *testing.T, ctx context.Context, c *websocket.Conn, ok func(State) bool) State {
	t.Helper()
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("waiting for a state: %v", err)
		}
		var state State
		if json.Unmarshal(data, &state) == nil && state.Type == "state" && ok(state) {
			return state
		}
	}
}

func titles(state State) []string {
	var out []string
	for _, s := range state.Shells {
		out = append(out, s.Title)
	}
	return out
}

func TestANewShellIsInFrontAndNumbered(t *testing.T) {
	r, base, ctx := testRegistry(t)
	folder := t.TempDir()
	w := dial(t, ctx, base, url.Values{"watch": {folder}})
	nextState(t, ctx, w, func(s State) bool { return len(s.Shells) == 0 && s.Active == "" })
	ask(t, ctx, w, map[string]string{"type": "new"})
	first := nextState(t, ctx, w, func(s State) bool { return len(s.Shells) == 1 })
	if first.Shells[0].Title != "sh" || first.Active != first.Shells[0].ID {
		t.Fatalf("first shell = %+v", first)
	}
	ask(t, ctx, w, map[string]string{"type": "new"})
	second := nextState(t, ctx, w, func(s State) bool { return len(s.Shells) == 2 })
	if !reflect.DeepEqual(titles(second), []string{"sh", "sh 2"}) || second.Active != second.Shells[1].ID {
		t.Fatalf("second = %+v", second)
	}
	if r.Get(second.Shells[1].ID) == nil {
		t.Fatal("the registry doesn't know the shell")
	}
}

func TestEnsureMakesAShellOnlyOnce(t *testing.T) {
	r, _, _ := testRegistry(t)
	folder := t.TempDir()
	r.Ensure(folder)
	r.Ensure(folder)
	if n := len(r.State(folder).Shells); n != 1 {
		t.Fatalf("%d shells, want 1", n)
	}
}

func TestOutputComesBackAndIsReplayedToALaterPanel(t *testing.T) {
	r, base, ctx := testRegistry(t)
	s := r.New(t.TempDir())
	c := attach(t, ctx, base, s.ID)
	send(t, ctx, c, "echo replay-$((40+2))\r")
	readUntil(t, ctx, c, "replay-42")
	_ = c.Close(websocket.StatusNormalClosure, "")

	// The shell ran on without a panel, and a new one sees what it said.
	later := attach(t, ctx, base, s.ID)
	_, first, err := later.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(first), "replay-42") {
		t.Fatalf("the first message %q doesn't hold the scrollback", first)
	}
	send(t, ctx, later, "echo still-$((1+1))\r")
	readUntil(t, ctx, later, "still-2")
}

func TestTwoPanelsShowTheSameShell(t *testing.T) {
	r, base, ctx := testRegistry(t)
	s := r.New(t.TempDir())
	one := attach(t, ctx, base, s.ID)
	two := attach(t, ctx, base, s.ID)
	time.Sleep(100 * time.Millisecond)
	send(t, ctx, one, "echo both-$((2+3))\r")
	readUntil(t, ctx, one, "both-5")
	readUntil(t, ctx, two, "both-5")
}

func TestTheShellsEnvironmentIsATerminalsOfItsOwn(t *testing.T) {
	r, base, ctx := testRegistry(t)
	s := r.New(t.TempDir())
	c := attach(t, ctx, base, s.ID)
	send(t, ctx, c, `echo "env:$TERM_PROGRAM:$TERM:${AGENTMAN_HOME:-none}:${AGENTMAN_OPEN:-none}:${TMUX:-none}:${TMUX_PANE:-none}:$LANG"`+"\r")
	readUntil(t, ctx, c, "env:Agentman:xterm-256color:none:none:none:none:en_US.UTF-8")
}

func TestShellEnv(t *testing.T) {
	env := ShellEnv([]string{"HOME=/h", "LANG=fr_FR.UTF-8", "AGENTMAN_SOCKET=/s", "TERM_PROGRAM_VERSION=1", "COLORTERM=no"})
	joined := "\n" + strings.Join(env, "\n") + "\n"
	for _, want := range []string{"HOME=/h", "LANG=fr_FR.UTF-8", "TERM=xterm-256color", "COLORTERM=truecolor", "TERM_PROGRAM=Agentman"} {
		if !strings.Contains(joined, "\n"+want+"\n") {
			t.Errorf("%s is missing from %v", want, env)
		}
	}
	for _, gone := range []string{"AGENTMAN_SOCKET=", "TERM_PROGRAM_VERSION=", "COLORTERM=no", "LANG=en_US"} {
		if strings.Contains(joined, "\n"+gone) {
			t.Errorf("%s is still in %v", gone, env)
		}
	}
}

func TestAShellStartsAsALoginShellInItsFolder(t *testing.T) {
	r, _, _ := testRegistry(t)
	folder := t.TempDir()
	r.mu.Lock()
	s := r.add(folder, nil, "", "")
	agent := r.add(folder, []string{"tmux", "attach-session", "-t", "=it's"}, "demo", "claude:1")
	r.mu.Unlock()
	cmd := r.command(s)
	if cmd.Path != "/bin/sh" || !reflect.DeepEqual(cmd.Args, []string{"-sh"}) || cmd.Dir != folder {
		t.Fatalf("shell command = %s %v in %s", cmd.Path, cmd.Args, cmd.Dir)
	}
	want := []string{"-sh", "-c", `exec 'tmux' 'attach-session' '-t' '=it'\''s'`}
	if got := r.command(agent).Args; !reflect.DeepEqual(got, want) {
		t.Fatalf("agent command = %q, want %q", got, want)
	}
}

func TestTheEndOfAShellIsSaidAndItRestarts(t *testing.T) {
	r, base, ctx := testRegistry(t)
	folder := t.TempDir()
	w := dial(t, ctx, base, url.Values{"watch": {folder}})
	s := r.New(folder)
	c := attach(t, ctx, base, s.ID)
	send(t, ctx, c, "exit 3\r")
	readUntil(t, ctx, c, "[The shell ended with code 3. Restart it from the panel.]")
	nextState(t, ctx, w, func(st State) bool { return len(st.Shells) == 1 && st.Shells[0].Exited && st.Shells[0].Code == 3 })

	ask(t, ctx, w, map[string]string{"type": "restart", "id": s.ID})
	readUntil(t, ctx, c, clearScreen)
	nextState(t, ctx, w, func(st State) bool { return len(st.Shells) == 1 && !st.Shells[0].Exited })
	send(t, ctx, c, "echo again-$((3+4))\r")
	readUntil(t, ctx, c, "again-7")
	send(t, ctx, c, "exit\r")
	readUntil(t, ctx, c, "[The shell ended. Restart it from the panel.]")
}

func TestClearEmptiesTheScrollback(t *testing.T) {
	r, base, ctx := testRegistry(t)
	s := r.New(t.TempDir())
	c := attach(t, ctx, base, s.ID)
	send(t, ctx, c, "echo before-$((5+5))\r")
	readUntil(t, ctx, c, "before-10")
	ask(t, ctx, c, map[string]string{"type": "clear"})
	readUntil(t, ctx, c, clearScreen)
	time.Sleep(200 * time.Millisecond)
	s.mu.Lock()
	ring := string(s.ring)
	s.mu.Unlock()
	if strings.Contains(ring, "before-10") || !strings.HasPrefix(ring, clearScreen) {
		t.Fatalf("scrollback after clear = %q", ring)
	}
}

func TestATitleSetByAProgramNamesTheTab(t *testing.T) {
	r, base, ctx := testRegistry(t)
	folder := t.TempDir()
	w := dial(t, ctx, base, url.Values{"watch": {folder}})
	s := r.New(folder)
	c := attach(t, ctx, base, s.ID)
	send(t, ctx, c, `printf '\033]0;vim notes.md\007'`+"\r")
	nextState(t, ctx, w, func(st State) bool { return len(st.Shells) == 1 && st.Shells[0].Title == "vim notes.md" })
}

func TestTitlesSplitAcrossReads(t *testing.T) {
	var p oscParser
	parts := []string{"out\x1b", "]2", ";  my ti", "tle \x1b", "\\more"}
	got := ""
	for _, part := range parts {
		if title, ok := p.feed([]byte(part)); ok {
			got = title
		}
	}
	if got != "my title" {
		t.Fatalf("title = %q", got)
	}
	// Other sequences and empty titles name nothing.
	for _, seq := range []string{"\x1b]7;file:///tmp\x07", "\x1b]0;   \x07", "\x1b[31mred"} {
		if title, ok := p.feed([]byte(seq)); ok {
			t.Errorf("%q gave the title %q", seq, title)
		}
	}
	if title, ok := p.feed([]byte("\x1b]0;zsh\x07")); !ok || title != "zsh" {
		t.Errorf("BEL-ended title = %q, %v", title, ok)
	}
}

func TestClosingFollowsTheEditorsRules(t *testing.T) {
	r, _, _ := testRegistry(t)
	folder := t.TempDir()
	a := r.New(folder)
	b := r.New(folder)
	c := r.New(folder)
	// In front: c, the last made. A split puts d beside it.
	r.Split(folder)
	st := r.State(folder)
	d := st.Shells[3].ID
	if st.Active != c.ID || st.Splits[c.ID] != d {
		t.Fatalf("after split: %+v", st)
	}
	// A second split of the same tab does nothing.
	r.Split(folder)
	if n := len(r.State(folder).Shells); n != 4 {
		t.Fatalf("%d shells after a second split", n)
	}
	// Closing the right half leaves the tab whole.
	r.Close(folder, d)
	st = r.State(folder)
	if len(st.Splits) != 0 || st.Active != c.ID || len(st.Shells) != 3 {
		t.Fatalf("after closing the right half: %+v", st)
	}
	// Closing a left half in front puts its right half in front.
	r.Split(folder)
	e := r.State(folder).Shells[3].ID
	r.Close(folder, c.ID)
	st = r.State(folder)
	if st.Active != e || len(st.Splits) != 0 {
		t.Fatalf("after closing the left half: %+v", st)
	}
	// Closing the one in front brings the tab in its place.
	r.Activate(folder, a.ID)
	r.Close(folder, a.ID)
	st = r.State(folder)
	if st.Active != b.ID || !reflect.DeepEqual(ids(st), []string{b.ID, e}) {
		t.Fatalf("after closing the first: %+v", st)
	}
	// The last tab closed leaves the one before it in front.
	r.Activate(folder, e)
	r.Close(folder, e)
	if st = r.State(folder); st.Active != b.ID {
		t.Fatalf("after closing the last: %+v", st)
	}
	r.Close(folder, b.ID)
	if st = r.State(folder); len(st.Shells) != 0 || st.Active != "" {
		t.Fatalf("after closing everything: %+v", st)
	}
}

func TestUnsplitClosesTheRightHalf(t *testing.T) {
	r, _, _ := testRegistry(t)
	folder := t.TempDir()
	left := r.New(folder)
	r.Split(folder)
	r.Unsplit(folder)
	st := r.State(folder)
	if !reflect.DeepEqual(ids(st), []string{left.ID}) || len(st.Splits) != 0 || st.Active != left.ID {
		t.Fatalf("after unsplit: %+v", st)
	}
}

func ids(st State) []string {
	var out []string
	for _, s := range st.Shells {
		out = append(out, s.ID)
	}
	return out
}

func TestTheAgentsTerminalIsReusedWhileItRuns(t *testing.T) {
	r, base, ctx := testRegistry(t)
	var found atomic.Bool
	r.AgentArgv = func(_ context.Context, session string) ([]string, error) {
		if !found.Load() || session != "claude:1" {
			return nil, ErrNoAgentTerminal
		}
		return []string{"/bin/sleep", "30"}, nil
	}
	folder := t.TempDir()
	w := dial(t, ctx, base, url.Values{"watch": {folder}})
	nextState(t, ctx, w, func(State) bool { return true })
	// Until its pane is found, there is no terminal to show. to find the agent's pane, there is no terminal to show.
	ask(t, ctx, w, map[string]string{"type": "agent", "session": "claude:1", "title": "demo"})
	for {
		_, data, err := w.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var reply map[string]any
		_ = json.Unmarshal(data, &reply)
		if reply["type"] == "error" {
			if reply["error"] != ErrNoAgentTerminal.Error() {
				t.Fatalf("error = %v", reply["error"])
			}
			break
		}
	}
	found.Store(true)
	r.New(folder)
	ask(t, ctx, w, map[string]string{"type": "agent", "session": "claude:1", "title": "demo"})
	st := nextState(t, ctx, w, func(s State) bool { return len(s.Shells) == 2 })
	agent := st.Shells[1]
	if agent.Title != "demo" || agent.Session != "claude:1" || st.Active != agent.ID {
		t.Fatalf("agent tab = %+v in %+v", agent, st)
	}
	r.Activate(folder, st.Shells[0].ID)
	ask(t, ctx, w, map[string]string{"type": "agent", "session": "claude:1", "title": "demo"})
	st = nextState(t, ctx, w, func(s State) bool { return s.Active == agent.ID })
	if len(st.Shells) != 2 {
		t.Fatalf("a second agent tab: %+v", st)
	}
}

func TestShellsEndAfterTheAppsLastConnectionCloses(t *testing.T) {
	r, base, ctx := testRegistry(t)
	r.Linger = 150 * time.Millisecond
	folder := t.TempDir()
	r.Hold()
	s := r.New(folder)
	c := attach(t, ctx, base, s.ID)
	send(t, ctx, c, "echo up-$((1+1))\r")
	readUntil(t, ctx, c, "up-2")

	// A connection opening again in time keeps them.
	r.Release()
	r.Hold()
	time.Sleep(300 * time.Millisecond)
	if r.Get(s.ID) == nil {
		t.Fatal("the shell ended while the app was connected")
	}

	r.Release()
	deadline := time.Now().Add(5 * time.Second)
	for r.Get(s.ID) != nil {
		if time.Now().After(deadline) {
			t.Fatal("the shell outlived the app")
		}
		time.Sleep(20 * time.Millisecond)
	}
	s.mu.Lock()
	p := s.proc
	s.mu.Unlock()
	select {
	case <-p.done:
	case <-time.After(5 * time.Second):
		t.Fatal("the shell's process is still running")
	}
	// Its panel was let go.
	for {
		if _, _, err := c.Read(ctx); err != nil {
			if websocket.CloseStatus(err) != websocket.StatusNormalClosure {
				t.Fatalf("panel closed with %v", err)
			}
			break
		}
	}
}

func TestTheScrollbackKeepsTheEndAndWholeCharacters(t *testing.T) {
	s := &PanelShell{viewers: map[*viewer]struct{}{}}
	s.outputLocked([]byte("é"))
	s.outputLocked([]byte(strings.Repeat("é", Scrollback/2)))
	if len(s.ring) > Scrollback {
		t.Fatalf("scrollback is %d bytes", len(s.ring))
	}
	if s.ring[0] != "é"[0] {
		t.Fatalf("scrollback starts mid-character: % x", s.ring[:4])
	}
}

func TestPasswdShell(t *testing.T) {
	passwd := "root:x:0:0:root:/root:/bin/bash\nme:x:1000:1000::/home/me:/usr/bin/fish\n"
	if got := passwdShell(passwd, "1000"); got != "/usr/bin/fish" {
		t.Fatalf("shell = %q", got)
	}
	if got := passwdShell(passwd, "7"); got != "" {
		t.Fatalf("shell for nobody = %q", got)
	}
}

func TestTheLoginShellIsAProgram(t *testing.T) {
	if shell := LoginShell(); !executable(shell) {
		t.Fatalf("login shell %q isn't a program", shell)
	}
}
