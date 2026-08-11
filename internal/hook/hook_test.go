package hook

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
)

func TestParseCodexNotifyPayload(t *testing.T) {
	raw := []byte(`{"type":"agent-turn-complete","thread-id":"thread-123","turn-id":"turn-9","cwd":"/work","input-messages":["hello"],"last-assistant-message":"finished"}`)
	payload, err := ParsePayload(raw)
	if err != nil {
		t.Fatal(err)
	}
	if payload.SessionID != "thread-123" || payload.Cwd != "/work" {
		t.Fatalf("Codex identity was not normalized: %+v", payload)
	}
	if payload.HookEventName != string(NameStop) || payload.LastAssistantMessage != "finished" {
		t.Fatalf("Codex completion was not normalized: %+v", payload)
	}
}

func TestServerRejectsUnknownDeliveryAndOversizedBody(t *testing.T) {
	server := NewServer("secret")
	request := func(path string, body []byte) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
		req.Header.Set("X-Agentman-Token", "secret")
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, req)
		return response
	}

	if got := request("/hook/opencode/Stop", nil).Code; got != http.StatusBadRequest {
		t.Fatalf("unknown hook status = %d, want %d", got, http.StatusBadRequest)
	}
	large := []byte(strings.Repeat("x", maxHookBody+1))
	if got := request("/hook/claude/Stop", large).Code; got != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized hook status = %d, want %d", got, http.StatusRequestEntityTooLarge)
	}
}

func TestServerRejectsEmptyAuthenticationSecret(t *testing.T) {
	server := NewServer("")
	req := httptest.NewRequest(http.MethodPost, "/hook/claude/Stop", strings.NewReader(`{"session_id":"s"}`))
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, req)
	if response.Code != http.StatusForbidden {
		t.Fatalf("empty-secret hook status = %d, want %d", response.Code, http.StatusForbidden)
	}
	select {
	case event := <-server.Events():
		t.Fatalf("unauthenticated event was accepted: %+v", event)
	default:
	}
	if _, ok := server.LastFired(protocol.KindClaude); ok {
		t.Fatal("unauthenticated event updated hook state")
	}
}

func TestServerAcknowledgesEmptyPayloadWithoutInventingSession(t *testing.T) {
	server := NewServer("secret")
	req := httptest.NewRequest(http.MethodPost, "/hook/claude/Stop", nil)
	req.Header.Set("X-Agentman-Token", "secret")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, req)
	if response.Code != http.StatusAccepted {
		t.Fatalf("empty payload status = %d, want %d", response.Code, http.StatusAccepted)
	}
	select {
	case event := <-server.Events():
		t.Fatalf("empty payload invented an event: %+v", event)
	default:
	}
	if _, ok := server.LastFired(protocol.KindClaude); ok {
		t.Fatal("empty payload updated hook health")
	}
}

func TestConfigPersistsPrivateCustomLoopbackAddress(t *testing.T) {
	home := t.TempDir()
	cfg, err := LoadConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	cfg.HookAddr = "127.0.0.1:9876"
	if err := SaveConfig(home, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ListenAddr() != cfg.HookAddr {
		t.Fatalf("listener = %q, want %q", loaded.ListenAddr(), cfg.HookAddr)
	}
	for _, path := range []string{filepath.Join(home, ".agentman"), filepath.Join(home, ".agentman", "config.json")} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		want := os.FileMode(0o600)
		if info.IsDir() {
			want = 0o700
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s mode = %#o, want %#o", path, got, want)
		}
	}
}

func TestHookAddressMustRemainLoopbackAndStable(t *testing.T) {
	for _, addr := range []string{"0.0.0.0:8787", "192.168.1.10:8787", "localhost:8787", "127.0.0.1:0", "127.0.0.1"} {
		if ValidateAddr(addr) == nil {
			t.Errorf("unsafe or unusable address %q was accepted", addr)
		}
	}
	for _, addr := range []string{"127.0.0.1:8787", "[::1]:8787"} {
		if err := ValidateAddr(addr); err != nil {
			t.Errorf("safe address %q was rejected: %v", addr, err)
		}
	}
}
