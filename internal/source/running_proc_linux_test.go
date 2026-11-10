//go:build linux

package source

import (
	"os"
	"path/filepath"
	"testing"
)

// On Linux the check reads /proc: this test's own process is listed by its
// name, and a file it holds open is among its open files, so a rollout a
// codex process holds open would be seen.
func TestProcShowsAProcessAndTheFilesItHoldsOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout-1.jsonl")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	processes, err := listProcesses()
	if err != nil {
		t.Fatal(err)
	}
	var me string
	for _, process := range processes {
		if process.PID == os.Getpid() {
			me = process.Comm
		}
	}
	if me == "" {
		t.Fatal("this process isn't listed")
	}
	if open, known := heldOpen(path, me, listProcesses, openFiles); !open || !known {
		t.Fatalf("a file this process holds open: open=%v known=%v", open, known)
	}
	file.Close()
	if open, _ := heldOpen(path, me, listProcesses, openFiles); open {
		t.Fatal("closed, but still open")
	}
}
