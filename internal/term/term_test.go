package term

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// serveCommand serves one terminal running argv and dials it.
func serveCommand(t *testing.T, size Size, argv ...string) (*websocket.Conn, context.Context) {
	t.Helper()
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		_ = Serve(r.Context(), c, exec.Command(argv[0], argv[1:]...), size)
	}))
	t.Cleanup(web.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(web.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.CloseNow() })
	return c, ctx
}

// readUntil collects output until it contains want, or fails.
func readUntil(t *testing.T, ctx context.Context, c *websocket.Conn, want string) string {
	t.Helper()
	var out strings.Builder
	for !strings.Contains(out.String(), want) {
		kind, data, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("waiting for %q, got %q then %v", want, out.String(), err)
		}
		if kind == websocket.MessageBinary {
			out.Write(data)
		}
	}
	return out.String()
}

func TestKeystrokesGoInAndOutputComesBack(t *testing.T) {
	c, ctx := serveCommand(t, Size{Cols: 80, Rows: 24}, "/bin/sh", "-c", `read line; echo "got:$line"`)
	if err := c.Write(ctx, websocket.MessageBinary, []byte("hello\r")); err != nil {
		t.Fatal(err)
	}
	readUntil(t, ctx, c, "got:hello")
	// The program ended, and the terminal says so by closing normally.
	for {
		if _, _, err := c.Read(ctx); err != nil {
			if websocket.CloseStatus(err) != websocket.StatusNormalClosure {
				t.Fatalf("close = %v, want a normal closure once the program exits", err)
			}
			return
		}
	}
}

func TestTheTerminalHasTheSizeItWasGivenAndFollowsResizes(t *testing.T) {
	c, ctx := serveCommand(t, Size{Cols: 101, Rows: 33}, "/bin/sh", "-c", `stty size; read _; stty size; sleep 1`)
	readUntil(t, ctx, c, "33 101")
	if err := c.Write(ctx, websocket.MessageText, []byte(`{"type":"resize","cols":120,"rows":40}`)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if err := c.Write(ctx, websocket.MessageBinary, []byte("\r")); err != nil {
		t.Fatal(err)
	}
	readUntil(t, ctx, c, "40 120")
}

// Closing the app's terminal stops the program in it.
func TestHangingUpStopsTheProgram(t *testing.T) {
	done := make(chan struct{})
	web := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		_ = Serve(context.Background(), c, exec.Command("/bin/sh", "-c", "echo ready; sleep 60"), Size{Cols: 80, Rows: 24})
		close(done)
	}))
	defer web.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(web.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	readUntil(t, ctx, c, "ready")
	_ = c.Close(websocket.StatusNormalClosure, "")
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the program was still running after the app hung up")
	}
}

func TestTheEnvironmentIsATerminalsOutsideTmux(t *testing.T) {
	env := Env([]string{"HOME=/h", "TMUX=/tmp/x,1,0", "TMUX_PANE=%1", "TERM=screen", "PATH=/bin"})
	joined := "\n" + strings.Join(env, "\n") + "\n"
	for _, gone := range []string{"TMUX=", "TMUX_PANE=", "TERM=screen"} {
		if strings.Contains(joined, "\n"+gone) {
			t.Errorf("%s is still in %v", gone, env)
		}
	}
	for _, want := range []string{"HOME=/h", "PATH=/bin", "TERM=xterm-256color"} {
		if !strings.Contains(joined, "\n"+want+"\n") {
			t.Errorf("%s is missing from %v", want, env)
		}
	}
}
