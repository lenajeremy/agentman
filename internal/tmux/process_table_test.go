package tmux

import (
	"context"
	"os"
	"os/exec"
	"testing"

	"github.com/lenajeremy/agentman/internal/procinfo"
)

func requireKernelTable(t *testing.T) {
	t.Helper()
	if !procinfo.Supported {
		t.Skip("the process table is read with ps here")
	}
}

// startNamed runs sleep under a name of its own choosing, the way a program
// that sets its own argv[0] does.
func startNamed(t *testing.T, name string) int {
	t.Helper()
	child := &exec.Cmd{Path: "/bin/sleep", Args: []string{name, "30"}}
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
	return child.Process.Pid
}

// The kernel's table must say what ps says: the same parents, and for the
// user's own processes, the same name each was run by.
func TestKernelProcessTreeAgreesWithPS(t *testing.T) {
	requireKernelTable(t)
	child := startNamed(t, "agentman-named-child")
	kernel, err := kernelProcessTree(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ps, err := psProcessTree(context.Background())
	if err != nil {
		t.Skipf("ps: %v", err)
	}
	for _, pid := range []int{os.Getpid(), child} {
		if kernel.Command(pid) == "" || kernel.Command(pid) != ps.Command(pid) {
			t.Errorf("Command(%d) = %q, ps says %q", pid, kernel.Command(pid), ps.Command(pid))
		}
	}
	if kernel.Command(child) != "agentman-named-child" {
		t.Errorf("the child is named %q", kernel.Command(child))
	}
	if !kernel.OwnsPID(os.Getpid(), child) {
		t.Error("this process does not own its child")
	}
	compared := 0
	for pid, parent := range ps.parents {
		if got, ok := kernel.parents[pid]; ok {
			compared++
			if got != parent {
				t.Errorf("pid %d: parent %d, ps says %d", pid, got, parent)
			}
		}
	}
	if compared < 10 {
		t.Fatalf("only %d processes in common with ps", compared)
	}
}

// A settled process is named from memory, which is what spares a read of
// every process every second; a new one is read again until it settles.
func TestKernelProcessTreeRemembersSettledProcesses(t *testing.T) {
	requireKernelTable(t)
	child := startNamed(t, "agentman-young-child")
	if _, err := kernelProcessTree(context.Background()); err != nil {
		t.Fatal(err)
	}
	commands.mu.Lock()
	for key, known := range commands.byProcess {
		switch key.pid {
		case 1, child:
			known.command = "remembered"
			commands.byProcess[key] = known
		}
	}
	commands.mu.Unlock()

	tree, err := kernelProcessTree(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// launchd has run since boot.
	if got := tree.Command(1); got != "remembered" {
		t.Errorf("a settled process was read again: %q", got)
	}
	if got := tree.Command(child); got != "agentman-young-child" {
		t.Errorf("a new process was named from memory: %q", got)
	}

	// A process that has ended is forgotten.
	commands.mu.Lock()
	gone := processKey{pid: 1 << 30, comm: "gone"}
	commands.byProcess[gone] = knownCommand{command: "gone"}
	commands.mu.Unlock()
	if _, err := kernelProcessTree(context.Background()); err != nil {
		t.Fatal(err)
	}
	commands.mu.Lock()
	_, kept := commands.byProcess[gone]
	commands.mu.Unlock()
	if kept {
		t.Error("an ended process is still remembered")
	}
}

func TestKernelProcessTreeHonorsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := kernelProcessTree(ctx); err == nil {
		t.Fatal("a cancelled snapshot succeeded")
	}
}

func BenchmarkSnapshotProcessTree(b *testing.B) {
	ctx := context.Background()
	for b.Loop() {
		if _, err := snapshotProcessTree(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPSProcessTree(b *testing.B) {
	ctx := context.Background()
	for b.Loop() {
		if _, err := psProcessTree(ctx); err != nil {
			b.Fatal(err)
		}
	}
}
