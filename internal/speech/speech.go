// Package speech reads an agent's answer out loud.
//
// The point is to let you look away. Agentman already tells your phone a turn
// has finished; this tells you what the turn said, so a long build or a slow
// review does not require watching a terminal to know where it got to.
//
// Two rules shape everything here. It must never block the hook — a hook that
// is slow makes the agent slow, and audio is not worth that. And it must never
// fail loudly: if the network is down or the endpoint is unhappy, the right
// outcome is silence, not a broken workflow.
package speech

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// DefaultEndpoint is Celery's speech service.
const DefaultEndpoint = "https://celery-api-production-150e.up.railway.app/v1/speak"

// DefaultVoice is a neutral one; the delivery is directed by instructions
// rather than by picking a character.
const DefaultVoice = "nova"

// DefaultInstructions describe how an agent's answer should sound: informative
// rather than performed, because it will be heard many times a day.
const DefaultInstructions = "Read this like a colleague giving a quick status update: " +
	"calm, brisk, and matter-of-fact. Do not dramatise."

// maxSpoken caps what is sent. The server allows more, but an agent's answer
// read out in full becomes a monologue nobody listens to; the opening is where
// the useful part almost always is.
const maxSpoken = 600

// Config is the speech section of ~/.agentman/config.json.
type Config struct {
	// Enabled is opt-in on purpose. Audio that starts without being asked for
	// is a bad surprise, especially for someone in an office.
	Enabled bool `json:"enabled"`
	// Token authenticates to Celery. The same device token the Mac app holds.
	Token string `json:"token,omitempty"`
	// Endpoint allows pointing at a self-hosted server.
	Endpoint string `json:"endpoint,omitempty"`
	Voice    string `json:"voice,omitempty"`
	// Instructions direct the delivery, not the words.
	Instructions string `json:"instructions,omitempty"`
	// Speed multiplies the delivery rate; 1.0 is natural, 1.2 is a brisk
	// skim without sounding rushed.
	Speed float64 `json:"speed,omitempty"`
}

// Speaker turns finished turns into audio.
type Speaker struct {
	cfg  Config
	log  *slog.Logger
	http *http.Client

	// One at a time. Two agents finishing together should not talk over each
	// other, and the second thing said is rarely worth hearing under the first.
	mu      sync.Mutex
	playing *exec.Cmd
}

func New(cfg Config, log *slog.Logger) *Speaker {
	if cfg.Endpoint == "" {
		cfg.Endpoint = DefaultEndpoint
	}
	if cfg.Voice == "" {
		cfg.Voice = DefaultVoice
	}
	if cfg.Instructions == "" {
		cfg.Instructions = DefaultInstructions
	}
	if cfg.Speed <= 0 {
		cfg.Speed = 1.0
	}
	return &Speaker{
		cfg: cfg,
		log: log,
		// Generous, but not unbounded: speech takes a couple of seconds to
		// synthesise and there is no user waiting on the response.
		http: &http.Client{Timeout: 45 * time.Second},
	}
}

// Enabled reports whether speaking is configured and switched on.
func (s *Speaker) Enabled() bool {
	return s != nil && s.cfg.Enabled && s.cfg.Token != ""
}

// Speak fetches audio for text and plays it, without blocking the caller.
//
// Every failure is logged at debug and otherwise swallowed. A hook must not
// care whether the speaker worked.
func (s *Speaker) Speak(text string) {
	if !s.Enabled() {
		return
	}
	spoken := Prepare(text)
	if spoken == "" {
		return
	}
	go func() {
		if err := s.speak(spoken); err != nil {
			s.log.Debug("speech failed", "err", err)
		}
	}()
}

func (s *Speaker) speak(text string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	body, err := json.Marshal(map[string]any{
		"text":         text,
		"voice":        s.cfg.Voice,
		"instructions": s.cfg.Instructions,
		"speed":        s.cfg.Speed,
		"format":       "mp3",
	})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.Endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.cfg.Token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("speak returned %d: %s", resp.StatusCode, strings.TrimSpace(string(detail)))
	}

	audio, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return err
	}
	return s.play(audio)
}

// play writes the clip to a temporary file and hands it to the system player.
//
// A file rather than a pipe because afplay wants a path, and the file is
// removed as soon as it has been played — generated speech is not something to
// leave lying around on disk.
func (s *Speaker) play(audio []byte) error {
	file, err := os.CreateTemp("", "agentman-speech-*.mp3")
	if err != nil {
		return err
	}
	path := file.Name()
	defer os.Remove(path)

	if _, err := file.Write(audio); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}

	player, args := playerFor(path)
	if player == "" {
		return fmt.Errorf("no audio player available on this system")
	}

	cmd := exec.Command(player, args...)

	// Stop whatever is already speaking. A newer answer supersedes an older
	// one; queueing them would leave you listening to stale news.
	s.mu.Lock()
	if s.playing != nil && s.playing.Process != nil {
		_ = s.playing.Process.Kill()
	}
	s.playing = cmd
	s.mu.Unlock()

	err = cmd.Run()

	s.mu.Lock()
	if s.playing == cmd {
		s.playing = nil
	}
	s.mu.Unlock()

	return err
}

// playerFor picks a command that can play an mp3 from a path.
func playerFor(path string) (string, []string) {
	candidates := []struct {
		name string
		args []string
	}{
		{"afplay", []string{path}}, // macOS
		{"mpv", []string{"--really-quiet", "--no-video", path}},
		{"ffplay", []string{"-nodisp", "-autoexit", "-loglevel", "quiet", path}},
		{"paplay", []string{path}},
		{"aplay", []string{path}},
	}
	for _, c := range candidates {
		if found, err := exec.LookPath(c.name); err == nil {
			args := append([]string{}, c.args...)
			_ = found
			return c.name, args
		}
	}
	return "", nil
}

var (
	fencedCode  = regexp.MustCompile("(?s)```.*?```")
	inlineCode  = regexp.MustCompile("`[^`]*`")
	markdownURL = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	bareURL     = regexp.MustCompile(`https?://\S+`)
	filePath    = regexp.MustCompile(`(?:[\w.-]+/){1,}[\w.-]+\.\w+(?::\d+)?`)
	heading     = regexp.MustCompile(`(?m)^#{1,6}\s*`)
	bullet      = regexp.MustCompile(`(?m)^\s*[-*+]\s+`)
	emphasis    = regexp.MustCompile(`\*{1,2}([^*]+)\*{1,2}`)
	tableRow    = regexp.MustCompile(`(?m)^\s*\|.*\|\s*$`)
	whitespace  = regexp.MustCompile(`\s+`)
)

// Prepare turns an agent's answer into something worth hearing.
//
// Agent output is written to be read: fenced code, file paths with line
// numbers, tables, markdown emphasis. Read aloud verbatim it is unbearable —
// a code block becomes a minute of punctuation — so the parts that only make
// sense on a screen are removed rather than pronounced.
//
// This is deliberately blunt. It is a summary for the ear, not a transcript,
// and anything it drops is still on the screen where it was written.
func Prepare(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}

	// Code first, so its contents cannot be mistaken for prose by later rules.
	out := fencedCode.ReplaceAllString(text, " ")
	out = tableRow.ReplaceAllString(out, " ")
	out = markdownURL.ReplaceAllString(out, "$1")
	out = bareURL.ReplaceAllString(out, " ")
	out = inlineCode.ReplaceAllString(out, " ")
	out = filePath.ReplaceAllString(out, " ")
	out = heading.ReplaceAllString(out, "")
	out = bullet.ReplaceAllString(out, "")
	out = emphasis.ReplaceAllString(out, "$1")
	out = whitespace.ReplaceAllString(out, " ")
	out = dropGuttedSentences(out)
	out = whitespace.ReplaceAllString(out, " ")
	out = strings.TrimSpace(out)

	if out == "" {
		return ""
	}
	return clipAtSentence(out, maxSpoken)
}

// dropGuttedSentences removes sentences left meaningless by the stripping.
//
// "See internal/api/speak.go:34 and https://example.com for details." has its
// two references removed and becomes "See and for details." — grammatical
// nonsense that is worse to hear than the reference was. Sentences that exist
// only to point at something on screen have nothing to say out loud, so once
// the pointer is gone the sentence goes with it.
func dropGuttedSentences(text string) string {
	var kept []string
	for _, sentence := range splitSentences(text) {
		trimmed := strings.TrimSpace(sentence)
		if trimmed == "" {
			continue
		}
		words := strings.Fields(trimmed)
		// Short and made entirely of connectives is the signature of a
		// sentence whose content has been removed.
		if len(words) <= 6 && allFiller(words) {
			continue
		}
		kept = append(kept, trimmed)
	}
	return strings.Join(kept, " ")
}

// splitSentences breaks text after sentence-ending punctuation, keeping the
// punctuation attached to the sentence it closes.
//
// Done by hand because Go's regexp is RE2, which has no lookbehind: the
// natural `(?<=[.!?])\s+` does not compile, and MustCompile panics during
// package init — taking every binary that imports this package down with it.
func splitSentences(text string) []string {
	var sentences []string
	start := 0
	for i := range len(text) {
		if text[i] != '.' && text[i] != '!' && text[i] != '?' {
			continue
		}
		// Split only where whitespace follows, so "3.5" and "e.g." stay whole.
		end := i + 1
		if end >= len(text) || !isASCIISpace(text[end]) {
			continue
		}
		sentences = append(sentences, text[start:end])
		for end < len(text) && isASCIISpace(text[end]) {
			end++
		}
		start, i = end, end-1
	}
	if start < len(text) {
		sentences = append(sentences, text[start:])
	}
	return sentences
}

// isASCIISpace matches regexp's \s class exactly.
func isASCIISpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\f' || b == '\r'
}

// filler is the set of words that carry no information on their own. A
// sentence of nothing but these has been hollowed out by the stripping.
var filler = map[string]bool{
	"see": true, "and": true, "for": true, "details": true, "the": true,
	"in": true, "at": true, "to": true, "of": true, "a": true, "an": true,
	"is": true, "are": true, "it": true, "this": true, "that": true,
	"here": true, "there": true, "from": true, "with": true, "or": true,
	"more": true, "full": true, "above": true, "below": true, "also": true,
}

func allFiller(words []string) bool {
	for _, w := range words {
		clean := strings.ToLower(strings.Trim(w, ".,;:!?()\"'"))
		if clean == "" {
			continue
		}
		if !filler[clean] {
			return false
		}
	}
	return true
}

// clipAtSentence trims to a length without stopping mid-thought.
func clipAtSentence(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	cut := string(runes[:limit])
	// Prefer the last sentence end; fall back to the last word.
	if idx := strings.LastIndexAny(cut, ".!?"); idx > limit/3 {
		return strings.TrimSpace(cut[:idx+1])
	}
	if idx := strings.LastIndex(cut, " "); idx > 0 {
		return strings.TrimSpace(cut[:idx]) + "…"
	}
	return strings.TrimSpace(cut) + "…"
}

// ConfigPath is where the speech settings live.
func ConfigPath(home string) string {
	return filepath.Join(home, ".agentman", "config.json")
}
