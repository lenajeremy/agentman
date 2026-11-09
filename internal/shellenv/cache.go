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
// changes or a week has passed. It can hold API keys a profile exports, so it
// is kept encrypted, with the key in the Keychain; it is never on disk as text.

const (
	// MaxCacheAge is how long a saved environment is trusted. Startup files
	// can source others that the fingerprint can't see.
	MaxCacheAge  = 7 * 24 * time.Hour
	cacheKeyName = "shell-env-key"
)

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
// is current. fresh says the shell was run.
func (c Cache) Environment(ctx context.Context) (env []string, fresh bool, err error) {
	fingerprint := Fingerprint(c.Home, os.Getenv("SHELL"))
	if saved, ok := c.read(ctx); ok && saved.Fingerprint == fingerprint &&
		c.Now().Sub(time.UnixMilli(saved.SavedAt)) < MaxCacheAge {
		return saved.Env, false, nil
	}
	env, err = c.Load(ctx)
	if err != nil {
		return nil, true, err
	}
	// Not being able to save only means the shell runs again next time.
	_ = c.write(ctx, cached{Fingerprint: fingerprint, SavedAt: c.Now().UnixMilli(), Env: env})
	return env, true, nil
}

// Fingerprint changes when the shell or any of its startup files do.
func Fingerprint(home, shell string) string {
	parts := []string{shell}
	for _, name := range []string{".zshenv", ".zprofile", ".zshrc", ".zlogin", ".bash_profile", ".bashrc", ".profile"} {
		if info, err := os.Stat(filepath.Join(home, name)); err == nil {
			parts = append(parts, fmt.Sprintf("%s:%d:%d", name, info.Size(), info.ModTime().UnixNano()))
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
