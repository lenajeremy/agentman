package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Devices keeps the phones and tablets that reach this Mac through the
// relay, in a file beside the daemon's config: when each was first and last
// seen, the name it gives itself, and whether it is connected now. The relay
// keeps no such list by design; the daemon sees each device's requests.
type Devices struct {
	path string
	now  func() time.Time

	mu       sync.Mutex
	list     map[string]*protocol.Device
	saved    time.Time
	onChange func([]protocol.Device)
}

// deviceSaveEvery spaces out saves for a device that is only still there: an
// open phone sends a request every few seconds.
const deviceSaveEvery = time.Minute

// NewDevices reads the devices kept at path. None is connected until it
// sends something.
func NewDevices(path string) *Devices {
	d := &Devices{path: path, now: time.Now, list: map[string]*protocol.Device{}}
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
	return d
}

// OnChange is told the whole list whenever a device arrives, leaves or
// names itself.
func (d *Devices) OnChange(f func([]protocol.Device)) {
	d.mu.Lock()
	d.onChange = f
	d.mu.Unlock()
}

// Seen notes a request from a device: a new one, one back, or one still here.
func (d *Devices) Seen(id string) {
	d.update(id, func(device *protocol.Device, now int64) bool {
		changed := !device.Connected
		device.Connected = true
		device.LastSeen = now
		return changed
	})
}

// Named keeps what a device says about itself.
func (d *Devices) Named(id string, info protocol.DeviceInfo) {
	d.update(id, func(device *protocol.Device, now int64) bool {
		changed := device.DeviceInfo != info || !device.Connected
		device.DeviceInfo = info
		device.Connected = true
		device.LastSeen = now
		return changed
	})
}

// Left notes that a device's connection closed.
func (d *Devices) Left(id string) {
	d.mu.Lock()
	device := d.list[id]
	d.mu.Unlock()
	if device == nil {
		return
	}
	d.update(id, func(device *protocol.Device, now int64) bool {
		changed := device.Connected
		device.Connected = false
		return changed
	})
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

// update changes one device, new or known, and saves and tells of the change.
func (d *Devices) update(id string, change func(*protocol.Device, int64) bool) {
	if id == "" {
		return
	}
	now := d.now()
	d.mu.Lock()
	device := d.list[id]
	changed := false
	if device == nil {
		device = &protocol.Device{ID: id, FirstSeen: now.UnixMilli()}
		d.list[id] = device
		changed = true
	}
	if change(device, now.UnixMilli()) {
		changed = true
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
