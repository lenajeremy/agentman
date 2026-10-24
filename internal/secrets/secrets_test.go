package secrets

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type call struct {
	stdin string
	args  []string
}

func fakeKeychain(stored map[string]string, calls *[]call) *Keychain {
	return &Keychain{Service: "agentman", run: func(_ context.Context, stdin string, args ...string) (string, int, error) {
		*calls = append(*calls, call{stdin, args})
		name := args[slices.Index(args, "-a")+1]
		switch args[0] {
		case "find-generic-password":
			value, ok := stored[name]
			if !ok {
				return "", itemNotFound, errors.New("not found")
			}
			return value + "\n", 0, nil
		case "add-generic-password":
			stored[name] = strings.SplitN(stdin, "\n", 2)[0]
			return "", 0, nil
		case "delete-generic-password":
			if _, ok := stored[name]; !ok {
				return "", itemNotFound, errors.New("not found")
			}
			delete(stored, name)
			return "", 0, nil
		}
		return "", 1, errors.New("unexpected")
	}}
}

// A value goes to the security tool on stdin, never in its arguments, where
// any process listing could read it.
func TestAKeychainValueNeverAppearsOnTheCommandLine(t *testing.T) {
	var calls []call
	stored := map[string]string{}
	k := fakeKeychain(stored, &calls)
	const key = "sk-ant-api03-secret"
	if err := k.Set(context.Background(), "anthropic-api-key", key); err != nil {
		t.Fatal(err)
	}
	for _, c := range calls {
		for _, arg := range c.args {
			if strings.Contains(arg, key) {
				t.Fatalf("the key was passed as an argument: %v", c.args)
			}
		}
	}
	if calls[0].args[len(calls[0].args)-1] != "-w" || calls[0].stdin != key+"\n"+key+"\n" {
		t.Fatalf("set called %+v; want -w last and the value twice on stdin", calls[0])
	}
	if got, err := k.Get(context.Background(), "anthropic-api-key"); err != nil || got != key {
		t.Fatalf("Get = %q, %v", got, err)
	}
}

func TestAMissingSecretIsNotFoundNotAnError(t *testing.T) {
	var calls []call
	k := fakeKeychain(map[string]string{}, &calls)
	if _, err := k.Get(context.Background(), "nothing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get = %v, want ErrNotFound", err)
	}
	if err := k.Delete(context.Background(), "nothing"); err != nil {
		t.Fatalf("deleting what is not there = %v", err)
	}
}

func TestAValueMustBeOneLine(t *testing.T) {
	var calls []call
	k := fakeKeychain(map[string]string{}, &calls)
	for _, bad := range []string{"", "two\nlines", "nul\x00"} {
		if err := k.Set(context.Background(), "x", bad); err == nil {
			t.Errorf("Set(%q) was accepted", bad)
		}
	}
	if len(calls) != 0 {
		t.Fatalf("the tool was run for a value that cannot be stored: %v", calls)
	}
}

// Without a Keychain, values live in a file only the user can read.
func TestTheFileStoreIsPrivate(t *testing.T) {
	f := &File{Path: filepath.Join(t.TempDir(), "secrets.json")}
	ctx := context.Background()
	if err := f.Set(ctx, "anthropic-api-key", "sk-test"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(f.Path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("mode = %o, want 600", mode)
	}
	if got, _ := f.Get(ctx, "anthropic-api-key"); got != "sk-test" {
		t.Fatalf("Get = %q", got)
	}
	if err := f.Delete(ctx, "anthropic-api-key"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Get(ctx, "anthropic-api-key"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete, Get = %v", err)
	}
}
