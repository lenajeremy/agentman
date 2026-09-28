package main

import (
	"reflect"
	"testing"
)

// Every agent can reopen a session by id and every one spells it differently.
// `am <agent> --resume <id>` has to mean the same thing for all of them, or
// the phone cannot send one request and the person has to remember four.
func TestResumeFlagSpeaksEachAgentsOwnDialect(t *testing.T) {
	cases := []struct {
		agent string
		args  []string
		want  []string
	}{
		{"claude", []string{"--resume", "abc"}, []string{"--resume", "abc"}},
		{"codex", []string{"--resume", "abc"}, []string{"resume", "abc"}},
		{"kiro", []string{"--resume", "abc"}, []string{"--resume-id", "abc"}},
		{"antigravity", []string{"--resume", "abc"}, []string{"--conversation", "abc"}},
		{"cursor", []string{"--resume", "abc"}, []string{"--resume", "abc"}},
		// Flags on either side are preserved in order.
		{"codex", []string{"--model", "o3", "--resume", "abc", "hello"},
			[]string{"--model", "o3", "resume", "abc", "hello"}},
	}
	for _, c := range cases {
		got, err := resolveResumeFlag(c.agent, c.args)
		if err != nil {
			t.Errorf("%s: %v", c.agent, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %q, want %q", c.agent, got, c.want)
		}
	}
}

// A bare --resume is a request for that CLI's own picker, which is a
// reasonable thing to want from a terminal and not ours to intercept.
func TestBareResumeIsLeftForTheCLIsOwnPicker(t *testing.T) {
	for _, args := range [][]string{
		{"--resume"},
		{"--resume", "--model", "o3"},
	} {
		got, err := resolveResumeFlag("codex", args)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, args) {
			t.Errorf("rewrote a bare picker request: got %q, want %q", got, args)
		}
	}
}

func TestResumeIsRefusedForAgentsThatCannotDoIt(t *testing.T) {
	if _, err := resolveResumeFlag("opencode", []string{"--resume", "abc"}); err == nil {
		t.Error("accepted --resume for OpenCode, whose sessions live in a running server")
	}
}
