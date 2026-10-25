package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// briefStore keeps the latest brief on each agent, and how far into its
// conversation that brief has read, so the next one reads only what is new.
type briefStore struct {
	mu       sync.Mutex
	path     string
	briefs   map[string]protocol.Brief
	readUpTo map[string]string
	// posted is the summary last put in the conversation for each agent, so
	// the same news is not told twice.
	posted map[string]string
}

type briefFile struct {
	Briefs   map[string]protocol.Brief `json:"briefs"`
	ReadUpTo map[string]string         `json:"readUpTo"`
	Posted   map[string]string         `json:"posted"`
}

func loadBriefs(dir string) *briefStore {
	b := &briefStore{
		path:     filepath.Join(dir, "briefs.json"),
		briefs:   map[string]protocol.Brief{},
		readUpTo: map[string]string{},
		posted:   map[string]string{},
	}
	raw, err := os.ReadFile(b.path)
	if err != nil {
		return b
	}
	var stored briefFile
	if json.Unmarshal(raw, &stored) != nil {
		return b
	}
	for id, brief := range stored.Briefs {
		b.briefs[id] = brief
	}
	for id, last := range stored.ReadUpTo {
		b.readUpTo[id] = last
	}
	for id, summary := range stored.Posted {
		b.posted[id] = summary
	}
	return b
}

func (b *briefStore) saveLocked() error {
	raw, err := json.MarshalIndent(briefFile{Briefs: b.briefs, ReadUpTo: b.readUpTo, Posted: b.posted}, "", "  ")
	if err != nil {
		return err
	}
	tmp := b.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, b.path)
}

func (b *briefStore) get(sessionID string) (protocol.Brief, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	brief, ok := b.briefs[sessionID]
	return brief, ok
}

func (b *briefStore) all() []protocol.Brief {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]protocol.Brief, 0, len(b.briefs))
	for _, brief := range b.briefs {
		out = append(out, brief)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	return out
}

func (b *briefStore) lastRead(sessionID string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.readUpTo[sessionID]
}

// put stores a brief, and reports whether it is news worth posting: notable,
// and not what was last posted for this agent.
func (b *briefStore) put(brief protocol.Brief, lastMessageID string) (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.briefs[brief.SessionID] = brief
	if lastMessageID != "" {
		b.readUpTo[brief.SessionID] = lastMessageID
	}
	post := brief.Notable && brief.Summary != "" && b.posted[brief.SessionID] != brief.Summary
	if post {
		b.posted[brief.SessionID] = brief.Summary
	}
	return post, b.saveLocked()
}

// Brief timing. A burst of turns is briefed once, after it settles, and a
// busy agent at most once a turnGap. A question on screen is not briefed: it
// becomes a card at once (cards.go).
const (
	briefAfterTurn = 20 * time.Second
	briefTurnGap   = 90 * time.Second
	briefTick      = 3 * time.Second
	briefMessages  = 60
)

// briefSchema is the brief's shape, as structured output.
var briefSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"summary":  map[string]any{"type": "string"},
		"goal":     map[string]any{"type": "string"},
		"done":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"now":      map[string]any{"type": "string"},
		"blocked":  map[string]any{"type": "string"},
		"needsYou": map[string]any{"type": "string"},
		"files":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"notable":  map[string]any{"type": "boolean"},
	},
	"required":             []string{"summary", "goal", "done", "now", "blocked", "needsYou", "files", "notable"},
	"additionalProperties": false,
}

// scheduleBrief asks for a brief on an agent after delay, but no sooner than
// gap after its last one. A brief already due earlier stays as it is.
func (m *Manager) scheduleBrief(sessionID string, delay, gap time.Duration) {
	m.briefMu.Lock()
	defer m.briefMu.Unlock()
	due := time.Now().Add(delay)
	if last, ok := m.lastBriefAt[sessionID]; ok && due.Before(last.Add(gap)) {
		due = last.Add(gap)
	}
	if existing, ok := m.briefDue[sessionID]; ok && !existing.After(due) {
		return
	}
	m.briefDue[sessionID] = due
}

// dueBriefs takes the agents whose briefs are due now.
func (m *Manager) dueBriefs(now time.Time) []string {
	m.briefMu.Lock()
	defer m.briefMu.Unlock()
	var due []string
	for id, at := range m.briefDue {
		if !at.After(now) {
			due = append(due, id)
			delete(m.briefDue, id)
		}
	}
	sort.Strings(due)
	return due
}

// runBriefs writes due briefs, one at a time, until ctx ends.
func (m *Manager) runBriefs(ctx context.Context) {
	ticker := time.NewTicker(briefTick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			for _, id := range m.dueBriefs(now) {
				if ctx.Err() != nil {
					return
				}
				m.writeBrief(ctx, id)
			}
		}
	}
}

func (m *Manager) writeBrief(ctx context.Context, sessionID string) {
	settings := m.currentSettings()
	if !settings.Briefs {
		return
	}
	model, err := m.model(ctx)
	if err != nil || model == nil {
		return
	}
	var session protocol.Session
	found := false
	for _, s := range m.agents.Sessions() {
		if s.ID == sessionID {
			session, found = s, true
			break
		}
	}
	if !found {
		return
	}

	page := m.agents.Request(ctx, protocol.Request{
		Type: protocol.ReqFetchMessages, SessionID: sessionID, Limit: briefMessages,
	}).Page
	if page == nil {
		return
	}
	fresh := messagesAfter(page.Messages, m.briefs.lastRead(sessionID))
	if len(fresh) == 0 && session.Question == nil {
		return
	}

	previous := "none"
	if brief, ok := m.briefs.get(sessionID); ok {
		raw, _ := json.Marshal(brief)
		previous = string(raw)
	}
	prompt := fmt.Sprintf("Agent: %s (%s) in %s\nState now: %s\n\nPrevious brief: %s\n\nWhat was written since:\n%s",
		session.Name, session.Kind, shortPath(session.Cwd), stateText(session), previous, formatTranscript(fresh))

	params := anthropic.BetaMessageNewParams{
		Model:     anthropic.Model(settings.BriefModel),
		MaxTokens: 4000,
		System:    []anthropic.BetaTextBlockParam{{Text: briefPrompt}},
		Messages:  []anthropic.BetaMessageParam{anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(prompt))},
		OutputConfig: anthropic.BetaOutputConfigParam{
			Effort: anthropic.BetaOutputConfigEffortLow,
			Format: anthropic.BetaJSONOutputFormatParam{Schema: briefSchema},
		},
	}
	addFallbacks(&params, settings.BriefModel)

	m.briefMu.Lock()
	m.lastBriefAt[sessionID] = time.Now()
	m.briefMu.Unlock()

	reply, err := model.Stream(ctx, params, nil)
	if err != nil || reply.StopReason == anthropic.BetaStopReasonRefusal {
		return
	}
	var fields struct {
		Summary  string   `json:"summary"`
		Goal     string   `json:"goal"`
		Done     []string `json:"done"`
		Now      string   `json:"now"`
		Blocked  string   `json:"blocked"`
		NeedsYou string   `json:"needsYou"`
		Files    []string `json:"files"`
		Notable  bool     `json:"notable"`
	}
	if json.Unmarshal([]byte(replyText(reply)), &fields) != nil || strings.TrimSpace(fields.Summary) == "" {
		return
	}
	brief := protocol.Brief{
		SessionID: sessionID, Name: session.Name, Summary: fields.Summary, Goal: fields.Goal,
		Done: fields.Done, Now: fields.Now, Blocked: fields.Blocked, NeedsYou: fields.NeedsYou,
		Files: fields.Files, Notable: fields.Notable, UpdatedAt: time.Now().UnixMilli(),
	}
	lastID := ""
	if len(page.Messages) > 0 {
		lastID = page.Messages[len(page.Messages)-1].ID
	}
	post, err := m.briefs.put(brief, lastID)
	if err != nil {
		return
	}
	m.emit(protocol.Event{Type: protocol.EvtManagerBrief, SessionID: sessionID, Brief: &brief})

	// What the agent needs from the user goes in front of them as a card with
	// a box to reply, unless its question is on screen, which has a card of
	// its own. Once a brief no longer says it, the card goes.
	m.cardMu.Lock()
	needsYou := strings.TrimSpace(brief.NeedsYou) != "" && session.Question == nil
	switch {
	case needsYou:
		m.postNeedsYouLocked(session, brief)
	case strings.TrimSpace(brief.NeedsYou) == "":
		m.settleNeedsYouLocked(sessionID, "No longer needed")
	}
	m.cardMu.Unlock()
	if post && !needsYou {
		m.addEntry(protocol.ManagerEntry{
			Role: "brief", Text: brief.Summary, SessionID: sessionID,
		})
	}
}

// messagesAfter is the messages that follow the one with ID last, or all of
// them when last is unknown or not among them.
func messagesAfter(messages []protocol.Message, last string) []protocol.Message {
	if last == "" {
		return messages
	}
	for i, message := range messages {
		if message.ID == last {
			return messages[i+1:]
		}
	}
	return messages
}

// replyText is the text a reply ends with.
func replyText(reply *anthropic.BetaMessage) string {
	var b strings.Builder
	for _, block := range reply.Content {
		if text, ok := block.AsAny().(anthropic.BetaTextBlock); ok {
			b.WriteString(text.Text)
		}
	}
	return b.String()
}
