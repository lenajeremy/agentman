package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/relay"
)

const (
	maxFrame     = 4 << 20
	writeTimeout = 10 * time.Second
	// Reconnect backoff. A laptop lid closes, a train enters a tunnel, the
	// relay redeploys — all routine, none worth surfacing as an error.
	minBackoff = time.Second
	maxBackoff = 30 * time.Second
)

// Client connects a daemon to a relay and keeps that connection alive.
type Client struct {
	url   string
	token string

	mu sync.Mutex
	ws *websocket.Conn

	// OnPairCode is called when the relay issues a pairing code.
	OnPairCode func(code string, expiresAt time.Time)
	// OnStatus reports connection transitions for the CLI to display.
	OnStatus func(connected bool, detail string)
}

// NewClient creates a relay client. The URL may use http(s) or ws(s).
func NewClient(relayURL, daemonToken string) *Client {
	return &Client{url: normalizeWS(relayURL), token: daemonToken}
}

// Account returns the account this daemon's token maps to, which is what the
// relay derives independently. Useful for showing the user which account their
// devices will pair against.
func (c *Client) Account() relay.AccountID {
	return relay.DeriveAccount(c.token)
}

// Send implements Transport, delivering a daemon event to connected apps.
//
// A missing connection is not an error: the daemon runs whether or not anyone
// is watching, and events for a disconnected relay are simply dropped rather
// than queued. Queuing would mean holding user data, which is what this design
// exists to avoid.
func (c *Client) Send(event protocol.Event) error {
	c.mu.Lock()
	ws := c.ws
	c.mu.Unlock()
	if ws == nil {
		return nil
	}

	envelope, err := protocol.NewEnvelope(newID(), protocol.PeerApp, event)
	if err != nil {
		return err
	}
	frame, err := json.Marshal(envelope)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	return ws.Write(ctx, websocket.MessageText, frame)
}

// RequestPairCode asks the relay for a pairing code.
func (c *Client) RequestPairCode(ctx context.Context) error {
	c.mu.Lock()
	ws := c.ws
	c.mu.Unlock()
	if ws == nil {
		return fmt.Errorf("daemon: not connected to the relay")
	}

	envelope, err := protocol.NewEnvelope(newID(), protocol.PeerRelay,
		protocol.Control{Type: protocol.CtlPairRequest})
	if err != nil {
		return err
	}
	frame, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	return ws.Write(ctx, websocket.MessageText, frame)
}

// Run maintains the relay connection until ctx is cancelled, dispatching app
// requests to the handler.
func (c *Client) Run(ctx context.Context, handler func(context.Context, protocol.Request) protocol.Event) error {
	backoff := minBackoff

	for {
		if ctx.Err() != nil {
			return nil
		}

		err := c.connectOnce(ctx, handler)
		if ctx.Err() != nil {
			return nil
		}

		if c.OnStatus != nil {
			detail := "reconnecting"
			if err != nil {
				detail = err.Error()
			}
			c.OnStatus(false, detail)
		}

		// Jittered backoff: without it, every daemon reconnects in lockstep
		// after a relay redeploy and stampedes it.
		jitter := time.Duration(rand.Int64N(int64(backoff / 2)))
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff + jitter):
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

func (c *Client) connectOnce(ctx context.Context, handler func(context.Context, protocol.Request) protocol.Event) error {
	dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	ws, _, err := websocket.Dial(dialCtx, c.url+"/ws/daemon", &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + c.token}},
	})
	if err != nil {
		return err
	}
	ws.SetReadLimit(maxFrame)

	c.mu.Lock()
	c.ws = ws
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.ws = nil
		c.mu.Unlock()
		ws.CloseNow()
	}()

	if c.OnStatus != nil {
		c.OnStatus(true, c.url)
	}

	// Send the current session list immediately: a phone that reconnects
	// should see agents without waiting for the next discovery tick.
	if event := handler(ctx, protocol.Request{Type: protocol.ReqListSessions}); event.Type != "" {
		_ = c.Send(event)
	}

	for {
		_, data, err := ws.Read(ctx)
		if err != nil {
			return err
		}

		var envelope protocol.Envelope
		if json.Unmarshal(data, &envelope) != nil {
			continue
		}

		if envelope.To == protocol.PeerRelay {
			c.handleControl(envelope)
			continue
		}
		if envelope.To != protocol.PeerDaemon {
			continue
		}

		req, err := DecodeRequest(envelope.Payload)
		if err != nil {
			continue
		}

		// Each request is handled on its own goroutine so a slow page read
		// cannot stall live events or another request behind it.
		go func(req protocol.Request, replyTo string) {
			event := handler(ctx, req)
			if event.Type == "" {
				return // acknowledged with nothing to say (subscribe/unsubscribe)
			}
			c.sendReply(replyTo, event)
		}(req, envelope.ID)
	}
}

func (c *Client) handleControl(envelope protocol.Envelope) {
	var control protocol.Control
	if json.Unmarshal(envelope.Payload, &control) != nil {
		return
	}
	if control.Type == protocol.CtlPairCode && c.OnPairCode != nil {
		c.OnPairCode(control.Code, time.UnixMilli(control.ExpiresAt))
	}
}

// sendReply sends an event tagged with the request it answers, so the app can
// match a page to the scroll that asked for it.
func (c *Client) sendReply(replyTo string, event protocol.Event) {
	c.mu.Lock()
	ws := c.ws
	c.mu.Unlock()
	if ws == nil {
		return
	}

	envelope, err := protocol.NewEnvelope(newID(), protocol.PeerApp, event)
	if err != nil {
		return
	}
	envelope.ReplyTo = replyTo
	frame, err := json.Marshal(envelope)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	_ = ws.Write(ctx, websocket.MessageText, frame)
}

func normalizeWS(raw string) string {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	switch {
	case strings.HasPrefix(raw, "https://"):
		return "wss://" + strings.TrimPrefix(raw, "https://")
	case strings.HasPrefix(raw, "http://"):
		return "ws://" + strings.TrimPrefix(raw, "http://")
	case strings.HasPrefix(raw, "ws://"), strings.HasPrefix(raw, "wss://"):
		return raw
	default:
		return "wss://" + raw
	}
}

func newID() string {
	return fmt.Sprintf("%d-%d", time.Now().UnixMilli(), rand.Int64N(1<<24))
}
