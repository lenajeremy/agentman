package source

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/lenajeremy/agentman/internal/question"
)

func TestClaudeQuestionSpecsCarryEveryPreviewFromTranscript(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	input := map[string]any{"questions": []map[string]any{
		{
			"question": "Choose a preview path", "header": "Preview",
			"multiSelect": false,
			"options": []map[string]any{
				{"label": "First", "description": "The first preview.", "preview": "PATH ONE\n\nfirst body"},
				{"label": "Second", "description": "The second preview.", "preview": "PATH TWO\n\nsecond body"},
			},
		},
	}}
	line := claudeQuestionToolLine(t, input)
	if err := os.WriteFile(path, append(line, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	specs, err := readLatestClaudeQuestionSpecs(path, info.Size())
	if err != nil {
		t.Fatal(err)
	}
	detected := &question.Question{
		Prompt: "Choose a preview path",
		Options: []question.Option{
			{Key: "1", Label: "First", Selected: true, Preview: "visible fallback"},
			{Key: "2", Label: "Second"},
		},
	}
	if !applyClaudeQuestionSpecs(detected, specs) {
		t.Fatal("matching AskUserQuestion input was not applied")
	}
	if detected.Title != "Preview" || detected.Options[0].Description != "The first preview." ||
		detected.Options[0].Preview != "PATH ONE\n\nfirst body" ||
		detected.Options[1].Preview != "PATH TWO\n\nsecond body" {
		t.Errorf("transcript metadata was lost: %+v", detected)
	}
}

func TestClaudeQuestionSpecsChooseLatestToolCall(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	old := claudeQuestionToolLine(t, map[string]any{"questions": []map[string]any{
		{"question": "Old", "header": "Old", "options": []map[string]any{
			{"label": "A"}, {"label": "B"},
		}},
	}})
	latest := claudeQuestionToolLine(t, map[string]any{"questions": []map[string]any{
		{"question": "Current", "header": "Current", "options": []map[string]any{
			{"label": "First", "preview": "new one"},
			{"label": "Second", "preview": "new two"},
		}},
	}})
	body := append(append(append([]byte(nil), old...), '\n'), latest...)
	body = append(body, '\n')
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	specs, err := readLatestClaudeQuestionSpecs(path, info.Size())
	if err != nil {
		t.Fatal(err)
	}
	if len(specs) != 1 || specs[0].Question != "Current" {
		t.Errorf("latest specs = %+v", specs)
	}
}

func claudeQuestionToolLine(t *testing.T, input any) []byte {
	t.Helper()
	record := map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"content": []map[string]any{
				{"type": "tool_use", "name": "AskUserQuestion", "input": input},
			},
		},
	}
	raw, err := json.Marshal(record)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
