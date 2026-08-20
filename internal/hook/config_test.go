package hook

import (
	"bytes"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeExistingConfig(t *testing.T, home string, body []byte) string {
	t.Helper()
	dir := filepath.Join(home, ".agentman")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertConfigUnchanged(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("invalid config was overwritten:\n got: %q\nwant: %q", got, want)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(path) {
		t.Fatalf("loading invalid config left unexpected files: %+v", entries)
	}
}

func TestLoadConfigRejectsMalformedJSONWithoutReplacingIt(t *testing.T) {
	home := t.TempDir()
	body := []byte(`{"token":"recoverable"`)
	path := writeExistingConfig(t, home, body)

	for range 2 {
		cfg, err := LoadConfig(home)
		if err == nil {
			t.Fatal("malformed config was accepted")
		}
		if cfg.Token != "" {
			t.Fatalf("malformed config minted token %q", cfg.Token)
		}
		message := err.Error()
		if !strings.Contains(message, "malformed JSON") ||
			!strings.Contains(message, "restore this file") ||
			!strings.Contains(message, "pair devices again") {
			t.Fatalf("error is not actionable: %v", err)
		}
		assertConfigUnchanged(t, path, body)
	}
}

func TestLoadConfigRejectsMissingTokenWithoutReplacingIt(t *testing.T) {
	for _, body := range [][]byte{
		[]byte(`{"hookAddr":"127.0.0.1:9876"}`),
		[]byte(`{"token":" \t "}`),
	} {
		t.Run(string(body), func(t *testing.T) {
			home := t.TempDir()
			path := writeExistingConfig(t, home, body)

			cfg, err := LoadConfig(home)
			if err == nil {
				t.Fatal("tokenless config was accepted")
			}
			if cfg.Token != "" {
				t.Fatalf("tokenless config minted token %q", cfg.Token)
			}
			message := err.Error()
			if !strings.Contains(message, "authentication token is missing") ||
				!strings.Contains(message, "restore this file") ||
				!strings.Contains(message, "pair devices again") {
				t.Fatalf("error is not actionable: %v", err)
			}
			assertConfigUnchanged(t, path, body)
		})
	}
}

func TestLoadConfigCreatesOnlyWhenAbsentAndReloadsValidConfig(t *testing.T) {
	home := t.TempDir()
	first, err := LoadConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := hex.DecodeString(first.Token)
	if err != nil || len(decoded) != 24 {
		t.Fatalf("first-run token is not 24 random bytes encoded as hex: %q", first.Token)
	}

	path := filepath.Join(home, ".agentman", "config.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("first-run config mode = %#o, want 0600", got)
	}

	second, err := LoadConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	if second.Token != first.Token {
		t.Fatalf("valid config identity changed from %q to %q", first.Token, second.Token)
	}
}

func TestLoadConfigPreservesValidExistingConfig(t *testing.T) {
	home := t.TempDir()
	body := []byte("{\n  \"token\": \"existing-identity\",\n  \"hookAddr\": \"127.0.0.1:9876\"\n}\n")
	path := writeExistingConfig(t, home, body)
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Token != "existing-identity" || cfg.HookAddr != "127.0.0.1:9876" {
		t.Fatalf("loaded config = %+v", cfg)
	}
	assertConfigUnchanged(t, path, body)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("valid config mode = %#o, want repaired 0600", got)
	}
}
