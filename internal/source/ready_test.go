package source

import "testing"

// A launch types its first message in only when the agent is idle at its
// prompt. Typing into an open menu is the failure this guards against: the
// keystrokes land in the menu, and Enter can approve whatever row is focused.
func TestFirstPromptWaitsForTheIdlePrompt(t *testing.T) {
	cases := []struct {
		name  string
		ready func(string) bool
		pane  string
		want  bool
	}{
		{"kiro idle", KiroReadyForInput,
			"kiro_default · auto · ◔ 1%    /work\n›  ask a question or describe a task ↵\n", true},
		{"kiro working", KiroReadyForInput,
			"kiro_default · auto · ◔ 1%    /work\n›  Kiro is working · 5s · Type to steer · Ctrl+S to queue\n", false},
		{"kiro approval open", KiroReadyForInput,
			" shell requires approval\n ❯ Yes, single permission\n   No (Tab to edit)\n" +
				" esc to close · ↑↓ to navigate · ↵ to select · Tab to edit\n", false},
		{"kiro still starting", KiroReadyForInput, "Loading…\n", false},

		{"antigravity idle", AntigravityReadyForInput,
			">\n────\n? for shortcuts                         Gemini 3.8 Flash · high\n", true},
		{"antigravity working", AntigravityReadyForInput,
			">\n────\nesc to cancel                           Gemini 3.8 Flash · high\n", false},
		{"antigravity trust prompt", AntigravityReadyForInput,
			"Accessing workspace:\n/work\nDo you trust the contents of this project?\n" +
				"> Yes, I trust this folder\n  No, exit\n  ↑/↓ Navigate · enter Confirm\n", false},
		{"antigravity still starting", AntigravityReadyForInput, "\n", false},
	}
	for _, tc := range cases {
		if got := tc.ready(tc.pane); got != tc.want {
			t.Errorf("%s: ready = %v, want %v", tc.name, got, tc.want)
		}
	}
}
