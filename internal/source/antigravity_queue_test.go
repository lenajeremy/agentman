package source

import (
	"context"
	"slices"
	"strings"
	"testing"
)

// Shaped like agy 1.4.2's pane and log while it holds a message sent mid-turn,
// as captured on 2026-10-19 (texts replaced).
const antigravityHoldingPane = `
────────────────────────────────────────────────────────────
> Write a short story about a lighthouse keeper.
⣟  Generating...
└ Tip: You can switch conversations with /resume.
▸ Also give it a title. (alt+m to send immediately)
▸ And keep it under 300 words.
──────────────────────────────────────────────────────────────────────────────
>
──────────────────────────────────────────────────────────────────────────────
  Press up to edit queued messages                                                Gemini 4 Argon · high`

const antigravityIdlePane = `
> Write a short story about a lighthouse keeper.
  The keeper climbed the stairs…
──────────────────────────────────────────────────────────────────────────────
>
──────────────────────────────────────────────────────────────────────────────
? for shortcuts                                                                   Gemini 4 Argon · high`

func paneLines(pane string) []string { return strings.Split(strings.TrimRight(pane, "\n"), "\n") }

func TestAntigravityPaneShowsHeldMessagesAndTheirKey(t *testing.T) {
	shown, texts, key := antigravityPaneQueue(paneLines(antigravityHoldingPane))
	if !shown || key != "M-m" {
		t.Fatalf("shown = %v, key = %q", shown, key)
	}
	if !slices.Equal(texts, []string{"Also give it a title.", "And keep it under 300 words."}) {
		t.Fatalf("texts = %q", texts)
	}
	if shown, texts, _ := antigravityPaneQueue(paneLines(antigravityIdlePane)); shown || texts != nil {
		t.Fatalf("idle pane: shown = %v, texts = %q", shown, texts)
	}
	rebound := strings.Replace(antigravityHoldingPane, "alt+m", "ctrl+q", 1)
	if _, _, key := antigravityPaneQueue(paneLines(rebound)); key != "C-q" {
		t.Fatalf("rebound key = %q", key)
	}
}

func TestAntigravityLogFollowsHeldMessages(t *testing.T) {
	log := strings.Join([]string{
		`I1019 23:32:02.170794 940 input_loop.go:106] HandleUserInput called with text: "Write a story."`,
		`I1019 23:32:02.185458 555 conversation_manager.go:783] Forwarding user message to conversation b760ab74-6ae1-4696-bff9-d905fc3f00ae (items=1, media=0)`,
		`I1019 23:32:06.506950 1499 input_loop.go:106] HandleUserInput called with text: "Also give it a title."`,
		`I1019 23:32:08.000000 1499 input_loop.go:106] HandleUserInput called with text: "And keep it \"short\".\nThanks"`,
	}, "\n")
	if got := antigravityLogQueue(log); !slices.Equal(got, []string{"Also give it a title.", "And keep it \"short\".\nThanks"}) {
		t.Fatalf("held = %q", got)
	}
	// Send now: everything goes into the running turn.
	sent := log + "\n" + `I1019 23:32:21.122849 1777 input_loop.go:1255] Forwarding mid-turn user message immediately to conversation b760ab74-6ae1-4696-bff9-d905fc3f00ae (items=1, media=0)`
	if got := antigravityLogQueue(sent); got != nil {
		t.Fatalf("held after send now = %q", got)
	}
	// The turn ends: agy sends the oldest held ones together.
	flushed := log + "\n" +
		`I1019 23:33:00.000000 1 input_loop.go:900] Auto-flush: sending 1 queued input(s) (combinedLength=21)` + "\n" +
		`I1019 23:33:00.010000 1 conversation_manager.go:783] Forwarding user message to conversation b760ab74-6ae1-4696-bff9-d905fc3f00ae (items=1, media=0)`
	if got := antigravityLogQueue(flushed); !slices.Equal(got, []string{"And keep it \"short\".\nThanks"}) {
		t.Fatalf("held after a flush of one = %q", got)
	}
}

func TestAntigravityQueuedPrefersTheLogsFullText(t *testing.T) {
	log := strings.Join([]string{
		`… input_loop.go:106] HandleUserInput called with text: "/resume"`,
		`… input_loop.go:106] HandleUserInput called with text: "A long message the pane cuts short at its width"`,
	}, "\n")
	pane := []string{"A long message the pane cuts sh"}
	if got := antigravityQueued(log, pane); !slices.Equal(got, []string{"A long message the pane cuts short at its width"}) {
		t.Fatalf("queued = %q", got)
	}
	// The log accounts for fewer than the pane shows: trust the pane.
	if got := antigravityQueued("", []string{"one", "two"}); !slices.Equal(got, []string{"one", "two"}) {
		t.Fatalf("queued without a log = %q", got)
	}
}

func TestAntigravityPromptMustBeEmptyForSendNow(t *testing.T) {
	if !antigravityPromptEmpty(paneLines(antigravityHoldingPane)) {
		t.Fatal("empty prompt read as holding text")
	}
	typing := strings.Replace(antigravityHoldingPane, "──\n>\n──", "──\n> half a sentence\n──", 1)
	if antigravityPromptEmpty(paneLines(typing)) {
		t.Fatal("a prompt with text read as empty")
	}
	if antigravityPromptEmpty([]string{"no rules here"}) {
		t.Fatal("a pane without a prompt box read as empty")
	}
}

func TestAntigravitySendNowRefusesWhenNothingCanBeSent(t *testing.T) {
	for name, pane := range map[string]string{
		"idle":   antigravityIdlePane,
		"typing": strings.Replace(antigravityHoldingPane, "──\n>\n──", "──\n> half a sentence\n──", 1),
	} {
		s := &AntigravitySource{
			sessions:    map[string]antigravitySession{"antigravity:a": {tmuxName: "agentman-antigravity-1-a"}},
			capturePane: func(context.Context, string) (string, error) { return pane, nil },
		}
		if err := s.SendQueued(context.Background(), "antigravity:a"); err == nil {
			t.Errorf("%s: SendQueued pressed a key", name)
		}
	}
	s := &AntigravitySource{sessions: map[string]antigravitySession{"antigravity:b": {}}}
	if err := s.SendQueued(context.Background(), "antigravity:b"); err == nil {
		t.Error("SendQueued for a session without a pane did not fail")
	}
}

func TestAntigravitySendKeyWhenTheHintIsOffThePane(t *testing.T) {
	// A long held message pushes the hint past the pane's edge, as on a real
	// 80-column pane on 2026-10-20.
	long := strings.Replace(antigravityHoldingPane,
		"▸ Also give it a title. (alt+m to send immediately)",
		"▸ Also give it a title, and make the ending less abrupt than the draft you just wro", 1)
	if shown, _, key := antigravityPaneQueue(paneLines(long)); !shown || key != "M-m" {
		t.Fatalf("shown = %v, key = %q, want agy's default", shown, key)
	}
	// After a send-now they are already going: nothing to press.
	sending := strings.Replace(antigravityHoldingPane, "(alt+m to send immediately)", "\n  ⎿  Sending to agent...", 1)
	if shown, _, key := antigravityPaneQueue(paneLines(sending)); !shown || key != "" {
		t.Fatalf("while sending: shown = %v, key = %q", shown, key)
	}
}
