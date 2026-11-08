package manager

import (
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

// Commands whose words after the second don't change what kind of thing
// they do, so a rule can stop there.
var learnableTools = []string{"go", "cargo", "npm", "pnpm", "yarn", "bun", "make", "swift", "gradle", "mvn", "xcodebuild", "git"}

// Subcommands that run whatever they are given: those are only learned
// word for word.
var openEndedSubcommands = []string{"run", "exec", "x", "dlx", "install", "add", "i"}

var outwardWords = []string{"deploy", "publish", "release", "upload", "destroy"}

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
	if command == "" || private(command) || strings.ContainsAny(command, "`><") || strings.Contains(command, "$(") {
		return "", false
	}
	if len(splitCommand(command)) != 1 || runsAsRoot(command) {
		return "", false
	}
	words := strings.Fields(command)
	if _, named := askReasons[words[0]]; named {
		return "", false
	}
	// In what the command is, not its flags: `swift build -c release` is
	// only a build.
	head := strings.ToLower(strings.Join(words[:min(3, len(words))], " "))
	for _, word := range outwardWords {
		if strings.Contains(head, word) && !strings.HasSuffix(head, "-c "+word) {
			return "", false
		}
	}
	if words[0] == "git" {
		if len(words) < 2 {
			return "", false
		}
		if _, named := gitReasons[words[1]]; named {
			return "", false
		}
	}
	if !slices.Contains(learnableTools, words[0]) || len(words) < 2 || strings.HasPrefix(words[1], "-") {
		return command, true
	}
	if slices.Contains(openEndedSubcommands, words[1]) {
		// `npm run lint` names one script; anything looser runs anything.
		if words[1] == "run" && slices.Contains([]string{"npm", "pnpm", "yarn", "bun"}, words[0]) && len(words) >= 3 {
			return strings.Join(words[:3], " ") + "*", true
		}
		return command, true
	}
	return words[0] + " " + words[1] + "*", true
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
	if isDenial(q.Options, optionKey) {
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
func (m *Manager) answerOfferCardLocked(entry protocol.ManagerEntry, req protocol.Request) protocol.Event {
	card := entry.Card
	var (
		outcome string
		err     error
	)
	switch card.Offer["type"] {
	case offerRule:
		outcome, err = m.takeRuleOffer(card, req.OptionKey)
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
