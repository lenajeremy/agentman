//go:build darwin

package procinfo

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestNameAndCwdOfAChildProcess(t *testing.T) {
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pid := startChild(t, dir)
	if name, err := Name(pid); err != nil || name != "sleep" {
		t.Fatalf("Name = %q, %v", name, err)
	}
	if cwd, err := Cwd(pid); err != nil || cwd != dir {
		t.Fatalf("Cwd = %q, %v; want %q", cwd, err, dir)
	}
}

func TestListeningFindsThisProcessesSockets(t *testing.T) {
	var ports []int
	for _, address := range []string{"127.0.0.1:0", "[::1]:0", ":0"} {
		listener, err := net.Listen("tcp", address)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { listener.Close() })
		ports = append(ports, listener.Addr().(*net.TCPAddr).Port)
	}
	// A connected socket is not a listener.
	conn, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(ports[0]))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })

	found, err := Listening()
	if err != nil {
		t.Fatal(err)
	}
	mine := found[os.Getpid()]
	want := map[int]string{ports[0]: "127.0.0.1", ports[1]: "::1", ports[2]: "::"}
	if len(mine) != len(want) {
		t.Fatalf("listeners = %+v, want ports %v", mine, ports)
	}
	for _, listener := range mine {
		if want[listener.Port] != listener.Addr.String() {
			t.Errorf("port %d bound to %s, want %s", listener.Port, listener.Addr, want[listener.Port])
		}
	}
}
