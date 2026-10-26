package shellenv

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"
)

func TestParseKeepsTheEnvironmentAfterTheMarker(t *testing.T) {
	// What a chatty profile prints first, then the environment.
	out := []byte("Welcome back!\nPATH=/not/this\n" + marker +
		"PATH=/opt/homebrew/bin:/usr/bin\x00HOME=/Users/me\x00PWD=/Users/me\x00TERM=xterm-ghostty\x00EMPTY=\x00TMUX=/tmp/x\x00")
	env, err := Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"PATH=/opt/homebrew/bin:/usr/bin", "HOME=/Users/me", "EMPTY="}
	if !slices.Equal(env, want) {
		t.Fatalf("got %q, want %q", env, want)
	}
}

func TestParseRefusesOutputWithoutAnEnvironment(t *testing.T) {
	if _, err := Parse([]byte("PATH=/usr/bin\n")); err == nil {
		t.Fatal("no marker should be an error")
	}
	if _, err := Parse([]byte(marker)); err == nil {
		t.Fatal("nothing after the marker should be an error")
	}
}

// The real thing, with a shell that has no profile to read: what it sets up
// comes back, and the shell's own bookkeeping doesn't.
func TestLoadRunsTheLoginShell(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("ZDOTDIR", home)
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("AGENTMAN_SHELLENV_PROBE", "kept")
	env, err := Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(env, "AGENTMAN_SHELLENV_PROBE=kept") {
		t.Fatalf("the inherited variable is missing from %q", env)
	}
	for _, entry := range env {
		if strings.HasPrefix(entry, "PWD=") || strings.HasPrefix(entry, "SHLVL=") {
			t.Fatalf("%q should have been left out", entry)
		}
	}
}

func TestApplySetsEachVariable(t *testing.T) {
	t.Setenv("AGENTMAN_SHELLENV_A", "before")
	set := Apply([]string{"AGENTMAN_SHELLENV_A=after", "AGENTMAN_SHELLENV_B=new", "not-a-variable"})
	t.Cleanup(func() { os.Unsetenv("AGENTMAN_SHELLENV_B") })
	if os.Getenv("AGENTMAN_SHELLENV_A") != "after" || os.Getenv("AGENTMAN_SHELLENV_B") != "new" {
		t.Fatalf("not applied: %q %q", os.Getenv("AGENTMAN_SHELLENV_A"), os.Getenv("AGENTMAN_SHELLENV_B"))
	}
	if !slices.Equal(set, []string{"AGENTMAN_SHELLENV_A", "AGENTMAN_SHELLENV_B"}) {
		t.Fatalf("set %q", set)
	}
}
