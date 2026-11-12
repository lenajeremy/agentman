package source

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/lenajeremy/agentman/internal/question"
)

// agy shortens a long command in its permission prompt to its first lines
// and "⋯ (46 lines hidden)", and wraps a long line wherever the edge falls,
// mid-word. Whoever approves it from the phone or the Mac should read what
// will run, every line of it, as written.
//
// The prompt itself is not on disk, but the call it asks about is: agy
// writes the model's step, run_command and its CommandLine with it, before
// it asks, and nothing more until the answer. So the command is read from
// the transcript, and shown in place of the screen's only when the screen's
// is that same command cut short: what is visible of it, spaces aside, must
// begin it (and end it, when lines after the hidden ones show).

// antigravityHiddenLines is agy's marker for the lines it left out.
var antigravityHiddenLines = regexp.MustCompile(`^⋯ \(\d+ lines? hidden\)$`)

// antigravityCommandTail bounds how much of a transcript is read for the
// pending call: the step that asked is the last or nearly.
const antigravityCommandTail = 512 << 10

// withFullCommand replaces the command an approval shows with the whole of
// it from the conversation's transcript, when the screen's is that command
// shortened or wrapped.
func (s *AntigravitySource) withFullCommand(transcript string, found *question.Question, form question.AntigravityForm) {
	if found == nil || form.Kind != "approval" || found.Detail == "" || transcript == "" {
		return
	}
	command := s.pendingCommand(transcript)
	if command == "" || command == found.Detail || !antigravityShowsCommand(found.Detail, command) {
		return
	}
	found.Detail = command
}

// pendingCommand is the CommandLine of the run_command call the
// conversation is waiting on: the newest model step's first call with no
// result written after it. Cached on the file's size and time, since a sweep
// asks every few seconds while a prompt is open.
func (s *AntigravitySource) pendingCommand(transcript string) string {
	info, err := os.Stat(transcript)
	if err != nil {
		return ""
	}
	s.cacheMu.Lock()
	cached, ok := s.commands[transcript]
	s.cacheMu.Unlock()
	if ok && cached.size == info.Size() && cached.mtime.Equal(info.ModTime()) {
		return cached.command
	}
	command := readPendingCommand(transcript, info.Size())
	s.cacheMu.Lock()
	if s.commands == nil {
		s.commands = map[string]antigravityCommandEntry{}
	}
	s.commands[transcript] = antigravityCommandEntry{size: info.Size(), mtime: info.ModTime(), command: command}
	s.cacheMu.Unlock()
	return command
}

func readPendingCommand(transcript string, size int64) string {
	file, err := os.Open(transcript)
	if err != nil {
		return ""
	}
	defer file.Close()
	start := max(0, size-antigravityCommandTail)
	raw := make([]byte, size-start)
	if _, err := file.ReadAt(raw, start); err != nil && err != io.EOF {
		return ""
	}
	if start > 0 {
		if newline := bytes.IndexByte(raw, '\n'); newline >= 0 {
			raw = raw[newline+1:]
		}
	}
	type step struct {
		Index     *int   `json:"step_index"`
		Type      string `json:"type"`
		ToolCalls []struct {
			Name string `json:"name"`
			Args struct {
				CommandLine string `json:"CommandLine"`
			} `json:"args"`
		} `json:"tool_calls"`
	}
	lines := bytes.Split(bytes.TrimRight(raw, "\n"), []byte{'\n'})
	after := 0 // results written after the call, one per call answered
	for i := len(lines) - 1; i >= 0; i-- {
		var record step
		if json.Unmarshal(lines[i], &record) != nil || record.Index == nil {
			continue
		}
		if record.Type != "PLANNER_RESPONSE" || len(record.ToolCalls) == 0 {
			after++
			continue
		}
		// Call j of a step is answered by the j-th record after it.
		if after >= len(record.ToolCalls) || record.ToolCalls[after].Name != "run_command" {
			return ""
		}
		return record.ToolCalls[after].Args.CommandLine
	}
	return ""
}

// antigravityShowsCommand reports whether a prompt's detail is command as agy
// draws it: its visible lines, spaces and line breaks aside, begin the
// command, and those after a hidden-lines marker end it.
func antigravityShowsCommand(detail, command string) bool {
	whole := compactSpace(command)
	var head, tail []string
	hidden := false
	for _, line := range strings.Split(detail, "\n") {
		switch {
		case antigravityHiddenLines.MatchString(strings.TrimSpace(line)):
			hidden = true
		case hidden:
			tail = append(tail, line)
		default:
			head = append(head, line)
		}
	}
	shown := compactSpace(strings.Join(head, ""))
	if shown == "" || !strings.HasPrefix(whole, shown) {
		return false
	}
	if !hidden {
		return shown == whole
	}
	return strings.HasSuffix(whole, compactSpace(strings.Join(tail, "")))
}

// compactSpace is text without its spaces, tabs and line breaks, so a
// command agy wrapped mid-word compares equal to the command.
func compactSpace(text string) string {
	return strings.Map(func(r rune) rune {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, text)
}
