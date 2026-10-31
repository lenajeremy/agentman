package source

import (
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
)

var testSkills = []protocol.Command{
	{Name: "frontend-design", Skill: true},
	{Name: "review", Skill: true},
	{Name: "compact"}, // a command, not a skill
	{Name: "imagegen", Prefix: "$", Skill: true},
}

func TestEachAgentGetsTheSkillsItIsToldToUseInItsOwnForm(t *testing.T) {
	cases := []struct {
		kind protocol.Kind
		in   string
		want string
		ids  string
	}{
		// Claude and Cursor: in place, a space after.
		{protocol.KindClaude, "Use /frontend-design, then ship.", "Use /frontend-design , then ship.", ""},
		{protocol.KindClaude, "Use /frontend-design to fix it", "Use /frontend-design to fix it", ""},
		{protocol.KindCursorCLI, "Fix it with /review.", "Fix it with /review .", ""},
		// Codex: its $ skills, in place.
		{protocol.KindCodex, "Draw it with $imagegen.", "Draw it with $imagegen .", ""},
		{protocol.KindCodex, "Draw it with /imagegen", "Draw it with /imagegen", ""},
		// Antigravity: at the front, in words where it was.
		{protocol.KindAntigravity, "Use /frontend-design to fix the header", "/frontend-design Use frontend-design to fix the header", ""},
		{protocol.KindAntigravity, "/review then /frontend-design it", "/review /frontend-design then frontend-design it", ""},
		{protocol.KindAntigravity, "/review", "/review", ""},
		// Kiro: in words.
		{protocol.KindKiro, "Use /frontend-design to fix the header.", "Use the frontend-design skill to fix the header.", ""},
		{protocol.KindKiro, "/review the login page", "Use the review skill. the login page", ""},
		// OpenCode: in words, and the ids beside.
		{protocol.KindOpenCode, "Polish it with /frontend-design and /review.", "Polish it with the frontend-design skill and the review skill.", "frontend-design review"},
		// Not skills: commands, paths, unknown names, a slash in a word.
		{protocol.KindAntigravity, "Run /compact and read /usr/bin and /nope and a/review", "Run /compact and read /usr/bin and /nope and a/review", ""},
	}
	for _, c := range cases {
		got, ids := SkillText(c.kind, c.in, testSkills)
		if got != c.want || strings.Join(ids, " ") != c.ids {
			t.Errorf("%s %q:\n got  %q %v\n want %q %q", c.kind, c.in, got, ids, c.want, c.ids)
		}
	}
}
