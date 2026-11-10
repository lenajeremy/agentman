package shellenv

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type memoryKeys map[string]string

func (m memoryKeys) Get(_ context.Context, name string) (string, error) {
	if value, ok := m[name]; ok {
		return value, nil
	}
	return "", errors.New("not found")
}
func (m memoryKeys) Set(_ context.Context, name, value string) error { m[name] = value; return nil }

type cacheLab struct {
	cache Cache
	home  string
	runs  int
	fail  bool
	clock time.Time
}

func newCacheLab(t *testing.T) *cacheLab {
	t.Helper()
	lab := &cacheLab{home: t.TempDir(), clock: time.UnixMilli(1_000_000)}
	lab.cache = Cache{
		Path: filepath.Join(t.TempDir(), "shell-env"), Keys: memoryKeys{}, Home: lab.home,
		Now: func() time.Time { return lab.clock },
		Load: func(context.Context) ([]string, error) {
			lab.runs++
			if lab.fail {
				return nil, errors.New("the shell timed out")
			}
			return []string{"OPENAI_API_KEY=sk-very-secret", "PATH=/a", "SSH_AUTH_SOCK=/private/tmp/com.apple.launchd.old/Listeners"}, nil
		},
	}
	if err := os.WriteFile(filepath.Join(lab.home, ".zshrc"), []byte("export A=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return lab
}

func (l *cacheLab) get(t *testing.T) ([]string, bool) {
	t.Helper()
	env, fresh, err := l.cache.Environment(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return env, fresh
}

func TestTheShellRunsOnceThenTheSavedCopyIsUsed(t *testing.T) {
	lab := newCacheLab(t)
	if _, fresh := lab.get(t); !fresh {
		t.Fatal("first time should run the shell")
	}
	env, fresh := lab.get(t)
	if fresh || lab.runs != 1 || len(env) != 2 || env[1] != "PATH=/a" {
		t.Fatalf("fresh %v runs %d env %v", fresh, lab.runs, env)
	}
}

func TestChangingAStartupFileReadsTheShellAgain(t *testing.T) {
	lab := newCacheLab(t)
	lab.get(t)
	if err := os.WriteFile(filepath.Join(lab.home, ".zshrc"), []byte("export A=22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, fresh := lab.get(t); !fresh || lab.runs != 2 {
		t.Fatalf("fresh %v runs %d", fresh, lab.runs)
	}
}

func TestASavedCopyLeavesOutWhatChangesEachLogin(t *testing.T) {
	lab := newCacheLab(t)
	first, _ := lab.get(t)
	if len(first) != 3 {
		t.Fatalf("the shell's own answer should be whole: %v", first)
	}
	again, fresh := lab.get(t)
	if fresh || len(again) != 2 || strings.Contains(strings.Join(again, " "), "SSH_AUTH_SOCK") {
		t.Fatalf("fresh %v env %v", fresh, again)
	}
}

func TestASocketTheProfileChoseIsKept(t *testing.T) {
	kept := lasting([]string{"SSH_AUTH_SOCK=/Users/me/.1password/agent.sock", "PATH=/a"})
	if len(kept) != 2 {
		t.Fatalf("got %v", kept)
	}
	if got := lasting([]string{"SSH_AUTH_SOCK=/private/tmp/com.apple.launchd.x/Listeners"}); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

func TestAShellThatFailsFallsBackOnTheSavedCopy(t *testing.T) {
	lab := newCacheLab(t)
	lab.get(t)
	lab.clock = lab.clock.Add(MaxCacheAge + time.Hour)
	lab.fail = true
	env, fresh := lab.get(t)
	if fresh || len(env) != 2 {
		t.Fatalf("fresh %v env %v", fresh, env)
	}
}

func TestZshsOwnFolderCounts(t *testing.T) {
	lab := newCacheLab(t)
	folder := t.TempDir()
	if err := os.WriteFile(filepath.Join(folder, ".zshrc"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := []string{"ZDOTDIR=" + folder}
	before := Fingerprint(lab.home, "/bin/zsh", env)
	if err := os.WriteFile(filepath.Join(folder, ".zshrc"), []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}
	if Fingerprint(lab.home, "/bin/zsh", env) == before {
		t.Fatal("a change in ZDOTDIR went unnoticed")
	}
}

func TestADayOldCopyIsReadAgain(t *testing.T) {
	lab := newCacheLab(t)
	lab.get(t)
	lab.clock = lab.clock.Add(MaxCacheAge + time.Minute)
	if _, fresh := lab.get(t); !fresh {
		t.Fatal("kept too long")
	}
}

func TestNothingSecretIsOnDiskAsText(t *testing.T) {
	lab := newCacheLab(t)
	lab.get(t)
	raw, err := os.ReadFile(lab.cache.Path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("sk-very-secret")) {
		t.Fatal("the key is readable in the file")
	}
	if info, _ := os.Stat(lab.cache.Path); info.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", info.Mode().Perm())
	}
}

func TestALostKeyMeansReadingTheShellNotFailing(t *testing.T) {
	lab := newCacheLab(t)
	lab.get(t)
	lab.cache.Keys = memoryKeys{}
	if _, fresh := lab.get(t); !fresh || lab.runs != 2 {
		t.Fatalf("fresh %v runs %d", fresh, lab.runs)
	}
}
