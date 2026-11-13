package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/lenajeremy/agentman/internal/daemon"
	"github.com/lenajeremy/agentman/internal/term"
)

// testGateway is the app gateway with its routes, its panels' shells /bin/sh
// in a home of the test's own (never the user's shell or its tmux).
func testGateway(t *testing.T) (*appGateway, string, context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	g := newAppGateway("tok")
	g.serve(ctx, daemon.NewLocalServer(filepath.Join(t.TempDir(), "am.sock")), func(string) (string, bool) { return "", false })
	shells := g.ready.Load().shells
	shells.ShellPath = "/bin/sh"
	home := t.TempDir()
	shells.Environ = func() []string {
		return []string{"PATH=/usr/bin:/bin", "HOME=" + home, "ENV=/dev/null", "HISTFILE=/dev/null"}
	}
	t.Cleanup(shells.StopAll)
	mux := http.NewServeMux()
	mux.HandleFunc("/app", g.protocol)
	mux.HandleFunc("/app/terminal", g.terminal)
	web := httptest.NewServer(mux)
	t.Cleanup(web.Close)
	return g, web.URL, ctx
}

func terminalURL(base string, query url.Values) string {
	query.Set("token", "tok")
	return base + "/app/terminal?" + query.Encode()
}

func TestTerminalRoutesRefuseWhatIsntThere(t *testing.T) {
	_, base, _ := testGateway(t)
	for _, query := range []url.Values{
		{"watch": {"relative/folder"}},
		{"watch": {filepath.Join(t.TempDir(), "gone")}},
		{"id": {"t999"}},
	} {
		res, err := http.Get(terminalURL(base, query))
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != http.StatusNotFound {
			t.Errorf("%v: %d %s, want 404", query, res.StatusCode, body)
		}
	}
	res, err := http.Get(base + "/app/terminal?watch=" + url.QueryEscape(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("without the token: %d, want 403", res.StatusCode)
	}
}

func TestAPanelMakesAShellAndShowsIt(t *testing.T) {
	_, base, ctx := testGateway(t)
	ws := "ws" + strings.TrimPrefix(base, "http")
	watch, _, err := websocket.Dial(ctx, terminalURL(ws, url.Values{"watch": {t.TempDir()}}), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer watch.CloseNow()
	if err := watch.Write(ctx, websocket.MessageText, []byte(`{"type":"new"}`)); err != nil {
		t.Fatal(err)
	}
	var state term.State
	for len(state.Shells) == 0 {
		_, data, err := watch.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_ = json.Unmarshal(data, &state)
	}
	if state.Shells[0].Title != "sh" || state.Active != state.Shells[0].ID {
		t.Fatalf("state = %+v", state)
	}
	shell, _, err := websocket.Dial(ctx, terminalURL(ws, url.Values{"id": {state.Active}, "cols": {"90"}, "rows": {"20"}}), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer shell.CloseNow()
	if err := shell.Write(ctx, websocket.MessageBinary, []byte("stty size; echo $TERM_PROGRAM\r")); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	for !strings.Contains(out.String(), "20 90") || !strings.Contains(out.String(), "Agentman") {
		_, data, err := shell.Read(ctx)
		if err != nil {
			t.Fatalf("got %q then %v", out.String(), err)
		}
		out.Write(data)
	}
}

func TestShellsEndAfterTheAppGoes(t *testing.T) {
	g, base, _ := testGateway(t)
	shells := g.ready.Load().shells
	shells.Linger = 100 * time.Millisecond
	shell := shells.New(t.TempDir())
	// An app connection opens and closes (this daemon answers 503, as one
	// still starting does), and its shells go a little after.
	res, err := http.Get(base + "/app?token=tok")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	deadline := time.Now().Add(5 * time.Second)
	for shells.Get(shell.ID) != nil {
		if time.Now().After(deadline) {
			t.Fatal("the shell outlived the app")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
