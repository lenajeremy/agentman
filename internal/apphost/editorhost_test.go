package apphost

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func hostDo(t *testing.T, name string, args ...any) any {
	t.Helper()
	raw := make([]json.RawMessage, len(args))
	for i, a := range args {
		raw[i], _ = json.Marshal(a)
	}
	method, ok := Methods()[name]
	if !ok {
		t.Fatalf("no method %s", name)
	}
	result, err := method(raw)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return result
}

func TestTheEditorSavesAndReadsAFileThroughTheHostCalls(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "notes.md")
	hostDo(t, "EditorFiles.CreateFile", path)
	hostDo(t, "EditorFiles.Write", path, "# Hello\n")
	read := hostDo(t, "EditorFiles.Read", path).(EditorFile)
	if read.Kind != "text" || read.Text != "# Hello\n" {
		t.Fatalf("Read = %+v", read)
	}
	entries := hostDo(t, "EditorFiles.List", dir, "").([]EditorEntry)
	if len(entries) != 1 || entries[0].Name != "notes.md" {
		t.Fatalf("List = %+v", entries)
	}
	if _, err := Methods()["EditorFiles.Move"]([]json.RawMessage{json.RawMessage(`"` + path + `"`)}); err == nil {
		t.Fatal("Move ran with one argument")
	}
}

func TestAWatchedFolderTellsOfANewFile(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	got := make(chan FilesChanged, 1)
	go Watch(ctx, dir, func(c FilesChanged) error {
		got <- c
		return nil
	})
	time.Sleep(200 * time.Millisecond)
	must(t, os.WriteFile(filepath.Join(dir, "new.go"), []byte("package x"), 0o644))
	select {
	case c := <-got:
		if c.Root != dir || len(c.Changes) != 1 || c.Changes[0].Type != 1 || filepath.Base(c.Changes[0].Path) != "new.go" {
			t.Fatalf("changes = %+v", c)
		}
	case <-ctx.Done():
		t.Fatal("no change told")
	}
}
