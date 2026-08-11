package relay

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"hash/fnv"
	"math/big"
	"sync"
	"time"
)

// PairingCodeTTL is how long a pairing code stays valid. Short on purpose, so
// a guessable code stops being useful almost immediately.
const PairingCodeTTL = 60 * time.Second

// A pairing code is a two-digit account shard followed by six random digits.
//
// The shard exists so a failed guess can be attributed to something. A wrong
// code belongs to no account by definition — that is what makes it wrong — so
// without a partition the only place to charge it is a bucket shared by every
// user, and an attacker flooding that bucket locks everyone out. The shard is
// derived from the account, so a guess names the group it is aimed at even
// when it names no code, and the damage stops at that group.
//
// The random half stays six digits, so the odds of guessing any particular
// live code are unchanged; the shard adds addressing, not weakness.
const (
	pairingShardDigits  = 2
	pairingRandomDigits = 6
	// PairingShards must match pairingShardDigits: 10^2.
	PairingShards = 100

	// pairingTokenBytes is the entropy behind a scanned pairing. At 16 bytes
	// the space is 2^128, so brute force is not a threat the relay has to
	// defend against — which is what lets the scanned path skip the rate
	// limiting that the eight-digit typed path needs.
	pairingTokenBytes = 16
)

// IsPairingToken reports whether a submitted secret is a scanned token rather
// than a typed code. They are told apart by shape, not by a flag from the
// client, so a caller cannot claim the token path to dodge rate limiting.
func IsPairingToken(secret string) bool {
	if len(secret) != base64.RawURLEncoding.EncodedLen(pairingTokenBytes) {
		return false
	}
	_, err := base64.RawURLEncoding.DecodeString(secret)
	return err == nil
}

// ShardForAccount returns the bucket an account's codes live in.
//
// FNV rather than the account hash itself: the account is already a truncated
// SHA-256, and reusing its leading digits would put the shard and the identity
// in the same bits for no reason.
func ShardForAccount(account AccountID) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(account))
	return fmt.Sprintf("%0*d", pairingShardDigits, h.Sum32()%PairingShards)
}

// ShardForCode reads the shard out of a submitted code.
//
// Returns false for anything malformed, which is deliberately not treated as
// a shard of its own: garbage would otherwise get a private budget and could
// be used to avoid rate limiting entirely.
func ShardForCode(code string) (string, bool) {
	if len(code) != pairingShardDigits+pairingRandomDigits {
		return "", false
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			return "", false
		}
	}
	return code[:pairingShardDigits], true
}

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
	account AccountID
	expires time.Time
	// peer is the other key for this same pairing. A pairing is reachable by
	// two secrets — a long one for scanning and a short one for typing — and
	// redeeming either has to retire both, or the unused one would linger as a
	// second way in.
	peer string
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

// PairingSecrets are the two ways to redeem one pairing.
type PairingSecrets struct {
	// Code is eight digits, meant to be read off a screen and typed.
	Code string
	// Token is long and random, meant to be scanned from a QR code. Because
	// nobody types it, it can carry enough entropy that guessing is not a
	// threat worth rate limiting — which is the whole reason it exists.
	Token string
}

// NewPairing issues a short-lived pairing reachable by either secret.
func (h *Hub) NewPairing(account AccountID) (PairingSecrets, error) {
	random, err := randomDigits(pairingRandomDigits)
	if err != nil {
		return PairingSecrets{}, err
	}
	token, err := randomToken(pairingTokenBytes)
	if err != nil {
		return PairingSecrets{}, err
	}
	code := ShardForAccount(account) + random

	h.mu.Lock()
	defer h.mu.Unlock()
	h.sweepPairingsLocked()

	expires := time.Now().Add(PairingCodeTTL)
	h.pairings[code] = pairing{account: account, expires: expires, peer: token}
	h.pairings[token] = pairing{account: account, expires: expires, peer: code}
	return PairingSecrets{Code: code, Token: token}, nil
}

// NewPairingCode issues a pairing and returns only its typed code.
func (h *Hub) NewPairingCode(account AccountID) (string, error) {
	secrets, err := h.NewPairing(account)
	return secrets.Code, err
}

// RedeemPairingCode exchanges a code for the account it authorizes.
//
// A code is single use and expires on its own. Brute force is bounded by rate
// limiting the caller (see limiter) rather than by penalising codes: a wrong
// guess belongs to nobody in particular, so making outstanding codes pay for
// it lets any stranger invalidate every other user's pairing on a shared relay.
func (h *Hub) RedeemPairingCode(code string) (AccountID, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sweepPairingsLocked()

	entry, ok := h.pairings[code]
	if !ok {
		return "", false
	}
	delete(h.pairings, code)
	// Retire the sibling too: a pairing consumed by scanning must not still be
	// redeemable by typing, and vice versa.
	if entry.peer != "" {
		delete(h.pairings, entry.peer)
	}
	return entry.account, true
}

// randomToken returns a URL-safe secret with n bytes of entropy.
func randomToken(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
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
	// One pairing occupies two entries — the typed code and the scanned token
	// — so count only the codes, or /health reports double.
	for key := range h.pairings {
		if !IsPairingToken(key) {
			pendingPairings++
		}
	}
	return len(h.daemons), apps, pendingPairings
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
