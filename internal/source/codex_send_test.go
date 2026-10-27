package source

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/tmux"
)

// codexSink is a Codex pane stand-in that records every byte typed into it,
// with bracketed paste on as Codex's composer has it.
func codexSink(t *testing.T, name string) (*CodexSource, string) {
	t.Helper()
	out := filepath.Join(t.TempDir(), "typed")
	ctx := context.Background()
	if err := tmux.Launch(ctx, name, t.TempDir(), []string{"sh", "-c", `printf '\033[?2004h'; stty raw -echo; cat > ` + out}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tmux.Kill(context.Background(), name) })
	// The pane's shell takes a moment to switch the terminal to raw.
	time.Sleep(300 * time.Millisecond)
	s := &CodexSource{
		held:        newCodexHeld(),
		capturePane: func(context.Context, string) (string, error) { return "› Ask Codex to do anything", nil },
		sessions: map[string]codexSession{"codex:s1": {
			tmuxName: name, meta: protocol.Session{ID: "codex:s1", State: protocol.StateIdle},
		}},
	}
	return s, out
}

func typedInto(t *testing.T, out string) string {
	t.Helper()
	var typed string
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		data, _ := os.ReadFile(out)
		if typed = string(data); strings.HasSuffix(typed, "\r") {
			break
		}
	}
	return typed
}

// Codex 0.162 makes a picture's path an attachment ("[Image #1]") only when
// it arrives as a paste of its own; typed ahead of the words it was words.
func TestCodexPicturesArePastedOnTheirOwnAheadOfTheWords(t *testing.T) {
	s, out := codexSink(t, "agentman-codex-pictures")
	mode, err := s.InjectWithAttachments(context.Background(), "codex:s1", "what changed?",
		[]string{"/Users/me/.agentman/images/codex-s1/a.png", "/Users/me/Shots/Screen Shot.png"})
	if err != nil || mode != protocol.InjectTmux {
		t.Fatalf("mode %q, err %v", mode, err)
	}
	want := "\x15" +
		"\x1b[200~/Users/me/.agentman/images/codex-s1/a.png\x1b[201~" +
		"\x1b[200~'/Users/me/Shots/Screen Shot.png'\x1b[201~" +
		"what changed?\r"
	if got := typedInto(t, out); got != want {
		t.Fatalf("typed %q\nwant  %q", got, want)
	}
}

func TestCodexPicturesAloneAreSent(t *testing.T) {
	s, out := codexSink(t, "agentman-codex-picture-alone")
	if _, err := s.InjectWithAttachments(context.Background(), "codex:s1", "", []string{"/tmp/a.png"}); err != nil {
		t.Fatal(err)
	}
	if got := typedInto(t, out); got != "\x15\x1b[200~/tmp/a.png\x1b[201~\r" {
		t.Fatalf("typed %q", got)
	}
}

// Ending on an @path left Codex's file menu open, and Enter inserted the file
// rather than sending: the message sat in the composer, unsent.
func TestCodexMessageEndingOnAFileIsSent(t *testing.T) {
	s, out := codexSink(t, "agentman-codex-mention")
	if _, err := s.Inject(context.Background(), "codex:s1", "explain @main.go"); err != nil {
		t.Fatal(err)
	}
	if got := typedInto(t, out); got != "\x15explain @main.go \r" {
		t.Fatalf("typed %q", got)
	}
}

func TestCodexTypedClosesTheFileMenu(t *testing.T) {
	cases := map[string]string{
		"explain @main.go":                 "explain @main.go ",
		"look at\n\n@/Users/me/report.pdf": "look at\n\n@/Users/me/report.pdf ",
		"explain @main.go ":                "explain @main.go ",
		"compare @a.go with @b.go please":  "compare @a.go with @b.go please",
		"email me@example.com":             "email me@example.com",
		"$imagegen":                        "$imagegen ",
		"draw a cat with $imagegen":        "draw a cat with $imagegen ",
		"$imagegen draw a cat":             "$imagegen draw a cat",
		"":                                 "",
	}
	for in, want := range cases {
		if got := codexTyped(in); got != want {
			t.Errorf("codexTyped(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCodexPastedPathIsQuotedOnlyForASpace(t *testing.T) {
	cases := map[string]string{
		"/tmp/a.png":             "/tmp/a.png",
		"/tmp/Screen Shot.png":   "'/tmp/Screen Shot.png'",
		"/tmp/it's here/a b.png": "/tmp/it's here/a b.png",
	}
	for in, want := range cases {
		if got := codexPastedPath(in); got != want {
			t.Errorf("codexPastedPath(%q) = %q, want %q", in, got, want)
		}
	}
}

// A Codex session Agentman can't type into takes no pictures either.
func TestCodexPicturesNeedAPane(t *testing.T) {
	s := &CodexSource{sessions: map[string]codexSession{"codex:s1": {meta: protocol.Session{ID: "codex:s1"}}}}
	if _, err := s.InjectWithAttachments(context.Background(), "codex:s1", "look", []string{"/tmp/a.png"}); !errors.Is(err, ErrAttachmentsAsPaths) {
		t.Fatalf("err = %v", err)
	}
}

// A message with pictures sent into a running turn is read when the rollout
// records it: the pictures as attachments, their marks in the words.
func TestHeldPictureMessageIsReadByItsAttachments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	held := newCodexHeld()
	held.add("codex:a", path, "/Users/me/.agentman/images/codex-a/a.png what is this?")
	text := "[Image #1]  what is this?"
	line, err := json.Marshal(map[string]any{
		"type": "event_msg",
		"payload": map[string]any{"type": "item_completed", "item": map[string]any{
			"type": "UserMessage", "id": "u1",
			"content": []any{
				map[string]any{"type": "local_image", "path": "/Users/me/.agentman/images/codex-a/a.png"},
				map[string]any{"type": "text", "text": text, "text_elements": []any{map[string]any{
					"byte_range": map[string]any{"start": 0, "end": 10}, "placeholder": "[Image #1]"}}},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	appendTranscript(t, path, string(line)+"\n")
	if got := held.pending("codex:a", path, true); got != nil {
		t.Fatalf("pending = %q", got)
	}
}

// Enter confirms Codex's trust prompt and pickers; its approvals confirm on
// a number, and an Enter after one would land on whatever follows.
func TestCodexMenuWaitsForEnterByItsFooter(t *testing.T) {
	cases := map[string]bool{
		"codex_trust_folder_real_pane.txt": true,
		"codex_model_picker_real_pane.txt": true,
		"codex_approval_real_pane.txt":     false,
	}
	for fixture, want := range cases {
		pane, err := os.ReadFile(filepath.Join("..", "question", "testdata", fixture))
		if err != nil {
			t.Fatal(err)
		}
		if got := codexMenuWaitsForEnter(string(pane)); got != want {
			t.Errorf("%s: %v, want %v", fixture, got, want)
		}
	}
}
