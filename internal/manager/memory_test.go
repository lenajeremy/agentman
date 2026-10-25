package manager

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/lenajeremy/agentman/internal/knowledge"
	"github.com/lenajeremy/agentman/internal/protocol"
)

func summaryReply(summary string, remember ...remembered) string {
	raw, _ := json.Marshal(struct {
		Summary  string       `json:"summary"`
		Remember []remembered `json:"remember"`
	}{summary, append([]remembered{}, remember...)})
	return textReply(string(raw))
}

// A conversation of turns: the user's words, a tool call and its result,
// and a reply, each turn long enough to count.
func longHistory(turns int) []anthropic.BetaMessageParam {
	var history []anthropic.BetaMessageParam
	filler := strings.Repeat("the build passed and the site is deployed. ", 120)
	for i := range turns {
		id := "toolu_" + string(rune('a'+i))
		history = append(history,
			anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock("what is site doing now?")),
			anthropic.BetaMessageParam{Role: anthropic.BetaMessageParamRoleAssistant, Content: []anthropic.BetaContentBlockParamUnion{
				anthropic.NewBetaToolUseBlock(id, map[string]any{"agent": "site"}, "read_messages"),
			}},
			anthropic.NewBetaUserMessage(anthropic.NewBetaToolResultBlock(id, filler, false)),
			anthropic.BetaMessageParam{Role: anthropic.BetaMessageParamRoleAssistant, Content: []anthropic.BetaContentBlockParamUnion{
				anthropic.NewBetaTextBlock("site deployed."),
			}},
		)
	}
	return history
}

func (h *harness) withHistory(t *testing.T, history []anthropic.BetaMessageParam) {
	t.Helper()
	for _, message := range history {
		if err := h.m.conversation.appendHistory(message); err != nil {
			t.Fatal(err)
		}
	}
}

// A long conversation is summarised before it is sent again: the turns
// before the latest two give way to the summary, the old history is kept
// aside, lasting things go to the private notes, and the apps are shown a
// divider.
func TestALongConversationIsCompactedOnEitherEngine(t *testing.T) {
	model := &scriptedModel{replies: []string{
		summaryReply("The user follows the site agent's deploy.",
			remembered{Title: "Deploys go through staging", Body: "The user wants every deploy to go to staging first."}),
		textReply("site is idle now."),
	}}
	h := newHarness(t, model, true)
	h.withHistory(t, longHistory(50))
	before := len(h.m.conversation.messages())
	if h.m.historyTokens() < compactAt {
		t.Fatalf("the history is only %d tokens", h.m.historyTokens())
	}

	if event := h.m.send(context.Background(), "and now?"); event.Type == protocol.EvtError {
		t.Fatalf("send: %s", event.Error)
	}
	h.waitIdle(t)

	history := h.m.conversation.messages()
	if len(history) >= before {
		t.Fatalf("the history wasn't shortened: %d messages, was %d", len(history), before)
	}
	first := history[0]
	if first.Role != anthropic.BetaMessageParamRoleUser || first.Content[0].OfText == nil ||
		!strings.Contains(first.Content[0].OfText.Text, "The user follows the site agent's deploy.") {
		t.Fatalf("the history doesn't start with the summary: %+v", first)
	}
	// The summary was asked for as JSON, from a plain transcript.
	ask := model.request(0)
	if ask.OutputConfig.Format.Schema == nil || len(ask.Messages) != 1 ||
		!strings.Contains(ask.Messages[0].Content[0].OfText.Text, "[Agentman used read_messages") {
		t.Errorf("the summary request was %+v", ask.Messages)
	}
	// The reply after it was sent the short history.
	if n := len(model.request(1).Messages); n >= before {
		t.Errorf("the next request still carried %d messages", n)
	}
	archived, _ := filepath.Glob(filepath.Join(h.dir, "history-*.jsonl"))
	if len(archived) != 1 {
		t.Errorf("archived histories: %v", archived)
	}
	notes, _ := h.m.knowledge.List(knowledge.Private)
	if len(notes) != 1 || notes[0].Title != "Deploys go through staging" {
		t.Errorf("private notes: %+v", notes)
	}
	found := false
	for _, entry := range h.entries() {
		if entry.Role == "compacted" && strings.Contains(entry.Text, "kept 1 in private notes") {
			found = true
		}
	}
	if !found {
		t.Error("no divider for the compaction")
	}
}

// A new chat archives the conversation and starts again with the cards
// still open, keeps what lasted in the private notes, and opens the next
// conversation with them.
func TestANewChatStartsAgainAndRemembers(t *testing.T) {
	model := &scriptedModel{replies: []string{
		summaryReply("", remembered{Title: "Uses pnpm", Body: "The user uses pnpm in every project."}),
	}}
	h := newHarness(t, model, true)
	h.withHistory(t, longHistory(2))
	h.m.addEntry(protocol.ManagerEntry{Role: "user", Text: "hello"})
	card := h.m.addEntry(protocol.ManagerEntry{Role: "card", Text: "site needs you", Card: &protocol.ManagerCard{
		ID: "c1", Kind: protocol.CardNeedsYou, State: protocol.CardOpen, SessionID: "claude:a", AgentName: "site", Title: "deploy?",
	}})

	event := h.m.reset(context.Background())
	if event.Type != protocol.EvtManagerReset {
		t.Fatalf("reset: %+v", event)
	}
	if len(h.m.conversation.messages()) != 0 {
		t.Error("the history is still there")
	}
	entries, _ := h.m.conversation.page("", 100)
	if len(entries) != 1 || entries[0].ID != card.ID {
		t.Fatalf("the new conversation holds %+v", entries)
	}
	waitFor(t, "the private note from the last conversation", func() bool {
		notes, _ := h.m.knowledge.List(knowledge.Private)
		return len(notes) == 1
	})
	// The next turn starts with the private notes in view, the ones just
	// kept among them.
	if start := h.m.freshStart(); !strings.Contains(start, "new conversation") || !strings.Contains(start, "Uses pnpm") {
		t.Errorf("the next turn opens with %q", start)
	}
	if again := h.m.freshStart(); again != "" {
		t.Errorf("a later turn opened with %q too", again)
	}
}

// Agentman keeps its own private notes without asking; a shared note still
// waits for the user.
func TestPrivateNotesAreAgentmansOwnAndSharedOnesWait(t *testing.T) {
	h := newHarness(t, nil, true)
	ctx := context.Background()
	saved := h.m.runTool(ctx, "save_private_note", []byte(`{"title":"Works late","body":"The user works late; brief them in the morning."}`))
	if saved.isError || !strings.Contains(saved.activity, "Works late") {
		t.Fatalf("save: %+v", saved)
	}
	again := h.m.runTool(ctx, "save_private_note", []byte(`{"title":"Works late","body":"The user works late, until 2am."}`))
	notes, _ := h.m.knowledge.List(knowledge.Private)
	if again.isError || len(notes) != 1 {
		t.Fatalf("saving the same title made %d notes", len(notes))
	}
	note, _ := h.m.knowledge.Read(knowledge.Private, notes[0].Name)
	if !strings.Contains(note.Body, "2am") {
		t.Errorf("the note wasn't updated: %q", note.Body)
	}
	if gone := h.m.runTool(ctx, "delete_private_note", []byte(`{"name":"`+notes[0].Name+`"}`)); gone.isError {
		t.Fatalf("delete: %+v", gone)
	}
	if notes, _ := h.m.knowledge.List(knowledge.Private); len(notes) != 0 {
		t.Error("the private note is still there")
	}

	private := h.m.runTool(ctx, "suggest_note", []byte(`{"title":"Likes short replies","body":"Keep it brief.","tier":"private"}`))
	if private.isError || len(h.m.conversation.openCards()) != 0 {
		t.Errorf("a private suggestion waited for the user: %+v", private)
	}
	shared := h.m.runTool(ctx, "suggest_note", []byte(`{"title":"Use pnpm","body":"Every project uses pnpm.","tier":"shared"}`))
	if shared.isError || len(h.m.conversation.openCards()) != 1 {
		t.Errorf("a shared suggestion didn't wait for the user: %+v", shared)
	}
}
