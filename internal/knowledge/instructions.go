package knowledge

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// The block Agentman keeps in a project's instruction file. Everything
// between the markers is Agentman's, rewritten from the shared notes marked
// "always"; everything outside them is left exactly as it was.
const (
	beginMarker = "<!-- agentman:begin -->"
	endMarker   = "<!-- agentman:end -->"
)

// InstructionFile is the file an agent of kind reads its project
// instructions from: CLAUDE.md for Claude Code, AGENTS.md for the rest.
func InstructionFile(dir, kind string) string {
	if kind == "claude" {
		return filepath.Join(dir, "CLAUDE.md")
	}
	return filepath.Join(dir, "AGENTS.md")
}

// SyncInstructions writes the shared notes marked "always" into the
// instruction file for an agent of kind working in dir, and says which file
// and how many notes. With no such notes the block is taken out, and a file
// that held only the block is left empty rather than deleted.
func (s *Store) SyncInstructions(dir, kind string) (string, int, error) {
	if !filepath.IsAbs(dir) {
		return "", 0, errors.New("knowledge: the project folder must be an absolute path")
	}
	listed, err := s.List(Shared)
	if err != nil {
		return "", 0, err
	}
	var always []Note
	for _, meta := range listed {
		if !meta.Always {
			continue
		}
		note, err := s.Read(Shared, meta.Name)
		if err == nil {
			always = append(always, note)
		}
	}
	path := InstructionFile(dir, kind)
	current, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", 0, err
	}
	if errors.Is(err, os.ErrNotExist) && len(always) == 0 {
		return path, 0, nil
	}
	next := withBlock(string(current), block(always))
	if next == string(current) {
		return path, len(always), nil
	}
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	return path, len(always), os.WriteFile(path, []byte(next), mode)
}

// block is the managed block for notes, or nothing for none.
func block(notes []Note) string {
	if len(notes) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(beginMarker + "\n")
	b.WriteString("## From Agentman\n\n")
	b.WriteString("Kept by Agentman from the user's shared notes. Edits here are replaced; change the notes in Agentman instead.\n")
	for _, note := range notes {
		b.WriteString("\n### " + note.Title + "\n\n")
		b.WriteString(strings.TrimSpace(note.Body) + "\n")
	}
	b.WriteString(endMarker + "\n")
	return b.String()
}

// withBlock puts block in text in place of the old one, or at the end.
func withBlock(text, block string) string {
	start := strings.Index(text, beginMarker)
	end := strings.Index(text, endMarker)
	if start >= 0 && end > start {
		end += len(endMarker)
		if end < len(text) && text[end] == '\n' {
			end++
		}
		return text[:start] + block + text[end:]
	}
	if block == "" {
		return text
	}
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	if text != "" {
		text += "\n"
	}
	return text + block
}
