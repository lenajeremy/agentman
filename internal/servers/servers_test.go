package servers

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/relay"
)

func TestParseLsofListeners(t *testing.T) {
	output := strings.Join([]string{
		"p621", "cControlCenter", "f9", "n*:7000", "f10", "n*:7000",
		"p788", "cnode", "f20", "n127.0.0.1:5173", "f21", "n[::1]:5173",
		"p900", "cpython3", "f3", "n192.168.1.20:8000",
		"p901", "cvite", "f4", "n[::1]:3000",
	}, "\n")
	got := parseLsofListeners(output)
	want := []Listener{
		{Port: 3000, PID: 901, Command: "vite", Loopback: true},
		{Port: 5173, PID: 788, Command: "node", Loopback: true},
		{Port: 7000, PID: 621, Command: "ControlCenter", Loopback: true},
		{Port: 8000, PID: 900, Command: "python3", Loopback: false},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
}

func TestParseProcAddress(t *testing.T) {
	cases := []struct {
		field    string
		loopback bool
		port     int
	}{
		{"0100007F:1F90", true, 8080},                          // 127.0.0.1
		{"00000000:0BB8", true, 3000},                          // 0.0.0.0
		{"1401A8C0:1F40", false, 8000},                         // 192.168.1.20
		{"00000000000000000000000001000000:1435", true, 5173},  // ::1
		{"00000000000000000000000000000000:0050", true, 80},    // ::
		{"0000000000000000FFFF00000100007F:0BB8", true, 3000},  // ::ffff:127.0.0.1
		{"B80D0120000000000000000001000000:0BB8", false, 3000}, // 2001:db8::1
	}
	for _, tc := range cases {
		loopback, port, ok := parseProcAddress(tc.field)
		if !ok || loopback != tc.loopback || port != tc.port {
			t.Errorf("%s: got (%v, %d, %v), want (%v, %d)", tc.field, loopback, port, ok, tc.loopback, tc.port)
		}
	}
}

// TestScanProc builds a miniature /proc: a socket table, one process holding
// a listening socket, and its working directory.
func TestScanProc(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "net/tcp"),
		"  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode\n"+
			"   0: 0100007F:1435 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1000        0 4242 1\n"+
			"   1: 0100007F:1F90 0100007F:D431 01 00000000:00000000 00:00000000 00000000  1000        0 9999 1\n")
	mustWrite(t, filepath.Join(root, "321/comm"), "node\n")
	if err := os.MkdirAll(filepath.Join(root, "321/fd"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("socket:[4242]", filepath.Join(root, "321/fd/7")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/home/dev/app", filepath.Join(root, "321/cwd")); err != nil {
		t.Fatal(err)
	}

	scan, err := scanProc(root)
	if err != nil {
		t.Fatal(err)
	}
	want := []Listener{{Port: 5173, PID: 321, Command: "node", Loopback: true}}
	if fmt.Sprint(scan.Listeners) != fmt.Sprint(want) {
		t.Fatalf("listeners %+v, want %+v (the established socket must be ignored)", scan.Listeners, want)
	}
	if scan.Cwds[321] != "/home/dev/app" {
		t.Fatalf("cwd = %q", scan.Cwds[321])
	}
}

type fakeTree map[int]int // child → parent

func (f fakeTree) OwnsPID(ancestor, pid int) bool {
	for range 12 {
		if pid == ancestor {
			return true
		}
		parent, ok := f[pid]
		if !ok {
			return false
		}
		pid = parent
	}
	return false
}

func TestAttribute(t *testing.T) {
	defer pinHome(t, "/Users/dev")()
	owners := []Owner{
		{SessionID: "claude:a", Cwd: "/Users/dev/app", PID: 100, LastActivity: 5},
		{SessionID: "codex:b", Cwd: "/Users/dev/app/web", LastActivity: 1},
		{SessionID: "opencode:c", Cwd: "/Users/dev/other", LastActivity: 9},
		{SessionID: "claude:home", Cwd: "/Users/dev", LastActivity: 99},
	}
	scan := Scan{
		Listeners: []Listener{
			{Port: 3000, PID: 101, Loopback: true},  // child of the claude agent
			{Port: 5173, PID: 200, Loopback: true},  // detached, in app/web
			{Port: 8080, PID: 300, Loopback: true},  // in other
			{Port: 9000, PID: 400, Loopback: true},  // in the home directory only
			{Port: 4000, PID: 100, Loopback: true},  // the agent's own socket
			{Port: 6000, PID: 999, Loopback: true},  // the daemon itself
			{Port: 50123, PID: 101, Loopback: true}, // a test's ephemeral port
			{Port: 7000, PID: 101, Loopback: false}, // bound to a LAN address
			{Port: 443, PID: 101, Loopback: true},   // privileged
		},
		Cwds: map[int]string{
			101: "/tmp/elsewhere", 200: "/Users/dev/app/web/src", 300: "/Users/dev/other",
			400: "/Users/dev",
		},
	}
	tree := fakeTree{101: 100, 100: 1}

	got := Attribute(owners, scan, tree, 999)
	ports := func(id string) string {
		var out []string
		for _, l := range got[id] {
			out = append(out, strconv.Itoa(l.Port))
		}
		return strings.Join(out, ",")
	}
	// Ancestry beats directory: 3000 runs from /tmp but descends from claude.
	if ports("claude:a") != "3000" {
		t.Errorf("claude:a = %q, want 3000", ports("claude:a"))
	}
	// The deepest containing directory wins over the shallower claude:a.
	if ports("codex:b") != "5173" {
		t.Errorf("codex:b = %q, want 5173", ports("codex:b"))
	}
	if ports("opencode:c") != "8080" {
		t.Errorf("opencode:c = %q, want 8080", ports("opencode:c"))
	}
	// A session rooted at home must not claim unrelated servers.
	if ports("claude:home") != "" {
		t.Errorf("claude:home = %q, want nothing", ports("claude:home"))
	}
}

func TestWatcherConfirmsBeforeShowingAndProbesOnce(t *testing.T) {
	scans := 0
	probes := 0
	w := &Watcher{
		scan: func(context.Context) (Scan, error) {
			scans++
			listeners := []Listener{{Port: 3000, PID: 10, Command: "node", Loopback: true}}
			if scans == 3 {
				listeners = append(listeners, Listener{Port: 8787, PID: 11, Command: "am", Loopback: true})
			}
			return Scan{Listeners: listeners, Cwds: map[int]string{10: "/src/app", 11: "/src/app"}}, nil
		},
		tree: func(context.Context) (Ancestry, error) { return nil, nil },
		probe: func(_ context.Context, port int) (string, bool) {
			probes++
			return "Vite App", true
		},
		now:       time.Now,
		sightings: map[listenerKey]int{},
		probes:    map[listenerKey]probeResult{},
	}
	w.Ignore(8787)
	owners := []Owner{{SessionID: "s", Cwd: "/src/app"}}

	first, _ := w.Update(context.Background(), owners)
	if len(first["s"]) != 0 {
		t.Fatalf("shown after one sighting: %+v", first)
	}
	second, _ := w.Update(context.Background(), owners)
	want := []protocol.Server{{Port: 3000, Command: "node", Title: "Vite App"}}
	if fmt.Sprint(second["s"]) != fmt.Sprint(want) {
		t.Fatalf("second scan = %+v, want %+v", second["s"], want)
	}
	third, _ := w.Update(context.Background(), owners)
	if fmt.Sprint(third["s"]) != fmt.Sprint(want) {
		t.Fatalf("ignored port leaked, or server dropped: %+v", third["s"])
	}
	if probes != 1 {
		t.Fatalf("probed %d times, want once while the server stays up", probes)
	}
}

func TestWatcherRetriesServersThatWereNotReady(t *testing.T) {
	now := time.Unix(1000, 0)
	ready := false
	w := &Watcher{
		scan: func(context.Context) (Scan, error) {
			return Scan{
				Listeners: []Listener{{Port: 3000, PID: 10, Loopback: true}},
				Cwds:      map[int]string{10: "/src/app"},
			}, nil
		},
		tree:      func(context.Context) (Ancestry, error) { return nil, nil },
		probe:     func(context.Context, int) (string, bool) { return "", ready },
		now:       func() time.Time { return now },
		sightings: map[listenerKey]int{},
		probes:    map[listenerKey]probeResult{},
	}
	owners := []Owner{{SessionID: "s", Cwd: "/src/app"}}
	for range 2 {
		got, _ := w.Update(context.Background(), owners)
		if len(got["s"]) != 0 {
			t.Fatal("a port that does not answer HTTP was shown")
		}
	}
	ready = true
	now = now.Add(negativeProbeTTL)
	got, _ := w.Update(context.Background(), owners)
	if len(got["s"]) != 1 {
		t.Fatal("a server that became ready was never probed again")
	}
}

func TestProbeReadsTitleAndRejectsNonHTTP(t *testing.T) {
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, "<!doctype html><html><head><TITLE>\n  Vite &amp; React\n</TITLE></head></html>")
	}))
	defer app.Close()
	title, ok := Probe(context.Background(), portOf(t, app.URL))
	if !ok || title != "Vite & React" {
		t.Fatalf("got (%q, %v)", title, ok)
	}

	// A listener that speaks something other than HTTP, like a database.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			_, _ = conn.Write([]byte("\x00\x00\x00\x08not http"))
			conn.Close()
		}
	}()
	if _, ok := Probe(context.Background(), listener.Addr().(*net.TCPAddr).Port); ok {
		t.Fatal("a non-HTTP listener was accepted as a web server")
	}
}

func TestSharerOpensAndClosesLinks(t *testing.T) {
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "from the agent's server")
	}))
	defer app.Close()
	port := portOf(t, app.URL)

	server := relay.NewServer("servers-test-secret-0123", "test", slog.New(slog.DiscardHandler), false)
	if err := server.EnablePreviews("http://localhost"); err != nil {
		t.Fatal(err)
	}
	relayHTTP := httptest.NewServer(server.Handler())
	defer relayHTTP.Close()

	sharer := NewSharer(relayHTTP.URL, "daemon-token-for-servers-test")
	defer sharer.CloseAll()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	link, err := sharer.Open(ctx, port)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := sharer.Open(ctx, port); err != nil || again != link {
		t.Fatalf("second Open = (%q, %v), want the same link %q", again, err, link)
	}
	if sharer.Links()[port] != link {
		t.Fatalf("Links() = %v", sharer.Links())
	}

	// A timeout, because a request that arrives while the link is registering
	// can be held rather than answered, and an untimed client then waits for the
	// package deadline to kill the test instead of failing.
	client := &http.Client{Timeout: 10 * time.Second}
	get := func() int {
		req, _ := http.NewRequest(http.MethodGet, relayHTTP.URL+"/", nil)
		req.Host = strings.TrimPrefix(link, "http://")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	// The relay registers a link after the sharer has been handed it, so the
	// first request can arrive before there is anything behind it. Wait for the
	// link to serve rather than assuming it already does.
	opened := time.Now().Add(10 * time.Second)
	status := get()
	for status != http.StatusOK && time.Now().Before(opened) {
		time.Sleep(25 * time.Millisecond)
		status = get()
	}
	if status != http.StatusOK {
		t.Fatalf("link returned %d", status)
	}

	// The server went away: Retain without it must take the link down.
	sharer.Retain(map[int]bool{})
	deadline := time.Now().Add(5 * time.Second)
	for get() != http.StatusNotFound {
		if time.Now().After(deadline) {
			t.Fatal("the link kept working after its server was gone")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(sharer.Links()) != 0 {
		t.Fatalf("Links() after Retain = %v", sharer.Links())
	}
}

func TestSharerExplainsRelayWithoutPreviews(t *testing.T) {
	server := relay.NewServer("servers-test-secret-0123", "test", slog.New(slog.DiscardHandler), false)
	relayHTTP := httptest.NewServer(server.Handler())
	defer relayHTTP.Close()

	sharer := NewSharer(relayHTTP.URL, "daemon-token-for-servers-test")
	defer sharer.CloseAll()
	_, err := sharer.Open(context.Background(), 3000)
	if err == nil || !strings.Contains(err.Error(), "does not offer preview links") {
		t.Fatalf("err = %v", err)
	}
}

func portOf(t *testing.T, rawURL string) int {
	t.Helper()
	_, portText, err := net.SplitHostPort(strings.TrimPrefix(rawURL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(portText)
	return port
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func pinHome(t *testing.T, home string) func() {
	t.Helper()
	previous := homeDir
	homeDir = func() string { return home }
	return func() { homeDir = previous }
}
