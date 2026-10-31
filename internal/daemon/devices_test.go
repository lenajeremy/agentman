package daemon

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// A device is kept from its first request: connected while its connection
// lasts, named when it says who it is, and remembered after the daemon
// restarts, though no longer connected. The Mac hears of each change, not of
// each request.
func TestDevicesAreKeptFromWhatTheySend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "devices.json")
	clock := time.UnixMilli(1_000_000)
	devices := NewDevices(path)
	devices.now = func() time.Time { return clock }
	var told [][]protocol.Device
	devices.OnChange(func(list []protocol.Device) { told = append(told, list) })

	devices.Seen("phone")
	clock = clock.Add(time.Second)
	devices.Seen("phone")
	if len(told) != 1 {
		t.Fatalf("told %d times; a device still there is no news", len(told))
	}
	devices.Named("phone", protocol.DeviceInfo{Name: "Jeremiah's iPhone", Model: "iPhone 17 Pro", Platform: "ios", OS: "26.1"})
	clock = clock.Add(time.Second)
	devices.Seen("tablet")
	devices.Left("tablet")
	devices.Left("unknown")

	list := devices.List()
	if len(list) != 2 || list[0].ID != "phone" || !list[0].Connected || list[1].Connected {
		t.Fatalf("connected first: %+v", list)
	}
	if list[0].Name != "Jeremiah's iPhone" || list[0].FirstSeen != 1_000_000 || list[0].LastSeen != 1_001_000 {
		t.Fatalf("phone = %+v", list[0])
	}
	if len(told) != 4 {
		t.Fatalf("told %d times, want: new phone, named, new tablet, tablet left", len(told))
	}

	again := NewDevices(path)
	kept := again.List()
	if len(kept) != 2 || kept[0].Connected || kept[1].Connected {
		t.Fatalf("after a restart nothing is connected: %+v", kept)
	}
	if kept[0].ID != "tablet" || kept[1].Name != "Jeremiah's iPhone" {
		t.Fatalf("kept = %+v", kept)
	}
}

// A pairing request waits for the relay's answer to it, whether a code or a
// reason; a code nobody asked for goes where it always did.
func TestPairCodeWaitsForTheRelaysAnswer(t *testing.T) {
	socket := newRecordingDaemonSocket(nil)
	conn := newDaemonRelayConn(socket)
	defer conn.Close()
	client := NewClient("wss://relay.example", "token")
	client.ws = conn
	var stray []string
	client.OnPairCode = func(code, _ string, _ time.Time) { stray = append(stray, code) }

	answer := func(control protocol.Control) {
		t.Helper()
		var request protocol.Envelope
		deadline := time.Now().Add(time.Second)
		for {
			frames := socket.snapshot()
			if len(frames) > 0 {
				if err := json.Unmarshal(frames[len(frames)-1], &request); err != nil {
					t.Fatal(err)
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("no pairing request sent")
			}
			time.Sleep(5 * time.Millisecond)
		}
		reply, err := protocol.NewEnvelope("r", protocol.PeerRelay, control)
		if err != nil {
			t.Fatal(err)
		}
		reply.ReplyTo = request.ID
		client.handleControl(reply)
		socket.mu.Lock()
		socket.frames = nil
		socket.mu.Unlock()
	}

	type result struct {
		control protocol.Control
		err     error
	}
	ask := func() chan result {
		done := make(chan result, 1)
		go func() {
			control, err := client.PairCode(context.Background())
			done <- result{control, err}
		}()
		return done
	}

	done := ask()
	answer(protocol.Control{Type: protocol.CtlPairCode, Code: "0123456789", Token: "pair-token", ExpiresAt: 42})
	got := <-done
	if got.err != nil || got.control.Code != "0123456789" || got.control.Token != "pair-token" {
		t.Fatalf("code: %+v", got)
	}

	done = ask()
	answer(protocol.Control{Type: protocol.CtlError, Message: "too many pairing codes requested"})
	if got := <-done; got.err == nil || got.err.Error() != "too many pairing codes requested" {
		t.Fatalf("reason: %+v", got)
	}

	stale, _ := protocol.NewEnvelope("s", protocol.PeerRelay, protocol.Control{Type: protocol.CtlPairCode, Code: "9999999999"})
	stale.ReplyTo = "nobody"
	client.handleControl(stale)
	if len(stray) != 1 || stray[0] != "9999999999" {
		t.Fatalf("stray = %v", stray)
	}

	client.ws = nil
	if _, err := client.PairCode(context.Background()); err != ErrNoRelay {
		t.Fatalf("offline: %v", err)
	}
}
