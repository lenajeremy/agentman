package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestExplicitPortIsRespected(t *testing.T) {
	// A user running several projects at once has to be able to choose a port.
	// Overriding that silently would break the session they were actually
	// looking at, which is worse than the one they were not.
	cases := []struct {
		name string
		args []string
		want string
		ok   bool
	}{
		{"separate value", []string{"--port", "4097"}, "4097", true},
		{"equals form", []string{"--port=4097"}, "4097", true},
		{"single dash", []string{"-port", "4097"}, "4097", true},
		{"single dash equals", []string{"-port=4097"}, "4097", true},
		{"after other flags", []string{"--pure", "--port", "5000"}, "5000", true},
		{"none given", []string{"--pure"}, "", false},
		{"no arguments", nil, "", false},
		// A trailing --port with nothing after it is OpenCode's error to
		// report, not ours: claiming a port the user never named would be a
		// guess, and this path only decides whether to add our own flag.
		{"dangling flag", []string{"--port"}, "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := explicitPort(tc.args)
			if ok != tc.ok || got != tc.want {
				t.Errorf("explicitPort(%q) = (%q, %v), want (%q, %v)", tc.args, got, ok, tc.want, tc.ok)
			}
		})
	}
}

// TestExplicitPortIgnoresLookalikes guards the obvious wrong implementation:
// a substring match would catch these and hand back nonsense.
func TestExplicitPortIgnoresLookalikes(t *testing.T) {
	for _, args := range [][]string{
		{"--porcelain"},
		{"--report=4097"},
		{"--print-logs"},
		{"export", "--portable"},
	} {
		if got, ok := explicitPort(args); ok {
			t.Errorf("explicitPort(%q) matched %q; only --port sets the API port", args, got)
		}
	}
}

func TestOpenCodeHealthyOnlyAcceptsOpenCode(t *testing.T) {
	ctx := context.Background()

	t.Run("a real server", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/global/health" {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Write([]byte(`{"healthy":true,"version":"1.18.15"}`))
		}))
		defer server.Close()

		if !openCodeHealthy(ctx, server.URL) {
			t.Error("a live OpenCode server was not recognised, so the wrapper would " +
				"refuse to attach and the user would lose their existing sessions")
		}
	})

	t.Run("something else on the port", func(t *testing.T) {
		// The reason this probes the health endpoint instead of just dialling:
		// any listener accepts a connection, and attaching to the wrong one
		// fails in a way that points nowhere near the cause.
		other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer other.Close()

		if openCodeHealthy(ctx, other.URL) {
			t.Error("a non-OpenCode listener was mistaken for OpenCode")
		}
	})

	t.Run("nothing listening", func(t *testing.T) {
		if openCodeHealthy(ctx, "http://127.0.0.1:1") {
			t.Error("a closed port reported healthy")
		}
	})
}

func TestPortBusyDetectsAListener(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()

	port := server.Listener.Addr().(*net.TCPAddr).Port
	if !portBusy(port) {
		t.Errorf("port %d holds a listener but was reported free", port)
	}
	// Port 1 is privileged and unbound in any environment this runs in.
	if portBusy(1) {
		t.Error("port 1 reported busy")
	}
}

// TestDoctorPointsAtTheWrapper keeps the advice runnable.
//
// `opencode serve` is headless: following that advice leaves you with an API
// the daemon can see and no TUI to type into. The fix for "OpenCode isn't
// showing up" has to be the command that actually gives you a session.
func TestDoctorPointsAtTheWrapper(t *testing.T) {
	source, err := os.ReadFile("hooks.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(source), "start it with `opencode serve`") {
		t.Error("doctor still recommends `opencode serve`, which starts a server with no TUI")
	}
}
