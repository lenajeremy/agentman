package relay

import (
	"crypto/rand"
	"encoding/base32"
	"errors"
	"net/http"
	"sync"
	"time"
)

// An ephemeral parcel locker for images on their way from a phone to a Mac.
//
// The bytes never travel over a websocket. An app frame is capped at 128 KiB
// on purpose — the relay is public, an app has no legitimate reason to send
// bulk, and raising that ceiling to carry a screenshot would make the cheapest
// frame anyone can flood with several times larger. So the phone POSTs the
// image here, sends the id down the socket it already holds, and the daemon
// collects it over plain HTTPS in the direction it already dials. Keeping bulk
// off the control channel also means a slow upload cannot stall the frames
// that carry liveness and messages.
//
// Memory rather than disk: this process is given 8 GiB and normally uses about
// 20 MiB, a blob lives only for the seconds between an upload finishing and a
// daemon collecting it, and a map needs no cleanup after a crash, no file
// handles, and no sweeper racing a reader.
const (
	// One image. Phones downscale before sending, so this is headroom rather
	// than a target.
	maxUploadBytes = 4 << 20
	// Per account, because an account is cheap to obtain: pairing is
	// self-sovereign by design, so anyone willing to pair can have one.
	maxAccountUploads     = 10
	maxAccountUploadBytes = 40 << 20
	// The ceiling that actually protects the process, and the one that matters:
	// pairing is self-sovereign, so an attacker can hold as many accounts as it
	// likes and the per-account caps only shape the traffic rather than bound
	// it. 512 MiB is 128 uncollected uploads at full size, and 6% of the 8 GiB
	// this process is given.
	maxTotalUploadBytes = 512 << 20
	// A connected daemon collects within a second. This is the allowance for
	// one that is asleep, not a storage policy.
	uploadTTL = 2 * time.Minute
)

// imageTypes is what a daemon will be handed. The client's Content-Type is not
// consulted: the bytes are sniffed, so a mislabelled or hostile upload cannot
// decide what it will be written to disk as.
var imageTypes = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

var errUploadCapacity = errors.New("relay: upload capacity reached")

type upload struct {
	account AccountID
	mime    string
	data    []byte
	expires time.Time
}

type uploadStore struct {
	mu    sync.Mutex
	items map[string]*upload
	total int
}

func newUploadStore() *uploadStore {
	return &uploadStore{items: map[string]*upload{}}
}

// put stores one image and returns the ticket the daemon will present.
func (s *uploadStore) put(account AccountID, mime string, data []byte, now time.Time) (string, error) {
	if len(data) == 0 || len(data) > maxUploadBytes {
		return "", errors.New("relay: upload is empty or too large")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(now)

	count, bytes := 0, 0
	for _, item := range s.items {
		if item.account == account {
			count++
			bytes += len(item.data)
		}
	}
	if count >= maxAccountUploads || bytes+len(data) > maxAccountUploadBytes {
		return "", errUploadCapacity
	}
	if s.total+len(data) > maxTotalUploadBytes {
		return "", errUploadCapacity
	}

	id, err := newUploadID()
	if err != nil {
		return "", err
	}
	s.items[id] = &upload{account: account, mime: mime, data: data, expires: now.Add(uploadTTL)}
	s.total += len(data)
	return id, nil
}

// take returns an upload and removes it, so a ticket is good exactly once.
//
// The account must match the one that uploaded it. A ticket is unguessable
// anyway, but the check is what makes the containment a rule rather than a
// property of the random number generator.
func (s *uploadStore) take(account AccountID, id string, now time.Time) (*upload, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(now)

	item, ok := s.items[id]
	if !ok || item.account != account {
		return nil, false
	}
	delete(s.items, id)
	s.total -= len(item.data)
	return item, true
}

func (s *uploadStore) sweepLocked(now time.Time) {
	for id, item := range s.items {
		if now.After(item.expires) {
			delete(s.items, id)
			s.total -= len(item.data)
		}
	}
}

// pending reports what is held, for tests and for /health.
func (s *uploadStore) pending() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.items), s.total
}

var uploadEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

func newUploadID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return uploadEncoding.EncodeToString(raw[:]), nil
}

// sniffImage identifies an upload from its own bytes, returning the media type
// and the extension it should be written with.
func sniffImage(data []byte) (mime, ext string, ok bool) {
	head := data
	if len(head) > 512 {
		head = head[:512]
	}
	mime = http.DetectContentType(head)
	ext, ok = imageTypes[mime]
	return mime, ext, ok
}
