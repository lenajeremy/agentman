// Package attachments collects images a phone sent and keeps them on disk for
// as long as they are worth keeping.
//
// An agent cannot be handed bytes. The daemon drives it by typing into a tmux
// pane, so the only way to give it a picture is to put the picture somewhere
// and type where that is — which is also exactly what a person does when they
// drag a file into a terminal. It works on Linux, over SSH and in a detached
// session, none of which is true of setting the system clipboard and sending
// Ctrl-V.
package attachments

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	// Matches the relay's own per-upload cap, with a little slack so a body
	// that is exactly at the limit is not rejected here by an off-by-one.
	maxAttachmentBytes = (4 << 20) + 4096
	// How long a sent image stays readable. The transcript keeps pointing at
	// it after the agent has read it — scroll back tomorrow and tap it and it
	// should still open — so deleting on read is wrong. A week is long enough
	// to be useful and short enough not to accumulate.
	retention = 7 * 24 * time.Hour
	// A second bound, so that a heavy week cannot run away. Roughly five
	// hundred downscaled screenshots.
	maxTotalBytes = 100 << 20
	fetchTimeout  = 30 * time.Second
)

// imageTypes is what may be written, keyed by what the bytes actually are.
var imageTypes = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

// Store fetches images from the relay and files them under a directory it owns.
type Store struct {
	dir      string
	relayURL string
	token    string
	client   *http.Client
}

// New returns a store writing beneath dir, which should be agentman's own
// configuration directory rather than anywhere a project can see.
func New(dir, relayURL, token string) *Store {
	return &Store{
		dir:      filepath.Join(dir, "images"),
		relayURL: strings.TrimRight(relayURL, "/"),
		token:    token,
		client:   &http.Client{Timeout: fetchTimeout},
	}
}

// Save collects one upload and returns the absolute path it was written to.
//
// The bytes are identified here rather than trusted from the response: the
// relay is a separate process reachable from the internet, and this decides
// what gets written to a developer's disk. A name is generated rather than
// taken from anything the phone sent, so no part of the path is attacker
// chosen.
func (s *Store) Save(ctx context.Context, sessionID, uploadID string) (string, error) {
	if !validUploadID(uploadID) {
		return "", errors.New("attachments: malformed upload id")
	}
	data, err := s.fetch(ctx, uploadID)
	if err != nil {
		return "", err
	}
	ext, ok := sniff(data)
	if !ok {
		return "", errors.New("attachments: that upload is not an image")
	}

	dir := filepath.Join(s.dir, sessionDir(sessionID))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("attachments: could not create the image directory: %w", err)
	}
	name, err := randomName()
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, name+ext)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", fmt.Errorf("attachments: could not write the image: %w", err)
	}
	return path, nil
}

func (s *Store) fetch(ctx context.Context, uploadID string) ([]byte, error) {
	if s.relayURL == "" {
		return nil, errors.New("attachments: this daemon has no relay to collect from")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.relayURL+"/upload/"+uploadID, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+s.token)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("attachments: could not reach the relay: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, errors.New("attachments: that image expired before it could be collected")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("attachments: the relay refused the image (%s)", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAttachmentBytes+1))
	if err != nil {
		return nil, fmt.Errorf("attachments: could not read the image: %w", err)
	}
	if len(data) > maxAttachmentBytes {
		return nil, errors.New("attachments: that image is too large")
	}
	return data, nil
}

// Sweep deletes images that are past their welcome, oldest first.
//
// Called at startup as well as on a timer: leave the daemon off for a
// fortnight and it should tidy up the moment it comes back, rather than
// waiting an hour for the first tick.
func (s *Store) Sweep(now time.Time) error {
	type file struct {
		path string
		size int64
		age  time.Time
	}
	var kept []file
	var total int64

	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		sub := filepath.Join(s.dir, entry.Name())
		items, readErr := os.ReadDir(sub)
		if readErr != nil {
			continue
		}
		for _, item := range items {
			info, infoErr := item.Info()
			// Only plain files, and never through a symlink: this walks a
			// directory that is deleted from, so it must not be steerable out
			// of the tree it owns.
			if infoErr != nil || !info.Mode().IsRegular() {
				continue
			}
			path := filepath.Join(sub, item.Name())
			if now.Sub(info.ModTime()) > retention {
				_ = os.Remove(path)
				continue
			}
			kept = append(kept, file{path: path, size: info.Size(), age: info.ModTime()})
			total += info.Size()
		}
	}

	sort.Slice(kept, func(i, j int) bool { return kept[i].age.Before(kept[j].age) })
	for _, item := range kept {
		if total <= maxTotalBytes {
			break
		}
		if os.Remove(item.path) == nil {
			total -= item.size
		}
	}

	// A session directory with nothing left in it is litter.
	for _, entry := range entries {
		if entry.IsDir() {
			_ = os.Remove(filepath.Join(s.dir, entry.Name()))
		}
	}
	return nil
}

// sessionDir turns a session id into one safe directory name. Ids carry a
// colon ("claude:<uuid>") and come from an agent rather than from this
// process, so nothing in them is allowed to steer the path.
func sessionDir(sessionID string) string {
	var b strings.Builder
	for _, r := range sessionID {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	name := strings.Trim(b.String(), "-")
	if name == "" {
		return "session"
	}
	if len(name) > 64 {
		name = name[:64]
	}
	return name
}

var nameEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// randomName never begins with a character tmux could read as a flag, because
// the path it becomes is typed into a pane as an argument.
func randomName() (string, error) {
	var raw [10]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return strings.ToLower(nameEncoding.EncodeToString(raw[:])), nil
}

func validUploadID(id string) bool {
	if len(id) < 16 || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if !(r >= 'A' && r <= 'Z') && !(r >= '2' && r <= '7') {
			return false
		}
	}
	return true
}

func sniff(data []byte) (string, bool) {
	head := data
	if len(head) > 512 {
		head = head[:512]
	}
	ext, ok := imageTypes[http.DetectContentType(head)]
	return ext, ok
}
