package manager

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/anthropics/anthropic-sdk-go/shared/constant"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// maxSteps bounds one reply's tool calls. Reading a few agents takes a
// handful; anything near this is going in circles.
const maxSteps = 16

var toolsOnce = sync.OnceValue(apiTools)

// runTurn writes Agentman's reply to what the user said: it streams text to
// the apps as it arrives, runs the tools the model asks for, and goes on
// until the model is done.
func (m *Manager) runTurn(ctx context.Context, model Model, text string) {
	defer func() {
		m.mu.Lock()
		if m.stopTurn != nil {
			m.stopTurn()
			m.stopTurn = nil
		}
		m.mu.Unlock()
	}()
	m.setStatus("thinking", "")
	settled := "idle"
	defer func() { m.setStatus(settled, "") }()

	// What happened since the user last wrote, such as requests Agentman
	// approved and cards it put up, goes first, so the model knows.
	blocks := []anthropic.BetaContentBlockParamUnion{anthropic.NewBetaTextBlock(text)}
	if notes := m.takeNotes(); notes != "" {
		blocks = append([]anthropic.BetaContentBlockParamUnion{anthropic.NewBetaTextBlock(notes)}, blocks...)
	}
	if err := m.conversation.appendHistory(anthropic.NewBetaUserMessage(blocks...)); err != nil {
		m.addEntry(protocol.ManagerEntry{Role: "error", Text: "Couldn't save the conversation: " + err.Error()})
		return
	}

	for range maxSteps {
		entryID := newID()
		began := false
		var written strings.Builder
		reply, err := model.Stream(ctx, m.chatParams(), func(delta string) {
			if !began {
				began = true
				m.emit(protocol.Event{Type: protocol.EvtManagerEntry, Entry: &protocol.ManagerEntry{
					ID: entryID, Role: "assistant", Ts: time.Now().UnixMilli(), Streaming: true,
				}})
			}
			written.WriteString(delta)
			m.emit(protocol.Event{Type: protocol.EvtManagerDelta, EntryID: entryID, Delta: delta})
		})
		if err != nil {
			// What was shown stays shown; the model never saw this reply, so
			// it is not in the history.
			if began {
				m.addEntry(protocol.ManagerEntry{ID: entryID, Role: "assistant", Text: strings.TrimSpace(written.String())})
			}
			if ctx.Err() != nil {
				m.addEntry(protocol.ManagerEntry{Role: "activity", Text: "Stopped"})
				return
			}
			m.addEntry(protocol.ManagerEntry{Role: "error", Text: describeAPIError(err)})
			settled = "error"
			return
		}
		if err := m.conversation.appendHistory(reply.ToParam()); err != nil {
			m.addEntry(protocol.ManagerEntry{Role: "error", Text: "Couldn't save the conversation: " + err.Error()})
			return
		}
		if final := strings.TrimSpace(replyText(reply)); final != "" {
			m.addEntry(protocol.ManagerEntry{ID: entryID, Role: "assistant", Text: final})
		}

		switch reply.StopReason {
		case anthropic.BetaStopReasonToolUse:
		case anthropic.BetaStopReasonRefusal:
			m.addEntry(protocol.ManagerEntry{Role: "error", Text: "Agentman can't help with that one."})
			return
		case anthropic.BetaStopReasonMaxTokens:
			m.addEntry(protocol.ManagerEntry{Role: "error", Text: "That reply was cut off for length."})
			return
		default:
			return
		}

		var results []anthropic.BetaContentBlockParamUnion
		for _, block := range reply.Content {
			use, ok := block.AsAny().(anthropic.BetaToolUseBlock)
			if !ok {
				continue
			}
			if ctx.Err() != nil {
				results = append(results, anthropic.NewBetaToolResultBlock(use.ID, "Not run: the user stopped this reply.", true))
				continue
			}
			result := m.runTool(ctx, use.Name, []byte(use.JSON.Input.Raw()))
			results = append(results, toolResultBlock(use.ID, result))
			if result.activity != "" {
				m.addEntry(protocol.ManagerEntry{
					Role: "activity", Text: result.activity, Tool: use.Name,
					SessionID: result.sessionID, Path: result.path,
				})
			}
		}
		if err := m.conversation.appendHistory(anthropic.NewBetaUserMessage(results...)); err != nil {
			m.addEntry(protocol.ManagerEntry{Role: "error", Text: "Couldn't save the conversation: " + err.Error()})
			return
		}
		if ctx.Err() != nil {
			m.addEntry(protocol.ManagerEntry{Role: "activity", Text: "Stopped"})
			return
		}
	}
	m.addEntry(protocol.ManagerEntry{Role: "error", Text: "Agentman stopped after too many steps."})
}

// chatParams is one request for the conversation. The system prompt and
// tools come first and stay the same, and a cache breakpoint is placed on
// the conversation, so each request reuses the last one's cached prefix.
func (m *Manager) chatParams() anthropic.BetaMessageNewParams {
	settings := m.currentSettings()
	params := anthropic.BetaMessageNewParams{
		Model:     anthropic.Model(settings.ChatModel),
		MaxTokens: 64000,
		System:    []anthropic.BetaTextBlockParam{{Text: systemPrompt(settings)}},
		Tools:     toolsOnce(),
		Messages:  m.conversation.messages(),
		OutputConfig: anthropic.BetaOutputConfigParam{
			Effort: anthropic.BetaOutputConfigEffort(settings.Effort),
		},
		CacheControl: anthropic.BetaCacheControlEphemeralParam{},
		// A long-running conversation is summarised server-side when it nears
		// the context limit, rather than failing.
		ContextManagement: anthropic.BetaContextManagementConfigParam{
			Edits: []anthropic.BetaContextManagementConfigEditUnionParam{
				{OfCompact20260112: &anthropic.BetaCompact20260112EditParam{}},
			},
		},
		Betas: []anthropic.AnthropicBeta{anthropic.AnthropicBetaCompact2026_01_12},
	}
	addFallbacks(&params, settings.ChatModel)
	return params
}

// addFallbacks has a refusal re-served by a fallback model the API picks, on
// the models that support it.
func addFallbacks(params *anthropic.BetaMessageNewParams, model string) {
	if !supportsFallbacks(model) {
		return
	}
	params.Fallbacks = anthropic.BetaFallbacksParamUnion{OfDefault: constant.ValueOf[constant.Default]()}
	params.Betas = append(params.Betas, anthropic.AnthropicBetaServerSideFallback2026_07_01)
}

// toolResultBlock is a tool's result as the API takes it, with its image
// when it has one.
func toolResultBlock(toolUseID string, result toolResult) anthropic.BetaContentBlockParamUnion {
	if result.image == nil {
		return anthropic.NewBetaToolResultBlock(toolUseID, result.text, result.isError)
	}
	block := anthropic.BetaToolResultBlockParam{
		ToolUseID: toolUseID,
		Content: []anthropic.BetaToolResultBlockParamContentUnion{
			{OfText: &anthropic.BetaTextBlockParam{Text: result.text}},
			{OfImage: &anthropic.BetaImageBlockParam{
				Source: anthropic.BetaImageBlockParamSourceUnion{OfBase64: result.image},
			}},
		},
		IsError: param.NewOpt(result.isError),
	}
	return anthropic.BetaContentBlockParamUnion{OfToolResult: &block}
}
