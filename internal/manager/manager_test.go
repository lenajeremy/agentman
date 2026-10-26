package manager

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/secrets"
)

// scriptedModel replays prepared API replies, so no test calls the API.
type scriptedModel struct {
	mu       sync.Mutex
	replies  []string
	requests []anthropic.BetaMessageNewParams
	// wait, when set, makes every call block until it is cancelled.
	wait bool
}

func (s *scriptedModel) Stream(ctx context.Context, params anthropic.BetaMessageNewParams, onText func(string)) (*anthropic.BetaMessage, error) {
	s.mu.Lock()
	s.requests = append(s.requests, params)
	i := len(s.requests) - 1
	s.mu.Unlock()
	if s.wait {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if i >= len(s.replies) {
		return nil, errors.New("scripted model ran out of replies")
	}
	var reply anthropic.BetaMessage
	if err := json.Unmarshal([]byte(s.replies[i]), &reply); err != nil {
		return nil, err
	}
	if onText != nil {
		for _, block := range reply.Content {
			if text, ok := block.AsAny().(anthropic.BetaTextBlock); ok {
				half := len(text.Text) / 2
				onText(text.Text[:half])
				onText(text.Text[half:])
			}
		}
	}
	return &reply, nil
}

func (s *scriptedModel) request(i int) anthropic.BetaMessageNewParams {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requests[i]
}

func (s *scriptedModel) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.requests)
}

func textReply(text string) string {
	raw, _ := json.Marshal(text)
	return `{"id":"msg_t","type":"message","role":"assistant","model":"claude-opus-5-5",` +
		`"content":[{"type":"text","text":` + string(raw) + `}],"stop_reason":"end_turn",` +
		`"usage":{"input_tokens":1,"output_tokens":1}}`
}

func toolReply(id, name, input string) string {
	return `{"id":"msg_u","type":"message","role":"assistant","model":"claude-opus-5-5",` +
		`"content":[{"type":"tool_use","id":"` + id + `","name":"` + name + `","input":` + input + `}],` +
		`"stop_reason":"tool_use","usage":{"input_tokens":1,"output_tokens":1}}`
}

// fakeAgents is the daemon: two agents, and canned answers to the requests
// Agentman's tools make.
type fakeAgents struct {
	mu       sync.Mutex
	sessions []protocol.Session
	requests []protocol.Request
	answer   func(protocol.Request) protocol.Event
}

func (f *fakeAgents) Sessions() []protocol.Session {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]protocol.Session(nil), f.sessions...)
}

func (f *fakeAgents) setSessions(sessions []protocol.Session) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions = sessions
}

// sent is the requests of one type Agentman made.
func (f *fakeAgents) sent(kind protocol.RequestType) []protocol.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []protocol.Request
	for _, req := range f.requests {
		if req.Type == kind {
			out = append(out, req)
		}
	}
	return out
}

func (f *fakeAgents) Request(_ context.Context, req protocol.Request) protocol.Event {
	f.mu.Lock()
	f.requests = append(f.requests, req)
	f.mu.Unlock()
	if f.answer != nil {
		return f.answer(req)
	}
	return protocol.Event{Type: protocol.EvtError, Error: "not in this test"}
}

type memorySecrets struct {
	mu     sync.Mutex
	values map[string]string
}

func (s *memorySecrets) Get(_ context.Context, name string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if value, ok := s.values[name]; ok {
		return value, nil
	}
	return "", secrets.ErrNotFound
}

func (s *memorySecrets) Set(_ context.Context, name, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[name] = value
	return nil
}

func (s *memorySecrets) Delete(_ context.Context, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.values, name)
	return nil
}

type harness struct {
	m       *Manager
	model   *scriptedModel
	agents  *fakeAgents
	secrets *memorySecrets
	dir     string

	mu     sync.Mutex
	events []protocol.Event
	news   []protocol.ManagerEntry
}

func newHarness(t *testing.T, model *scriptedModel, withKey bool) *harness {
	t.Helper()
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("GEMINI_API_KEY", "")
	t.Setenv("GOOGLE_API_KEY", "")
	h := &harness{
		model: model,
		agents: &fakeAgents{sessions: []protocol.Session{
			{ID: "claude:a", Kind: "claude", Name: "site", Cwd: "/w/site", State: protocol.StateBusy, LastActivityAt: time.Now().UnixMilli()},
			{ID: "codex:b", Kind: "codex", Name: "api", Cwd: "/w/api", State: protocol.StateWaitingInput,
				Inject: protocol.InjectTmux, Question: permission("q1", "npm test")},
		}},
		secrets: &memorySecrets{values: map[string]string{}},
		dir:     t.TempDir(),
	}
	if withKey {
		h.secrets.values[keyName] = "sk-ant-test"
	}
	h.m = h.newManager(t)
	return h
}

func (h *harness) newManager(t *testing.T) *Manager {
	t.Helper()
	m, err := New(Config{
		Dir: h.dir, Secrets: h.secrets, Agents: h.agents,
		Emit: func(e protocol.Event) {
			h.mu.Lock()
			h.events = append(h.events, e)
			h.mu.Unlock()
		},
		NewModel: func(string) Model { return h.model },
		News: func(entry protocol.ManagerEntry) {
			h.mu.Lock()
			h.news = append(h.news, entry)
			h.mu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func (h *harness) told() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return roles(h.news)
}

func (h *harness) entries() []protocol.ManagerEntry {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []protocol.ManagerEntry
	for _, e := range h.events {
		if e.Type == protocol.EvtManagerEntry && e.Entry != nil && !e.Entry.Streaming {
			out = append(out, *e.Entry)
		}
	}
	return out
}

// waitIdle waits for the reply in progress to finish.
func (h *harness) waitIdle(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		h.m.mu.Lock()
		running := h.m.stopTurn != nil
		h.m.mu.Unlock()
		if !running {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the reply never finished")
}

func roles(entries []protocol.ManagerEntry) []string {
	var out []string
	for _, e := range entries {
		out = append(out, e.Role)
	}
	return out
}

// The user says something, Agentman's reply streams to every app, and both
// are kept for the next time the daemon starts.
func TestAReplyStreamsToTheAppsAndIsKept(t *testing.T) {
	h := newHarness(t, &scriptedModel{replies: []string{textReply("Both agents are fine.")}}, true)
	sent := h.m.Handle(context.Background(), protocol.Request{Type: protocol.ReqManagerSend, Text: "how is everyone?"})
	if sent.Type != protocol.EvtManagerEntry || sent.Entry.Role != "user" {
		t.Fatalf("send answered %+v", sent)
	}
	h.waitIdle(t)

	var deltas []string
	h.mu.Lock()
	for _, e := range h.events {
		if e.Type == protocol.EvtManagerDelta {
			deltas = append(deltas, e.Delta)
		}
	}
	h.mu.Unlock()
	if strings.Join(deltas, "") != "Both agents are fine." || len(deltas) < 2 {
		t.Fatalf("deltas = %q, want the reply in pieces", deltas)
	}
	entries := h.entries()
	if !slices.Equal(roles(entries), []string{"user", "assistant"}) || entries[1].Text != "Both agents are fine." {
		t.Fatalf("entries = %+v", entries)
	}

	again := h.newManager(t)
	history := again.Handle(context.Background(), protocol.Request{Type: protocol.ReqManagerHistory})
	if len(history.Entries) != 2 || history.Entries[1].Text != "Both agents are fine." {
		t.Fatalf("after a restart the conversation is %+v", history.Entries)
	}
	if got := len(again.conversation.messages()); got != 2 {
		t.Fatalf("after a restart the model's history has %d messages, want 2", got)
	}
}

// Agentman is news on its own account: briefs, word from agents and cards go
// to the phones, and so does a reply the user may have left behind. A quick
// answer to someone still there doesn't, nor what the user said themselves.
func TestAgentmanSaysWhatIsNewsOnItsOwn(t *testing.T) {
	h := newHarness(t, &scriptedModel{replies: []string{textReply("Both are fine."), textReply("Still fine.")}}, true)
	if !h.m.On() {
		t.Fatal("Agentman with its key isn't on")
	}
	h.m.Handle(context.Background(), protocol.Request{Type: protocol.ReqManagerSend, Text: "how is everyone?"})
	h.waitIdle(t)
	if told := h.told(); len(told) != 0 {
		t.Fatalf("a quick reply to the user was news: %v", told)
	}

	defer func(was time.Duration) { replyNewsAfter = was }(replyNewsAfter)
	replyNewsAfter = 0
	h.m.Handle(context.Background(), protocol.Request{Type: protocol.ReqManagerSend, Text: "and now?"})
	h.waitIdle(t)

	h.m.addEntry(protocol.ManagerEntry{Role: "brief", Text: "site finished.", SessionID: "claude:a"})
	h.m.addEntry(protocol.ManagerEntry{Role: "agent", Text: "Deployed.", SessionID: "claude:a"})
	h.m.addEntry(protocol.ManagerEntry{Role: "activity", Text: "Read site"})
	h.m.addEntry(protocol.ManagerEntry{Role: "card", Card: &protocol.ManagerCard{ID: "c1", State: protocol.CardOpen}})
	h.m.addEntry(protocol.ManagerEntry{Role: "card", Card: &protocol.ManagerCard{ID: "c2", State: protocol.CardAnswered}})
	if told := h.told(); !slices.Equal(told, []string{"assistant", "brief", "agent", "card"}) {
		t.Fatalf("news = %v, want the long reply, the brief, the agent's word and the open card", told)
	}

	if newHarness(t, &scriptedModel{}, false).m.On() {
		t.Error("Agentman without a key is on")
	}
}

// Agentman reads agents through the daemon, the user sees what it did, and
// the model gets the result back.
func TestAgentmanChecksTheAgentsAndSaysWhatItDid(t *testing.T) {
	model := &scriptedModel{replies: []string{
		toolReply("tu_1", "list_agents", `{}`),
		textReply("api needs you: it wants to run npm test."),
	}}
	h := newHarness(t, model, true)
	h.m.Handle(context.Background(), protocol.Request{Type: protocol.ReqManagerSend, Text: "anything need me?"})
	h.waitIdle(t)

	entries := h.entries()
	if !slices.Equal(roles(entries), []string{"user", "activity", "assistant"}) {
		t.Fatalf("entries = %v", roles(entries))
	}
	if entries[1].Text != "Checked 2 agents" {
		t.Fatalf("activity = %q", entries[1].Text)
	}
	second := model.request(1)
	last := second.Messages[len(second.Messages)-1]
	raw, _ := json.Marshal(last)
	if !strings.Contains(string(raw), "tool_result") || !strings.Contains(string(raw), "needs the user: Bash command Run it npm test") {
		t.Fatalf("the tool result sent back was %s", raw)
	}
}

func TestReadingAnAgentGoesThroughTheDaemonByName(t *testing.T) {
	model := &scriptedModel{replies: []string{
		toolReply("tu_1", "read_messages", `{"agent":"site","limit":5}`),
		textReply("site is fixing the header."),
	}}
	h := newHarness(t, model, true)
	h.agents.answer = func(req protocol.Request) protocol.Event {
		return protocol.Event{Type: protocol.EvtPage, Page: &protocol.Page{SessionID: req.SessionID, Messages: []protocol.Message{
			{ID: "1", Role: protocol.RoleAssistant, Ts: time.Now().UnixMilli(), Text: "Fixing the header now."},
		}}}
	}
	h.m.Handle(context.Background(), protocol.Request{Type: protocol.ReqManagerSend, Text: "what is site doing?"})
	h.waitIdle(t)

	h.agents.mu.Lock()
	req := h.agents.requests[0]
	h.agents.mu.Unlock()
	if req.Type != protocol.ReqFetchMessages || req.SessionID != "claude:a" || req.Limit != 5 {
		t.Fatalf("the daemon was asked %+v", req)
	}
	raw, _ := json.Marshal(model.request(1).Messages)
	if !strings.Contains(string(raw), "Fixing the header now.") {
		t.Fatal("the transcript never reached the model")
	}
}

// A screenshot an agent took goes to the model as an image, and the user is
// shown it too.
func TestAgentmanLooksAtAScreenshotAndShowsIt(t *testing.T) {
	model := &scriptedModel{replies: []string{
		toolReply("tu_1", "view_image", `{"agent":"site","path":"/tmp/shot.png"}`),
		textReply("The header is cut off on mobile."),
	}}
	h := newHarness(t, model, true)
	h.agents.answer = func(req protocol.Request) protocol.Event {
		return protocol.Event{Type: protocol.EvtWorkspace, Workspace: &protocol.WorkspaceResult{
			Kind: "image", SessionID: req.SessionID, Image: "iVBORw0KGgo=", MIME: "image/png",
		}}
	}
	h.m.Handle(context.Background(), protocol.Request{Type: protocol.ReqManagerSend, Text: "show me site's screenshot"})
	h.waitIdle(t)

	activity := h.entries()[1]
	if activity.Path != "/tmp/shot.png" || activity.SessionID != "claude:a" {
		t.Fatalf("activity = %+v, want the image's path so the app can show it", activity)
	}
	raw, _ := json.Marshal(model.request(1).Messages)
	if !strings.Contains(string(raw), `"media_type":"image/png"`) || !strings.Contains(string(raw), "iVBORw0KGgo=") {
		t.Fatal("the image never reached the model")
	}
}

func TestWithoutAKeyAgentmanAsksForOne(t *testing.T) {
	model := &scriptedModel{}
	h := newHarness(t, model, false)
	event := h.m.Handle(context.Background(), protocol.Request{Type: protocol.ReqManagerSend, Text: "hi"})
	if event.Type != protocol.EvtError || !strings.Contains(event.Error, "API key") {
		t.Fatalf("send without a key = %+v", event)
	}
	if model.calls() != 0 {
		t.Fatal("the model was called without a key")
	}
	settings := h.m.Handle(context.Background(), protocol.Request{Type: protocol.ReqManagerSettings})
	if settings.ManagerSettings.KeySet || settings.ManagerStatus.State != "needs_key" {
		t.Fatalf("settings = %+v / %+v", settings.ManagerSettings, settings.ManagerStatus)
	}
}

// The key is stored, and no event any app hears ever carries it.
func TestTheKeyIsStoredAndNeverSentBack(t *testing.T) {
	h := newHarness(t, &scriptedModel{}, false)
	const key = "sk-ant-api03-very-secret"
	event := h.m.Handle(context.Background(), protocol.Request{Type: protocol.ReqManagerSetKey, Text: key})
	if !event.ManagerSettings.KeySet {
		t.Fatalf("set key answered %+v", event)
	}
	if got, _ := h.secrets.Get(context.Background(), keyName); got != key {
		t.Fatalf("stored %q", got)
	}
	h.mu.Lock()
	all, _ := json.Marshal(append(h.events, event))
	h.mu.Unlock()
	if strings.Contains(string(all), key) {
		t.Fatal("an event carried the key")
	}
	h.m.Handle(context.Background(), protocol.Request{Type: protocol.ReqManagerSetKey, Text: ""})
	if _, err := h.secrets.Get(context.Background(), keyName); !errors.Is(err, secrets.ErrNotFound) {
		t.Fatal("an empty key did not remove the stored one")
	}
}

// Stop ends the reply, and the history stays one the API accepts.
func TestStopEndsTheReply(t *testing.T) {
	model := &scriptedModel{wait: true}
	h := newHarness(t, model, true)
	h.m.Handle(context.Background(), protocol.Request{Type: protocol.ReqManagerSend, Text: "read everything"})
	for model.calls() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if busy := h.m.Handle(context.Background(), protocol.Request{Type: protocol.ReqManagerSend, Text: "and this"}); busy.Type != protocol.EvtError {
		t.Fatal("a second message was taken while the first was being answered")
	}
	h.m.Handle(context.Background(), protocol.Request{Type: protocol.ReqManagerStop})
	h.waitIdle(t)
	entries := h.entries()
	if last := entries[len(entries)-1]; last.Role != "activity" || last.Text != "Stopped" {
		t.Fatalf("last entry = %+v", last)
	}
}

// A daemon stopped mid tool call leaves a call without a result. Loading
// closes it by appending, never by editing what the model saw.
func TestARestartClosesAToolCallLeftOpen(t *testing.T) {
	dir := t.TempDir()
	c, err := loadConversation(dir)
	if err != nil {
		t.Fatal(err)
	}
	var open anthropic.BetaMessage
	if err := json.Unmarshal([]byte(toolReply("tu_9", "list_agents", `{}`)), &open); err != nil {
		t.Fatal(err)
	}
	_ = c.appendHistory(anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock("hi")))
	_ = c.appendHistory(open.ToParam())

	reloaded, err := loadConversation(dir)
	if err != nil {
		t.Fatal(err)
	}
	history := reloaded.messages()
	if len(history) != 3 {
		t.Fatalf("history has %d messages, want the open call closed by a third", len(history))
	}
	raw, _ := json.Marshal(history[2])
	if !strings.Contains(string(raw), `"tool_use_id":"tu_9"`) {
		t.Fatalf("closing message = %s", raw)
	}
}

func TestARefusalIsSaidPlainly(t *testing.T) {
	refusal := `{"id":"m","type":"message","role":"assistant","model":"claude-opus-5-5","content":[],` +
		`"stop_reason":"refusal","usage":{"input_tokens":1,"output_tokens":1}}`
	h := newHarness(t, &scriptedModel{replies: []string{refusal}}, true)
	h.m.Handle(context.Background(), protocol.Request{Type: protocol.ReqManagerSend, Text: "x"})
	h.waitIdle(t)
	entries := h.entries()
	if last := entries[len(entries)-1]; last.Role != "error" || !strings.Contains(last.Text, "can't help") {
		t.Fatalf("last entry = %+v", last)
	}
}

// The request is shaped for caching and for the model it goes to.
func TestTheRequestIsShapedForTheModel(t *testing.T) {
	h := newHarness(t, &scriptedModel{}, true)
	settings := defaultSettings()
	settings.Style = "Speak like a ship's captain."
	h.m.Handle(context.Background(), protocol.Request{Type: protocol.ReqManagerSetSettings, Manager: &settings})

	params := h.m.chatParams()
	if string(params.Model) != "claude-opus-5-5" || !strings.Contains(params.System[0].Text, "ship's captain") {
		t.Fatalf("model %s, system %q", params.Model, params.System[0].Text)
	}
	var names []string
	for _, tool := range params.Tools {
		names = append(names, tool.OfTool.Name)
	}
	if !slices.Equal(names, []string{"list_agents", "get_brief", "read_messages", "view_image", "list_changes", "file_diff",
		"list_commits", "send_to_agent", "answer_question", "interrupt", "dismiss_card",
		"search_knowledge", "read_knowledge", "delete_note", "save_private_note", "delete_private_note", "suggest_note",
		"list_keys", "give_key", "update_instructions",
		"schedule_reminder", "watch_agent", "rename_agent", "list_schedules", "update_schedule", "cancel_schedule"}) {
		t.Fatalf("tools = %v", names)
	}
	if !slices.Contains(params.Betas, anthropic.AnthropicBetaServerSideFallback2026_07_01) ||
		!slices.Contains(params.Betas, anthropic.AnthropicBetaCompact2026_01_12) {
		t.Fatalf("betas = %v, want fallbacks and compaction", params.Betas)
	}

	settings.ChatModel = "claude-haiku-5-5"
	h.m.Handle(context.Background(), protocol.Request{Type: protocol.ReqManagerSetSettings, Manager: &settings})
	if slices.Contains(h.m.chatParams().Betas, anthropic.AnthropicBetaServerSideFallback2026_07_01) {
		t.Fatal("fallbacks were asked of a model without them")
	}

	bad := settings
	bad.Effort = "extreme"
	if event := h.m.Handle(context.Background(), protocol.Request{Type: protocol.ReqManagerSetSettings, Manager: &bad}); event.Type != protocol.EvtError {
		t.Fatal("an unknown effort was saved")
	}
}

// A finished turn leads to a brief from only what is new, and a notable
// brief is told in the conversation once, not every time it is rewritten.
func TestABriefIsWrittenFromWhatIsNewAndToldOnce(t *testing.T) {
	brief := func(summary string) string {
		raw, _ := json.Marshal(map[string]any{
			"summary": summary, "goal": "Ship the header", "done": []string{"Fixed mobile"}, "now": "",
			"blocked": "", "needsYou": "", "files": []string{"header.tsx"}, "notable": true,
		})
		return textReply(string(raw))
	}
	model := &scriptedModel{replies: []string{brief("site finished the header."), brief("site finished the header.")}}
	h := newHarness(t, model, true)
	messages := []protocol.Message{
		{ID: "1", Role: protocol.RoleUser, Ts: 1, Text: "fix the header"},
		{ID: "2", Role: protocol.RoleAssistant, Ts: 2, Text: "Done: the header fits on mobile."},
	}
	h.agents.answer = func(req protocol.Request) protocol.Event {
		return protocol.Event{Type: protocol.EvtPage, Page: &protocol.Page{SessionID: req.SessionID, Messages: messages}}
	}

	h.m.writeBrief(context.Background(), "claude:a")
	stored, ok := h.m.briefs.get("claude:a")
	if !ok || stored.Summary != "site finished the header." || stored.Name != "site" {
		t.Fatalf("brief = %+v", stored)
	}
	if posted := h.entries(); len(posted) != 1 || posted[0].Role != "brief" || posted[0].SessionID != "claude:a" {
		t.Fatalf("entries = %+v", posted)
	}

	// Nothing new: no call, no brief.
	h.m.writeBrief(context.Background(), "claude:a")
	if model.calls() != 1 {
		t.Fatalf("the model was called %d times for nothing new", model.calls())
	}

	// Something new, but the same news: briefed again, not told again.
	messages = append(messages, protocol.Message{ID: "3", Role: protocol.RoleAssistant, Ts: 3, Text: "Also tidied the CSS."})
	h.m.writeBrief(context.Background(), "claude:a")
	raw, _ := json.Marshal(model.request(1).Messages)
	if strings.Contains(string(raw), "fix the header") || !strings.Contains(string(raw), "tidied the CSS") {
		t.Fatalf("the second brief read %s, want only what is new", raw)
	}
	if posted := h.entries(); len(posted) != 1 {
		t.Fatalf("the same news was told %d times", len(posted))
	}
}

// A finished turn is briefed once the burst settles. A question is not
// briefed at all: it is taken at once, without a call to the model, and
// once however many sweeps see it.
func TestQuestionsAreTakenAtOnceAndTurnsBriefedLater(t *testing.T) {
	h := newHarness(t, &scriptedModel{}, true)
	h.agents.answer = delivered
	h.m.TurnComplete(h.agents.sessions[0])
	h.m.NeedsAnswer(h.agents.sessions[1])
	h.m.NeedsAnswer(h.agents.sessions[1])

	h.m.briefMu.Lock()
	turnDue, questionBriefed := h.m.briefDue["claude:a"], h.m.briefDue["codex:b"]
	h.m.briefMu.Unlock()
	if until := time.Until(turnDue); until < 15*time.Second {
		t.Fatalf("a finished turn is briefed in %v, before the burst can settle", until)
	}
	if !questionBriefed.IsZero() {
		t.Fatal("a question was scheduled for a brief")
	}
	waitFor(t, "the question to be approved", func() bool { return len(h.agents.sent(protocol.ReqAnswer)) > 0 })
	time.Sleep(20 * time.Millisecond)
	if n := len(h.agents.sent(protocol.ReqAnswer)); n != 1 {
		t.Fatalf("the question was answered %d times", n)
	}
	if h.model.calls() != 0 {
		t.Fatal("taking a question called the model")
	}
}

func TestAgentsAreFoundByIDOrName(t *testing.T) {
	h := newHarness(t, &scriptedModel{}, true)
	for query, want := range map[string]string{"claude:a": "claude:a", "API": "codex:b", "sit": "claude:a"} {
		got, err := h.m.resolveAgent(map[string]any{"agent": query})
		if err != nil || got.ID != want {
			t.Errorf("resolveAgent(%q) = %v, %v", query, got.ID, err)
		}
	}
	if _, err := h.m.resolveAgent(map[string]any{"agent": "nope"}); err == nil {
		t.Error("an agent that isn't running was found")
	}
	h.agents.sessions = append(h.agents.sessions, protocol.Session{ID: "claude:c", Name: "site-2"})
	if _, err := h.m.resolveAgent(map[string]any{"agent": "sit"}); err == nil || !strings.Contains(err.Error(), "several") {
		t.Errorf("an ambiguous name = %v", err)
	}
}
