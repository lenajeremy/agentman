package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Keys are the API keys Agentman hands to agents: OpenAI, Gemini,
// ElevenLabs. Their values live in the Keychain, under "key:<NAME>", and
// never reach the model, an app or an agent's conversation. Handing one out
// writes NAME=value into the project's .env, and only when git will not pick
// that file up, so a key is never one commit away from a public repository.
// What is kept here is each key's name, description and rule.

// keyStore is the keys' names, descriptions and rules, in keys.json.
type keyStore struct {
	mu   sync.Mutex
	path string
	keys map[string]protocol.Key
}

var validKeyName = regexp.MustCompile(`^[A-Z_][A-Z0-9_]{0,63}$`)

const (
	keyAccount  = "key:"
	maxKeyValue = 8 << 10
)

func loadKeys(dir string) *keyStore {
	k := &keyStore{path: filepath.Join(dir, "keys.json"), keys: map[string]protocol.Key{}}
	raw, err := os.ReadFile(k.path)
	if err != nil {
		return k
	}
	var stored []protocol.Key
	if json.Unmarshal(raw, &stored) == nil {
		for _, key := range stored {
			if validKeyName.MatchString(key.Name) {
				k.keys[key.Name] = key
			}
		}
	}
	return k
}

func (k *keyStore) list() []protocol.Key {
	k.mu.Lock()
	defer k.mu.Unlock()
	keys := make([]protocol.Key, 0, len(k.keys))
	for _, key := range k.keys {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].Name < keys[j].Name })
	return keys
}

func (k *keyStore) get(name string) (protocol.Key, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	key, ok := k.keys[name]
	return key, ok
}

func (k *keyStore) put(key protocol.Key) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.keys[key.Name] = key
	return k.saveLocked()
}

func (k *keyStore) remove(name string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.keys, name)
	return k.saveLocked()
}

func (k *keyStore) saveLocked() error {
	keys := make([]protocol.Key, 0, len(k.keys))
	for _, key := range k.keys {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i].Name < keys[j].Name })
	raw, err := json.MarshalIndent(keys, "", "  ")
	if err != nil {
		return err
	}
	tmp := k.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, k.path)
}

// setKey saves a key's details, and its value when one is sent.
func (m *Manager) saveKey(ctx context.Context, next *protocol.Key, value string) protocol.Event {
	if next == nil {
		return protocol.Event{Type: protocol.EvtError, Error: "No key was sent."}
	}
	key := *next
	key.Name = strings.ToUpper(strings.TrimSpace(key.Name))
	if !validKeyName.MatchString(key.Name) {
		return protocol.Event{Type: protocol.EvtError, Error: "Name it the way it goes in .env: capitals, digits and _, like ELEVENLABS_API_KEY."}
	}
	switch key.Rule {
	case "":
		key.Rule = protocol.KeyAuto
	case protocol.KeyAuto, protocol.KeyAsk:
		key.Projects = nil
	case protocol.KeyProjects:
		var projects []string
		for _, project := range key.Projects {
			if project = filepath.Clean(strings.TrimSpace(expandHome(project))); filepath.IsAbs(project) {
				projects = append(projects, project)
			}
		}
		if len(projects) == 0 {
			return protocol.Event{Type: protocol.EvtError, Error: "List the folders this key is for."}
		}
		key.Projects = projects
	default:
		return protocol.Event{Type: protocol.EvtError, Error: "A key's rule is auto, projects or ask."}
	}
	key.Description = clip(strings.TrimSpace(key.Description), 300)
	previous, existed := m.keys.get(key.Name)
	key.Set = existed && previous.Set
	if value = strings.TrimSpace(value); value != "" {
		if len(value) > maxKeyValue || strings.ContainsAny(value, "\n\r\x00") {
			return protocol.Event{Type: protocol.EvtError, Error: "That doesn't look like a key: it is one line of text."}
		}
		if err := m.secrets.Set(ctx, keyAccount+key.Name, value); err != nil {
			return protocol.Event{Type: protocol.EvtError, Error: "Couldn't keep it in the Keychain: " + err.Error()}
		}
		key.Set = true
	}
	key.UpdatedAt = time.Now().UnixMilli()
	if err := m.keys.put(key); err != nil {
		return protocol.Event{Type: protocol.EvtError, Error: "Couldn't save it: " + err.Error()}
	}
	return m.keysEvent()
}

func (m *Manager) deleteKey(ctx context.Context, name string) protocol.Event {
	if _, ok := m.keys.get(name); !ok {
		return protocol.Event{Type: protocol.EvtError, Error: "There is no key called " + name + "."}
	}
	_ = m.secrets.Delete(ctx, keyAccount+name)
	if err := m.keys.remove(name); err != nil {
		return protocol.Event{Type: protocol.EvtError, Error: err.Error()}
	}
	return m.keysEvent()
}

func (m *Manager) keysEvent() protocol.Event {
	event := protocol.Event{Type: protocol.EvtKeys, Keys: m.keys.list()}
	m.emit(event)
	return event
}

// giveKey hands a key to an agent, by its rule: written into the agent's
// .env at once, or put in front of the user as a card. It says what
// happened, in words for whoever asked; the value is never in them.
func (m *Manager) giveKey(ctx context.Context, session protocol.Session, name, reason string) (string, error) {
	name = strings.ToUpper(strings.TrimSpace(name))
	key, ok := m.keys.get(name)
	if !ok {
		var names []string
		for _, key := range m.keys.list() {
			names = append(names, key.Name)
		}
		if len(names) == 0 {
			return "", errors.New("no keys are kept yet; the user adds them in Agentman's settings")
		}
		return "", fmt.Errorf("there is no key called %s; the keys are %s", name, strings.Join(names, ", "))
	}
	if !key.Set {
		return "", fmt.Errorf("%s has no value yet; the user adds it in Agentman's settings", name)
	}
	if session.Cwd == "" || !filepath.IsAbs(session.Cwd) {
		return "", errors.New("the agent's folder isn't known, so there is no .env to write")
	}
	ask := ""
	switch {
	case key.Rule == protocol.KeyAsk:
		ask = "you asked to be asked about this key"
	case key.Rule == protocol.KeyProjects && !inAnyFolder(session.Cwd, key.Projects):
		ask = "this folder isn't one the key is for"
	}
	safe, why := envIsSafe(ctx, session.Cwd)
	if ask != "" || !safe {
		if ask == "" {
			ask = why
		}
		m.cardMu.Lock()
		m.postKeyCardLocked(session, key, reason, ask, safe)
		m.cardMu.Unlock()
		return fmt.Sprintf("Waiting for the user: %s. %s will be told when %s is in .env.", ask, session.Name, name), nil
	}
	if err := m.writeKey(ctx, session.Cwd, key); err != nil {
		return "", err
	}
	text := fmt.Sprintf("Gave %s %s, in its .env", session.Name, name)
	m.addEntry(protocol.ManagerEntry{Role: "activity", Text: text, Tool: "give_key", SessionID: session.ID})
	m.note(text)
	return fmt.Sprintf("%s is now in %s. Read it from the environment; its value is not shown here.", name, shortPath(filepath.Join(session.Cwd, ".env"))), nil
}

// writeKey puts a key's value in dir's .env.
func (m *Manager) writeKey(ctx context.Context, dir string, key protocol.Key) error {
	value, err := m.secrets.Get(ctx, keyAccount+key.Name)
	if err != nil || value == "" {
		return fmt.Errorf("%s's value couldn't be read from the Keychain", key.Name)
	}
	return writeEnv(filepath.Join(dir, ".env"), key.Name, value)
}

// postKeyCardLocked asks the user whether an agent may have a key.
func (m *Manager) postKeyCardLocked(session protocol.Session, key protocol.Key, reason, why string, safe bool) {
	for _, open := range m.conversation.openCards() {
		if open.Card.Kind == protocol.CardKey && open.Card.SessionID == session.ID && open.Card.KeyName == key.Name {
			m.settleCardLocked(open, "Asked again")
		}
	}
	options := []protocol.QuestionOption{{Key: "give", Label: "Give it"}, {Key: "deny", Label: "Don't"}}
	if !safe {
		options = []protocol.QuestionOption{
			{Key: "gitignore", Label: "Add .env to .gitignore and give it"},
			{Key: "deny", Label: "Don't"},
		}
	}
	id := newID()
	card := &protocol.ManagerCard{
		ID: id, Kind: protocol.CardKey, State: protocol.CardOpen,
		SessionID: session.ID, AgentName: session.Name,
		Title: "asks for " + key.Name, Detail: strings.TrimSpace(reason), Reason: why,
		Context: key.Description, Options: options, KeyName: key.Name,
	}
	text := fmt.Sprintf("%s asks for %s (waits for you: %s)", session.Name, key.Name, why)
	m.addEntry(protocol.ManagerEntry{ID: id, Role: "card", Text: text, SessionID: session.ID, Card: card})
	m.note(fmt.Sprintf("Put a card in front of the user [card %s]: %s", id, text))
}

// answerKeyCardLocked is the user's answer to a key card.
func (m *Manager) answerKeyCardLocked(ctx context.Context, session protocol.Session, card *protocol.ManagerCard, choice string) (string, error) {
	key, ok := m.keys.get(card.KeyName)
	if !ok || !key.Set {
		return "", errors.New(card.KeyName + " is no longer kept")
	}
	switch choice {
	case "deny":
		_, _ = m.sendTo(ctx, session, fmt.Sprintf("(From Agentman) The user didn't give you %s. Carry on without it, or ask them.", key.Name))
		return "You didn't give it", nil
	case "gitignore":
		if err := ignoreEnv(session.Cwd); err != nil {
			return "", fmt.Errorf("couldn't add .env to .gitignore: %w", err)
		}
	case "give":
	default:
		return "", errors.New("choose Give it or Don't")
	}
	if safe, why := envIsSafe(ctx, session.Cwd); !safe {
		return "", errors.New(why)
	}
	if err := m.writeKey(ctx, session.Cwd, key); err != nil {
		return "", err
	}
	_, _ = m.sendTo(ctx, session, fmt.Sprintf("(From Agentman) %s is now in .env, from the user. Read it from the environment.", key.Name))
	if choice == "gitignore" {
		return "You gave it " + key.Name + ", and .env is now in .gitignore", nil
	}
	return "You gave it " + key.Name, nil
}

// envIsSafe reports whether dir's .env can hold a key without git taking
// it: the folder isn't in a repository, or .env is ignored and not tracked.
func envIsSafe(ctx context.Context, dir string) (bool, string) {
	git := func(args ...string) error {
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
		return cmd.Run()
	}
	if git("rev-parse", "--is-inside-work-tree") != nil {
		return true, ""
	}
	if git("ls-files", "--error-unmatch", ".env") == nil {
		return false, ".env is committed to git"
	}
	err := git("check-ignore", "-q", ".env")
	if err == nil {
		return true, ""
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return false, ".env isn't in .gitignore"
	}
	return false, "couldn't check whether git ignores .env"
}

// ignoreEnv adds .env to dir's .gitignore.
func ignoreEnv(dir string) error {
	path := filepath.Join(dir, ".gitignore")
	current, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	text := string(current)
	if text != "" && !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	text += ".env\n"
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	return os.WriteFile(path, []byte(text), mode)
}

// writeEnv sets name in the .env at path, replacing its line if it has
// one, and keeping every other line as it was.
func writeEnv(path, name, value string) error {
	current, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	line := name + "=" + envValue(value)
	var lines []string
	replaced := false
	if len(current) > 0 {
		for _, existing := range strings.Split(strings.TrimRight(string(current), "\n"), "\n") {
			trimmed := strings.TrimPrefix(strings.TrimSpace(existing), "export ")
			if strings.HasPrefix(trimmed, name+"=") {
				if !replaced {
					lines = append(lines, line)
					replaced = true
				}
				continue
			}
			lines = append(lines, existing)
		}
	}
	if !replaced {
		lines = append(lines, line)
	}
	mode := os.FileMode(0o600)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	tmp := path + ".agentman.tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(lines, "\n")+"\n"), mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// envValue quotes a value that .env readers would otherwise split or cut.
func envValue(value string) string {
	plain := true
	for _, r := range value {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_-.:/+=@,", r)) {
			plain = false
			break
		}
	}
	if plain {
		return value
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `\$`).Replace(value) + `"`
}

func inAnyFolder(dir string, folders []string) bool {
	for _, folder := range folders {
		if insideFolder(dir, folder) {
			return true
		}
	}
	return false
}

func expandHome(path string) string {
	if path == "~" || strings.HasPrefix(path, "~/") {
		return filepath.Join(homeDir(), strings.TrimPrefix(path, "~"))
	}
	return path
}
