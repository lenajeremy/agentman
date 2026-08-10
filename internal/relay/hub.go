package relay

import (
	"crypto/rand"
	"errors"
	"math/big"
	"sync"
	"time"
)

// PairingCodeTTL is how long a pairing code stays valid. Short on purpose: the
// code is six digits, so it must stop being useful almost immediately.
const PairingCodeTTL = 60 * time.Second

// pairingAttemptLimit caps redemption attempts per code before it is burned.
// Six digits is only a million possibilities, and a minute is a long time for
// an attacker with a script.
const pairingAttemptLimit = 5

// ErrNoPeer is returned when a frame has nowhere to go.
var ErrNoPeer = errors.New("relay: peer not connected")

// Conn is one live websocket participant. The hub only needs to be able to
// push bytes at it, which keeps the transport swappable and the tests free of
// real sockets.
type Conn interface {
	// Send delivers one frame. It must be safe for concurrent use and must not
	// block indefinitely — a wedged phone must never stall the daemon.
	Send(frame []byte) error
	// Close terminates the connection.
	Close() error
}

// Hub is the relay's entire state: who is connected right now.
//
// Everything here is in memory and deliberately disposable. Restarting the
// relay drops connections, which every client already handles by reconnecting,
// and loses nothing else because there is nothing else.
type Hub struct {
	mu sync.RWMutex
	// daemons holds at most one daemon per account — the newest connection
	// wins, so restarting the daemon cleanly replaces a stale socket.
	daemons map[AccountID]Conn
	// apps holds every connected device for an account, since a user may have
	// a phone and a tablet open at once.
	apps map[AccountID]map[string]Conn
	// pairings are the only time-limited state, expired lazily on access.
	pairings map[string]pairing
	// lastSeen lets an app be told how long the Mac has been gone, rather than
	// just "offline".
	lastSeen map[AccountID]time.Time
}

type pairing struct {
	account  AccountID
	expires  time.Time
	attempts int
}

// NewHub creates an empty hub.
func NewHub() *Hub {
	return &Hub{
		daemons:  map[AccountID]Conn{},
		apps:     map[AccountID]map[string]Conn{},
		pairings: map[string]pairing{},
		lastSeen: map[AccountID]time.Time{},
	}
}

// AddDaemon registers a daemon connection, returning any previous one so the
// caller can close it.
func (h *Hub) AddDaemon(account AccountID, conn Conn) (replaced Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	replaced = h.daemons[account]
	h.daemons[account] = conn
	return replaced
}

// RemoveDaemon deregisters a daemon, ignoring a connection that has already
// been replaced by a newer one.
func (h *Hub) RemoveDaemon(account AccountID, conn Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if current, ok := h.daemons[account]; ok && current == conn {
		delete(h.daemons, account)
		h.lastSeen[account] = time.Now()
	}
}

// AddApp registers a device connection under a caller-supplied id.
func (h *Hub) AddApp(account AccountID, deviceID string, conn Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.apps[account] == nil {
		h.apps[account] = map[string]Conn{}
	}
	h.apps[account][deviceID] = conn
}

// RemoveApp deregisters a device.
func (h *Hub) RemoveApp(account AccountID, deviceID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if devices, ok := h.apps[account]; ok {
		delete(devices, deviceID)
		if len(devices) == 0 {
			delete(h.apps, account)
		}
	}
}

// DaemonOnline reports whether an account's daemon is connected, and when it
// was last seen if not.
func (h *Hub) DaemonOnline(account AccountID) (bool, time.Time) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if _, ok := h.daemons[account]; ok {
		return true, time.Time{}
	}
	return false, h.lastSeen[account]
}

// ToDaemon forwards a frame from a device to its daemon.
//
// Returns ErrNoPeer immediately when the Mac is offline rather than buffering.
// Holding the frame would mean storing user data, which is the one thing this
// relay does not do — and a prompt "your Mac is offline" beats a message that
// silently arrives an hour later.
func (h *Hub) ToDaemon(account AccountID, frame []byte) error {
	h.mu.RLock()
	conn, ok := h.daemons[account]
	h.mu.RUnlock()
	if !ok {
		return ErrNoPeer
	}
	return conn.Send(frame)
}

// ToApps fans a frame out to every device on an account, returning how many
// received it. Send errors are ignored: a dead socket is the reader loop's
// problem to notice and clean up.
func (h *Hub) ToApps(account AccountID, frame []byte) int {
	h.mu.RLock()
	devices := make([]Conn, 0, len(h.apps[account]))
	for _, conn := range h.apps[account] {
		devices = append(devices, conn)
	}
	h.mu.RUnlock()

	var delivered int
	for _, conn := range devices {
		if conn.Send(frame) == nil {
			delivered++
		}
	}
	return delivered
}

// NewPairingCode issues a short-lived code for an account.
func (h *Hub) NewPairingCode(account AccountID) (string, error) {
	code, err := randomDigits(6)
	if err != nil {
		return "", err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sweepPairingsLocked()
	h.pairings[code] = pairing{account: account, expires: time.Now().Add(PairingCodeTTL)}
	return code, nil
}

// RedeemPairingCode exchanges a code for the account it authorizes.
//
// A code is single use: consumed on success, and burned after a few failed
// attempts so a six-digit space cannot be walked within its lifetime.
func (h *Hub) RedeemPairingCode(code string) (AccountID, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sweepPairingsLocked()

	entry, ok := h.pairings[code]
	if !ok {
		// Count the miss against every live code: without a valid code there
		// is nothing else to attribute a guess to, and this bounds how many
		// guesses any outstanding pairing can survive.
		h.chargeFailedAttemptLocked()
		return "", false
	}
	delete(h.pairings, code)
	return entry.account, true
}

func (h *Hub) chargeFailedAttemptLocked() {
	for code, entry := range h.pairings {
		entry.attempts++
		if entry.attempts >= pairingAttemptLimit {
			delete(h.pairings, code)
			continue
		}
		h.pairings[code] = entry
	}
}

func (h *Hub) sweepPairingsLocked() {
	now := time.Now()
	for code, entry := range h.pairings {
		if now.After(entry.expires) {
			delete(h.pairings, code)
		}
	}
}

// Stats reports connection counts. Deliberately the only introspection the
// relay offers: it can describe who is connected, never what they said.
func (h *Hub) Stats() (daemons, apps, pendingPairings int) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, devices := range h.apps {
		apps += len(devices)
	}
	return len(h.daemons), apps, len(h.pairings)
}

func randomDigits(n int) (string, error) {
	digits := make([]byte, n)
	for i := range digits {
		v, err := rand.Int(rand.Reader, big.NewInt(10))
		if err != nil {
			return "", err
		}
		digits[i] = byte('0' + v.Int64())
	}
	return string(digits), nil
}
