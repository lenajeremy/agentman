package main

import "testing"

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
