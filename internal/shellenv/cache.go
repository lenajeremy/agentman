package shellenv

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Running the login shell at every start can bring up a macOS permission
// dialog at login, with no agent running, when a profile touches a protected
// folder. So what it sets up is kept, and read again only when a startup file
// changes or a day has passed. It can hold API keys a profile exports, so it
// is kept encrypted, with the key in the Keychain; it is never on disk as text.

const (
	// MaxCacheAge is how long a saved environment is trusted. Startup files
	// can source others that the fingerprint can't see, and a value can come
	// from a command, such as a login token.
	MaxCacheAge  = 24 * time.Hour
	cacheKeyName = "shell-env-key"
)

// perLogin are set afresh for each login, or expire within hours. A saved copy
// would be out of date, and would replace the current value: an old
// SSH_AUTH_SOCK breaks every agent's git over SSH.
var perLogin = map[string]bool{
	"SSH_AUTH_SOCK": true, "SSH_AGENT_PID": true, "SSH_CONNECTION": true, "SSH_CLIENT": true, "SSH_TTY": true,
	"TMPDIR": true, "XPC_SERVICE_NAME": true, "XPC_FLAGS": true, "__CFBundleIdentifier": true,
	"__CF_USER_TEXT_ENCODING": true, "SECURITYSESSIONID": true, "LaunchInstanceID": true,
	"Apple_PubSub_Socket_Render": true, "DISPLAY": true, "AWS_SESSION_TOKEN": true, "AWS_SECURITY_TOKEN": true,
}

// Keys is where the encryption key lives.
type Keys interface {
	Get(ctx context.Context, name string) (string, error)
	Set(ctx context.Context, name, value string) error
}

type cached struct {
	Fingerprint string   `json:"fingerprint"`
	SavedAt     int64    `json:"savedAt"`
	Env         []string `json:"env"`
}

// Cache keeps a login shell's environment between starts.
type Cache struct {
	Path string
	Keys Keys
	Home string
	Now  func() time.Time
	// Load reads the login shell; Load above, outside tests.
	Load func(ctx context.Context) ([]string, error)
}

// Environment is the login shell's environment, from the saved copy when it
// is current. fresh says the shell was run. A shell that fails falls back on
// a saved copy of any age, which is better than none: without one, agents
// can't even find node.
func (c Cache) Environment(ctx context.Context) (env []string, fresh bool, err error) {
	saved, have := c.read(ctx)
	if have && saved.Fingerprint == Fingerprint(c.Home, os.Getenv("SHELL"), saved.Env) &&
		c.Now().Sub(time.UnixMilli(saved.SavedAt)) < MaxCacheAge {
		return saved.Env, false, nil
	}
	env, err = c.Load(ctx)
	if err != nil {
		if have {
			return saved.Env, false, nil
		}
		return nil, true, err
	}
	// Not being able to save only means the shell runs again next time.
	_ = c.write(ctx, cached{
		Fingerprint: Fingerprint(c.Home, os.Getenv("SHELL"), env),
		SavedAt:     c.Now().UnixMilli(), Env: lasting(env),
	})
	return env, true, nil
}

func lasting(env []string) []string {
	kept := make([]string, 0, len(env))
	for _, entry := range env {
		name, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		// A socket the profile points at, such as a password manager's, is
		// the same every login; macOS's own is not.
		stable := name == "SSH_AUTH_SOCK" && !strings.HasPrefix(value, "/private/tmp/") && !strings.HasPrefix(value, "/tmp/")
		if !perLogin[name] || stable {
			kept = append(kept, entry)
		}
	}
	return kept
}

// Fingerprint changes when the shell or any of its startup files do, wherever
// the environment says zsh keeps them.
func Fingerprint(home, shell string, env []string) string {
	parts := []string{shell}
	dirs := []string{home}
	for _, entry := range env {
		if dir, ok := strings.CutPrefix(entry, "ZDOTDIR="); ok && dir != "" && dir != home {
			dirs = append(dirs, dir)
		}
	}
	for _, dir := range dirs {
		for _, name := range []string{".zshenv", ".zprofile", ".zshrc", ".zlogin", ".bash_profile", ".bashrc", ".profile"} {
			if info, err := os.Stat(filepath.Join(dir, name)); err == nil {
				parts = append(parts, fmt.Sprintf("%s/%s:%d:%d", dir, name, info.Size(), info.ModTime().UnixNano()))
			}
		}
	}
	for _, name := range []string{".config/fish/config.fish", ".config/fish/conf.d"} {
		if info, err := os.Stat(filepath.Join(home, name)); err == nil {
			parts = append(parts, fmt.Sprintf("%s:%d", name, info.ModTime().UnixNano()))
		}
	}
	return strings.Join(parts, "|")
}

func (c Cache) read(ctx context.Context) (cached, bool) {
	var saved cached
	raw, err := os.ReadFile(c.Path)
	if err != nil {
		return saved, false
	}
	key, err := c.Keys.Get(ctx, cacheKeyName)
	if err != nil {
		return saved, false
	}
	block, err := gcm(key)
	if err != nil || len(raw) < block.NonceSize() {
		return saved, false
	}
	text, err := block.Open(nil, raw[:block.NonceSize()], raw[block.NonceSize():], nil)
	if err != nil || json.Unmarshal(text, &saved) != nil {
		return saved, false
	}
	return saved, true
}

func (c Cache) write(ctx context.Context, saved cached) error {
	key, err := c.Keys.Get(ctx, cacheKeyName)
	if err != nil {
		bytes := make([]byte, 32)
		if _, err := rand.Read(bytes); err != nil {
			return err
		}
		key = hex.EncodeToString(bytes)
		if err := c.Keys.Set(ctx, cacheKeyName, key); err != nil {
			return err
		}
	}
	block, err := gcm(key)
	if err != nil {
		return err
	}
	text, err := json.Marshal(saved)
	if err != nil {
		return err
	}
	nonce := make([]byte, block.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(c.Path), 0o700); err != nil {
		return err
	}
	tmp := c.Path + ".tmp"
	if err := os.WriteFile(tmp, block.Seal(nonce, nonce, text, nil), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, c.Path)
}

func gcm(key string) (cipher.AEAD, error) {
	bytes, err := hex.DecodeString(key)
	if err != nil {
		return nil, err
	}
	if len(bytes) != 32 {
		return nil, errors.New("shellenv: the saved key is the wrong size")
	}
	block, err := aes.NewCipher(bytes)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
