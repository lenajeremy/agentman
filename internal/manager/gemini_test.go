package manager

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// fakeGemini answers streamGenerateContent with scripted SSE chunks and
// keeps every request it was sent.
type fakeGemini struct {
	mu       sync.Mutex
	requests []map[string]any
	paths    []string
	keys     []string
	replies  []string
	status   int
}

func (f *fakeGemini) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	f.mu.Lock()
	f.requests = append(f.requests, body)
	f.paths = append(f.paths, r.URL.String())
	f.keys = append(f.keys, r.Header.Get("x-goog-api-key"))
	reply := ""
	if len(f.replies) > 0 {
		reply, f.replies = f.replies[0], f.replies[1:]
	}
	status := f.status
	f.mu.Unlock()
	if status != 0 {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = w.Write([]byte(reply))
}

func sse(chunks ...string) string {
	var b strings.Builder
	for _, chunk := range chunks {
		b.WriteString("data: " + chunk + "\r\n\r\n")
	}
	return b.String()
}

func fakeGeminiModel(t *testing.T, fake *fakeGemini) *geminiModel {
	t.Helper()
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)
	return &geminiModel{key: "gm-test", base: server.URL, client: server.Client(), signatures: map[string]string{}}
}

func lookup(value any, path ...any) any {
	for _, step := range path {
		switch key := step.(type) {
		case string:
			object, _ := value.(map[string]any)
			value = object[key]
		case int:
			list, _ := value.([]any)
			if key >= len(list) {
				return nil
			}
			value = list[key]
		}
	}
	return value
}

func chatRequest() anthropic.BetaMessageNewParams {
	image := anthropic.BetaContentBlockParamUnion{OfImage: &anthropic.BetaImageBlockParam{
		Source: anthropic.BetaImageBlockParamSourceUnion{OfBase64: &anthropic.BetaBase64ImageSourceParam{
			MediaType: anthropic.BetaBase64ImageSourceMediaTypeImagePNG, Data: "iVBORw0KGgo=",
		}},
	}}
	return anthropic.BetaMessageNewParams{
		Model:     anthropic.Model("gemini-4-argon"),
		MaxTokens: 64000,
		System:    []anthropic.BetaTextBlockParam{{Text: "You manage coding agents."}},
		Tools:     apiTools(),
		Messages: []anthropic.BetaMessageParam{
			anthropic.NewBetaUserMessage(image, anthropic.NewBetaTextBlock("What is the site agent doing?")),
			{Role: anthropic.BetaMessageParamRoleAssistant, Content: []anthropic.BetaContentBlockParamUnion{
				anthropic.NewBetaToolUseBlock("call_7", map[string]any{"agent": "site"}, "read_messages"),
			}},
			anthropic.NewBetaUserMessage(toolResultBlock("call_7", toolResult{text: "site finished the build."})),
		},
		OutputConfig: anthropic.BetaOutputConfigParam{Effort: anthropic.BetaOutputConfigEffortXhigh},
	}
}

// The conversation reaches Gemini in its terms: the system prompt, the
// user's picture and words, the call and its result, and the tools, with
// the effort as a thinking level and the length within the model's limit.
func TestGeminiIsSentTheConversationInItsTerms(t *testing.T) {
	fake := &fakeGemini{replies: []string{sse(`{"candidates":[{"content":{"role":"model","parts":[{"text":"Done."}]},"finishReason":"STOP"}]}`)}}
	model := fakeGeminiModel(t, fake)
	if _, err := model.Stream(context.Background(), chatRequest(), nil); err != nil {
		t.Fatal(err)
	}
	req := fake.requests[0]
	if !strings.HasSuffix(fake.paths[0], "/models/gemini-4-argon:streamGenerateContent?alt=sse") || fake.keys[0] != "gm-test" {
		t.Fatalf("sent to %s with key %q", fake.paths[0], fake.keys[0])
	}
	if lookup(req, "systemInstruction", "parts", 0, "text") != "You manage coding agents." {
		t.Errorf("system = %v", lookup(req, "systemInstruction"))
	}
	if lookup(req, "contents", 0, "role") != "user" ||
		lookup(req, "contents", 0, "parts", 0, "inlineData", "mimeType") != "image/png" ||
		lookup(req, "contents", 0, "parts", 1, "text") != "What is the site agent doing?" {
		t.Errorf("the user's turn = %v", lookup(req, "contents", 0))
	}
	call := lookup(req, "contents", 1, "parts", 0)
	if lookup(req, "contents", 1, "role") != "model" || lookup(call, "functionCall", "name") != "read_messages" ||
		lookup(call, "functionCall", "id") != "call_7" || lookup(call, "functionCall", "args", "agent") != "site" {
		t.Errorf("the call = %v", call)
	}
	// A call in the current turn whose signature was never seen gets the
	// placeholder Gemini documents, or it refuses the request.
	if lookup(call, "thoughtSignature") != skipSignature {
		t.Errorf("signature = %v", lookup(call, "thoughtSignature"))
	}
	result := lookup(req, "contents", 2, "parts", 0, "functionResponse")
	if lookup(result, "name") != "read_messages" || lookup(result, "id") != "call_7" ||
		lookup(result, "response", "result") != "site finished the build." {
		t.Errorf("the result = %v", result)
	}
	functions, _ := lookup(req, "tools", 0, "functionDeclarations").([]any)
	if len(functions) != len(toolDefs) || lookup(functions[0], "parametersJsonSchema", "type") != "object" {
		t.Errorf("tools = %d, first %v", len(functions), lookup(functions, 0))
	}
	if lookup(req, "generationConfig", "thinkingConfig", "thinkingLevel") != "high" ||
		lookup(req, "generationConfig", "maxOutputTokens") != float64(32768) {
		t.Errorf("config = %v", lookup(req, "generationConfig"))
	}
}

// A reply streams its words and comes back as a Claude message with its
// tool calls; each call's signature goes back with it on the next request.
func TestGeminiRepliesComeBackAsClaudeMessages(t *testing.T) {
	fake := &fakeGemini{replies: []string{
		sse(
			`{"candidates":[{"content":{"role":"model","parts":[{"text":"Let me "}]}}]}`,
			`{"candidates":[{"content":{"role":"model","parts":[{"text":"check."}]}}]}`,
			`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"id":"call_9","name":"list_agents","args":{}},"thoughtSignature":"SIG9"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":80,"candidatesTokenCount":12}}`,
		),
		sse(`{"candidates":[{"content":{"role":"model","parts":[{"text":"All quiet."}]},"finishReason":"STOP"}]}`),
	}}
	model := fakeGeminiModel(t, fake)
	params := chatRequest()
	var streamed strings.Builder
	reply, err := model.Stream(context.Background(), params, func(text string) { streamed.WriteString(text) })
	if err != nil {
		t.Fatal(err)
	}
	if streamed.String() != "Let me check." || replyText(reply) != "Let me check." {
		t.Errorf("streamed %q, text %q", streamed.String(), replyText(reply))
	}
	if reply.StopReason != anthropic.BetaStopReasonToolUse || reply.Usage.InputTokens != 80 {
		t.Errorf("stop %q usage %+v", reply.StopReason, reply.Usage)
	}
	var use anthropic.BetaToolUseBlock
	for _, block := range reply.Content {
		if found, ok := block.AsAny().(anthropic.BetaToolUseBlock); ok {
			use = found
		}
	}
	if use.ID != "call_9" || use.Name != "list_agents" || use.JSON.Input.Raw() != "{}" {
		t.Fatalf("tool use = %+v (input %q)", use, use.JSON.Input.Raw())
	}

	// The loop carries on: the reply and its result join the history.
	params.Messages = append(params.Messages, reply.ToParam(),
		anthropic.NewBetaUserMessage(toolResultBlock("call_9", toolResult{text: "3 agents"})))
	if _, err := model.Stream(context.Background(), params, nil); err != nil {
		t.Fatal(err)
	}
	contents, _ := lookup(fake.requests[1], "contents").([]any)
	model2 := contents[len(contents)-2]
	if lookup(model2, "parts", 0, "text") != "Let me check." ||
		lookup(model2, "parts", 1, "functionCall", "id") != "call_9" ||
		lookup(model2, "parts", 1, "thoughtSignature") != "SIG9" {
		t.Errorf("the reply went back as %v", model2)
	}
}

// Briefs ask for JSON to a schema; Gemini is asked the same way.
func TestGeminiIsAskedForJSONWhenABriefIs(t *testing.T) {
	fake := &fakeGemini{replies: []string{sse(`{"candidates":[{"content":{"role":"model","parts":[{"text":"{\"summary\":\"ok\"}"}]},"finishReason":"STOP"}]}`)}}
	model := fakeGeminiModel(t, fake)
	params := anthropic.BetaMessageNewParams{
		Model:     anthropic.Model("gemini-3.8-flash"),
		MaxTokens: 4000,
		Messages:  []anthropic.BetaMessageParam{anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock("brief this"))},
		OutputConfig: anthropic.BetaOutputConfigParam{
			Effort: anthropic.BetaOutputConfigEffortLow,
			Format: anthropic.BetaJSONOutputFormatParam{Schema: briefSchema},
		},
	}
	reply, err := model.Stream(context.Background(), params, nil)
	if err != nil {
		t.Fatal(err)
	}
	config := lookup(fake.requests[0], "generationConfig")
	if lookup(config, "responseMimeType") != "application/json" || lookup(config, "responseJsonSchema", "type") != "object" ||
		lookup(config, "thinkingConfig", "thinkingLevel") != "low" || lookup(config, "maxOutputTokens") != float64(4000) {
		t.Errorf("config = %v", config)
	}
	if replyText(reply) != `{"summary":"ok"}` || reply.StopReason != anthropic.BetaStopReasonEndTurn {
		t.Errorf("reply %q stop %q", replyText(reply), reply.StopReason)
	}
}

func TestGeminiRefusalsReadPlainly(t *testing.T) {
	fake := &fakeGemini{status: 429, replies: []string{`{"error":{"code":429,"message":"Quota exceeded","status":"RESOURCE_EXHAUSTED"}}`}}
	_, err := fakeGeminiModel(t, fake).Stream(context.Background(), chatRequest(), nil)
	if got := describeAPIError(err); !strings.Contains(got, "Gemini") || !strings.Contains(got, "quota") {
		t.Errorf("a 429 reads %q", got)
	}
	blocked := &fakeGemini{replies: []string{sse(`{"promptFeedback":{"blockReason":"SAFETY"}}`)}}
	reply, err := fakeGeminiModel(t, blocked).Stream(context.Background(), chatRequest(), nil)
	if err != nil || reply.StopReason != anthropic.BetaStopReasonRefusal {
		t.Errorf("a blocked prompt: %v, stop %q", err, reply.StopReason)
	}
}

// Each engine runs on its own key: a chat model on Gemini needs only the
// Gemini key, and the settings say which keys are set.
func TestEachEngineRunsOnItsOwnKey(t *testing.T) {
	h := newHarness(t, nil, false)
	h.secrets.values["gemini-api-key"] = "gm-test"
	h.m = h.newManager(t)
	if h.m.active() {
		t.Fatal("Agentman ran on Claude without the Anthropic key")
	}
	settings := h.m.currentSettings()
	if settings.Provider != providerAnthropic || settings.AnthropicKeySet || !settings.GeminiKeySet {
		t.Fatalf("settings = %+v", settings)
	}
	settings.ChatModel, settings.BriefModel = "gemini-3.8-flash", "gemini-3.5-flash-lite"
	if event := h.m.setSettings(&settings); event.Type == protocol.EvtError {
		t.Fatalf("set: %s", event.Error)
	}
	if !h.m.active() || h.m.currentSettings().Provider != providerGemini || !h.m.currentSettings().KeySet {
		t.Fatalf("on Gemini with its key, Agentman is not active: %+v", h.m.currentSettings())
	}
	if titleModelFor(settings.ChatModel) != geminiTitleModel {
		t.Error("titles stayed on Claude")
	}

	event := h.m.setKey(context.Background(), providerGemini, "")
	if event.Type == protocol.EvtError || h.m.active() || h.secrets.values["gemini-api-key"] != "" {
		t.Fatalf("removing the Gemini key left Agentman on: %+v", event)
	}
}
