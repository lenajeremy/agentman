package source

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

func TestCodexDiscoveryCachesUnchangedRolloutReads(t *testing.T) {
	const sessionID = "01900000-aaaa-bbbb-cccc-000000000088"
	home := fakeCodexHome(t, sessionID, "/Users/me/cache", time.Minute)
	paths, err := filepath.Glob(filepath.Join(home, ".codex", "sessions", "*", "*", "*", "rollout-*"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("rollout paths = %v, err = %v", paths, err)
	}
	path := paths[0]

	source, err := NewCodexSource(home)
	if err != nil {
		t.Fatal(err)
	}
	source.processCheck = alwaysRunning
	source.listPanes = noPanes
	metaReads := 0
	activityReads := 0
	source.readMeta = func(path string) (codexMeta, error) {
		metaReads++
		return readCodexMeta(path)
	}
	source.readActivity = func(ctx context.Context, path string, modTime time.Time) (protocol.State, int64, error) {
		activityReads++
		return codexActivity(ctx, path, modTime)
	}

	for range 2 {
		if _, err := source.Discover(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if metaReads != 1 || activityReads != 1 {
		t.Fatalf("unchanged rollout used %d metadata and %d activity reads, want 1 each",
			metaReads, activityReads)
	}

	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("{\"noise\":true}\n"); err != nil {
		file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Discover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if metaReads != 1 || activityReads != 2 {
		t.Fatalf("append used %d metadata and %d activity reads, want 1 and 2",
			metaReads, activityReads)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	replacement := path + ".replacement"
	if err := os.WriteFile(replacement, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Discover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if metaReads != 2 || activityReads != 3 {
		t.Fatalf("replacement used %d metadata and %d activity reads, want 2 and 3",
			metaReads, activityReads)
	}

	source.processCheck = func(context.Context) bool { return false }
	if _, err := source.Discover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(source.rolloutCache) != 0 {
		t.Fatalf("ended session left %d rollout cache entries", len(source.rolloutCache))
	}
}

func BenchmarkCodexActivityCacheHit(b *testing.B) {
	path := filepath.Join(b.TempDir(), "rollout.jsonl")
	if err := os.WriteFile(path, []byte("{\"noise\":true}\n"), 0o600); err != nil {
		b.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		b.Fatal(err)
	}
	source, err := NewCodexSource(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	reads := 0
	source.readActivity = func(context.Context, string, time.Time) (protocol.State, int64, error) {
		reads++
		return protocol.StateBusy, info.ModTime().UnixMilli(), nil
	}
	if _, _, err := source.cachedCodexActivity(context.Background(), path, info); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		state, _, err := source.cachedCodexActivity(context.Background(), path, info)
		if err != nil || state != protocol.StateBusy {
			b.Fatalf("cache hit returned state=%q err=%v", state, err)
		}
	}
	if reads != 1 {
		b.Fatalf("cache benchmark performed %d scans, want 1", reads)
	}
}
