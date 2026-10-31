package source

import (
	"regexp"
	"strings"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// A message can name a skill anywhere: "Use /frontend-design to fix the
// header", "$imagegen". Each agent takes a skill named mid-sentence its own
// way (checked 2026-10-31 on Claude 2.1.316, Codex 0.164, Cursor 2026.10.22,
// OpenCode 2.2, Antigravity 1.4.12 and Kiro 2.37), so the daemon sends it in
// that agent's form:
//
//   - Claude, Cursor: "/name" where it is, with a space after it. Claude
//     points the model at a skill so named only when whitespace follows it,
//     and runs a skill the person alone may start only then; Cursor attaches
//     a "/name" after a space anywhere.
//   - Codex: "$name" where it is, a space after it; Codex attaches it.
//   - Antigravity: "/name" at the front, where alone agy attaches it, and
//     its name in words where it was.
//   - Kiro: "the name skill" in words. A leading "/name" swaps itself for
//     the skill's text with nothing to say it was asked for, and Kiro
//     followed the rest of the message instead.
//   - OpenCode: "the name skill" in words, and the skill's id beside the
//     message, which OpenCode's service attaches; it reads nothing in text.

var skillWord = regexp.MustCompile(`(^|\s)([/$][A-Za-z0-9][A-Za-z0-9._:\-]*)`)

// skillMention is one skill a message names, and where.
type skillMention struct {
	start, end int // byte offsets of the word, prefix included
	name       string
}

// namedSkills finds the skills a message names: a skill's prefix and name,
// at the start or after a space, punctuation after it not part of it.
func namedSkills(text string, commands []protocol.Command) []skillMention {
	known := map[string]string{}
	for _, c := range commands {
		if !c.Skill {
			continue
		}
		prefix := c.Prefix
		if prefix == "" {
			prefix = "/"
		}
		known[prefix+c.Name] = c.Name
	}
	if len(known) == 0 {
		return nil
	}
	var found []skillMention
	for _, m := range skillWord.FindAllStringSubmatchIndex(text, -1) {
		start := m[4]
		word := text[start:m[5]]
		for word != "" {
			if name, ok := known[word]; ok {
				found = append(found, skillMention{start: start, end: start + len(word), name: name})
				break
			}
			last := word[len(word)-1]
			if !strings.ContainsRune(".,;:!?)]}'\"", rune(last)) {
				break
			}
			word = word[:len(word)-1]
		}
	}
	return found
}

// SkillText is a message as an agent of this kind takes the skills it
// names, and, for OpenCode, the ids of the skills to send beside it.
func SkillText(kind protocol.Kind, text string, commands []protocol.Command) (string, []string) {
	mentions := namedSkills(text, commands)
	if len(mentions) == 0 {
		return text, nil
	}
	switch kind {
	case protocol.KindClaude, protocol.KindCursorCLI, protocol.KindCodex:
		return spacedAfter(text, mentions), nil
	case protocol.KindAntigravity:
		return leadingSkills(text, mentions), nil
	case protocol.KindKiro:
		return skillsInWords(text, mentions), nil
	case protocol.KindOpenCode:
		ids := make([]string, 0, len(mentions))
		seen := map[string]bool{}
		for _, m := range mentions {
			if !seen[m.name] {
				seen[m.name] = true
				ids = append(ids, m.name)
			}
		}
		return skillsInWords(text, mentions), ids
	}
	return text, nil
}

// spacedAfter puts a space after each named skill that punctuation follows.
func spacedAfter(text string, mentions []skillMention) string {
	var b strings.Builder
	at := 0
	for _, m := range mentions {
		b.WriteString(text[at:m.end])
		if m.end < len(text) && !isSpace(text[m.end]) {
			b.WriteByte(' ')
		}
		at = m.end
	}
	b.WriteString(text[at:])
	return b.String()
}

// leadingSkills puts each named skill at the front as "/name", in the
// order named, and its name in words where it was. Skills already leading
// the message stay as they are.
func leadingSkills(text string, mentions []skillMention) string {
	var lead []string
	seen := map[string]bool{}
	var b strings.Builder
	at := 0
	leading := true
	for _, m := range mentions {
		between := text[at:m.start]
		if leading && strings.TrimSpace(between) == "" {
			// Already at the front: it stays there.
			if !seen[m.name] {
				seen[m.name] = true
				lead = append(lead, text[m.start:m.end])
			}
			at = m.end
			continue
		}
		leading = false
		if !seen[m.name] {
			seen[m.name] = true
			lead = append(lead, text[m.start:m.start+1]+m.name)
		}
		b.WriteString(between)
		b.WriteString(m.name)
		at = m.end
	}
	b.WriteString(text[at:])
	rest := strings.TrimLeft(b.String(), " \t")
	if rest == "" {
		return strings.Join(lead, " ")
	}
	return strings.Join(lead, " ") + " " + rest
}

// skillsInWords writes each named skill as "the name skill"; one leading
// the message becomes "Use the name skill." before the rest.
func skillsInWords(text string, mentions []skillMention) string {
	var b strings.Builder
	at := 0
	for i, m := range mentions {
		between := text[at:m.start]
		if i == 0 && strings.TrimSpace(between) == "" {
			b.WriteString("Use the " + m.name + " skill.")
			at = m.end
			continue
		}
		b.WriteString(between)
		b.WriteString("the " + m.name + " skill")
		at = m.end
	}
	b.WriteString(text[at:])
	return b.String()
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }
