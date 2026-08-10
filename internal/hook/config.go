package hook

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// DefaultPort is where the daemon listens for hook deliveries. It binds
// loopback only — these events never leave the machine.
const DefaultPort = 8787

// DefaultAddr is the loopback listen address.
var DefaultAddr = fmt.Sprintf("127.0.0.1:%d", DefaultPort)

// Config is the daemon's local state, kept in ~/.agentman/config.json.
type Config struct {
	// Token authenticates hook deliveries. Binding to loopback keeps other
	// machines out, but any local process can still reach the port, and a
	// forged "turn complete" would ring the user's phone with a lie. The
	// token is cheap and closes that gap.
	Token string `json:"token"`
}

// ConfigDir returns ~/.agentman, creating it if needed.
func ConfigDir(home string) (string, error) {
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return "", err
		}
	}
	dir := filepath.Join(home, ".agentman")
	// 0700: the directory holds the hook token.
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// LoadConfig reads the local config, creating one with a fresh token on first
// use so neither install nor serve has to be run in a particular order.
func LoadConfig(home string) (Config, error) {
	dir, err := ConfigDir(home)
	if err != nil {
		return Config{}, err
	}
	path := filepath.Join(dir, "config.json")

	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		var cfg Config
		if json.Unmarshal(raw, &cfg) == nil && cfg.Token != "" {
			return cfg, nil
		}
		// Corrupt or tokenless: fall through and mint a new one rather than
		// leaving the daemon unable to authenticate anything.
	case !os.IsNotExist(err):
		return Config{}, err
	}

	token, err := newToken()
	if err != nil {
		return Config{}, err
	}
	cfg := Config{Token: token}
	body, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return Config{}, err
	}
	if err := writeFileAtomic(path, body, 0o600); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func newToken() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// writeFileAtomic writes via a temporary file in the same directory and
// renames it into place.
//
// This matters most for ~/.claude/settings.json: a crash or a full disk
// midway through a naive write would leave the user's real Claude Code config
// truncated. A rename is atomic, so the file is either the old one or the new
// one and never a broken half.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	// Flush to disk before the rename so a power loss cannot leave an empty
	// file where a valid config used to be.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
