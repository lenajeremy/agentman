package source

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// writeCodexModels is the model list Codex keeps for its picker.
func writeCodexModels(t *testing.T, home string, slugs ...string) {
	t.Helper()
	var models []any
	for i, slug := range slugs {
		models = append(models, obj{"slug": slug, "display_name": slug, "visibility": "list", "priority": i,
			"supported_reasoning_levels": []any{obj{"effort": "medium"}}})
	}
	writeJSONL(t, filepath.Join(home, ".codex", "models_cache.json"), []any{obj{"models": models}})
}

func switchSource(t *testing.T) (*CodexSource, *fakeCodexConn) {
	t.Helper()
	home := t.TempDir()
	now := time.Now()
	writeCodexModels(t, home, "gpt-6.1-sol", "gpt-6-astra")
	path := writeDaemonRollout(t, home, "t1", "/work/api", now.Add(-time.Hour), now.Add(-time.Minute), "task_complete")
	thread := openThread("t1", path, "idle")
	thread.Effort = "medium"
	return connectedDaemonSource(t, home, nil, thread)
}

func settingsUpdate(t *testing.T, conn *fakeCodexConn) map[string]any {
	t.Helper()
	request := conn.expect(t, "thread/settings/update")
	var params map[string]any
	if err := json.Unmarshal(request.Params, &params); err != nil {
		t.Fatal(err)
	}
	conn.reply(t, request, map[string]any{})
	return params
}

// A session with no pane gets the mode and model choices it never had.
func TestCodexOffersModeAndModelWithoutAPane(t *testing.T) {
	src, _ := switchSource(t)
	session := discovered(t, src)["codex:t1"]
	if !slices.Equal(session.Modes, codexModes) || !slices.Equal(session.Models, []string{"gpt-6.1-sol", "gpt-6-astra"}) ||
		session.Model != "gpt-6.1-sol" || session.ModelScope != protocol.ModelScopeSession {
		t.Fatalf("session = %+v", session)
	}
}

func TestCodexSwitchesTheModelThroughTheDaemon(t *testing.T) {
	src, conn := switchSource(t)
	result := make(chan error, 1)
	go func() { result <- src.SetModel(context.Background(), "codex:t1", "gpt-6-astra") }()
	params := settingsUpdate(t, conn)
	if params["threadId"] != "t1" || params["model"] != "gpt-6-astra" || len(params) != 2 {
		t.Fatalf("settings = %v", params)
	}
	if err := waitErr(t, result); err != nil {
		t.Fatal(err)
	}
	// Shown at once, before Codex's own notification of it.
	if model := discovered(t, src)["codex:t1"].Model; model != "gpt-6-astra" {
		t.Fatalf("model = %q", model)
	}

	if err := src.SetModel(context.Background(), "codex:t1", "made-up"); err == nil {
		t.Fatal("switched to a model Codex does not list")
	}
	conn.quiet(t)
}

// Plan mode keeps the thread's model and reasoning effort: a mode names
// both, and an effort left out falls back to the model's default.
func TestCodexSwitchesToPlanModeKeepingTheEffort(t *testing.T) {
	src, conn := switchSource(t)
	result := make(chan error, 1)
	go func() { result <- src.SetMode(context.Background(), "codex:t1", "plan") }()
	params := settingsUpdate(t, conn)
	data, _ := json.Marshal(params["collaborationMode"])
	if !sameJSON(t, data, `{"mode":"plan","settings":{"model":"gpt-6.1-sol","reasoning_effort":"medium","developer_instructions":null}}`) {
		t.Fatalf("collaborationMode = %s", data)
	}
	if err := waitErr(t, result); err != nil {
		t.Fatal(err)
	}
	if mode := discovered(t, src)["codex:t1"].Mode; mode != "plan" {
		t.Fatalf("mode = %q", mode)
	}
	if err := src.SetMode(context.Background(), "codex:t1", "yolo"); err == nil {
		t.Fatal("switched to a mode Codex does not have")
	}
	conn.quiet(t)
}

func TestCodexCompactsThroughTheDaemon(t *testing.T) {
	src, conn := switchSource(t)
	result := injectAsync(src, "codex:t1", " /compact ", nil)
	request := conn.expect(t, "thread/compact/start")
	var params map[string]any
	json.Unmarshal(request.Params, &params)
	if params["threadId"] != "t1" {
		t.Fatalf("compact params = %v", params)
	}
	conn.reply(t, request, map[string]any{})
	if r := awaitSend(t, result); r.err != nil || r.mode != protocol.InjectAPI {
		t.Fatalf("send = %+v", r)
	}
}

// A thread's effort is read with it and follows Codex's settings updates.
func TestCodexLiveThreadsTrackTheEffort(t *testing.T) {
	effort := "high"
	wire := wireThread("a", "idle")
	wire.Effort = &effort
	fake := &fakeCodexCalls{pages: [][]string{{"a"}}, threads: map[string]codexWireThread{"a": wire}}
	live := newCodexLiveThreads(fake.call)
	live.connected()
	if threads, _ := live.snapshot(); threads["a"].Effort != "high" {
		t.Fatalf("effort = %q", threads["a"].Effort)
	}
	live.apply(notification(t, "thread/settings/updated", map[string]any{"threadId": "a", "threadSettings": map[string]any{"model": "m", "effort": "low"}}))
	if threads, _ := live.snapshot(); threads["a"].Effort != "low" {
		t.Fatalf("effort after update = %q", threads["a"].Effort)
	}
}
