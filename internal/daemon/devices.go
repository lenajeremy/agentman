package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Devices keeps the phones and tablets that reach this Mac through the
// relay, in a file beside the daemon's config: when each was first and last
// seen, the name it gives itself, and whether it is connected now. The relay
// keeps no such list by design; the daemon sees each device's requests.
//
// The relay names connections, not devices: account-p<pairing>-<moment>,
// where the pairing is the same each time one phone connects. A device is
// kept under account-p<pairing>, and is connected while any of its
// connections is open. A relay from before 2026-11-10 names a connection
// account-<moment> alone, with nothing to tell one phone from another.
type Devices struct {
	path string
	now  func() time.Time

	mu sync.Mutex
	// list is each device, by account-p<pairing>, or by the connection for
	// one an older relay named.
	list map[string]*protocol.Device
	// open is each open connection's device.
	open map[string]string
	// closed is the connections seen to close, and when. A request sent just
	// before a phone left can be handled after it left: it must not open the
	// connection again, or the phone shows as connected for good. A
	// connection's name is never used twice, so a minute's memory does.
	closed   map[string]time.Time
	saved    time.Time
	onChange func([]protocol.Device)
}

// closedKept is how long a closed connection is remembered.
const closedKept = time.Minute

// deviceSaveEvery spaces out saves for a device that is only still there: an
// open phone sends a request every few seconds.
const deviceSaveEvery = time.Minute

// deviceOf is the device a connection is: its account and pairing, or the
// connection itself from an older relay.
func deviceOf(conn string) string {
	account, rest, ok := strings.Cut(conn, "-")
	if !ok {
		return conn
	}
	pairing, _, ok := strings.Cut(rest, "-")
	if !ok || len(pairing) < 2 || pairing[0] != 'p' {
		return conn
	}
	return account + "-" + pairing
}

// paired reports whether a device is kept by its pairing, rather than by a
// connection an older relay named.
func paired(id string) bool {
	_, rest, ok := strings.Cut(id, "-")
	return ok && len(rest) >= 2 && rest[0] == 'p' && !strings.Contains(rest, "-")
}

// NewDevices reads the devices kept at path. None is connected until it
// sends something.
func NewDevices(path string) *Devices {
	d := &Devices{path: path, now: time.Now, list: map[string]*protocol.Device{}, open: map[string]string{}, closed: map[string]time.Time{}}
	if data, err := os.ReadFile(path); err == nil {
		var kept []protocol.Device
		if json.Unmarshal(data, &kept) == nil {
			for _, device := range kept {
				if device.ID == "" {
					continue
				}
				device.Connected = false
				d.list[device.ID] = &device
			}
		}
	}
	// Kept before the same phone was recognised coming back.
	if d.collapseLocked() {
		d.save(d.listLocked())
	}
	return d
}

// OnChange is told the whole list whenever a device arrives, leaves or
// names itself.
func (d *Devices) OnChange(f func([]protocol.Device)) {
	d.mu.Lock()
	d.onChange = f
	d.mu.Unlock()
}

// Seen notes a request on a connection: a new device, one back, or one
// still here.
func (d *Devices) Seen(conn string) {
	d.update(conn, true, func(device *protocol.Device, now int64) bool {
		changed := !device.Connected
		device.Connected = true
		device.LastSeen = now
		return changed
	})
}

// Named keeps what a device says about itself.
func (d *Devices) Named(conn string, info protocol.DeviceInfo) {
	d.update(conn, true, func(device *protocol.Device, now int64) bool {
		changed := device.DeviceInfo != info || !device.Connected
		device.DeviceInfo = info
		device.Connected = true
		device.LastSeen = now
		return d.absorbLocked(device) || changed
	})
}

// Left notes that the relay saw a connection close. Its device is still
// connected while another of its connections is open: a phone reconnecting
// opens the new one before the old one is seen to close.
func (d *Devices) Left(conn string) { d.drop(conn, true) }

// Lost notes that this daemon lost sight of a connection because its own
// link to the relay ended. The phone's may be open still: once the daemon is
// back, it speaks again under the same name, and is connected again.
func (d *Devices) Lost(conn string) { d.drop(conn, false) }

func (d *Devices) drop(conn string, closed bool) {
	d.mu.Lock()
	id, known := d.open[conn]
	if !known {
		id = deviceOf(conn)
	}
	delete(d.open, conn)
	if closed {
		now := d.now()
		for gone, at := range d.closed {
			if now.Sub(at) > closedKept {
				delete(d.closed, gone)
			}
		}
		d.closed[conn] = now
	}
	_, listed := d.list[id]
	d.mu.Unlock()
	if !listed {
		return
	}
	d.update(id, false, func(device *protocol.Device, now int64) bool {
		still := d.openLocked(device.ID)
		changed := device.Connected != still
		device.Connected = still
		return changed
	})
}

// openLocked reports whether any connection of a device is open. d.mu is
// held.
func (d *Devices) openLocked(id string) bool {
	for _, device := range d.open {
		if device == id {
			return true
		}
	}
	return false
}

// sameDevice says whether two entries describe one phone by what it says of
// itself, for the entries older relays left: one per connection, 108 of them
// for one iPhone in four days. OS is left out: a phone that updated is still
// the same phone.
func sameDevice(a, b protocol.DeviceInfo) bool {
	if a.Name == "" && a.Model == "" {
		return false
	}
	return a.Name == b.Name && a.Model == b.Model && a.Platform == b.Platform
}

// absorbLocked folds into device the entries older relays left that say the
// same and are gone, keeping when the first was added, and reports whether
// it took any. A device kept by its pairing is never taken: two such are two
// phones, however alike. d.mu is held.
func (d *Devices) absorbLocked(device *protocol.Device) bool {
	took := false
	for id, other := range d.list {
		if id == device.ID || paired(id) || other.Connected || !sameDevice(device.DeviceInfo, other.DeviceInfo) {
			continue
		}
		device.FirstSeen = min(device.FirstSeen, other.FirstSeen)
		delete(d.list, id)
		took = true
	}
	return took
}

// collapseLocked folds a kept list's look-alikes into the latest of each,
// as absorbLocked would have as they arrived. d.mu is held, or d is not yet
// shared.
func (d *Devices) collapseLocked() bool {
	took := false
	for _, device := range d.listLocked() {
		if latest := d.list[device.ID]; latest != nil && d.absorbLocked(latest) {
			took = true
		}
	}
	return took
}

// List is every device, those connected first, then the most recently seen.
func (d *Devices) List() []protocol.Device {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.listLocked()
}

func (d *Devices) listLocked() []protocol.Device {
	out := make([]protocol.Device, 0, len(d.list))
	for _, device := range d.list {
		out = append(out, *device)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Connected != out[j].Connected {
			return out[i].Connected
		}
		if out[i].LastSeen != out[j].LastSeen {
			return out[i].LastSeen > out[j].LastSeen
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// update changes one device, new or known, and saves and tells of the
// change. With connected, key is a connection, now open, and its device is
// changed; otherwise key is the device.
func (d *Devices) update(key string, connected bool, change func(*protocol.Device, int64) bool) {
	if key == "" {
		return
	}
	now := d.now()
	d.mu.Lock()
	id := key
	closedAt, gone := d.closed[key]
	gone = gone && now.Sub(closedAt) <= closedKept
	if connected {
		id = deviceOf(key)
		if !gone {
			d.open[key] = id
		}
	}
	device := d.list[id]
	changed := false
	if device == nil {
		device = &protocol.Device{ID: id, FirstSeen: now.UnixMilli()}
		d.list[id] = device
		changed = true
	}
	before := device.Connected
	if change(device, now.UnixMilli()) {
		changed = true
	}
	// A request handled after its connection closed: what it says is kept,
	// but it opens nothing.
	if connected && gone {
		device.Connected = d.openLocked(id)
		changed = changed || device.Connected != before
	}
	var kept []protocol.Device
	if changed || now.Sub(d.saved) >= deviceSaveEvery {
		d.saved = now
		kept = d.listLocked()
	}
	onChange := d.onChange
	var list []protocol.Device
	if changed {
		list = d.listLocked()
	}
	d.mu.Unlock()

	if kept != nil {
		d.save(kept)
	}
	if changed && onChange != nil {
		onChange(list)
	}
}

func (d *Devices) save(list []protocol.Device) {
	if d.path == "" {
		return
	}
	for i := range list {
		list[i].Connected = false
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(d.path), 0o700)
	temp := d.path + ".tmp"
	if os.WriteFile(temp, data, 0o600) == nil {
		_ = os.Rename(temp, d.path)
	}
}
