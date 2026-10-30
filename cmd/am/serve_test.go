package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestResolveRelayPrecedence(t *testing.T) {
	t.Run("falls back to the built-in relay", func(t *testing.T) {
		t.Setenv(relayEnv, "")
		if got := resolveRelay(""); got != DefaultRelay {
			t.Errorf("resolveRelay(\"\") = %q, want the built-in default", got)
		}
	})

	t.Run("environment overrides the default", func(t *testing.T) {
		t.Setenv(relayEnv, "https://mine.example.com")
		if got := resolveRelay(""); got != "https://mine.example.com" {
			t.Errorf("got %q, want the environment value", got)
		}
	})

	t.Run("flag beats the environment", func(t *testing.T) {
		t.Setenv(relayEnv, "https://mine.example.com")
		if got := resolveRelay("https://explicit.example.com"); got != "https://explicit.example.com" {
			t.Errorf("got %q, want the explicit flag", got)
		}
	})

	t.Run("none opts out entirely", func(t *testing.T) {
		// Running without a relay is a legitimate mode — the daemon still
		// watches agents and prints locally — so baking in a default must not
		// take that away.
		t.Setenv(relayEnv, "https://mine.example.com")
		for _, off := range []string{"none", "NONE", "off", "-"} {
			if got := resolveRelay(off); got != "" {
				t.Errorf("resolveRelay(%q) = %q, want no relay", off, got)
			}
		}
	})

	t.Run("surrounding whitespace is ignored", func(t *testing.T) {
		// A pasted URL often carries a trailing space, and the failure it
		// causes ("no such host") points nowhere near the real problem.
		t.Setenv(relayEnv, "")
		if got := resolveRelay("  https://spaced.example.com  "); got != "https://spaced.example.com" {
			t.Errorf("got %q, want the trimmed URL", got)
		}
		t.Setenv(relayEnv, "  https://env.example.com  ")
		if got := resolveRelay(""); got != "https://env.example.com" {
			t.Errorf("got %q, want the trimmed environment value", got)
		}
	})
}

func TestNormalizeRelayURLRequiresEncryptedRemoteTransport(t *testing.T) {
	tests := []struct {
		raw     string
		want    string
		wantErr bool
	}{
		{raw: "relay.example.com", want: "https://relay.example.com"},
		{raw: "https://relay.example.com/", want: "https://relay.example.com"},
		{raw: "wss://relay.example.com", want: "https://relay.example.com"},
		{raw: "http://127.0.0.1:8080", want: "http://127.0.0.1:8080"},
		{raw: "ws://[::1]:8080", want: "http://[::1]:8080"},
		{raw: "http://localhost:8080", want: "http://localhost:8080"},
		{raw: "http://192.168.1.20:8080", wantErr: true},
		{raw: "ws://relay.example.com", wantErr: true},
		{raw: "ftp://relay.example.com", wantErr: true},
		{raw: "https://relay.example.com/path", wantErr: true},
		{raw: "https://user:pass@relay.example.com", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.raw, func(t *testing.T) {
			got, err := normalizeRelayURL(test.raw)
			if test.wantErr {
				if err == nil {
					t.Fatalf("normalizeRelayURL(%q) = %q, want error", test.raw, got)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("normalizeRelayURL(%q) = %q, %v; want %q", test.raw, got, err, test.want)
			}
		})
	}
}

func TestReadPairCodeResponseIsBoundedAndValidated(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	token := base64.RawURLEncoding.EncodeToString([]byte("0123456789abcdef"))
	valid := fmt.Sprintf(`{"code":"1234567890","token":%q,"expiresAt":%d}`,
		token, now.Add(time.Minute).UnixMilli())
	body, err := readPairCodeResponse(strings.NewReader(valid), now)
	if err != nil || body.Code != "1234567890" || body.Token != token {
		t.Fatalf("valid response = %+v, %v", body, err)
	}

	tests := []string{
		strings.Repeat("x", maxPairCodeResponse+1),
		`{"code":"123","token":"bad","expiresAt":0}`,
		fmt.Sprintf(`{"code":"12345x7890","token":%q,"expiresAt":%d}`,
			token, now.Add(time.Minute).UnixMilli()),
		fmt.Sprintf(`{"code":"1234567890","token":%q,"expiresAt":%d}`,
			token, now.Add(-time.Minute).UnixMilli()),
	}
	for _, raw := range tests {
		if _, err := readPairCodeResponse(strings.NewReader(raw), now); err == nil {
			t.Errorf("accepted invalid pairing response %q", raw[:min(len(raw), 80)])
		}
	}
}

func TestPairingURLOmitsOnlyTheDefaultRelay(t *testing.T) {
	const token = "Yz0gMUIZjTnemlQwXQ"

	// Omitted for the default relay: payload length drives the QR version, and
	// those sixty characters are the difference between sixteen printed rows
	// and twenty.
	got := PairingURL(DefaultRelay, token)
	if !strings.HasPrefix(got, "agentman://pair/v2/") {
		t.Fatalf("payload did not use compact v2 format: %q", got)
	}
	if strings.Contains(got, DefaultRelay) {
		t.Errorf("payload names the default relay, making the QR larger than it needs to be: %q", got)
	}

	// Spelled out for anything else. A self-hoster whose QR silently pointed at
	// the public relay would be a much worse outcome than a taller code.
	got = PairingURL("https://relay.example.com", token)
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(got, "agentman://pair/v2/"))
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Relay string `json:"r"`
		Token string `json:"t"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Relay != "https://relay.example.com" {
		t.Errorf("a custom relay was dropped from the payload: %q", got)
	}
	if payload.Token != token {
		t.Errorf("payload token = %q, want %q", payload.Token, token)
	}

	// A trailing slash must not defeat the comparison and force the long form.
	if PairingURL(DefaultRelay+"/", token) != PairingURL(DefaultRelay, token) {
		t.Error("a trailing slash produced a different payload")
	}
}

func TestAppAndDaemonAgreeOnTheDefaultRelay(t *testing.T) {
	// The daemon omits the relay from a QR when it is the default, and the app
	// fills it back in from its own copy of the constant. If the two drift, a
	// scanned pairing goes somewhere the daemon is not — and nothing else in
	// either codebase would notice.
	source, err := os.ReadFile("../../mobile/lib/pairing.ts")
	if err != nil {
		t.Skipf("mobile source not available: %v", err)
	}
	want := `export const DEFAULT_RELAY = "` + DefaultRelay + `";`
	if !strings.Contains(string(source), want) {
		t.Errorf("mobile/lib/pairing.ts does not declare %s\n"+
			"the app and daemon must agree on the default relay", want)
	}
}
