// Package push delivers alerts to a phone that is not currently connected.
//
// The daemon posts straight to Expo's push service rather than routing through
// the relay. That keeps the relay's zero-storage promise intact — it never sees
// a notification — and means a self-hosted relay needs no push configuration at
// all.
//
// What travels off the machine is deliberately thin. A local notification is
// composed on the phone from data it already holds, but a push payload passes
// through Expo and Apple, so by default it carries a session name and a reason
// and no transcript content. IncludePreview opts into sending a short excerpt
// for anyone who would rather read the answer on the lock screen.
package push

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ExpoEndpoint is Expo's push service.
const ExpoEndpoint = "https://exp.host/--/api/v2/push/send"

const (
	// maxTokens bounds the store. A phone that reinstalls produces a new token,
	// and the old one lingers until Expo reports it unregistered.
	maxTokens = 32
	// tokenTTL retires a device that has not checked in. Expo tokens are stable,
	// so this only sheds phones that stopped using the daemon entirely.
	tokenTTL = 60 * 24 * time.Hour
	// sendTimeout bounds one delivery. Alerts are worth little late, and the
	// caller is a discovery sweep that must not stall behind a slow network.
	sendTimeout = 10 * time.Second
	// maxPreviewRunes caps an opted-in excerpt. Lock screens truncate anyway.
	maxPreviewRunes = 140
)

// Config controls what leaves the machine.
type Config struct {
	// IncludePreview attaches a short excerpt of the agent's output. Off by
	// default: that text passes through Expo and Apple, which is a different
	// trust boundary from a notification composed on the phone.
	IncludePreview bool `json:"includePreview,omitempty"`
}

// Token is one registered device.
type Token struct {
	Value string `json:"value"`
	// LastSeen is refreshed whenever the app re-registers, which it does on
	// every connect, so an active phone never expires.
	LastSeen int64 `json:"lastSeen"`
}

// Store holds registered device tokens, persisted beside the daemon's config.
type Store struct {
	path string

	mu     sync.Mutex
	tokens map[string]int64
}

// NewStore loads the token file, tolerating a missing or unreadable one: push
// is an enhancement, and failing to read it must never stop the daemon.
func NewStore(dir string) *Store {
	s := &Store{
		path:   filepath.Join(dir, "push.json"),
		tokens: map[string]int64{},
	}
	raw, err := os.ReadFile(s.path)
	if err != nil {
		return s
	}
	var stored []Token
	if json.Unmarshal(raw, &stored) != nil {
		return s
	}
	cutoff := time.Now().Add(-tokenTTL).UnixMilli()
	for _, token := range stored {
		if ValidToken(token.Value) && token.LastSeen > cutoff {
			s.tokens[token.Value] = token.LastSeen
		}
	}
	return s
}

// ValidToken reports whether a string is shaped like an Expo push token.
//
// Checked because the value is supplied by whoever holds a device credential
// and is then sent to a third-party API. A malformed token cannot be delivered
// to, and an unbounded one has no business being persisted.
func ValidToken(value string) bool {
	if len(value) < 20 || len(value) > 256 {
		return false
	}
	if !strings.HasPrefix(value, "ExponentPushToken[") &&
		!strings.HasPrefix(value, "ExpoPushToken[") {
		return false
	}
	return strings.HasSuffix(value, "]")
}

// Register records a device, refreshing it if already known. Returns whether
// anything changed, so a caller can skip a needless disk write.
func (s *Store) Register(value string) (bool, error) {
	if !ValidToken(value) {
		return false, fmt.Errorf("push: not an Expo push token")
	}
	s.mu.Lock()
	_, existed := s.tokens[value]
	s.tokens[value] = time.Now().UnixMilli()
	if len(s.tokens) > maxTokens {
		s.evictOldestLocked()
	}
	s.mu.Unlock()
	if err := s.save(); err != nil {
		return !existed, err
	}
	return !existed, nil
}

// Forget drops a token Expo has told us is dead.
func (s *Store) Forget(value string) {
	s.mu.Lock()
	delete(s.tokens, value)
	s.mu.Unlock()
	_ = s.save()
}

// Tokens returns the currently registered devices.
func (s *Store) Tokens() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.tokens))
	for token := range s.tokens {
		out = append(out, token)
	}
	return out
}

func (s *Store) evictOldestLocked() {
	oldest, oldestAt := "", int64(0)
	for token, seen := range s.tokens {
		if oldest == "" || seen < oldestAt {
			oldest, oldestAt = token, seen
		}
	}
	delete(s.tokens, oldest)
}

func (s *Store) save() error {
	s.mu.Lock()
	stored := make([]Token, 0, len(s.tokens))
	for token, seen := range s.tokens {
		stored = append(stored, Token{Value: token, LastSeen: seen})
	}
	s.mu.Unlock()
	body, err := json.MarshalIndent(stored, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(s.path, body, 0o600)
}

// Alert is one notification to deliver.
type Alert struct {
	Title string
	Body  string
	// SessionID lets the app open the right screen when the alert is tapped.
	SessionID string
}

// Sender posts alerts to Expo.
type Sender struct {
	Endpoint string
	Client   *http.Client
	Store    *Store
	Config   Config
}

// NewSender builds a sender with a bounded HTTP client.
func NewSender(store *Store, cfg Config) *Sender {
	return &Sender{
		Endpoint: ExpoEndpoint,
		Client:   &http.Client{Timeout: sendTimeout},
		Store:    store,
		Config:   cfg,
	}
}

type expoMessage struct {
	To    string         `json:"to"`
	Title string         `json:"title"`
	Body  string         `json:"body"`
	Sound string         `json:"sound"`
	Data  map[string]any `json:"data,omitempty"`
}

type expoResponse struct {
	Data []struct {
		Status  string `json:"status"`
		Message string `json:"message"`
		Details struct {
			Error string `json:"error"`
		} `json:"details"`
	} `json:"data"`
}

// Send delivers one alert to every registered device.
//
// Errors are returned for logging but are never fatal: a failed push is a
// missed convenience, and the app still shows the state when next opened.
func (s *Sender) Send(ctx context.Context, alert Alert) error {
	if s == nil || s.Store == nil {
		return nil
	}
	tokens := s.Store.Tokens()
	if len(tokens) == 0 {
		return nil
	}

	messages := make([]expoMessage, 0, len(tokens))
	for _, token := range tokens {
		message := expoMessage{
			To: token, Title: alert.Title, Body: alert.Body, Sound: "default",
		}
		if alert.SessionID != "" {
			message.Data = map[string]any{"sessionId": alert.SessionID}
		}
		messages = append(messages, message)
	}

	body, err := json.Marshal(messages)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.Endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := s.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("push: expo returned %s", resp.Status)
	}

	var decoded expoResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return err
	}
	// Expo answers per message, in the order sent. A device that has uninstalled
	// the app reports DeviceNotRegistered, which is the only reliable signal that
	// a token is dead — dropping it keeps the store from growing forever.
	var failures []error
	for i, result := range decoded.Data {
		if result.Status == "ok" || i >= len(tokens) {
			continue
		}
		if result.Details.Error == "DeviceNotRegistered" {
			s.Store.Forget(tokens[i])
			continue
		}
		failures = append(failures, errors.New(result.Message))
	}
	return errors.Join(failures...)
}

// Preview returns the excerpt to attach to an alert, honouring the config.
func (s *Sender) Preview(text string) string {
	if s == nil || !s.Config.IncludePreview {
		return ""
	}
	return clip(text, maxPreviewRunes)
}

func clip(text string, limit int) string {
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return strings.TrimSpace(string(runes[:limit])) + "…"
}

// writeFileAtomic mirrors the hook package: a crash mid-write must not leave a
// truncated token file behind.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
