package manager

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Noticing an agent that has stopped getting anywhere: the same thing
// failing over and over, nothing for a long while, a context nearly full, or
// a usage limit. This reads transcripts in plain Go and costs no API calls.
//
// Only one thing happens by itself, and only in a folder on Handle: an agent
// repeating a failure is told, once, to stop and try something else.
// Interrupting one, or moving its work to another agent, is offered.

const (
	stuckSweep = time.Minute
	// quietAfter is how long a working agent can say nothing; a command
	// still running gets longer.
	quietAfter        = 20 * time.Minute
	quietCommandAfter = 45 * time.Minute
	// failingRun is how many failures in a row, and repeatRun how many of
	// one and the same step in a row, count as stuck.
	failingRun    = 4
	repeatRun     = 5
	contextFull   = 92
	stuckMessages = 40
	// limitRecent is how soon after a limit is mentioned it still matters.
	limitRecent = 30 * time.Minute
	// agentSendTimeout bounds what is sent to an agent under the cards' lock.
	agentSendTimeout = 15 * time.Second
)

const offerStuck = "stuck"

// Why an agent is stuck.
const (
	stuckFailing = "failing"
	stuckQuiet   = "quiet"
	stuckContext = "context"
	stuckLimit   = "limit"
)

const nudgeText = "You've tried this several times and it keeps failing. Stop, say what you think is actually wrong, and try a different approach."

const quietText = "You've been quiet for a while. Where have you got to? If you're stuck, say so."

var limitPhrase = regexp.MustCompile(`(?i)(usage limit|rate limit|limit reached|hit your .{0,20}limit|out of (credits|quota)|quota exceeded)`)

// stuckFinding is what is wrong with an agent, and what identifies this
// time it happened, so it is raised once.
type stuckFinding struct {
	reason  string
	summary string
	key     string
	// askID is the request it is stuck on, which a nudge is counted against.
	askID string
}

func (m *Manager) watchStuck(ctx context.Context) {
	ticker := time.NewTicker(stuckSweep)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.checkStuck(ctx)
		}
	}
}

// checkStuck looks at every agent once, when the user has asked for this.
func (m *Manager) checkStuck(ctx context.Context) {
	m.advanceMissions(ctx)
	settings := m.currentSettings()
	if settings.Autopilot == nil || !m.active() {
		return
	}
	sessions := m.agents.Sessions()
	if settings.Autopilot.Unstick {
		for _, session := range sessions {
			finding := m.diagnose(ctx, session, m.now())
			m.actOnStuck(ctx, settings, session, finding)
		}
	}
	m.checkOverlaps(ctx, settings, sessions)
	m.learnFromCorrections(ctx, settings, sessions)
	m.runOvernight(ctx, settings)
}

func (m *Manager) diagnose(ctx context.Context, session protocol.Session, now time.Time) stuckFinding {
	if session.State == protocol.StateEnded {
		return stuckFinding{}
	}
	if session.ContextPercent >= contextFull {
		return stuckFinding{reason: stuckContext, key: stuckContext,
			summary: fmt.Sprintf("%s has used %d%% of its context and may lose track of its work.", session.Name, session.ContextPercent)}
	}
	silent := now.Sub(time.UnixMilli(session.LastActivityAt))
	switch session.State {
	case protocol.StateBusy:
		messages := m.tailMessages(ctx, session.ID)
		if finding := failingFinding(session, messages); finding.reason != "" {
			finding.askID = lastTurn(messages).askID
			return finding
		}
		limit := quietAfter
		if lastToolRunning(messages) {
			limit = quietCommandAfter
		}
		if silent > limit {
			return stuckFinding{reason: stuckQuiet, key: fmt.Sprintf("%s|%d", stuckQuiet, session.LastActivityAt),
				summary: fmt.Sprintf("%s has been quiet for %s.", session.Name, roundDuration(silent))}
		}
	case protocol.StateIdle:
		if silent < limitRecent {
			if said := lastLimitMessage(m.tailMessages(ctx, session.ID)); said != "" {
				return stuckFinding{reason: stuckLimit, key: stuckLimit + "|" + said,
					summary: fmt.Sprintf("%s has hit a usage limit.", session.Name)}
			}
		}
	}
	return stuckFinding{}
}

func (m *Manager) tailMessages(ctx context.Context, sessionID string) []protocol.Message {
	page := m.agents.Request(ctx, protocol.Request{Type: protocol.ReqFetchMessages, SessionID: sessionID, Limit: stuckMessages}).Page
	if page == nil {
		return nil
	}
	return page.Messages
}

// failingFinding sees a run of failures, or one step repeated.
func failingFinding(session protocol.Session, messages []protocol.Message) stuckFinding {
	var tools []protocol.Message
	for _, message := range messages {
		if message.Role == protocol.RoleUser && message.Tool == nil && !message.IsSidechain && !isOwnMessage(message.Text) {
			// What the user said starts over; a nudge doesn't.
			tools = nil
		}
		if message.Tool != nil && !message.IsSidechain {
			tools = append(tools, message)
		}
	}
	run := 0
	for i := len(tools) - 1; i >= 0 && tools[i].Tool.Status == protocol.ToolError; i-- {
		run++
	}
	if run >= failingRun {
		last := tools[len(tools)-1]
		return stuckFinding{reason: stuckFailing, key: stuckFailing + "|" + tools[len(tools)-run].ID,
			summary: fmt.Sprintf("%s has failed %d times in a row, most recently at %s.", session.Name, run, stepName(last.Tool))}
	}
	// One step over and over, with nothing else between. Editing a file and
	// running the tests in turn is just work.
	if last := len(tools) - 1; last >= 0 && tools[last].Tool.Summary != "" {
		same := 0
		for i := last; i >= 0 && tools[i].Tool.Name == tools[last].Tool.Name && tools[i].Tool.Summary == tools[last].Tool.Summary; i-- {
			same++
		}
		if same >= repeatRun {
			return stuckFinding{reason: stuckFailing, key: stuckFailing + "|" + tools[last-same+1].ID,
				summary: fmt.Sprintf("%s keeps repeating the same step: %s.", session.Name, stepName(tools[last].Tool))}
		}
	}
	return stuckFinding{}
}

func stepName(tool *protocol.Tool) string {
	if tool.Summary != "" {
		return "`" + clip(tool.Summary, 80) + "`"
	}
	return tool.Name
}

func lastToolRunning(messages []protocol.Message) bool {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Tool != nil {
			return messages[i].Tool.Status == protocol.ToolRunning
		}
	}
	return false
}

// lastLimitMessage is the agent's closing words, if they are about a limit.
// Short ones only: an agent writing about rate limiting isn't at one.
func lastLimitMessage(messages []protocol.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		message := messages[i]
		if message.Tool != nil || message.Role == protocol.RoleUser {
			return ""
		}
		text := strings.TrimSpace(message.Text)
		if text == "" {
			continue
		}
		if len(text) < 300 && limitPhrase.MatchString(text) {
			return message.ID
		}
		return ""
	}
	return ""
}

// actOnStuck raises a finding once, and takes back a card whose agent has
// got going again.
func (m *Manager) actOnStuck(ctx context.Context, settings protocol.ManagerSettings, session protocol.Session, finding stuckFinding) {
	m.cardMu.Lock()
	defer m.cardMu.Unlock()
	open := m.openOfferLocked(session.ID, offerStuck)
	if finding.reason == "" {
		if open != nil {
			m.settleCardLocked(*open, "It got going again")
		}
		delete(m.stuck, session.ID)
		return
	}
	if m.stuck[session.ID] == finding.key || open != nil {
		return
	}
	// The user is looking at it. Nothing is said over them; it comes up
	// again if they leave and it's still stuck.
	if m.viewed(session.ID) {
		return
	}
	m.stuck[session.ID] = finding.key
	if finding.reason == stuckFailing && trustLevel(settings, session.Cwd) == protocol.TrustHandle && session.Question == nil &&
		m.verifies.spendNudge(session.ID, finding.askID) {
		sendCtx, cancel := context.WithTimeout(ctx, agentSendTimeout)
		defer cancel()
		if _, err := m.sendTo(sendCtx, session, nudgeText); err == nil {
			text := finding.summary + " Told it to stop and try something else."
			m.addEntry(protocol.ManagerEntry{Role: "activity", Tool: "unstick", Text: text, SessionID: session.ID})
			m.note(text)
			return
		}
	}
	m.postStuckCardLocked(session, finding)
}

func (m *Manager) openOfferLocked(sessionID, kind string) *protocol.ManagerEntry {
	for _, entry := range m.conversation.openCards() {
		if entry.Card.Kind == protocol.CardOffer && entry.Card.SessionID == sessionID && entry.Card.Offer["type"] == kind {
			found := entry
			return &found
		}
	}
	return nil
}

func (m *Manager) postStuckCardLocked(session protocol.Session, finding stuckFinding) {
	options := []protocol.QuestionOption{}
	other := "codex"
	if session.Kind == protocol.KindCodex {
		other = "claude"
	}
	switch finding.reason {
	case stuckFailing:
		options = append(options, protocol.QuestionOption{Key: "nudge", Label: "Tell it to try something else"})
	case stuckQuiet:
		options = append(options, protocol.QuestionOption{Key: "ask", Label: "Interrupt it and ask where it's got to"})
	case stuckContext:
		options = append(options, protocol.QuestionOption{Key: "restart", Label: "Start a fresh one from a summary"})
	case stuckLimit:
		options = append(options, protocol.QuestionOption{Key: "handoff", Label: "Hand it to " + agentLabel(launchKinds[other])})
	}
	options = append(options, protocol.QuestionOption{Key: "leave", Label: "Leave it"})
	id := newID()
	card := &protocol.ManagerCard{
		ID: id, Kind: protocol.CardOffer, State: protocol.CardOpen, SessionID: session.ID, AgentName: session.Name,
		Title: finding.summary, Options: options,
		Offer: map[string]string{"type": offerStuck, "reason": finding.reason, "other": other},
	}
	m.addEntry(protocol.ManagerEntry{ID: id, Role: "card", Text: finding.summary, SessionID: session.ID, Card: card})
}

// takeStuckOffer does what the user picked for a stuck agent.
func (m *Manager) takeStuckOffer(ctx context.Context, card *protocol.ManagerCard, option string) (string, error) {
	if option == "leave" {
		return "Left it", nil
	}
	session, live := m.session(card.SessionID)
	if !live {
		return "", errors.New(card.AgentName + " has ended")
	}
	switch option {
	case "nudge":
		if _, err := m.sendTo(ctx, session, nudgeText); err != nil {
			return "", err
		}
		return "Told it to try something else", nil
	case "ask":
		m.agents.Request(ctx, protocol.Request{Type: protocol.ReqInterrupt, SessionID: session.ID})
		if _, err := m.sendTo(ctx, session, quietText); err != nil {
			return "", err
		}
		return "Interrupted it and asked", nil
	case "restart", "handoff":
		kind := kindName(session.Kind)
		if option == "handoff" {
			kind = card.Offer["other"]
		}
		result := runStartAgent(ctx, m, map[string]any{
			"kind": kind, "folder": session.Cwd, "continue_from": session.ID,
			"task": "Carry on with this work where the last agent stopped.",
		})
		if result.isError {
			return "", errors.New(result.text)
		}
		return result.activity, nil
	}
	return "", errors.New("choose one of the options")
}

// kindName is the name start_agent knows an agent kind by.
func kindName(kind protocol.Kind) string {
	for name, known := range launchKinds {
		if known == kind {
			return name
		}
	}
	return "claude"
}
