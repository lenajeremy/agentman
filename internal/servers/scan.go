// Package servers finds the web servers coding agents start, and shares them.
//
// Nothing asks the agent to cooperate. Every few seconds the daemon lists the
// TCP ports listening on this machine, works out which agent session started
// each one — by process ancestry where the adapter knows the agent's pid, by
// working directory otherwise — and checks that the port actually answers
// HTTP. The phone then lists "localhost:5173" on that session, and tapping it
// opens a preview link through the same tunnel `am expose` uses.
package servers

import (
	"bufio"
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// scanTimeout bounds one listing. lsof can be slow on a busy machine, and a
// stuck scan must never stall the daemon.
const scanTimeout = 5 * time.Second

// Listener is one listening TCP socket.
type Listener struct {
	Port    int
	PID     int
	Command string
	// Loopback reports whether the socket accepts connections on a loopback
	// address — bound to 127.0.0.1, ::1 or every interface. A server bound
	// only to a LAN address cannot be reached through the tunnel, which
	// always dials loopback.
	Loopback bool
}

// Scan is one reading of the machine's listening sockets, plus the working
// directory of each process that owns one.
type Scan struct {
	Listeners []Listener
	Cwds      map[int]string
}

// ScanListeners reads the listening sockets for the current platform.
func ScanListeners(ctx context.Context) (Scan, error) {
	ctx, cancel := context.WithTimeout(ctx, scanTimeout)
	defer cancel()
	switch runtime.GOOS {
	case "linux":
		return scanProc("/proc")
	default:
		return scanLsof(ctx)
	}
}

/* --------------------------------- lsof ---------------------------------- */

func scanLsof(ctx context.Context) (Scan, error) {
	// -F prints one field per line (p=pid, c=command, n=address), which is
	// stable to parse where the column layout is not.
	out, err := exec.CommandContext(ctx, "lsof", "-nP", "-w", "-iTCP", "-sTCP:LISTEN", "-Fpcn").Output()
	if err != nil && len(out) == 0 {
		// lsof exits 1 when nothing matches, which is not a failure.
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return Scan{Cwds: map[int]string{}}, nil
		}
		return Scan{}, fmt.Errorf("servers: list listening ports: %w", err)
	}
	listeners := parseLsofListeners(string(out))
	cwds, err := lsofCwds(ctx, listeners)
	if err != nil {
		return Scan{}, err
	}
	return Scan{Listeners: listeners, Cwds: cwds}, nil
}

func parseLsofListeners(output string) []Listener {
	var (
		listeners []Listener
		pid       int
		command   string
	)
	for _, line := range strings.Split(output, "\n") {
		if len(line) < 2 {
			continue
		}
		value := line[1:]
		switch line[0] {
		case 'p':
			pid, _ = strconv.Atoi(value)
			command = ""
		case 'c':
			command = value
		case 'n':
			host, port, ok := splitHostPort(value)
			if !ok || pid <= 0 {
				continue
			}
			listeners = append(listeners, Listener{
				Port: port, PID: pid, Command: command, Loopback: reachableOnLoopback(host),
			})
		}
	}
	return dedupe(listeners)
}

func lsofCwds(ctx context.Context, listeners []Listener) (map[int]string, error) {
	cwds := map[int]string{}
	if len(listeners) == 0 {
		return cwds, nil
	}
	pids := make([]string, 0, len(listeners))
	seen := map[int]bool{}
	for _, l := range listeners {
		if !seen[l.PID] {
			seen[l.PID] = true
			pids = append(pids, strconv.Itoa(l.PID))
		}
	}
	out, err := exec.CommandContext(ctx, "lsof", "-nP", "-w", "-a", "-d", "cwd",
		"-p", strings.Join(pids, ","), "-Fpn").Output()
	if err != nil && len(out) == 0 {
		// A process that exited between the two calls makes lsof exit 1;
		// the rest of the answer is still good.
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return cwds, nil
		}
		return nil, fmt.Errorf("servers: read working directories: %w", err)
	}
	pid := 0
	for _, line := range strings.Split(string(out), "\n") {
		if len(line) < 2 {
			continue
		}
		switch line[0] {
		case 'p':
			pid, _ = strconv.Atoi(line[1:])
		case 'n':
			if pid > 0 {
				cwds[pid] = line[1:]
			}
		}
	}
	return cwds, nil
}

// splitHostPort parses lsof's "127.0.0.1:3000", "*:3000" and "[::1]:3000".
func splitHostPort(address string) (string, int, bool) {
	index := strings.LastIndex(address, ":")
	if index < 0 {
		return "", 0, false
	}
	port, err := strconv.Atoi(address[index+1:])
	if err != nil || port < 1 || port > 65535 {
		return "", 0, false
	}
	host := strings.TrimSuffix(strings.TrimPrefix(address[:index], "["), "]")
	return host, port, true
}

func reachableOnLoopback(host string) bool {
	switch host {
	case "*", "0.0.0.0", "::", "127.0.0.1", "::1", "localhost", "::ffff:127.0.0.1":
		return true
	}
	return strings.HasPrefix(host, "127.")
}

/* ---------------------------------- /proc -------------------------------- */

// scanProc reads Linux's socket tables directly, so Linux needs no lsof.
func scanProc(root string) (Scan, error) {
	inodes := map[string]struct {
		port     int
		loopback bool
	}{}
	for _, table := range []string{"net/tcp", "net/tcp6"} {
		file, err := os.Open(filepath.Join(root, table))
		if err != nil {
			continue // tcp6 is absent on kernels without IPv6
		}
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			fields := strings.Fields(scanner.Text())
			// sl local_address rem_address st tx:rx tr:when retrnsmt uid timeout inode
			if len(fields) < 10 || fields[3] != "0A" { // 0A is TCP_LISTEN
				continue
			}
			host, port, ok := parseProcAddress(fields[1])
			if !ok {
				continue
			}
			inodes[fields[9]] = struct {
				port     int
				loopback bool
			}{port, host}
		}
		file.Close()
	}

	scan := Scan{Cwds: map[int]string{}}
	if len(inodes) == 0 {
		return scan, nil
	}
	processes, err := os.ReadDir(root)
	if err != nil {
		return Scan{}, fmt.Errorf("servers: read %s: %w", root, err)
	}
	for _, entry := range processes {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		base := filepath.Join(root, entry.Name())
		fds, err := os.ReadDir(filepath.Join(base, "fd"))
		if err != nil {
			continue // another user's process, or it just exited
		}
		found := false
		for _, fd := range fds {
			target, err := os.Readlink(filepath.Join(base, "fd", fd.Name()))
			if err != nil || !strings.HasPrefix(target, "socket:[") {
				continue
			}
			inode := strings.TrimSuffix(strings.TrimPrefix(target, "socket:["), "]")
			socket, ok := inodes[inode]
			if !ok {
				continue
			}
			found = true
			command, _ := os.ReadFile(filepath.Join(base, "comm"))
			scan.Listeners = append(scan.Listeners, Listener{
				Port: socket.port, PID: pid, Command: strings.TrimSpace(string(command)),
				Loopback: socket.loopback,
			})
		}
		if found {
			if cwd, err := os.Readlink(filepath.Join(base, "cwd")); err == nil {
				scan.Cwds[pid] = cwd
			}
		}
	}
	scan.Listeners = dedupe(scan.Listeners)
	return scan, nil
}

// parseProcAddress decodes /proc/net/tcp's "0100007F:1F90" into whether the
// address is loopback-reachable and the port.
func parseProcAddress(field string) (loopback bool, port int, ok bool) {
	address, portHex, found := strings.Cut(field, ":")
	if !found {
		return false, 0, false
	}
	value, err := strconv.ParseUint(portHex, 16, 16)
	if err != nil || value == 0 {
		return false, 0, false
	}
	raw, err := hex.DecodeString(address)
	if err != nil {
		return false, 0, false
	}
	switch len(raw) {
	case 4:
		// IPv4, stored as a little-endian 32-bit word: 127.0.0.1 is 0100007F.
		first := raw[3]
		allZero := raw[0] == 0 && raw[1] == 0 && raw[2] == 0 && raw[3] == 0
		return allZero || first == 127, int(value), true
	case 16:
		allZero, isLoopback := true, true
		for i, b := range raw {
			if b != 0 {
				allZero = false
			}
			// ::1 is fifteen zero bytes and a one, stored as four
			// little-endian words, so the one sits in byte 15 - 3 = 12.
			if (i == 12 && b != 1) || (i != 12 && b != 0) {
				isLoopback = false
			}
		}
		// IPv4-mapped 127.x (::ffff:127.0.0.1) is 0000000000000000FFFF00000100007F.
		mapped := raw[8] == 0xff && raw[9] == 0xff && raw[10] == 0 && raw[11] == 0 && raw[15] == 127
		return allZero || isLoopback || mapped, int(value), true
	}
	return false, 0, false
}

// dedupe drops the second IPv4/IPv6 socket a server usually opens for one
// port, keeping the loopback-reachable reading if either is.
func dedupe(listeners []Listener) []Listener {
	type key struct{ pid, port int }
	index := map[key]int{}
	out := listeners[:0]
	for _, l := range listeners {
		k := key{l.PID, l.Port}
		if i, seen := index[k]; seen {
			out[i].Loopback = out[i].Loopback || l.Loopback
			continue
		}
		index[k] = len(out)
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Port != out[j].Port {
			return out[i].Port < out[j].Port
		}
		return out[i].PID < out[j].PID
	})
	return out
}
