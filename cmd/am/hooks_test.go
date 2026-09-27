package main

import (
	"os"
	"path/filepath"
	"testing"
)

// Regression: hooks were recorded at the symlink's target, which under
// Homebrew is /opt/homebrew/Caskroom/agentman/<version>/am. The next upgrade
// deleted that directory and every Claude hook failed with ENOENT, so sessions
// stopped reporting state with nothing in the daemon log to say why.
func TestStableBinaryPathKeepsTheSymlink(t *testing.T) {
	dir := t.TempDir()
	versioned := filepath.Join(dir, "Caskroom", "agentman", "0.6.0")
	if err := os.MkdirAll(versioned, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(versioned, "am")
	if err := os.WriteFile(target, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "am")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	if got := stableBinaryPath(link); got != link {
		t.Errorf("stableBinaryPath(%q) = %q, want the symlink itself so it survives an upgrade", link, got)
	}
}

func TestStableBinaryPathKeepsARealFile(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "am")
	if err := os.WriteFile(binary, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := stableBinaryPath(binary); got != binary {
		t.Errorf("stableBinaryPath(%q) = %q, want it unchanged", binary, got)
	}
}

// A dangling link cannot be run, so there is nothing to preserve; resolving is
// the only remaining chance of naming something real.
func TestStableBinaryPathFallsBackWhenNothingResolves(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "gone")
	if got := stableBinaryPath(missing); got != missing {
		t.Errorf("stableBinaryPath(%q) = %q, want the input back", missing, got)
	}
	if got := stableBinaryPath(""); got != "" {
		t.Errorf("stableBinaryPath(\"\") = %q, want empty", got)
	}
}
