package shellenv

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
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
			return []string{"OPENAI_API_KEY=sk-very-secret", "PATH=/a"}, nil
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

func TestAWeekOldCopyIsReadAgain(t *testing.T) {
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
