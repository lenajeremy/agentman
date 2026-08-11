package source

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"

	"github.com/lenajeremy/agentman/internal/question"
)

// Claude's pane contains only the preview for the currently highlighted
// option. The transcript's AskUserQuestion tool input is the authoritative
// source for every label, description, and preview in the form.
const maxClaudeQuestionTailBytes int64 = 4 << 20

type claudeQuestionSpecCache struct {
	transcriptSize int64
	specs          []claudeQuestionSpec
}

type claudeQuestionSpec struct {
	Question    string                     `json:"question"`
	Header      string                     `json:"header"`
	MultiSelect bool                       `json:"multiSelect"`
	Options     []claudeQuestionOptionSpec `json:"options"`
}

type claudeQuestionOptionSpec struct {
	Label       string `json:"label"`
	Description string `json:"description"`
	Preview     string `json:"preview"`
}

type claudeQuestionTranscriptRecord struct {
	Type    string `json:"type"`
	Message *struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type claudeQuestionTranscriptBlock struct {
	Type  string          `json:"type"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

func (s *ClaudeSource) enrichClaudeQuestion(
	sessionID, transcript string,
	detected *question.Question,
) {
	if detected == nil || transcript == "" {
		return
	}
	info, err := os.Stat(transcript)
	if err != nil {
		return
	}

	s.questionMu.Lock()
	cached, ok := s.questionSpecs[sessionID]
	s.questionMu.Unlock()
	if !ok || cached.transcriptSize != info.Size() {
		specs, err := readLatestClaudeQuestionSpecs(transcript, info.Size())
		if err != nil {
			return
		}
		cached = claudeQuestionSpecCache{transcriptSize: info.Size(), specs: specs}
		s.questionMu.Lock()
		s.questionSpecs[sessionID] = cached
		s.questionMu.Unlock()
	}
	applyClaudeQuestionSpecs(detected, cached.specs)
}

func (s *ClaudeSource) forgetClaudeQuestionSpecs(live map[string]bool) {
	s.questionMu.Lock()
	defer s.questionMu.Unlock()
	for id := range s.questionSpecs {
		if !live[id] {
			delete(s.questionSpecs, id)
		}
	}
}

func readLatestClaudeQuestionSpecs(path string, size int64) ([]claudeQuestionSpec, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	readSize := min(size, maxClaudeQuestionTailBytes)
	start := size - readSize
	raw := make([]byte, readSize)
	if _, err := file.ReadAt(raw, start); err != nil && err != io.EOF {
		return nil, err
	}
	if start > 0 {
		newline := bytes.IndexByte(raw, '\n')
		if newline < 0 {
			return nil, nil
		}
		raw = raw[newline+1:]
	}

	lines := bytes.Split(raw, []byte{'\n'})
	for i := len(lines) - 1; i >= 0; i-- {
		var record claudeQuestionTranscriptRecord
		if json.Unmarshal(lines[i], &record) != nil ||
			record.Type != "assistant" || record.Message == nil {
			continue
		}
		var blocks []claudeQuestionTranscriptBlock
		if json.Unmarshal(record.Message.Content, &blocks) != nil {
			continue
		}
		for j := len(blocks) - 1; j >= 0; j-- {
			block := blocks[j]
			if block.Type != "tool_use" || block.Name != "AskUserQuestion" {
				continue
			}
			var input struct {
				Questions []claudeQuestionSpec `json:"questions"`
			}
			if json.Unmarshal(block.Input, &input) != nil {
				continue
			}
			return boundedClaudeQuestionSpecs(input.Questions), nil
		}
	}
	return nil, nil
}

func boundedClaudeQuestionSpecs(specs []claudeQuestionSpec) []claudeQuestionSpec {
	if len(specs) > 4 {
		specs = specs[:4]
	}
	out := make([]claudeQuestionSpec, 0, len(specs))
	for _, spec := range specs {
		if len(spec.Question) > 64*1024 || len(spec.Header) > 64*1024 {
			continue
		}
		if len(spec.Options) > 256 {
			spec.Options = spec.Options[:256]
		}
		options := make([]claudeQuestionOptionSpec, 0, len(spec.Options))
		for _, option := range spec.Options {
			if len(option.Label) > 64*1024 || len(option.Description) > 64*1024 ||
				len(option.Preview) > 256*1024 {
				continue
			}
			option.Label = strings.TrimSpace(option.Label)
			option.Description = strings.TrimSpace(option.Description)
			option.Preview = strings.TrimSpace(option.Preview)
			options = append(options, option)
		}
		spec.Question = strings.TrimSpace(spec.Question)
		spec.Header = strings.TrimSpace(spec.Header)
		spec.Options = options
		out = append(out, spec)
	}
	return out
}

func applyClaudeQuestionSpecs(detected *question.Question, specs []claudeQuestionSpec) bool {
	for _, spec := range specs {
		if spec.Question != strings.TrimSpace(detected.Prompt) ||
			len(spec.Options) != len(detected.Options) {
			continue
		}
		detected.Title = spec.Header
		detected.Multiple = spec.MultiSelect
		for i, option := range spec.Options {
			detected.Options[i].Label = option.Label
			detected.Options[i].Description = option.Description
			detected.Options[i].Preview = option.Preview
		}
		return true
	}
	return false
}
