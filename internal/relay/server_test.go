package relay

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/lenajeremy/agentman/internal/protocol"
)

const testSecret = "test-secret-long-enough"

func newTestServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	server := NewServer(testSecret, "test", slog.New(slog.DiscardHandler))
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
		if guess("10.0.0.9", "000000") == http.StatusTooManyRequests {
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
