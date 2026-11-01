package source

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lenajeremy/agentman/internal/procinfo"
)

// What the kernel says a process holds reads as lsof's answer did: this
// process, holding an Antigravity conversation's folder and a Cursor chat
// store open, is found holding them.
func TestKernelOpenFilesReadsAsLsofDid(t *testing.T) {
	if !procinfo.Supported {
		t.Skip("open files are read with lsof here")
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	brain := filepath.Join(home, ".gemini", "antigravity-cli", "brain", agConversation)
	store := filepath.Join(home, ".cursor", "chats", "workspace", "chat", "store.db")
	for _, dir := range []string{brain, filepath.Dir(store)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	folder, err := os.Open(brain)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { folder.Close() })
	db, err := os.Create(store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	self := os.Getpid()
	held := parseAntigravityOpenFiles(home, kernelOpenFiles([]int{self, 0}))[self]
	cwd, _ := os.Getwd()
	cwd, _ = filepath.EvalSymlinks(cwd)
	if held.cwd != cwd || len(held.conversations) != 1 || held.conversations[0] != agConversation {
		t.Errorf("antigravity reading = %+v, want cwd %s and %s", held, cwd, agConversation)
	}
	open, known := cursorCLIOpenStores(t.Context(), home, []int{self})
	if !known || open[self] != store {
		t.Errorf("cursor reading = %v, %v; want %s", open, known, store)
	}
}
