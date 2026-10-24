// Package secrets keeps the user's keys: the Anthropic API key Agentman runs
// on, and later the keys it hands to agents.
//
// On a Mac they live in the login Keychain, as generic passwords under the
// service "agentman". Values go to /usr/bin/security on its standard input,
// never on its command line, where any process could read them. Elsewhere,
// where there is no Keychain, they live in a file in the config directory
// that only the user can read, as the hook token already does.
//
// Nothing here logs or returns a value except Get.
package secrets

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// ErrNotFound means no value is stored under that name.
var ErrNotFound = errors.New("secrets: not found")

// Store holds named values.
type Store interface {
	Get(ctx context.Context, name string) (string, error)
	Set(ctx context.Context, name, value string) error
	Delete(ctx context.Context, name string) error
}

// Default is the Keychain on a Mac, and a private file in configDir elsewhere.
func Default(configDir string) Store {
	if runtime.GOOS == "darwin" {
		if _, err := exec.LookPath(securityTool); err == nil {
			return NewKeychain("agentman")
		}
	}
	return &File{Path: filepath.Join(configDir, "secrets.json")}
}

const securityTool = "/usr/bin/security"

// Keychain stores values as generic passwords in the login Keychain.
type Keychain struct {
	Service string
	// run executes the security tool; injectable so tests need no Keychain.
	run func(ctx context.Context, stdin string, args ...string) (string, int, error)
}

func NewKeychain(service string) *Keychain {
	return &Keychain{Service: service, run: runSecurity}
}

// itemNotFound is the security tool's exit status for a missing item.
const itemNotFound = 44

func (k *Keychain) Get(ctx context.Context, name string) (string, error) {
	out, code, err := k.run(ctx, "", "find-generic-password", "-s", k.Service, "-a", name, "-w")
	if code == itemNotFound {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("secrets: read %s from the Keychain: %w", name, err)
	}
	return strings.TrimSuffix(out, "\n"), nil
}

func (k *Keychain) Set(ctx context.Context, name, value string) error {
	if err := checkValue(value); err != nil {
		return err
	}
	// -w last makes the tool prompt for the value, twice, on standard input.
	input := value + "\n" + value + "\n"
	if _, _, err := k.run(ctx, input, "add-generic-password", "-U", "-s", k.Service, "-a", name, "-w"); err != nil {
		return fmt.Errorf("secrets: save %s to the Keychain: %w", name, err)
	}
	return nil
}

func (k *Keychain) Delete(ctx context.Context, name string) error {
	_, code, err := k.run(ctx, "", "delete-generic-password", "-s", k.Service, "-a", name)
	if code == itemNotFound {
		return nil
	}
	if err != nil {
		return fmt.Errorf("secrets: delete %s from the Keychain: %w", name, err)
	}
	return nil
}

func runSecurity(ctx context.Context, stdin string, args ...string) (string, int, error) {
	cmd := exec.CommandContext(ctx, securityTool, args...)
	cmd.Stdin = strings.NewReader(stdin)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	code := 0
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code = exit.ExitCode()
		// The tool's stderr says what went wrong without ever containing the
		// value, which only travels on stdin.
		err = fmt.Errorf("%s (exit %d)", strings.TrimSpace(errOut.String()), code)
	}
	return out.String(), code, err
}

// checkValue refuses what cannot be stored faithfully: a value is one line.
func checkValue(value string) error {
	if value == "" {
		return errors.New("secrets: empty value")
	}
	if strings.ContainsAny(value, "\r\n\x00") {
		return errors.New("secrets: a value must be one line")
	}
	return nil
}

// File stores values in a JSON file that only the user can read.
type File struct {
	Path string
	mu   sync.Mutex
}

func (f *File) load() (map[string]string, error) {
	raw, err := os.ReadFile(f.Path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	values := map[string]string{}
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, fmt.Errorf("secrets: %s is not readable: %w", f.Path, err)
	}
	return values, nil
}

func (f *File) save(values map[string]string) error {
	raw, err := json.MarshalIndent(values, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(f.Path), 0o700); err != nil {
		return err
	}
	tmp := f.Path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, f.Path)
}

func (f *File) Get(_ context.Context, name string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	values, err := f.load()
	if err != nil {
		return "", err
	}
	value, ok := values[name]
	if !ok {
		return "", ErrNotFound
	}
	return value, nil
}

func (f *File) Set(_ context.Context, name, value string) error {
	if err := checkValue(value); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	values, err := f.load()
	if err != nil {
		return err
	}
	values[name] = value
	return f.save(values)
}

func (f *File) Delete(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	values, err := f.load()
	if err != nil {
		return err
	}
	if _, ok := values[name]; !ok {
		return nil
	}
	delete(values, name)
	return f.save(values)
}
