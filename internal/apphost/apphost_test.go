package apphost

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func call(t *testing.T, name string, args ...any) (any, error) {
	t.Helper()
	method, ok := Methods()[name]
	if !ok {
		t.Fatalf("no method %s", name)
	}
	raw := make([]json.RawMessage, len(args))
	for i, a := range args {
		raw[i], _ = json.Marshal(a)
	}
	return method(raw)
}

func TestAFolderIsSeenAndKnownToBeARepository(t *testing.T) {
	repo := t.TempDir()
	if err := os.Mkdir(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(repo, "src")
	_ = os.Mkdir(inside, 0o755)
	if got, _ := call(t, "Files.Folder", inside); got != (FolderInfo{Exists: true, Repository: true}) {
		t.Fatalf("a folder in a repository: %+v", got)
	}
	plain := t.TempDir()
	if got, _ := call(t, "Files.Folder", plain); got != (FolderInfo{Exists: true}) {
		t.Fatalf("a plain folder: %+v", got)
	}
	if got, _ := call(t, "Files.Folder", filepath.Join(plain, "gone")); got != (FolderInfo{}) {
		t.Fatalf("a folder that isn't there: %+v", got)
	}
	if got, _ := call(t, "Files.Folder", "relative/path"); got != (FolderInfo{}) {
		t.Fatalf("a relative path: %+v", got)
	}
}

func TestAPickedFileIsReadUpToTheLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "note.txt")
	_ = os.WriteFile(path, []byte("hello"), 0o644)
	got, err := call(t, "Files.Read", path, 1024)
	file, _ := got.(PickedFile)
	if err != nil || file.Name != "note.txt" || file.Size != 5 || file.Data != base64.StdEncoding.EncodeToString([]byte("hello")) {
		t.Fatalf("read: %+v, %v", file, err)
	}
	got, _ = call(t, "Files.Read", path, 2)
	if file := got.(PickedFile); file.Data != "" || file.Size != 5 {
		t.Fatalf("a file over the limit came with its data: %+v", file)
	}
	if _, err := call(t, "Files.Read", "note.txt", 1024); err == nil {
		t.Fatal("a relative path was read")
	}
	if _, err := call(t, "Files.Read", filepath.Dir(path), 1024); err == nil {
		t.Fatal("a folder was read as a file")
	}
}

func TestAMissingArgumentIsAnError(t *testing.T) {
	if _, err := call(t, "Files.Read"); err == nil {
		t.Fatal("Files.Read with no arguments answered")
	}
}

func TestThePageIsToldTheSystemAmServeRunsOn(t *testing.T) {
	got, err := call(t, "Host.System")
	if err != nil || got != runtime.GOOS {
		t.Fatalf("Host.System = %v, %v", got, err)
	}
}
