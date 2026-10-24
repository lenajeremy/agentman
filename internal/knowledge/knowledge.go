// Package knowledge is the user's notes for their agents: style guides,
// conventions, how things are done. Each is a Markdown file in
// ~/.agentman/knowledge, in one of two tiers:
//
//   - shared: any agent may read it, through am mcp, and a note marked
//     "always" can be written into a project's instruction file;
//   - private: only Agentman reads it. Agents get what Agentman chooses to
//     pass on.
//
// A note can open with a small header for its title and whether it always
// goes into instructions:
//
//	---
//	title: Commit style
//	always: true
//	---
package knowledge

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Tiers.
const (
	Shared  = "shared"
	Private = "private"
)

// Note is one note in full.
type Note struct {
	// Name is its file name without .md: lowercase letters, digits, - and _.
	Name   string `json:"name"`
	Tier   string `json:"tier"`
	Title  string `json:"title"`
	Always bool   `json:"always,omitempty"`
	Body   string `json:"body"`
	// UpdatedAt is when it was last written, in milliseconds.
	UpdatedAt int64 `json:"updatedAt"`
}

// Hit is a note that matches a search, with the lines around the match.
type Hit struct {
	Name    string `json:"name"`
	Tier    string `json:"tier"`
	Title   string `json:"title"`
	Snippet string `json:"snippet"`
	score   int
}

// ErrNotFound is a note that does not exist.
var ErrNotFound = errors.New("knowledge: no such note")

// MaxNoteBytes bounds one note, so one cannot crowd out a conversation.
const MaxNoteBytes = 64 << 10

var validName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// Store is the notes on disk.
type Store struct {
	dir string
}

// Open keeps notes under dir, ~/.agentman/knowledge.
func Open(dir string) *Store {
	return &Store{dir: dir}
}

// Slug turns a title into a note name.
func Slug(title string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(title) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case !dash && b.Len() > 0:
			b.WriteByte('-')
			dash = true
		}
		if b.Len() >= 60 {
			break
		}
	}
	return strings.Trim(b.String(), "-")
}

func (s *Store) path(tier, name string) (string, error) {
	if tier != Shared && tier != Private {
		return "", fmt.Errorf("knowledge: the tier is %q or %q", Shared, Private)
	}
	if !validName.MatchString(name) {
		return "", fmt.Errorf("knowledge: %q isn't a note name; use lowercase letters, digits, - and _", name)
	}
	return filepath.Join(s.dir, tier, name+".md"), nil
}

// List is every note, without bodies, newest first. Empty tier lists both.
func (s *Store) List(tier string) ([]Note, error) {
	var notes []Note
	for _, t := range []string{Shared, Private} {
		if tier != "" && tier != t {
			continue
		}
		entries, err := os.ReadDir(filepath.Join(s.dir, t))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			name, ok := strings.CutSuffix(entry.Name(), ".md")
			if !ok || entry.IsDir() || !validName.MatchString(name) {
				continue
			}
			note, err := s.Read(t, name)
			if err != nil {
				continue
			}
			note.Body = ""
			notes = append(notes, note)
		}
	}
	sort.Slice(notes, func(i, j int) bool { return notes[i].UpdatedAt > notes[j].UpdatedAt })
	return notes, nil
}

// Read is one note. Empty tier looks in shared, then private.
func (s *Store) Read(tier, name string) (Note, error) {
	if tier == "" {
		if note, err := s.Read(Shared, name); err == nil {
			return note, nil
		}
		return s.Read(Private, name)
	}
	path, err := s.path(tier, name)
	if err != nil {
		return Note{}, err
	}
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return Note{}, ErrNotFound
	}
	if err != nil {
		return Note{}, err
	}
	if info.Size() > MaxNoteBytes {
		return Note{}, fmt.Errorf("knowledge: %s is larger than %d KB", name, MaxNoteBytes>>10)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Note{}, err
	}
	note := parse(string(raw))
	note.Name, note.Tier, note.UpdatedAt = name, tier, info.ModTime().UnixMilli()
	if note.Title == "" {
		note.Title = name
	}
	return note, nil
}

// Write saves a note, moving it if its tier changed. The name comes from
// the title when it is empty.
func (s *Store) Write(note Note) (Note, error) {
	note.Title = strings.TrimSpace(note.Title)
	if note.Name == "" {
		note.Name = Slug(note.Title)
	}
	if note.Title == "" {
		note.Title = note.Name
	}
	if strings.ContainsAny(note.Title, "\n\r") {
		return Note{}, errors.New("knowledge: a title is one line")
	}
	path, err := s.path(note.Tier, note.Name)
	if err != nil {
		return Note{}, err
	}
	raw := format(note)
	if len(raw) > MaxNoteBytes {
		return Note{}, fmt.Errorf("knowledge: a note is at most %d KB", MaxNoteBytes>>10)
	}
	if !utf8.ValidString(raw) {
		return Note{}, errors.New("knowledge: a note is text")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return Note{}, err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(raw), 0o600); err != nil {
		return Note{}, err
	}
	if err := os.Rename(tmp, path); err != nil {
		return Note{}, err
	}
	// The same name in the other tier is the note before it moved.
	other := Private
	if note.Tier == Private {
		other = Shared
	}
	if old, err := s.path(other, note.Name); err == nil {
		_ = os.Remove(old)
	}
	return s.Read(note.Tier, note.Name)
}

// Delete removes a note.
func (s *Store) Delete(tier, name string) error {
	path, err := s.path(tier, name)
	if err != nil {
		return err
	}
	if err := os.Remove(path); errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	return nil
}

// Search finds notes whose title or body has the query's words, best
// first. Empty tier searches both.
func (s *Store) Search(query, tier string, limit int) ([]Hit, error) {
	words := strings.Fields(strings.ToLower(query))
	if len(words) == 0 {
		return nil, errors.New("knowledge: search for something")
	}
	notes, err := s.List(tier)
	if err != nil {
		return nil, err
	}
	var hits []Hit
	for _, meta := range notes {
		note, err := s.Read(meta.Tier, meta.Name)
		if err != nil {
			continue
		}
		title, body := strings.ToLower(note.Title), strings.ToLower(note.Body)
		score := 0
		for _, word := range words {
			if strings.Contains(title, word) || strings.Contains(note.Name, word) {
				score += 3
			}
			score += min(strings.Count(body, word), 5)
		}
		if score == 0 {
			continue
		}
		hits = append(hits, Hit{Name: note.Name, Tier: note.Tier, Title: note.Title, Snippet: snippet(note.Body, words), score: score})
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].score > hits[j].score })
	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

// snippet is the lines around the first line that has a word.
func snippet(body string, words []string) string {
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		lower := strings.ToLower(line)
		for _, word := range words {
			if strings.Contains(lower, word) {
				from, to := max(0, i-1), min(len(lines), i+2)
				return clip(strings.TrimSpace(strings.Join(lines[from:to], "\n")), 300)
			}
		}
	}
	return clip(strings.TrimSpace(body), 300)
}

func clip(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut] + "…"
}

// parse reads a note's header, if it has one, and its body.
func parse(raw string) Note {
	var note Note
	rest, ok := strings.CutPrefix(raw, "---\n")
	if !ok {
		note.Body = raw
		return note
	}
	header, body, ok := strings.Cut(rest, "\n---\n")
	if !ok {
		note.Body = raw
		return note
	}
	for _, line := range strings.Split(header, "\n") {
		key, value, _ := strings.Cut(line, ":")
		value = strings.TrimSpace(value)
		switch strings.TrimSpace(key) {
		case "title":
			note.Title = value
		case "always":
			note.Always = value == "true" || value == "yes"
		}
	}
	note.Body = strings.TrimPrefix(body, "\n")
	return note
}

func format(note Note) string {
	var b strings.Builder
	b.WriteString("---\ntitle: " + note.Title + "\n")
	if note.Always {
		b.WriteString("always: true\n")
	}
	b.WriteString("---\n\n")
	b.WriteString(strings.TrimSpace(note.Body))
	b.WriteString("\n")
	return b.String()
}

// Touch is for tests: it sets a note's time.
func (s *Store) Touch(tier, name string, at time.Time) error {
	path, err := s.path(tier, name)
	if err != nil {
		return err
	}
	return os.Chtimes(path, at, at)
}
