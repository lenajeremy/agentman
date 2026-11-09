package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Trust by folder, and learning standing rules from what the user keeps
// approving.
//
// A rule is only ever offered, and only for what is safe to make standing:
// never a push, a delete, a deploy, anything touching keys, or anything run
// as root, whatever the user has approved by hand.

const (
	// learnAfter is how many approvals of one thing, in one folder, earn
	// the offer of a rule.
	learnAfter = 3
	// learnWindow is how long an approval counts for.
	learnWindow = 14 * 24 * time.Hour
	maxLearned  = 500
	maxTrust    = 100
)

// Offer types, in a card's Offer["type"].
const offerRule = "rule"

// trustLevel is how much Agentman may do by itself in folder.
func trustLevel(settings protocol.ManagerSettings, folder string) string {
	level, longest := "", -1
	for _, entry := range settings.Trust {
		base := filepath.Clean(expandHome(strings.TrimSpace(entry.Folder)))
		if insideFolder(folder, base) && len(base) > longest {
			level, longest = entry.Level, len(base)
		}
	}
	if level != "" {
		return level
	}
	if settings.AutoApprove {
		return protocol.TrustHandle
	}
	return protocol.TrustWatch
}

func validateTrust(entries []protocol.FolderTrust) error {
	if len(entries) > maxTrust {
		return fmt.Errorf("keep it to %d folders", maxTrust)
	}
	for _, entry := range entries {
		folder := strings.TrimSpace(entry.Folder)
		if !filepath.IsAbs(folder) && !strings.HasPrefix(folder, "~") {
			return fmt.Errorf("a trusted folder is a full path, not %q", entry.Folder)
		}
		if !slices.Contains([]string{protocol.TrustWatch, protocol.TrustSuggest, protocol.TrustHandle}, entry.Level) {
			return fmt.Errorf("trust is watch, suggest or handle, not %q", entry.Level)
		}
	}
	return nil
}

// learnStore counts what the user approved by hand.
type learnStore struct {
	path string
	mu   sync.Mutex
	seen map[string]learnCount
}

type learnCount struct {
	Times []int64 `json:"times"`
	// Offered: the offer has been made, and is not made again.
	Offered bool `json:"offered"`
}

func loadLearned(dir string) *learnStore {
	store := &learnStore{path: filepath.Join(dir, "learned.json"), seen: map[string]learnCount{}}
	if raw, err := os.ReadFile(store.path); err == nil {
		_ = json.Unmarshal(raw, &store.seen)
		if store.seen == nil {
			store.seen = map[string]learnCount{}
		}
	}
	return store
}

func (l *learnStore) saveLocked() {
	if len(l.seen) > maxLearned {
		oldest, at := "", int64(-1)
		for key, count := range l.seen {
			last := int64(0)
			if n := len(count.Times); n > 0 {
				last = count.Times[n-1]
			}
			if at < 0 || last < at {
				oldest, at = key, last
			}
		}
		delete(l.seen, oldest)
	}
	raw, err := json.MarshalIndent(l.seen, "", "  ")
	if err != nil || os.MkdirAll(filepath.Dir(l.path), 0o700) != nil {
		return
	}
	tmp := l.path + ".tmp"
	if os.WriteFile(tmp, raw, 0o600) == nil {
		_ = os.Rename(tmp, l.path)
	}
}

// bump counts one approval and reports whether this one earns the offer.
func (l *learnStore) bump(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	count := l.seen[key]
	since := now.Add(-learnWindow).UnixMilli()
	count.Times = slices.DeleteFunc(count.Times, func(at int64) bool { return at < since })
	count.Times = append(count.Times, now.UnixMilli())
	earned := !count.Offered && len(count.Times) >= learnAfter
	if earned {
		count.Offered = true
	}
	l.seen[key] = count
	l.saveLocked()
	return earned
}

// The only programs a rule is ever offered for: developers' own tools, named
// plainly. Anything that merely runs something else, reads keys or reaches a
// server isn't here, so no way of writing it can get a rule offered.
var learnablePrograms = []string{
	"go", "cargo", "swift", "npm", "pnpm", "yarn", "bun", "make", "gradle", "mvn", "xcodebuild",
	"pytest", "ruff", "eslint", "tsc", "prettier", "black", "mypy", "jest", "vitest",
}

// learnableVerbs are the subcommands a rule can stretch over, as their other
// words only say what to test or build. Everything else is learned word for
// word.
var learnableVerbs = map[string][]string{
	"go":     {"test", "build", "vet", "fmt"},
	"cargo":  {"test", "build", "check", "clippy", "fmt"},
	"swift":  {"test", "build"},
	"gradle": {"test", "build"},
	"mvn":    {"test", "compile", "verify"},
}

var outwardWords = []string{"deploy", "publish", "release", "upload", "destroy", "push", "delete", "remove", "secret", "token", "credential", "password"}

// learnablePattern is the rule to offer for a request, or false when it is
// not something to make standing.
func learnablePattern(kind, target string) (string, bool) {
	target = strings.TrimSpace(target)
	switch kind {
	case "command":
		return learnableCommand(target)
	case "network":
		parsed, err := url.Parse(target)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
			return "", false
		}
		return parsed.Scheme + "://" + parsed.Host + "/*", true
	}
	return "", false
}

func learnableCommand(command string) (string, bool) {
	if command == "" || private(command) || strings.ContainsAny(command, "`<>$\n") || len(splitCommand(command)) != 1 {
		return "", false
	}
	words := strings.Fields(command)
	if !slices.Contains(learnablePrograms, words[0]) {
		return "", false
	}
	for i, word := range words {
		// `swift build -c release` is only a build.
		if word == "release" && i > 0 && (words[i-1] == "-c" || words[i-1] == "--configuration") {
			continue
		}
		lower := strings.ToLower(word)
		if slices.ContainsFunc(outwardWords, func(outward string) bool { return strings.Contains(lower, outward) }) {
			return "", false
		}
	}
	if len(words) >= 2 && slices.Contains(learnableVerbs[words[0]], words[1]) {
		return words[0] + " " + words[1] + "*", true
	}
	return command, true
}

// isAffirmative reports an option that says yes. Only those count as the
// user approving: "Skip" or "Cancel" aren't always worded as a no.
func isAffirmative(options []protocol.QuestionOption, key string) bool {
	for _, option := range options {
		if option.Key == key {
			label := strings.ToLower(strings.TrimSpace(option.Label))
			return strings.HasPrefix(label, "yes") || strings.HasPrefix(label, "allow") || strings.HasPrefix(label, "approve") ||
				strings.HasPrefix(label, "continue") || strings.HasPrefix(label, "proceed")
		}
	}
	return false
}

// Answered hears that the user answered an agent's question in the app,
// outside Agentman's cards.
func (m *Manager) Answered(session protocol.Session, req protocol.Request) {
	if strings.HasPrefix(req.ClientID, "agentman-") {
		return
	}
	m.cardMu.Lock()
	defer m.cardMu.Unlock()
	m.recordApprovalLocked(session, session.Question, req.OptionKey)
}

// recordApprovalLocked counts an approval by the user, and offers a rule
// when it has become a habit.
func (m *Manager) recordApprovalLocked(session protocol.Session, q *protocol.Question, optionKey string) {
	settings := m.currentSettings()
	if q == nil || optionKey == "" || !m.active() || settings.Autopilot == nil || !settings.Autopilot.LearnRules {
		return
	}
	if !isAffirmative(q.Options, optionKey) {
		return
	}
	kind, target := requestOf(q)
	pattern, ok := learnablePattern(kind, target)
	if !ok {
		return
	}
	// Agentman could already answer this: there is nothing to learn.
	if d := decide(session, kind, target, "yes", policyOf(settings)); d.auto {
		return
	}
	folder := filepath.Clean(session.Cwd)
	if !m.learned.bump(kind+"|"+pattern+"|"+folder, m.now()) {
		return
	}
	base := filepath.Base(folder)
	id := newID()
	card := &protocol.ManagerCard{
		ID: id, Kind: protocol.CardOffer, State: protocol.CardOpen, SessionID: session.ID, AgentName: "Agentman",
		Title: fmt.Sprintf("You've approved `%s` %d times in %s. Always allow it?", pattern, learnAfter, base),
		Offer: map[string]string{"type": offerRule, "kind": kind, "pattern": pattern, "folder": folder},
		Options: []protocol.QuestionOption{
			{Key: "here", Label: "Always allow in " + base},
			{Key: "everywhere", Label: "Always allow everywhere"},
			{Key: "no", Label: "Keep asking me"},
		},
	}
	m.addEntry(protocol.ManagerEntry{ID: id, Role: "card", Text: card.Title, SessionID: session.ID, Card: card})
}

// answerOfferCardLocked carries out what the user chose on an offer.
func (m *Manager) answerOfferCardLocked(ctx context.Context, entry protocol.ManagerEntry, req protocol.Request) protocol.Event {
	card := entry.Card
	var (
		outcome string
		err     error
	)
	switch card.Offer["type"] {
	case offerRule:
		outcome, err = m.takeRuleOffer(card, req.OptionKey)
	case offerMission:
		outcome, err = m.takeMissionOffer(ctx, card, req.OptionKey)
	case offerOverlap:
		outcome, err = m.takeOverlapOffer(ctx, card, req.OptionKey)
	case offerStuck:
		outcome, err = m.takeStuckOffer(ctx, card, req.OptionKey)
	default:
		err = errors.New("that offer can't be taken up here")
	}
	if err != nil {
		return protocol.Event{Type: protocol.EvtError, Error: "Couldn't do that: " + err.Error()}
	}
	closed := m.closeCardLocked(entry, protocol.CardAnswered, outcome)
	m.note("The user answered your offer [card " + card.ID + "]: " + outcome)
	return protocol.Event{Type: protocol.EvtManagerEntry, Entry: &closed}
}

func (m *Manager) takeRuleOffer(card *protocol.ManagerCard, option string) (string, error) {
	rule := protocol.PermissionRule{Action: "allow", Kind: card.Offer["kind"], Pattern: card.Offer["pattern"]}
	switch option {
	case "no":
		return "Keeps asking about `" + rule.Pattern + "`", nil
	case "here":
		rule.Folder = card.Offer["folder"]
	case "everywhere":
	default:
		return "", errors.New("choose one of the options")
	}
	settings := m.currentSettings()
	if !slices.Contains(settings.Rules, rule) {
		settings.Rules = append(slices.Clone(settings.Rules), rule)
		if event := m.setSettings(&settings); event.Type == protocol.EvtError {
			return "", errors.New(event.Error)
		}
	}
	return "Now allows `" + rule.Pattern + "`", nil
}
