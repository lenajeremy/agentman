package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/lenajeremy/agentman/internal/protocol"
)

const testSecret = "test-secret-long-enough"

func newTestServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	// Tests forge X-Forwarded-For to stand in for distinct callers, which only
	// means anything when the header is trusted.
	server := NewServer(testSecret, "test", slog.New(slog.DiscardHandler), true)
	http := httptest.NewServer(server.Handler())
	t.Cleanup(http.Close)
	return server, http
}

func wsAddr(httpURL string) string {
	return "ws" + strings.TrimPrefix(httpURL, "http")
}

// dialDaemon connects as a daemon using a raw token.
func dialDaemon(t *testing.T, base, token string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, wsAddr(base)+"/ws/daemon", &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + token}},
	})
	if err != nil {
		t.Fatalf("daemon dial: %v", err)
	}
	t.Cleanup(func() { ws.CloseNow() })
	return ws
}

// requestPairCode asks the relay for a code over HTTP.
func requestPairCode(t *testing.T, base, daemonToken string) string {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+"/pair/code", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+daemonToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("pair/code: HTTP %d: %s", resp.StatusCode, body)
	}
	var out struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out.Code
}

// redeem exchanges a code for a device token.
func redeem(t *testing.T, base, code string) (string, int) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"code": code, "deviceId": "test"})
	resp, err := http.Post(base+"/pair", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Token string `json:"token"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return out.Token, resp.StatusCode
}

func TestPairingDoesNotDisconnectTheDaemon(t *testing.T) {
	// Regression: pairing used to be a websocket control frame, so requesting
	// a code opened a second daemon socket, which replaced the live one and
	// knocked the real daemon offline every time the user paired a device.
	_, ts := newTestServer(t)
	const daemonToken = "daemon-token"

	daemon := dialDaemon(t, ts.URL, daemonToken)

	for range 3 {
		if code := requestPairCode(t, ts.URL, daemonToken); code == "" {
			t.Fatal("empty pairing code")
		}
	}

	// The daemon socket must still be usable: a read with a short deadline
	// should time out (nothing to read), not report a closed connection.
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, _, err := daemon.Read(ctx)
	if err != nil && ctx.Err() == nil {
		t.Fatalf("pairing disconnected the daemon: %v", err)
	}
}

func TestWebsocketPairRequestsAreRateLimited(t *testing.T) {
	server, ts := newTestServer(t)
	server.perClientPairings = newLimiter(1, time.Minute)
	server.globalPairings = newLimiter(10, time.Minute)
	daemon := dialDaemon(t, ts.URL, "daemon-token")

	for index := range 2 {
		envelope, err := protocol.NewEnvelope(fmt.Sprintf("pair-%d", index), protocol.PeerRelay,
			protocol.Control{Type: protocol.CtlPairRequest})
		if err != nil {
			t.Fatal(err)
		}
		if err := wsjson.Write(context.Background(), daemon, envelope); err != nil {
			t.Fatal(err)
		}
		var reply protocol.Envelope
		if err := wsjson.Read(context.Background(), daemon, &reply); err != nil {
			t.Fatal(err)
		}
		var control protocol.Control
		if err := json.Unmarshal(reply.Payload, &control); err != nil {
			t.Fatal(err)
		}
		want := protocol.CtlPairCode
		if index == 1 {
			want = protocol.CtlError
		}
		if control.Type != want {
			t.Fatalf("reply %d = %q, want %q", index, control.Type, want)
		}
	}
}

func TestAppSeesDaemonOnlineOnConnect(t *testing.T) {
	_, ts := newTestServer(t)
	const daemonToken = "daemon-token"
	dialDaemon(t, ts.URL, daemonToken)

	code := requestPairCode(t, ts.URL, daemonToken)
	deviceToken, status := redeem(t, ts.URL, code)
	if status != http.StatusOK || deviceToken == "" {
		t.Fatalf("redeem failed: HTTP %d", status)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	app, _, err := websocket.Dial(ctx, wsAddr(ts.URL)+"/ws/app", &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + deviceToken}},
	})
	if err != nil {
		t.Fatalf("app dial: %v", err)
	}
	defer app.CloseNow()

	_, data, err := app.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var env protocol.Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatal(err)
	}
	var control protocol.Control
	if err := json.Unmarshal(env.Payload, &control); err != nil {
		t.Fatal(err)
	}
	if control.Type != protocol.CtlHello {
		t.Fatalf("first frame = %q, want hello", control.Type)
	}
	if !control.DaemonOnline {
		t.Error("hello reported the daemon offline while it was connected")
	}
}

func TestAppFrameReachesDaemon(t *testing.T) {
	_, ts := newTestServer(t)
	const daemonToken = "daemon-token"
	daemon := dialDaemon(t, ts.URL, daemonToken)

	code := requestPairCode(t, ts.URL, daemonToken)
	deviceToken, _ := redeem(t, ts.URL, code)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	app, _, err := websocket.Dial(ctx, wsAddr(ts.URL)+"/ws/app", &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + deviceToken}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer app.CloseNow()

	env, err := protocol.NewEnvelope("req-1", protocol.PeerDaemon,
		protocol.Request{Type: protocol.ReqListSessions})
	if err != nil {
		t.Fatal(err)
	}
	frame, _ := json.Marshal(env)
	if err := app.Write(ctx, websocket.MessageText, frame); err != nil {
		t.Fatal(err)
	}

	_, data, err := daemon.Read(ctx)
	if err != nil {
		t.Fatalf("daemon did not receive the app's request: %v", err)
	}
	var got protocol.Envelope
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != "req-1" {
		t.Errorf("frame id = %q, want the app's original id", got.ID)
	}
	if got.From == "" {
		t.Error("relay did not identify the app connection for subscription ownership")
	}
}

func TestAppTokenIsRequired(t *testing.T) {
	_, ts := newTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for _, token := range []string{"", "garbage", "a.b"} {
		_, _, err := websocket.Dial(ctx, wsAddr(ts.URL)+"/ws/app?token="+token, nil)
		if err == nil {
			t.Errorf("app connected with token %q", token)
		}
	}
}

func TestPairCodeRequiresDaemonToken(t *testing.T) {
	_, ts := newTestServer(t)
	resp, err := http.Post(ts.URL+"/pair/code", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("HTTP %d, want 401 for an unauthenticated code request", resp.StatusCode)
	}
}

func TestDaemonTokenIsRejectedInQueryString(t *testing.T) {
	_, ts := newTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if ws, _, err := websocket.Dial(ctx,
		wsAddr(ts.URL)+"/ws/daemon?token=daemon-root-secret", nil); err == nil {
		ws.CloseNow()
		t.Fatal("daemon root token in a query string was accepted")
	}
}

func TestPairCodeCreationIsRateLimited(t *testing.T) {
	_, ts := newTestServer(t)
	var blocked bool
	for range 40 {
		req, err := http.NewRequest(http.MethodPost, ts.URL+"/pair/code", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer attacker-chosen-account")
		req.Header.Set("X-Forwarded-For", "198.51.100.44")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			blocked = true
			break
		}
	}
	if !blocked {
		t.Fatal("anonymous pairing creation was never rate limited")
	}
}

func TestPeerDirectionPolicy(t *testing.T) {
	allowed := []struct {
		from protocol.Peer
		to   protocol.Peer
	}{
		{protocol.PeerApp, protocol.PeerDaemon},
		{protocol.PeerApp, protocol.PeerRelay},
		{protocol.PeerDaemon, protocol.PeerApp},
		{protocol.PeerDaemon, protocol.PeerRelay},
	}
	for _, route := range allowed {
		if !allowedDestination(route.from, route.to) {
			t.Errorf("route %s -> %s was rejected", route.from, route.to)
		}
	}
	if allowedDestination(protocol.PeerApp, protocol.PeerApp) {
		t.Error("an app can impersonate the daemon to other apps")
	}
	if allowedDestination(protocol.PeerDaemon, protocol.PeerDaemon) {
		t.Error("a daemon can reflect frames back into itself")
	}
}

func TestBruteForceIsRateLimitedPerCaller(t *testing.T) {
	_, ts := newTestServer(t)
	const daemonToken = "daemon-token"
	dialDaemon(t, ts.URL, daemonToken)
	code := requestPairCode(t, ts.URL, daemonToken)

	guess := func(from, code string) int {
		body, _ := json.Marshal(map[string]string{"code": code, "deviceId": "x"})
		req, err := http.NewRequest(http.MethodPost, ts.URL+"/pair", strings.NewReader(string(body)))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-For", from)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}

	// An attacker walking the code space gets cut off well before a six-digit
	// space is anywhere near exhausted.
	var blocked bool
	for range 30 {
		if guess("10.0.0.9", "00000000") == http.StatusTooManyRequests {
			blocked = true
			break
		}
	}
	if !blocked {
		t.Fatal("guessing was never rate limited")
	}

	// And the victim, on a different address, can still pair — the whole point
	// of charging the caller rather than the code.
	if status := guess("10.0.0.1", code); status != http.StatusOK {
		t.Errorf("victim got HTTP %d; an attacker's guessing locked out a real user", status)
	}
}

func TestOneCallersGuessingDoesNotAffectAnother(t *testing.T) {
	// The property that replaced sharding. An earlier design charged failures
	// to a group of accounts, which meant one person mistyping could stop
	// everyone in that group from pairing. Behind a proxy that overwrites
	// X-Forwarded-For the caller is known exactly, so a failure is charged to
	// them alone and no innocent user is ever caught in it.
	server, ts := newTestServer(t)
	code, err := server.hub.NewPairingCode(DeriveAccount("victim-daemon"))
	if err != nil {
		t.Fatal(err)
	}

	guess := func(from, code string) int {
		body, _ := json.Marshal(map[string]string{"code": code, "deviceId": "x"})
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/pair", strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Forwarded-For", from)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}

	// One caller guesses until they are cut off.
	var blocked bool
	for range 30 {
		if guess("198.51.100.7", "00000000") == http.StatusTooManyRequests {
			blocked = true
			break
		}
	}
	if !blocked {
		t.Fatal("guessing was never rate limited")
	}

	// Everyone else is unaffected, including the owner of a real code.
	if status := guess("203.0.113.9", "11111111"); status != http.StatusForbidden {
		t.Errorf("an unrelated caller got HTTP %d, want 403 — they were caught "+
			"in someone else's rate limit", status)
	}
	if _, status := redeem(t, ts.URL, code); status != http.StatusOK {
		t.Errorf("the victim could not pair (HTTP %d) while another caller was "+
			"being rate limited", status)
	}
}

func TestIPv6CallersAreGroupedBySubnet(t *testing.T) {
	// A single subscriber is routinely handed a /64, so limiting per full v6
	// address would give one attacker 2^64 buckets and no limit at all.
	server, _ := newTestServer(t)
	req := func(addr string) string {
		r, _ := http.NewRequest(http.MethodPost, "http://x/pair", nil)
		r.Header.Set("X-Forwarded-For", addr)
		return server.clientKey(r)
	}
	if a, b := req("2001:db8:1:2::1"), req("2001:db8:1:2::9999"); a != b {
		t.Errorf("addresses in one /64 landed in different buckets: %q vs %q", a, b)
	}
	if a, b := req("2001:db8:1:2::1"), req("2001:db8:9:9::1"); a == b {
		t.Errorf("different /64s shared a bucket: %q", a)
	}
}

func TestForgedForwardedForIsIgnoredWithoutAProxy(t *testing.T) {
	// The header is client-supplied. A relay exposed directly must not believe
	// it, or an attacker mints a fresh bucket per request and per-caller
	// limiting stops existing.
	direct := NewServer(testSecret, "test", slog.New(slog.DiscardHandler), false)

	r, _ := http.NewRequest(http.MethodPost, "http://x/pair", nil)
	r.RemoteAddr = "192.0.2.10:5555"
	r.Header.Set("X-Forwarded-For", "198.51.100.1")

	if key := direct.clientKey(r); key != "192.0.2.10" {
		t.Errorf("clientKey = %q, want the socket address — a forged header was believed", key)
	}
}

func TestSuccessfulPairingSpendsNoBudget(t *testing.T) {
	// Only failures are charged. If a correct code consumed budget too, heavy
	// legitimate use would eventually rate limit itself.
	server, ts := newTestServer(t)
	account := DeriveAccount("busy-daemon")

	for range 40 {
		code, err := server.hub.NewPairingCode(account)
		if err != nil {
			t.Fatal(err)
		}
		if _, status := redeem(t, ts.URL, code); status != http.StatusOK {
			t.Fatalf("a valid pairing was rejected with HTTP %d after repeated "+
				"successful pairings", status)
		}
	}
}

func TestScannedTokenPairsAndIsNotRateLimited(t *testing.T) {
	// The reason the QR path exists: a scanned secret is never typed, so it can
	// carry 128 bits, and at that size guessing is not a threat the relay has
	// to defend against. That in turn means a flood of wrong typed codes — the
	// thing that can still exhaust a shard budget — cannot stop someone from
	// pairing by scanning.
	server, ts := newTestServer(t)
	account := DeriveAccount("daemon-token")

	secrets, err := server.hub.NewPairing(account)
	if err != nil {
		t.Fatal(err)
	}
	if !IsPairingToken(secrets.Token) {
		t.Fatalf("token %q is not recognised as one", secrets.Token)
	}

	// Exhaust this caller's typed-code budget.
	for range 30 {
		body, _ := json.Marshal(map[string]string{"code": "00000000"})
		resp, err := http.Post(ts.URL+"/pair", "application/json", strings.NewReader(string(body)))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	if _, status := redeem(t, ts.URL, secrets.Code); status != http.StatusTooManyRequests {
		t.Fatalf("typed path returned HTTP %d, want 429 — the setup did not exhaust it", status)
	}

	// The scanned token still works.
	token, status := redeem(t, ts.URL, secrets.Token)
	if status != http.StatusOK || token == "" {
		t.Fatalf("scanning returned HTTP %d; a long secret needs no rate limiting", status)
	}
}

func TestRedeemingOneSecretRetiresTheOther(t *testing.T) {
	// A pairing consumed by scanning must not still be redeemable by typing,
	// or the unused half would linger as a second way in for its full minute.
	server, ts := newTestServer(t)
	secrets, err := server.hub.NewPairing(DeriveAccount("daemon-token"))
	if err != nil {
		t.Fatal(err)
	}

	if _, status := redeem(t, ts.URL, secrets.Token); status != http.StatusOK {
		t.Fatalf("scanning failed with HTTP %d", status)
	}
	if _, status := redeem(t, ts.URL, secrets.Code); status == http.StatusOK {
		t.Error("the typed code still worked after the pairing was scanned")
	}
}

func TestConnectionCapacityIsBoundedPerClient(t *testing.T) {
	server := NewServer(testSecret, "test", nil, false)
	client := "203.0.113.7"
	for range maxConnectionsPerClient {
		if !server.acquireConnection(httptest.NewRecorder(), client) {
			t.Fatal("client was rejected below its connection ceiling")
		}
	}

	rejected := httptest.NewRecorder()
	if server.acquireConnection(rejected, client) {
		t.Fatal("one client occupied more than its connection ceiling")
	}
	if rejected.Code != http.StatusTooManyRequests {
		t.Fatalf("rejection status = %d, want %d", rejected.Code, http.StatusTooManyRequests)
	}

	// Another caller still has capacity; this is an isolation limit, not a
	// smaller accidental global limit.
	other := "203.0.113.8"
	if !server.acquireConnection(httptest.NewRecorder(), other) {
		t.Fatal("one abusive client exhausted another client's capacity")
	}
	server.releaseConnection(other)

	for range maxConnectionsPerClient {
		server.releaseConnection(client)
	}
	if len(server.connections) != 0 || len(server.clientConnections) != 0 {
		t.Fatal("released connections leaked capacity bookkeeping")
	}
}

func TestHealthExposesNoUserData(t *testing.T) {
	_, ts := newTestServer(t)
	dialDaemon(t, ts.URL, "daemon-token")

	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	// Counts and status only — there must be no endpoint that could leak a
	// session, a message, or an account identifier.
	allowed := map[string]bool{
		"status": true, "version": true, "daemons": true,
		"apps": true, "pendingPairings": true, "storage": true,
	}
	for key := range payload {
		if !allowed[key] {
			t.Errorf("/health exposes unexpected field %q", key)
		}
	}
	if strings.Contains(string(body), "daemon-token") {
		t.Error("/health leaked a daemon token")
	}
}
