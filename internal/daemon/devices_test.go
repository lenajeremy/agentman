package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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

// The relay names each connection afresh: the same phone back again arrives
// under a new id. Once it says who it is, it takes the place of its old
// entries, keeping when it was first added; two at once stay two. A list
// kept from before the fix collapses when read.
func TestTheSamePhoneBackAgainIsOneDevice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "devices.json")
	clock := time.UnixMilli(1_000_000)
	devices := NewDevices(path)
	devices.now = func() time.Time { return clock }
	phone := protocol.DeviceInfo{Name: "iPhone", Model: "iPhone", Platform: "ios", OS: "26.6"}

	for i, id := range []string{"acct-1", "acct-2", "acct-3"} {
		devices.Seen(id)
		info := phone
		if i == 2 {
			info.OS = "26.7" // updated since: still the same phone
		}
		devices.Named(id, info)
		clock = clock.Add(time.Hour)
		devices.Left(id)
	}
	list := devices.List()
	if len(list) != 1 || list[0].ID != "acct-3" || list[0].FirstSeen != 1_000_000 || list[0].OS != "26.7" {
		t.Fatalf("one phone, first added at the start: %+v", list)
	}

	// A second phone just like it, connected at the same time, is its own.
	devices.Seen("acct-4")
	devices.Named("acct-4", phone)
	devices.Seen("acct-5")
	devices.Named("acct-5", phone)
	if got := len(devices.List()); got != 2 {
		t.Fatalf("two connected at once: %d devices, want 2", got)
	}
	// A browser is not a phone.
	devices.Seen("acct-6")
	devices.Named("acct-6", protocol.DeviceInfo{Name: "Chrome", Platform: "web", OS: "0.0.0"})
	if got := len(devices.List()); got != 3 {
		t.Fatalf("with a browser: %d devices, want 3", got)
	}

	// Written before the fix: 40 entries for one phone.
	var old []protocol.Device
	for i := range 40 {
		old = append(old, protocol.Device{ID: fmt.Sprintf("acct-%d", 100+i), DeviceInfo: phone,
			FirstSeen: int64(2_000_000 + i), LastSeen: int64(3_000_000 + i)})
	}
	old = append(old, protocol.Device{ID: "acct-web", DeviceInfo: protocol.DeviceInfo{Name: "Chrome", Platform: "web"}, FirstSeen: 5, LastSeen: 6})
	data, _ := json.Marshal(old)
	stale := filepath.Join(t.TempDir(), "devices.json")
	if err := os.WriteFile(stale, data, 0o600); err != nil {
		t.Fatal(err)
	}
	read := NewDevices(stale).List()
	if len(read) != 2 || read[0].ID != "acct-139" || read[0].FirstSeen != 2_000_000 || read[0].LastSeen != 3_000_039 {
		t.Fatalf("collapsed: %+v", read)
	}
	if again := NewDevices(stale).List(); len(again) != 2 {
		t.Fatalf("the collapsed list is what was saved: %d entries", len(again))
	}
}

// The relay names a connection by its pairing: one phone is one device
// however often it connects, connected while any connection is open; two
// phones are two, however alike; and the first to say who it is takes over
// the entries older relays left for it, with when it was first added.
func TestAPairingIsOneDeviceAndTwoPairingsAreTwo(t *testing.T) {
	clock := time.UnixMilli(5_000_000)
	devices := NewDevices(filepath.Join(t.TempDir(), "devices.json"))
	devices.now = func() time.Time { return clock }
	phone := protocol.DeviceInfo{Name: "iPhone", Model: "iPhone", Platform: "ios", OS: "26.6"}

	// Left by an older relay: one entry per connection.
	for i := range 3 {
		conn := fmt.Sprintf("acct-17942%d", i)
		devices.Named(conn, phone)
		devices.Left(conn)
		clock = clock.Add(time.Minute)
	}
	if got := len(devices.List()); got != 1 {
		t.Fatalf("older relay: %d entries, want 1", got)
	}

	devices.Named("acct-pAAAA-1", phone)
	clock = clock.Add(time.Hour)
	devices.Left("acct-pAAAA-1")
	devices.Named("acct-pAAAA-2", phone)
	// Reconnecting: the new connection opens before the old one closes.
	devices.Seen("acct-pAAAA-3")
	devices.Left("acct-pAAAA-2")
	list := devices.List()
	if len(list) != 1 || list[0].ID != "acct-pAAAA" || !list[0].Connected || list[0].FirstSeen != 5_000_000 {
		t.Fatalf("one phone, connected, first added with the older entries: %+v", list)
	}

	// A second phone just like it, both gone: still two.
	devices.Named("acct-pBBBB-1", phone)
	devices.Left("acct-pBBBB-1")
	devices.Left("acct-pAAAA-3")
	list = devices.List()
	if len(list) != 2 || list[0].Connected || list[1].Connected {
		t.Fatalf("two phones, neither connected: %+v", list)
	}
	ids := []string{list[0].ID, list[1].ID}
	if !slices.Contains(ids, "acct-pAAAA") || !slices.Contains(ids, "acct-pBBBB") {
		t.Fatalf("ids = %v", ids)
	}
}

// When the daemon's own link to the relay ends, it loses sight of its phones,
// but their connections may be open still: back, the same connection speaks
// again and the phone is connected. Nothing seen closed stays held against a
// name past a minute.
func TestAPhoneTheDaemonLostSightOfComesBackUnderTheSameName(t *testing.T) {
	clock := time.UnixMilli(1_000_000)
	devices := NewDevices(filepath.Join(t.TempDir(), "devices.json"))
	devices.now = func() time.Time { return clock }
	devices.Seen("acct-pAAAA-1")
	devices.Lost("acct-pAAAA-1")
	if list := devices.List(); list[0].Connected {
		t.Fatalf("while the daemon is away: %+v", list)
	}
	devices.Seen("acct-pAAAA-1")
	if list := devices.List(); !list[0].Connected {
		t.Fatalf("the daemon back, the phone still there: %+v", list)
	}

	devices.Left("acct-pAAAA-1")
	clock = clock.Add(2 * closedKept)
	devices.Seen("acct-pAAAA-1")
	if list := devices.List(); !list[0].Connected {
		t.Fatalf("a close long past held against the name: %+v", list)
	}
}

// A request sent just before a phone left can be handled after it left. What
// it says is kept, but it does not bring the phone back as connected.
func TestARequestHandledAfterItsPhoneLeftOpensNothing(t *testing.T) {
	devices := NewDevices(filepath.Join(t.TempDir(), "devices.json"))
	devices.Seen("acct-pAAAA-1")
	devices.Left("acct-pAAAA-1")
	devices.Named("acct-pAAAA-1", protocol.DeviceInfo{Name: "iPhone", Platform: "ios"})
	devices.Seen("acct-pAAAA-1")
	list := devices.List()
	if len(list) != 1 || list[0].Connected || list[0].Name != "iPhone" {
		t.Fatalf("named, and gone: %+v", list)
	}
	// Its next connection is a new name, and opens as usual; when that one
	// closes, the old one isn't left holding it open.
	devices.Seen("acct-pAAAA-2")
	if list := devices.List(); !list[0].Connected {
		t.Fatalf("back: %+v", list)
	}
	devices.Left("acct-pAAAA-2")
	if list := devices.List(); list[0].Connected {
		t.Fatalf("gone again, but shown connected: %+v", list)
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
