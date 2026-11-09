package manager

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Standing answers: the few yes-or-no questions the user has said how to
// answer, so an agent needn't wait on them.
//
// Two days of real cards decided what these are. Most of what agents ask needs
// the user's hands, taste or say on what goes out, and when asked to approve a
// plan the user usually steered it instead, so a model answering freely would
// often have been wrong. Committing finished work and installing a finished
// build on their own device they kept saying yes to.
//
// The model only says which of these a need is. The reply is a fixed line, and
// the need's own words have to pass the checks below, so an agent that talks
// the model round can at most be told yes to committing its own work.

const (
	standingCommit  = "commit"
	standingInstall = "install"
	// standingOpening starts what is sent, and says who it is from.
	standingOpening = "(From Agentman, the user's standing answer) "
	// maxStandingAnswers is how many one request of the user's can use: a
	// commit and an install, and no running back and forth.
	maxStandingAnswers = 2
	maxStandingNeed    = 200
)

var (
	standingStops = regexp.MustCompile(`(?i)\b(?:push(?:es|ed|ing)?|pull request|prs?|merge[sd]?|merging|deploy(?:s|ed|ing|ment)?|` +
		`release[sd]?|releasing|publish(?:es|ed|ing)?|ship(?:s|ped|ping)?|testflight|app store|production|prod|live|railway|vercel|` +
		`delete[sd]?|deleting|remove[sd]?|removing|reset|revert(?:s|ed|ing)?|rollback|drop|force|password|token|secret|keys?|sudo|` +
		`sign(?:ed|ing)? in|log(?:ged|ging)? in|pay|payment|billing|charge|review|try|check|open|scan|pick|choose|which|also|plan|run|or)\b`)
	standingCommitWord  = regexp.MustCompile(`(?i)\bcommit(?:s|ted|ting)?\b`)
	standingInstallWord = regexp.MustCompile(`(?i)\b(?:install(?:s|ed|ing)?|update|put)\b`)
	standingDeviceWord  = regexp.MustCompile(`(?i)\b(?:phone|iphone|ipad|mac|device)\b`)
)

// standingReply is what to tell an agent whose need is of kind, when the user
// turned that answer on and the need is that and nothing more.
func standingReply(autopilot *protocol.Autopilot, kind, need string) (string, bool) {
	if autopilot == nil {
		return "", false
	}
	on, reply := false, ""
	switch kind {
	case standingCommit:
		on, reply = autopilot.ReplyCommit, "Yes, commit it. Don't push."
	case standingInstall:
		on, reply = autopilot.ReplyInstall, "Yes, install it."
	}
	if !on || !standingCovers(need, kind) {
		return "", false
	}
	return standingOpening + reply, true
}

// standingCovers checks a need's words, whatever the model made of them: it is
// about the one thing, and nothing in it goes further than that.
func standingCovers(need, kind string) bool {
	need = strings.TrimSpace(need)
	if need == "" || len(need) > maxStandingNeed || strings.Count(need, "?") > 1 || standingStops.MatchString(need) {
		return false
	}
	commit, install := standingCommitWord.MatchString(need), standingInstallWord.MatchString(need)
	switch kind {
	case standingCommit:
		return commit && !install
	case standingInstall:
		return install && !commit && standingDeviceWord.MatchString(need)
	}
	return false
}

// answerStandingLocked answers a need with the user's standing answer, when
// it has one and nothing says to leave it to them. cardMu is held.
func (m *Manager) answerStandingLocked(ctx context.Context, session protocol.Session, need, kind string, messages []protocol.Message) bool {
	settings := m.currentSettings()
	reply, ok := standingReply(settings.Autopilot, strings.TrimSpace(kind), need)
	if !ok {
		return false
	}
	// As the agent is now: the brief took a while to write, and the user
	// may have answered in the meantime.
	live, found := m.session(session.ID)
	if !found || live.State != protocol.StateIdle || live.Question != nil ||
		live.Inject == protocol.InjectNone || live.Inject == "" {
		return false
	}
	if trustLevel(settings, live.Cwd) != protocol.TrustHandle || m.viewed(live.ID) ||
		!m.verifies.spend("standing|"+live.ID, lastTurn(messages).askID, maxStandingAnswers) {
		return false
	}
	sendCtx, cancel := context.WithTimeout(ctx, agentSendTimeout)
	defer cancel()
	if _, err := m.sendTo(sendCtx, live, reply); err != nil {
		return false
	}
	said := strings.TrimPrefix(reply, standingOpening)
	m.settleNeedsYouLocked(live.ID, "Agentman answered: "+said)
	text := fmt.Sprintf("Told %s: %s", live.Name, said)
	m.addEntry(protocol.ManagerEntry{Role: "activity", Tool: "approve", Text: text, SessionID: live.ID})
	m.note(text)
	return true
}
