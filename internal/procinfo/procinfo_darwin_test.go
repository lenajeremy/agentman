//go:build darwin

package procinfo

import (
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestListAgreesWithPS(t *testing.T) {
	processes, err := List()
	if err != nil {
		t.Fatal(err)
	}
	parents := map[int]int{}
	for _, process := range processes {
		parents[process.PID] = process.PPID
	}
	if parents[os.Getpid()] != os.Getppid() {
		t.Fatalf("this process's parent = %d, want %d", parents[os.Getpid()], os.Getppid())
	}
	out, err := exec.Command("ps", "-axo", "pid=,ppid=").Output()
	if err != nil {
		t.Skipf("ps: %v", err)
	}
	compared := 0
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		pid, _ := strconv.Atoi(fields[0])
		ppid, _ := strconv.Atoi(fields[1])
		// A process that started or ended between the two readings is
		// in one of them only.
		if got, ok := parents[pid]; ok {
			compared++
			if got != ppid {
				t.Errorf("pid %d: parent %d, ps says %d", pid, got, ppid)
			}
		}
	}
	if compared < 10 {
		t.Fatalf("only %d processes in common with ps", compared)
	}
}

// startChild runs sleep in dir, for a process whose every detail is known.
func startChild(t *testing.T, dir string) int {
	t.Helper()
	child := exec.Command("/bin/sleep", "30")
	child.Dir = dir
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
	return child.Process.Pid
}

func TestReadsAChildProcess(t *testing.T) {
	pid := startChild(t, t.TempDir())
	args, err := Args(pid)
	if err != nil || !slices.Equal(args, []string{"/bin/sleep", "30"}) {
		t.Fatalf("Args = %q, %v", args, err)
	}
	if path, err := Path(pid); err != nil || path != "/bin/sleep" {
		t.Fatalf("Path = %q, %v", path, err)
	}
	processes, err := List()
	if err != nil {
		t.Fatal(err)
	}
	index := slices.IndexFunc(processes, func(p Process) bool { return p.PID == pid })
	if index < 0 {
		t.Fatal("the child is not in the process table")
	}
	if got := processes[index]; got.PPID != os.Getpid() || got.Comm != "sleep" || got.Started == 0 || got.UID != os.Getuid() {
		t.Fatalf("child = %+v", got)
	}
}

func TestArgsOfAnotherUsersProcessFail(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads every process")
	}
	// launchd is root's.
	if _, err := Args(1); err == nil {
		t.Fatal("read launchd's arguments without being root")
	}
	if path, err := Path(1); err != nil || path != "/sbin/launchd" {
		t.Fatalf("Path(1) = %q, %v", path, err)
	}
}

func TestParseProcArgs(t *testing.T) {
	raw := []byte("\x02\x00\x00\x00/usr/local/bin/node\x00\x00\x00\x00node\x00/tmp/a b.js\x00PATH=/bin\x00")
	args, err := parseProcArgs(raw)
	if err != nil || !slices.Equal(args, []string{"node", "/tmp/a b.js"}) {
		t.Fatalf("parseProcArgs = %q, %v", args, err)
	}
	if _, err := parseProcArgs([]byte("\x01\x00\x00\x00/bin/x")); err == nil {
		t.Fatal("parsed an unterminated path")
	}
}
