package source

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

const claudeInitReply = `{"type":"control_response","response":{"subtype":"success","request_id":"agentman-models","response":{"claude_code_version":"2.1.322","models":[` +
	`{"value":"default","displayName":"Default (recommended)"},{"value":"opus","displayName":"Opus 5.5"},` +
	`{"value":"fable","displayName":"Fable 5.2"},{"value":"claude-fable-5-1","displayName":"Fable 5.1"},` +
	`{"value":"claude-haiku-4-5-20251001","displayName":"Haiku 4.5"},{"value":"claude-opus-5-5","displayName":"Opus 5.5"}]}}}`

func TestClaudeModelsAreReadFromItsInitializeReply(t *testing.T) {
	stream := strings.Join([]string{
		`{"type":"system","subtype":"hook_started"}`,
		`{"type":"control_response","response":{"subtype":"success","request_id":"someone-else","response":{}}}`,
		claudeInitReply,
	}, "\n") + "\n"
	list, err := readClaudeModels(strings.NewReader(stream), "agentman-models")
	if err != nil {
		t.Fatal(err)
	}
	if list.Version != "2.1.322" {
		t.Errorf("version %q", list.Version)
	}
	// Its own order; "Default (recommended)" is no model of its own; each
	// row once.
	want := []string{"Opus 5.5", "Fable 5.2", "Fable 5.1", "Haiku 4.5"}
	if !reflect.DeepEqual(list.Labels, want) {
		t.Errorf("labels %v, want %v", list.Labels, want)
	}
}

func TestClaudeModelsFailWithoutAnAnswer(t *testing.T) {
	if _, err := readClaudeModels(strings.NewReader(`{"type":"system"}`+"\n"), "agentman-models"); err == nil {
		t.Error("an ended stream should fail")
	}
	refused := `{"type":"control_response","response":{"subtype":"error","request_id":"agentman-models","error":"not signed in"}}` + "\n"
	if _, err := readClaudeModels(strings.NewReader(refused), "agentman-models"); err == nil || !strings.Contains(err.Error(), "not signed in") {
		t.Errorf("a refusal should fail with its words: %v", err)
	}
	none := `{"type":"control_response","response":{"subtype":"success","request_id":"agentman-models","response":{"models":[{"value":"default","displayName":"Default (recommended)"}]}}}` + "\n"
	if _, err := readClaudeModels(strings.NewReader(none), "agentman-models"); err == nil {
		t.Error("no models should fail, keeping the list it had")
	}
}

// A stand-in for Claude Code: it notes how it was started and where, checks
// it was asked to initialize, answers, and ends when its input does.
func TestClaudeModelsProbeAsksAHeadlessClaudeInAnEmptyFolder(t *testing.T) {
	dir := t.TempDir()
	notes := filepath.Join(dir, "notes")
	script := filepath.Join(dir, "claude")
	body := "#!/bin/sh\n" +
		"echo \"$@\" > " + notes + ".args\n" +
		"pwd > " + notes + ".dir\n" +
		"ls -A > " + notes + ".files\n" +
		"read line\n" +
		"case \"$line\" in *initialize*) ;; *) exit 3 ;; esac\n" +
		"echo '{\"type\":\"system\",\"subtype\":\"init\"}'\n" +
		"echo '" + claudeInitReply + "'\n" +
		"cat > /dev/null\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	list, err := probeClaudeModels(context.Background(), script)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Labels) != 4 || list.Labels[1] != "Fable 5.2" {
		t.Errorf("labels %v", list.Labels)
	}
	args, _ := os.ReadFile(notes + ".args")
	for _, flag := range []string{"-p", "--input-format stream-json", "--output-format stream-json", "--setting-sources project", "--no-session-persistence"} {
		if !strings.Contains(string(args), flag) {
			t.Errorf("started without %q: %s", flag, args)
		}
	}
	files, _ := os.ReadFile(notes + ".files")
	if strings.TrimSpace(string(files)) != "" {
		t.Errorf("its folder should be empty: %q", files)
	}
	where, _ := os.ReadFile(notes + ".dir")
	if _, err := os.Stat(strings.TrimSpace(string(where))); !os.IsNotExist(err) {
		t.Errorf("its folder should be gone after: %s", where)
	}
}

// A catalog with a stand-in program and probe, its clock moved by hand.
type catalogLab struct {
	catalog *claudeModelCatalog
	binary  string
	clock   time.Time
	mu      sync.Mutex
	probes  int
	answer  []string
	fail    bool
}

func newCatalogLab(t *testing.T, home string) *catalogLab {
	t.Helper()
	lab := &catalogLab{binary: filepath.Join(home, "claude"), clock: time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)}
	if _, err := os.Stat(lab.binary); err != nil {
		if err := os.WriteFile(lab.binary, []byte("v1"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	lab.answer = []string{"Opus 5.5", "Fable 5.2"}
	c := newClaudeModelCatalog(home)
	c.look = func() (string, error) { return lab.binary, nil }
	c.now = func() time.Time { return lab.clock }
	c.probe = func(context.Context, string) (claudeModelList, error) {
		lab.mu.Lock()
		defer lab.mu.Unlock()
		lab.probes++
		if lab.fail {
			return claudeModelList{}, os.ErrDeadlineExceeded
		}
		return claudeModelList{Version: "2.1.322", Labels: append([]string(nil), lab.answer...)}, nil
	}
	lab.catalog = c
	return lab
}

// refresh asks for a refresh and waits for any probe it starts.
func (lab *catalogLab) refresh(t *testing.T) {
	t.Helper()
	lab.catalog.refreshIfDue()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		lab.catalog.mu.Lock()
		running := lab.catalog.running
		lab.catalog.mu.Unlock()
		if !running {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("probe still running")
}

func (lab *catalogLab) count() int {
	lab.mu.Lock()
	defer lab.mu.Unlock()
	return lab.probes
}

func TestClaudeModelsAreAskedWhenDueAndKept(t *testing.T) {
	home := t.TempDir()
	lab := newCatalogLab(t, home)

	lab.refresh(t)
	if lab.count() != 1 || !reflect.DeepEqual(lab.catalog.labels(), []string{"Opus 5.5", "Fable 5.2"}) {
		t.Fatalf("first: %d probes, %v", lab.count(), lab.catalog.labels())
	}
	// Kept on disk: a restart offers it at once, without asking.
	again := newCatalogLab(t, home)
	if got := again.catalog.labels(); !reflect.DeepEqual(got, []string{"Opus 5.5", "Fable 5.2"}) {
		t.Errorf("after a restart %v", got)
	}

	// Not again within the minute, nor while the program and the list are
	// the same and young.
	lab.refresh(t)
	lab.clock = lab.clock.Add(2 * time.Hour)
	lab.refresh(t)
	if lab.count() != 1 {
		t.Errorf("asked again too soon: %d", lab.count())
	}

	// A new Claude Code: asked again, and a model Anthropic took down goes.
	lab.answer = []string{"Opus 5.5", "Fable 5.3"}
	later := lab.clock.Add(time.Minute)
	if err := os.Chtimes(lab.binary, later, later); err != nil {
		t.Fatal(err)
	}
	lab.clock = later.Add(time.Minute)
	lab.refresh(t)
	if lab.count() != 2 || !reflect.DeepEqual(lab.catalog.labels(), []string{"Opus 5.5", "Fable 5.3"}) {
		t.Errorf("after an update: %d probes, %v", lab.count(), lab.catalog.labels())
	}

	// Six hours on, for what Anthropic changed on its side.
	lab.clock = lab.clock.Add(claudeModelsEvery)
	lab.refresh(t)
	if lab.count() != 3 {
		t.Errorf("not asked after six hours: %d", lab.count())
	}
}

func TestAFailedProbeKeepsTheListAndTriesLater(t *testing.T) {
	lab := newCatalogLab(t, t.TempDir())
	lab.refresh(t)
	lab.fail = true
	lab.clock = lab.clock.Add(claudeModelsEvery)
	lab.refresh(t)
	if !reflect.DeepEqual(lab.catalog.labels(), []string{"Opus 5.5", "Fable 5.2"}) {
		t.Errorf("a failure lost the list: %v", lab.catalog.labels())
	}
	lab.clock = lab.clock.Add(5 * time.Minute)
	lab.refresh(t)
	if lab.count() != 2 {
		t.Errorf("tried again before the retry was due: %d", lab.count())
	}
	lab.fail = false
	lab.answer = []string{"Opus 5.5"}
	lab.clock = lab.clock.Add(claudeModelsRetry)
	lab.refresh(t)
	if lab.count() != 3 || !reflect.DeepEqual(lab.catalog.labels(), []string{"Opus 5.5"}) {
		t.Errorf("after the retry: %d probes, %v", lab.count(), lab.catalog.labels())
	}
}

func TestOfferedModelsAreWhatClaudeCodeLists(t *testing.T) {
	s, err := NewClaudeSource(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// Never asked: the list agentman was written with.
	if got := s.offeredModels(); len(got) == 0 || got[0] != "claude-opus-5-5" {
		t.Errorf("fallback %v", got)
	}
	s.catalog.mu.Lock()
	s.catalog.loaded = true
	s.catalog.list = claudeModelList{Labels: []string{"Opus 5.5", "Fable 5.2", "Haiku 4.5"}}
	s.catalog.mu.Unlock()
	want := []string{"claude-opus-5-5", "claude-fable-5-2", "claude-haiku-4-5"}
	if got := s.offeredModels(); !reflect.DeepEqual(got, want) {
		t.Errorf("offered %v, want %v", got, want)
	}
}

func TestTheDaemonsTestsNeverStartTheRealClaude(t *testing.T) {
	c := newClaudeModelCatalog(t.TempDir())
	if _, err := c.look(); err == nil {
		t.Error("a test found a claude to start")
	}
}
