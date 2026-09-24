package servers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/lenajeremy/agentman/internal/tunnel"
)

// openTimeout bounds how long a tap on the phone waits for a link.
const openTimeout = 20 * time.Second

// Sharer keeps one preview tunnel per shared port, opened on request and
// closed when asked or when the server goes away.
//
// It runs the same tunnel client as `am expose`, inside the daemon. A tunnel
// has its own relay socket, separate from the daemon's, so opening one never
// disturbs the connection the phone is using.
type Sharer struct {
	relayURL string
	token    string

	mu     sync.Mutex
	shares map[int]*share
}

type share struct {
	cancel context.CancelFunc
	ready  chan struct{}
	done   chan struct{}
	link   string
	err    error
}

// NewSharer shares through the relay at relayURL as the daemon's account.
func NewSharer(relayURL, token string) *Sharer {
	return &Sharer{relayURL: relayURL, token: token, shares: map[int]*share{}}
}

// Open shares port and returns its link, reusing a tunnel already open.
func (s *Sharer) Open(ctx context.Context, port int) (string, error) {
	s.mu.Lock()
	current := s.shares[port]
	if current == nil {
		current = s.startLocked(port)
	}
	s.mu.Unlock()

	wait, cancel := context.WithTimeout(ctx, openTimeout)
	defer cancel()
	select {
	case <-current.ready:
		return current.link, nil
	case <-current.done:
		return "", current.err
	case <-wait.Done():
		// Nobody is waiting for this tunnel any more; do not leave it
		// retrying in the background.
		s.Close(port)
		return "", errors.New("servers: the relay did not answer in time")
	}
}

func (s *Sharer) startLocked(port int) *share {
	ctx, cancel := context.WithCancel(context.Background())
	current := &share{cancel: cancel, ready: make(chan struct{}), done: make(chan struct{})}
	s.shares[port] = current

	var once sync.Once
	client := &tunnel.Client{
		RelayURL: s.relayURL,
		Token:    s.token,
		Port:     port,
		OnReady: func(hello tunnel.Hello) {
			// Reconnects keep the same link, so only the first matters.
			once.Do(func() {
				s.mu.Lock()
				current.link = hello.URL
				s.mu.Unlock()
				close(current.ready)
			})
		},
	}
	go func() {
		err := client.Run(ctx)
		s.mu.Lock()
		current.err = describe(err)
		if s.shares[port] == current {
			delete(s.shares, port)
		}
		s.mu.Unlock()
		close(current.done)
	}()
	return current
}

// Close stops sharing port. Closing a port that is not shared does nothing.
func (s *Sharer) Close(port int) {
	s.mu.Lock()
	current := s.shares[port]
	delete(s.shares, port)
	s.mu.Unlock()
	if current != nil {
		current.cancel()
	}
}

// Links reports the link of every port currently shared.
func (s *Sharer) Links() map[int]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	links := make(map[int]string, len(s.shares))
	for port, current := range s.shares {
		if current.link != "" {
			links[port] = current.link
		}
	}
	return links
}

// Retain closes every share whose port is not in live, so a link dies with
// its server instead of pointing at whatever next listens on that port.
func (s *Sharer) Retain(live map[int]bool) {
	s.mu.Lock()
	var stale []*share
	for port, current := range s.shares {
		if !live[port] {
			stale = append(stale, current)
			delete(s.shares, port)
		}
	}
	s.mu.Unlock()
	for _, current := range stale {
		current.cancel()
	}
}

// CloseAll stops every share, for daemon shutdown.
func (s *Sharer) CloseAll() {
	s.Retain(nil)
}

// describe turns a tunnel refusal into something the phone can show.
func describe(err error) error {
	var refusal *tunnel.PermanentError
	if !errors.As(err, &refusal) {
		if err == nil {
			return errors.New("servers: sharing stopped")
		}
		return err
	}
	switch refusal.Status {
	case http.StatusNotFound:
		return errors.New("this relay does not offer preview links")
	case http.StatusTooManyRequests:
		return fmt.Errorf("already sharing the most servers allowed — stop one first")
	}
	return refusal
}
