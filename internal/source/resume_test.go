package source

import (
	"reflect"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Every CLI can reopen one of its own sessions and every one spells it
// differently. Keeping the four spellings in one place is what lets the
// phone, the daemon and `am <agent> --resume` all mean the same thing.
func TestResumeArgsSpeaksEachAgentsOwnDialect(t *testing.T) {
	cases := map[protocol.Kind][]string{
		protocol.KindClaude:      {"--resume", "abc"},
		protocol.KindCodex:       {"resume", "abc"},
		protocol.KindCursorCLI:   {"--resume", "abc"},
		protocol.KindKiro:        {"--resume-id", "abc"},
		protocol.KindAntigravity: {"--conversation", "abc"},
	}
	for kind, want := range cases {
		got, ok := ResumeArgs(kind, "abc")
		if !ok {
			t.Errorf("%s cannot resume, but it can", kind)
			continue
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %q, want %q", kind, got, want)
		}
	}
}

// OpenCode's sessions live in a running server and take prompts over its API,
// so there is no dead session to revive. The Cursor IDE has no CLI at all.
func TestResumeArgsRefusesWhatCannotBeReopened(t *testing.T) {
	for _, kind := range []protocol.Kind{protocol.KindOpenCode, protocol.KindCursor} {
		if _, ok := ResumeArgs(kind, "abc"); ok {
			t.Errorf("%s reported resumable", kind)
		}
		if CanResume(kind) {
			t.Errorf("CanResume(%s) = true", kind)
		}
	}
}

// An empty id would resume "the most recent session in this directory" for
// several of these CLIs, which is a different session than the one asked for.
func TestResumeArgsRefusesAnEmptyID(t *testing.T) {
	if _, ok := ResumeArgs(protocol.KindClaude, ""); ok {
		t.Error("accepted an empty session id; the CLI would pick its own session")
	}
}
