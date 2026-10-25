package manager

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"github.com/anthropics/anthropic-sdk-go"
)

// Gemini, as a second engine for Agentman. The loop, its history and its
// tools are written in the Claude API's terms; this model takes a request in
// those terms, puts it to the Gemini API, and hands the reply back as a
// Claude message, so nothing else needs to know which engine answered.

const (
	geminiAPI = "https://generativelanguage.googleapis.com/v1beta"
	// skipSignature is the value Gemini documents for a function call whose
	// thought signature was lost, such as one the daemon made before it
	// restarted. Without one, a call in the current turn is refused.
	skipSignature = "skip_thought_signature_validator"
	// maxSignatures bounds the signatures kept; only the current turn's are
	// ever checked.
	maxSignatures = 512
)

type geminiModel struct {
	key    string
	base   string
	client *http.Client

	mu sync.Mutex
	// signatures are the thought signatures Gemini put on its function
	// calls, by call id: each must go back with its call.
	signatures map[string]string
	order      []string
}

func newGeminiModel(apiKey string) Model {
	return &geminiModel{key: apiKey, base: geminiAPI, client: &http.Client{}, signatures: map[string]string{}}
}

// geminiError is a refusal from the Gemini API.
type geminiError struct {
	Status  int
	Code    string
	Message string
}

func (e *geminiError) Error() string {
	return fmt.Sprintf("gemini: %d %s: %s", e.Status, e.Code, e.Message)
}

// The parts of a Claude API request this reads, as the SDK writes them.
type claudeWire struct {
	Model        string          `json:"model"`
	MaxTokens    int             `json:"max_tokens"`
	System       json.RawMessage `json:"system"`
	Messages     []claudeTurn    `json:"messages"`
	Tools        []claudeTool    `json:"tools"`
	OutputConfig *struct {
		Effort string `json:"effort"`
		Format *struct {
			Schema json.RawMessage `json:"schema"`
		} `json:"format"`
	} `json:"output_config"`
}

type claudeTurn struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type claudePart struct {
	Type   string `json:"type"`
	Text   string `json:"text"`
	Source *struct {
		Type      string `json:"type"`
		MediaType string `json:"media_type"`
		Data      string `json:"data"`
	} `json:"source"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

type claudeTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// The Gemini API's request and reply, as far as Agentman uses them.
type geminiPart struct {
	Text             string          `json:"text,omitempty"`
	Thought          bool            `json:"thought,omitempty"`
	ThoughtSignature string          `json:"thoughtSignature,omitempty"`
	InlineData       *geminiBlob     `json:"inlineData,omitempty"`
	FunctionCall     *geminiCall     `json:"functionCall,omitempty"`
	FunctionResponse *geminiResponse `json:"functionResponse,omitempty"`
}

type geminiBlob struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type geminiCall struct {
	ID   string          `json:"id,omitempty"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

type geminiResponse struct {
	ID       string         `json:"id,omitempty"`
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiRequest struct {
	SystemInstruction *geminiContent  `json:"systemInstruction,omitempty"`
	Contents          []geminiContent `json:"contents"`
	Tools             []geminiTools   `json:"tools,omitempty"`
	GenerationConfig  geminiConfig    `json:"generationConfig"`
}

type geminiTools struct {
	FunctionDeclarations []geminiFunction `json:"functionDeclarations"`
}

type geminiFunction struct {
	Name                 string          `json:"name"`
	Description          string          `json:"description,omitempty"`
	ParametersJSONSchema json.RawMessage `json:"parametersJsonSchema,omitempty"`
}

type geminiConfig struct {
	MaxOutputTokens    int             `json:"maxOutputTokens,omitempty"`
	ResponseMimeType   string          `json:"responseMimeType,omitempty"`
	ResponseJSONSchema json.RawMessage `json:"responseJsonSchema,omitempty"`
	ThinkingConfig     *struct {
		ThinkingLevel string `json:"thinkingLevel"`
	} `json:"thinkingConfig,omitempty"`
}

type geminiChunk struct {
	Candidates []struct {
		Content      geminiContent `json:"content"`
		FinishReason string        `json:"finishReason"`
	} `json:"candidates"`
	PromptFeedback *struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
	UsageMetadata *struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
	} `json:"usageMetadata"`
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Status  string `json:"status"`
	} `json:"error"`
}

func (g *geminiModel) Stream(ctx context.Context, params anthropic.BetaMessageNewParams, onText func(string)) (*anthropic.BetaMessage, error) {
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	var wire claudeWire
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, err
	}
	request, err := g.request(wire)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	url := fmt.Sprintf("%s/models/%s:streamGenerateContent?alt=sse", g.base, wire.Model)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", g.key)
	resp, err := g.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, readGeminiError(resp)
	}
	return g.read(resp.Body, wire.Model, onText)
}

func readGeminiError(resp *http.Response) error {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	failure := &geminiError{Status: resp.StatusCode, Message: strings.TrimSpace(string(raw))}
	var body geminiChunk
	if json.Unmarshal(raw, &body) == nil && body.Error != nil {
		failure.Code = body.Error.Status
		failure.Message = body.Error.Message
	}
	return failure
}

// request turns a Claude API request into Gemini's.
func (g *geminiModel) request(wire claudeWire) (geminiRequest, error) {
	out := geminiRequest{GenerationConfig: geminiConfig{MaxOutputTokens: geminiMaxTokens(wire.Model, wire.MaxTokens)}}
	if system := systemText(wire.System); system != "" {
		out.SystemInstruction = &geminiContent{Parts: []geminiPart{{Text: system}}}
	}
	if wire.OutputConfig != nil {
		if level := thinkingLevel(wire.OutputConfig.Effort); level != "" {
			out.GenerationConfig.ThinkingConfig = &struct {
				ThinkingLevel string `json:"thinkingLevel"`
			}{ThinkingLevel: level}
		}
		if format := wire.OutputConfig.Format; format != nil && len(format.Schema) > 0 {
			out.GenerationConfig.ResponseMimeType = "application/json"
			out.GenerationConfig.ResponseJSONSchema = format.Schema
		}
	}
	var functions []geminiFunction
	for _, tool := range wire.Tools {
		if tool.Name == "" || len(tool.InputSchema) == 0 {
			continue
		}
		functions = append(functions, geminiFunction{
			Name: tool.Name, Description: tool.Description, ParametersJSONSchema: tool.InputSchema,
		})
	}
	if len(functions) > 0 {
		out.Tools = []geminiTools{{FunctionDeclarations: functions}}
	}

	turns := make([][]claudePart, len(wire.Messages))
	names := map[string]string{}
	for i, turn := range wire.Messages {
		parts, err := claudeParts(turn.Content)
		if err != nil {
			return out, err
		}
		turns[i] = parts
		for _, part := range parts {
			if part.Type == "tool_use" {
				names[part.ID] = part.Name
			}
		}
	}
	// The current turn starts at the user's last words: Gemini checks the
	// signatures on function calls after them, and only those.
	current := 0
	for i, turn := range wire.Messages {
		if turn.Role == "user" && hasWords(turns[i]) {
			current = i
		}
	}
	for i, turn := range wire.Messages {
		role := "user"
		if turn.Role == "assistant" {
			role = "model"
		}
		var parts []geminiPart
		for _, part := range turns[i] {
			parts = append(parts, g.parts(part, names, i > current)...)
		}
		if len(parts) == 0 {
			continue
		}
		// Gemini takes one turn per speaker in a row: a reply cut short, or
		// tool results and the next words, are joined.
		if n := len(out.Contents); n > 0 && out.Contents[n-1].Role == role {
			out.Contents[n-1].Parts = append(out.Contents[n-1].Parts, parts...)
			continue
		}
		out.Contents = append(out.Contents, geminiContent{Role: role, Parts: parts})
	}
	return out, nil
}

// parts is one block of a Claude message as Gemini parts. Thinking and other
// blocks only Claude reads are left out.
func (g *geminiModel) parts(part claudePart, names map[string]string, currentTurn bool) []geminiPart {
	switch part.Type {
	case "text":
		if strings.TrimSpace(part.Text) == "" {
			return nil
		}
		return []geminiPart{{Text: part.Text}}
	case "image":
		if part.Source == nil || part.Source.Data == "" {
			return nil
		}
		return []geminiPart{{InlineData: &geminiBlob{MimeType: part.Source.MediaType, Data: part.Source.Data}}}
	case "tool_use":
		args := part.Input
		if len(args) == 0 || string(args) == "null" {
			args = json.RawMessage("{}")
		}
		call := geminiPart{FunctionCall: &geminiCall{ID: part.ID, Name: part.Name, Args: args}}
		if signature := g.signature(part.ID); signature != "" {
			call.ThoughtSignature = signature
		} else if currentTurn {
			call.ThoughtSignature = skipSignature
		}
		return []geminiPart{call}
	case "tool_result":
		text, images := resultContent(part.Content)
		response := map[string]any{"result": text}
		if part.IsError {
			response = map[string]any{"error": text}
		}
		name := names[part.ToolUseID]
		if name == "" {
			name = "tool"
		}
		out := []geminiPart{{FunctionResponse: &geminiResponse{ID: part.ToolUseID, Name: name, Response: response}}}
		return append(out, images...)
	case "compaction":
		// A summary of the conversation so far, from Claude's compaction.
		var summary string
		if json.Unmarshal(part.Content, &summary) == nil && strings.TrimSpace(summary) != "" {
			return []geminiPart{{Text: summary}}
		}
	}
	return nil
}

func claudeParts(content json.RawMessage) ([]claudePart, error) {
	if len(content) == 0 {
		return nil, nil
	}
	var text string
	if json.Unmarshal(content, &text) == nil {
		return []claudePart{{Type: "text", Text: text}}, nil
	}
	var parts []claudePart
	if err := json.Unmarshal(content, &parts); err != nil {
		return nil, fmt.Errorf("gemini: a message could not be read: %w", err)
	}
	return parts, nil
}

func hasWords(parts []claudePart) bool {
	for _, part := range parts {
		if part.Type == "text" && strings.TrimSpace(part.Text) != "" {
			return true
		}
	}
	return false
}

// resultContent is a tool result's text and its images, as parts.
func resultContent(content json.RawMessage) (string, []geminiPart) {
	parts, err := claudeParts(content)
	if err != nil {
		return "", nil
	}
	var texts []string
	var images []geminiPart
	for _, part := range parts {
		switch part.Type {
		case "text":
			texts = append(texts, part.Text)
		case "image":
			if part.Source != nil && part.Source.Data != "" {
				images = append(images, geminiPart{InlineData: &geminiBlob{MimeType: part.Source.MediaType, Data: part.Source.Data}})
			}
		}
	}
	return strings.Join(texts, "\n"), images
}

func systemText(raw json.RawMessage) string {
	parts, err := claudeParts(raw)
	if err != nil {
		return ""
	}
	var texts []string
	for _, part := range parts {
		if part.Text != "" {
			texts = append(texts, part.Text)
		}
	}
	return strings.Join(texts, "\n\n")
}

// thinkingLevel is how hard Gemini thinks for an effort the user picked.
func thinkingLevel(effort string) string {
	switch effort {
	case "low", "medium":
		return effort
	case "high", "xhigh", "max":
		return "high"
	}
	return ""
}

// geminiMaxTokens keeps a request within what the model can write.
func geminiMaxTokens(model string, asked int) int {
	limit := 65536
	if strings.HasPrefix(model, "gemini-4-") {
		limit = 32768
	}
	if asked <= 0 || asked > limit {
		return limit
	}
	return asked
}

var callID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// read gathers a streamed reply into a Claude message.
func (g *geminiModel) read(body io.Reader, model string, onText func(string)) (*anthropic.BetaMessage, error) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64<<10), 32<<20)
	var text strings.Builder
	var calls []geminiPart
	finish := ""
	blocked := false
	inputTokens, outputTokens := 0, 0
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var chunk geminiChunk
		if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &chunk); err != nil {
			return nil, fmt.Errorf("gemini: an unreadable reply: %w", err)
		}
		if chunk.Error != nil {
			return nil, &geminiError{Status: chunk.Error.Code, Code: chunk.Error.Status, Message: chunk.Error.Message}
		}
		if chunk.PromptFeedback != nil && chunk.PromptFeedback.BlockReason != "" {
			blocked = true
		}
		if chunk.UsageMetadata != nil {
			inputTokens = chunk.UsageMetadata.PromptTokenCount
			outputTokens = chunk.UsageMetadata.CandidatesTokenCount
		}
		for _, candidate := range chunk.Candidates {
			for _, part := range candidate.Content.Parts {
				switch {
				case part.FunctionCall != nil:
					calls = append(calls, part)
				case part.Thought:
				case part.Text != "":
					text.WriteString(part.Text)
					if onText != nil {
						onText(part.Text)
					}
				}
			}
			if candidate.FinishReason != "" {
				finish = candidate.FinishReason
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	content := []map[string]any{}
	if text.Len() > 0 {
		content = append(content, map[string]any{"type": "text", "text": text.String()})
	}
	for i, part := range calls {
		id := part.FunctionCall.ID
		if !callID.MatchString(id) {
			id = fmt.Sprintf("call_%d_%d", len(content), i)
		}
		if part.ThoughtSignature != "" {
			g.keepSignature(id, part.ThoughtSignature)
		}
		input := part.FunctionCall.Args
		if len(input) == 0 || string(input) == "null" {
			input = json.RawMessage("{}")
		}
		content = append(content, map[string]any{"type": "tool_use", "id": id, "name": part.FunctionCall.Name, "input": input})
	}

	stop := "end_turn"
	switch {
	case len(calls) > 0:
		stop = "tool_use"
	case blocked:
		stop = "refusal"
	case finish == "MAX_TOKENS":
		stop = "max_tokens"
	case finish == "SAFETY" || finish == "RECITATION" || finish == "PROHIBITED_CONTENT" ||
		finish == "BLOCKLIST" || finish == "SPII" || finish == "IMAGE_SAFETY":
		stop = "refusal"
	case finish == "MALFORMED_FUNCTION_CALL":
		return nil, errors.New("gemini wrote a tool call it couldn't finish; ask again")
	}
	raw, err := json.Marshal(map[string]any{
		"id": "msg_gemini", "type": "message", "role": "assistant", "model": model,
		"content": content, "stop_reason": stop, "stop_sequence": nil,
		"usage": map[string]any{"input_tokens": inputTokens, "output_tokens": outputTokens},
	})
	if err != nil {
		return nil, err
	}
	var message anthropic.BetaMessage
	if err := json.Unmarshal(raw, &message); err != nil {
		return nil, err
	}
	return &message, nil
}

func (g *geminiModel) keepSignature(id, signature string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, known := g.signatures[id]; !known {
		g.order = append(g.order, id)
	}
	g.signatures[id] = signature
	for len(g.order) > maxSignatures {
		delete(g.signatures, g.order[0])
		g.order = g.order[1:]
	}
}

func (g *geminiModel) signature(id string) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.signatures[id]
}
