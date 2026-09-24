package relay

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/lenajeremy/agentman/internal/tunnel"
)

const testDaemonToken = "daemon-token-for-tunnel-tests"

// newPreviewRelay starts a relay with links under http://localhost, the one
// plaintext origin allowed. Requests reach a link by setting Host, since the
// test relay itself listens on 127.0.0.1.
func newPreviewRelay(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	server, httpServer := newTestServer(t)
	if err := server.EnablePreviews("http://localhost"); err != nil {
		t.Fatal(err)
	}
	return server, httpServer
}

// startTunnel runs an `am expose` client for port and waits for its link.
func startTunnel(t *testing.T, relayURL string, port int, nonce string) (tunnel.Hello, *tunnel.Client) {
	t.Helper()
	ready := make(chan tunnel.Hello, 4)
	client := &tunnel.Client{
		RelayURL: relayURL,
		Token:    testDaemonToken,
		Port:     port,
		Nonce:    nonce,
		OnReady:  func(h tunnel.Hello) { ready <- h },
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- client.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	select {
	case hello := <-ready:
		return hello, client
	case err := <-done:
		t.Fatalf("tunnel exited before it was ready: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("tunnel never became ready")
	}
	return tunnel.Hello{}, nil
}

func portOf(t *testing.T, rawURL string) int {
	t.Helper()
	_, portText, err := net.SplitHostPort(strings.TrimPrefix(rawURL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	return port
}

// linkRequest builds a request to the test relay addressed to a link.
func linkRequest(t *testing.T, method, relayURL, id, path string, body io.Reader) *http.Request {
	t.Helper()
	req, err := http.NewRequest(method, relayURL+path, body)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = id + ".localhost"
	return req
}

func TestPreviewProxiesToTheSharedPort(t *testing.T) {
	type seen struct{ host, forwardedHost, forwardedProto string }
	requests := make(chan seen, 1)
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- seen{r.Host, r.Header.Get("X-Forwarded-Host"), r.Header.Get("X-Forwarded-Proto")}
		fmt.Fprintf(w, "hello from %s", r.URL.Path)
	}))
	defer app.Close()
	appPort := portOf(t, app.URL)

	_, relay := newPreviewRelay(t)
	hello, _ := startTunnel(t, relay.URL, appPort, "nonce-proxies-to-port")
	if !validTunnelID(hello.ID) || hello.URL != "http://"+hello.ID+".localhost" {
		t.Fatalf("unexpected hello %+v", hello)
	}

	resp, err := http.DefaultClient.Do(linkRequest(t, http.MethodGet, relay.URL, hello.ID, "/some/page", nil))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "hello from /some/page" {
		t.Fatalf("got %d %q", resp.StatusCode, body)
	}
	if got := resp.Header.Get("X-Robots-Tag"); !strings.Contains(got, "noindex") {
		t.Fatalf("X-Robots-Tag = %q, want noindex", got)
	}

	got := <-requests
	// The app must see the name it listens under, or dev servers that check
	// Host (Vite, Django) reject the request.
	if want := "localhost:" + strconv.Itoa(appPort); got.host != want {
		t.Fatalf("app saw Host %q, want %q", got.host, want)
	}
	if got.forwardedHost != hello.ID+".localhost" || got.forwardedProto != "http" {
		t.Fatalf("forwarding headers = %+v", got)
	}
}

func TestPreviewStreamsLargeBodiesBothWays(t *testing.T) {
	// Larger than yamux's 256 KiB window many times over, so this only
	// passes if flow control keeps the stream moving.
	payload := make([]byte, 3<<20)
	if _, err := rand.Read(payload); err != nil {
		t.Fatal(err)
	}
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(w, r.Body)
	}))
	defer app.Close()

	_, relay := newPreviewRelay(t)
	hello, _ := startTunnel(t, relay.URL, portOf(t, app.URL), "nonce-large-bodies-x")

	resp, err := http.DefaultClient.Do(
		linkRequest(t, http.MethodPost, relay.URL, hello.ID, "/echo", bytes.NewReader(payload)))
	if err != nil {
		t.Fatal(err)
	}
	echoed, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(echoed, payload) {
		t.Fatalf("echoed %d bytes, want the same %d bytes back", len(echoed), len(payload))
	}
}

func TestPreviewHandlesConcurrentRequests(t *testing.T) {
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Hold each request briefly so they genuinely overlap on the tunnel.
		time.Sleep(20 * time.Millisecond)
		_, _ = io.WriteString(w, r.URL.Query().Get("n"))
	}))
	defer app.Close()

	_, relay := newPreviewRelay(t)
	hello, _ := startTunnel(t, relay.URL, portOf(t, app.URL), "nonce-concurrent-reqs")

	var wg sync.WaitGroup
	errs := make(chan error, 40)
	for i := range 40 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			want := strconv.Itoa(i)
			resp, err := http.DefaultClient.Do(
				linkRequest(t, http.MethodGet, relay.URL, hello.ID, "/?n="+want, nil))
			if err != nil {
				errs <- err
				return
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if string(body) != want {
				errs <- fmt.Errorf("request %s got %q", want, body)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestPreviewCarriesWebsocketUpgrades(t *testing.T) {
	// Dev-server hot reload runs over a websocket from the page back to the
	// server, so upgrades have to pass through the tunnel intact.
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer ws.CloseNow()
		kind, data, err := ws.Read(r.Context())
		if err != nil {
			return
		}
		_ = ws.Write(r.Context(), kind, append([]byte("echo: "), data...))
	}))
	defer app.Close()

	_, relay := newPreviewRelay(t)
	hello, _ := startTunnel(t, relay.URL, portOf(t, app.URL), "nonce-websocket-hmr")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, wsAddr(relay.URL)+"/hmr", &websocket.DialOptions{
		Host: hello.ID + ".localhost",
	})
	if err != nil {
		t.Fatalf("dial through link: %v", err)
	}
	defer ws.CloseNow()
	if err := ws.Write(ctx, websocket.MessageText, []byte("update")); err != nil {
		t.Fatal(err)
	}
	_, reply, err := ws.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if string(reply) != "echo: update" {
		t.Fatalf("reply = %q", reply)
	}
}

func TestPreviewExplainsWhenNothingIsListening(t *testing.T) {
	// Reserve a port, then free it, so nothing is listening there.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()

	_, relay := newPreviewRelay(t)
	hello, _ := startTunnel(t, relay.URL, port, "nonce-nothing-listens")

	resp, err := http.DefaultClient.Do(linkRequest(t, http.MethodGet, relay.URL, hello.ID, "/", nil))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway ||
		!strings.Contains(string(body), fmt.Sprintf("localhost:%d", port)) {
		t.Fatalf("got %d %q, want a 502 naming the port", resp.StatusCode, body)
	}
}

func TestPreviewReachesIPv6OnlyServers(t *testing.T) {
	listener, err := net.Listen("tcp", "[::1]:0")
	if err != nil {
		t.Skip("IPv6 loopback unavailable:", err)
	}
	app := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "v6")
	}))
	app.Listener = listener
	app.Start()
	defer app.Close()

	_, relay := newPreviewRelay(t)
	hello, _ := startTunnel(t, relay.URL, listener.Addr().(*net.TCPAddr).Port, "nonce-ipv6-only-app")

	resp, err := http.DefaultClient.Do(linkRequest(t, http.MethodGet, relay.URL, hello.ID, "/", nil))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "v6" {
		t.Fatalf("got %d %q", resp.StatusCode, body)
	}
}

func TestPreviewRewritesRedirectsToLocalhost(t *testing.T) {
	var appPort int
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, fmt.Sprintf("http://localhost:%d/next?x=1", appPort), http.StatusFound)
	}))
	defer app.Close()
	appPort = portOf(t, app.URL)

	_, relay := newPreviewRelay(t)
	hello, _ := startTunnel(t, relay.URL, appPort, "nonce-redirect-rewrite")

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Do(linkRequest(t, http.MethodGet, relay.URL, hello.ID, "/", nil))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got, want := resp.Header.Get("Location"), hello.URL+"/next?x=1"; got != want {
		t.Fatalf("Location = %q, want %q", got, want)
	}
}

func TestPreviewUnknownOrMalformedLinks(t *testing.T) {
	_, relay := newPreviewRelay(t)
	unknown := deriveTunnelID(testSecret, "nobody", "nonce-never-connected")
	for _, host := range []string{unknown + ".localhost", "not-a-link.localhost"} {
		req, _ := http.NewRequest(http.MethodGet, relay.URL+"/health", nil)
		req.Host = host
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		// A link host never falls through to the relay's own API.
		if resp.StatusCode != http.StatusNotFound {
			t.Fatalf("%s: status %d, want 404", host, resp.StatusCode)
		}
	}

	// The relay's own hostname is untouched.
	resp, err := http.Get(relay.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/health status %d", resp.StatusCode)
	}
}

func TestTunnelRefusalsArePermanent(t *testing.T) {
	_, withoutPreviews := newTestServer(t)
	_, withPreviews := newPreviewRelay(t)

	cases := []struct {
		name   string
		relay  string
		token  string
		status int
	}{
		{"previews disabled", withoutPreviews.URL, testDaemonToken, http.StatusNotFound},
		{"bad token", withPreviews.URL, " ", 0}, // rejected before dialing
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := &tunnel.Client{RelayURL: tc.relay, Token: tc.token, Port: 3000}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			err := client.Run(ctx)
			if err == nil {
				t.Fatal("Run returned nil, want a refusal")
			}
			var permanent *tunnel.PermanentError
			if tc.status != 0 && (!errors.As(err, &permanent) || permanent.Status != tc.status) {
				t.Fatalf("err = %v, want PermanentError %d", err, tc.status)
			}
		})
	}
}

func TestTunnelCapPerAccount(t *testing.T) {
	_, relay := newPreviewRelay(t)
	for i := range maxTunnelsPerAccount {
		startTunnel(t, relay.URL, 3000+i, fmt.Sprintf("nonce-account-cap-%02d", i))
	}
	client := &tunnel.Client{
		RelayURL: relay.URL, Token: testDaemonToken, Port: 4000, Nonce: "nonce-account-cap-over",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var permanent *tunnel.PermanentError
	if err := client.Run(ctx); !errors.As(err, &permanent) || permanent.Status != http.StatusTooManyRequests {
		t.Fatalf("err = %v, want a 429 refusal", err)
	}
}

func TestReconnectingTunnelKeepsItsLinkAndReplacesTheOld(t *testing.T) {
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "first")
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "second")
	}))
	defer second.Close()

	_, relay := newPreviewRelay(t)
	const nonce = "nonce-same-process-x"
	a, _ := startTunnel(t, relay.URL, portOf(t, first.URL), nonce)
	// A second connection with the same nonce is what a reconnect looks like.
	b, _ := startTunnel(t, relay.URL, portOf(t, second.URL), nonce)
	if a.URL != b.URL {
		t.Fatalf("link changed across reconnect: %s -> %s", a.URL, b.URL)
	}

	resp, err := http.DefaultClient.Do(linkRequest(t, http.MethodGet, relay.URL, b.ID, "/", nil))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "second" {
		t.Fatalf("got %q, want the newest connection to serve the link", body)
	}
}

func TestDeriveTunnelID(t *testing.T) {
	id := deriveTunnelID(testSecret, "account-a", "nonce-aaaaaaaaaaaa")
	if !validTunnelID(id) {
		t.Fatalf("derived id %q is not a valid link label", id)
	}
	if again := deriveTunnelID(testSecret, "account-a", "nonce-aaaaaaaaaaaa"); again != id {
		t.Fatal("derivation is not stable")
	}
	for _, other := range []string{
		deriveTunnelID(testSecret, "account-b", "nonce-aaaaaaaaaaaa"),
		deriveTunnelID(testSecret, "account-a", "nonce-bbbbbbbbbbbb"),
		deriveTunnelID("a-different-relay-secret", "account-a", "nonce-aaaaaaaaaaaa"),
	} {
		if other == id {
			t.Fatal("different inputs produced the same link")
		}
	}
}

func TestParsePreviewOrigin(t *testing.T) {
	valid := map[string]string{
		"https://preview.agentman.dev":     "https://abc.preview.agentman.dev",
		"https://Preview.Example.com/":     "https://abc.preview.example.com",
		"http://localhost:8080":            "http://abc.localhost:8080",
		"https://preview.example.com:8443": "https://abc.preview.example.com:8443",
	}
	for raw, want := range valid {
		origin, err := parsePreviewOrigin(raw)
		if err != nil {
			t.Fatalf("%s: %v", raw, err)
		}
		if got := origin.linkURL("abc"); got != want {
			t.Fatalf("%s: link %q, want %q", raw, got, want)
		}
	}
	for _, raw := range []string{
		"http://preview.example.com", // plaintext off loopback
		"https://example.com/previews",
		"ftp://example.com",
		"",
	} {
		if _, err := parsePreviewOrigin(raw); err == nil {
			t.Fatalf("%q accepted, want an error", raw)
		}
	}
}

func TestPreviewHeadersRequireHTTPSOnlyForHTTPSLinks(t *testing.T) {
	secure := http.Header{}
	// A dev server's own policy must not survive onto the public domain.
	secure.Set("Strict-Transport-Security", "max-age=0")
	setPreviewHeaders(secure, "https://abc.agentman.online")
	if got := secure.Get("Strict-Transport-Security"); got != "max-age=31536000" {
		t.Fatalf("Strict-Transport-Security = %q", got)
	}
	if !strings.Contains(secure.Get("X-Robots-Tag"), "noindex") {
		t.Fatal("missing noindex")
	}

	local := http.Header{}
	setPreviewHeaders(local, "http://abc.localhost:8099")
	if got := local.Get("Strict-Transport-Security"); got != "" {
		t.Fatalf("plaintext local link got Strict-Transport-Security %q", got)
	}
}
