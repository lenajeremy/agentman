package servers

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/lenajeremy/agentman/internal/procinfo"
)

func TestAddrOnLoopback(t *testing.T) {
	for address, want := range map[string]bool{
		"0.0.0.0": true, "::": true, "127.0.0.1": true, "127.0.1.1": true, "::1": true,
		"::ffff:127.0.0.1": true, "192.168.1.20": false, "fe80::1": false, "::ffff:10.0.0.1": false,
	} {
		if got := addrOnLoopback(netip.MustParseAddr(address)); got != want {
			t.Errorf("addrOnLoopback(%s) = %v, want %v", address, got, want)
		}
	}
}

// The kernel's answer must be the one lsof gives: the same sockets, the same
// command name, the same working directory.
func TestKernelScanAgreesWithLsof(t *testing.T) {
	if !procinfo.Supported {
		t.Skip("listening ports are not read from the kernel here")
	}
	if _, err := exec.LookPath("lsof"); err != nil {
		t.Skip("no lsof to compare with")
	}
	for _, address := range []string{"127.0.0.1:0", ":0"} {
		listener, err := net.Listen("tcp", address)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { listener.Close() })
	}
	kernel, err := scanKernel()
	if err != nil {
		t.Fatal(err)
	}
	lsof, err := scanLsof(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	self := os.Getpid()
	mine := func(scan Scan) []Listener {
		var own []Listener
		for _, listener := range scan.Listeners {
			if listener.PID == self {
				own = append(own, listener)
			}
		}
		return own
	}
	if len(mine(kernel)) != 2 || fmt.Sprint(mine(kernel)) != fmt.Sprint(mine(lsof)) {
		t.Fatalf("kernel %+v\nlsof   %+v", mine(kernel), mine(lsof))
	}
	cwd, _ := os.Getwd()
	cwd, _ = filepath.EvalSymlinks(cwd)
	if kernel.Cwds[self] != cwd || kernel.Cwds[self] != lsof.Cwds[self] {
		t.Fatalf("cwd = %q, lsof says %q, want %q", kernel.Cwds[self], lsof.Cwds[self], cwd)
	}
}
