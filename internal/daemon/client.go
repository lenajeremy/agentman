package daemon

import (
	"context"
	"encoding/json"
	"errors"
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
	// A short ordered burst queue keeps refresh, hook, and history work from
	// blocking on a slow relay socket. If it fills, reconnect/history recovery
	// is safer than retaining an unbounded copy of live transcript data.
	relayOutboundQueue = 32
	relayOutboundBytes = 8 << 20
	relayPingInterval  = 30 * time.Second
	// Reconnect backoff. A laptop lid closes, a train enters a tunnel, the
	// relay redeploys — all routine, none worth surfacing as an error.
	minBackoff = time.Second
	maxBackoff = 30 * time.Second
	// A paired device is trusted to control the user's agents, but it must not
	// be able to exhaust the daemon with an unbounded number of simultaneous
	// history reads or process-control requests.
	maxConcurrentRequests = 32
)

// Client connects a daemon to a relay and keeps that connection alive.
type Client struct {
	url   string
	token string

	mu sync.Mutex
	ws *daemonRelayConn

	// OnPairCode is called when the relay issues a pairing code.
	OnPairCode func(code, token string, expiresAt time.Time)
	// OnStatus reports connection transitions for the CLI to display.
	OnStatus func(connected bool, detail string)
	// OnDeviceDisconnected releases subscriptions owned by one app socket.
	OnDeviceDisconnected func(deviceID string)
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
	conn := c.ws
	c.mu.Unlock()
	if conn == nil {
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
	if len(frame) > maxFrame {
		return fmt.Errorf("daemon: event exceeds websocket frame limit")
	}

	return conn.Send(frame)
}

// RequestPairCode asks the relay for a pairing code.
func (c *Client) RequestPairCode(ctx context.Context) error {
	c.mu.Lock()
	conn := c.ws
	c.mu.Unlock()
	if conn == nil {
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
	if err := ctx.Err(); err != nil {
		return err
	}
	return conn.Send(frame)
}

// Run maintains the relay connection until ctx is cancelled, dispatching app
// requests to the handler.
func (c *Client) Run(
	ctx context.Context,
	handler func(context.Context, string, protocol.Request) protocol.Event,
) error {
	backoff := minBackoff

	for {
		if ctx.Err() != nil {
			return nil
		}

		attemptStarted := time.Now()
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
		// A connection that stayed healthy has paid off the previous failures;
		// do not make the next routine network handover inherit a 30-second wait.
		if time.Since(attemptStarted) >= 30*time.Second {
			backoff = minBackoff
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

func (c *Client) connectOnce(
	ctx context.Context,
	handler func(context.Context, string, protocol.Request) protocol.Event,
) error {
	dialCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	ws, _, err := websocket.Dial(dialCtx, c.url+"/ws/daemon", &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + c.token}},
	})
	if err != nil {
		return err
	}
	ws.SetReadLimit(maxFrame)
	conn := newDaemonRelayConn(ws)

	c.mu.Lock()
	c.ws = conn
	c.mu.Unlock()
	connectionCtx, stopConnection := context.WithCancel(ctx)
	devices := map[string]struct{}{}
	requests := make(chan struct{}, maxConcurrentRequests)
	mutationJobs := make(chan daemonRequestJob, maxConcurrentRequests)
	var requestWG sync.WaitGroup
	requestWG.Add(1)
	go func() {
		defer requestWG.Done()
		runOrderedMutationQueue(
			connectionCtx,
			mutationJobs,
			handler,
			func(replyTo string, event protocol.Event) { c.sendReplyOn(conn, replyTo, event) },
			func() { <-requests },
		)
	}()
	defer func() {
		stopConnection()
		close(mutationJobs)
		requestWG.Wait()
		if c.OnDeviceDisconnected != nil {
			for deviceID := range devices {
				c.OnDeviceDisconnected(deviceID)
			}
		}
		c.mu.Lock()
		if c.ws == conn {
			c.ws = nil
		}
		c.mu.Unlock()
		_ = conn.Close()
	}()

	if c.OnStatus != nil {
		c.OnStatus(true, c.url)
	}

	// Send the current session list immediately: a phone that reconnects
	// should see agents without waiting for the next discovery tick.
	if event := handler(connectionCtx, "", protocol.Request{Type: protocol.ReqListSessions}); event.Type != "" {
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
		if envelope.V != protocol.Version {
			return fmt.Errorf("daemon: relay uses incompatible protocol version %d", envelope.V)
		}

		if envelope.To == protocol.PeerRelay {
			if disconnected := c.handleControl(envelope); disconnected != "" {
				delete(devices, disconnected)
			}
			continue
		}
		if envelope.To != protocol.PeerDaemon {
			continue
		}
		if envelope.From == "" {
			continue
		}
		devices[envelope.From] = struct{}{}

		req, err := DecodeRequest(envelope.Payload)
		if err != nil {
			continue
		}

		// Subscription ownership is connection state, not background work. Apply
		// it synchronously so subscribe -> unsubscribe can never be reversed by
		// the scheduler and leave a transcript follower running after navigation.
		if req.Type == protocol.ReqSubscribe || req.Type == protocol.ReqUnsubscribe {
			event := handler(connectionCtx, envelope.From, req)
			if event.Type != "" {
				c.sendReplyOn(conn, envelope.ID, event)
			}
			continue
		}

		// History reads remain concurrent, while terminal mutations enter one
		// arrival-ordered queue. A mutex alone only prevents overlap; it does not
		// promise FIFO and could send two rapid messages in reverse order.
		select {
		case requests <- struct{}{}:
		default:
			c.sendReplyOn(conn, envelope.ID, protocol.Event{
				Type: protocol.EvtError, Error: "daemon is handling too many requests",
			})
			continue
		}
		if requestMutatesSession(req.Type) {
			mutationJobs <- daemonRequestJob{
				req: req, replyTo: envelope.ID, deviceID: envelope.From,
			}
			continue
		}
		requestWG.Add(1)
		go func(req protocol.Request, replyTo, deviceID string) {
			defer requestWG.Done()
			defer func() { <-requests }()
			event := handler(connectionCtx, deviceID, req)
			if event.Type == "" {
				return // acknowledged with nothing to say (subscribe/unsubscribe)
			}
			c.sendReplyOn(conn, replyTo, event)
		}(req, envelope.ID, envelope.From)
	}
}

type daemonRequestJob struct {
	req      protocol.Request
	replyTo  string
	deviceID string
}

// runOrderedMutationQueue is deliberately a single consumer. Terminal and API
// actions are not safely repeatable, and preserving their websocket arrival
// order is more important than parallelizing a user's button taps.
func runOrderedMutationQueue(
	ctx context.Context,
	jobs <-chan daemonRequestJob,
	handler func(context.Context, string, protocol.Request) protocol.Event,
	reply func(string, protocol.Event),
	release func(),
) {
	for job := range jobs {
		if ctx.Err() == nil {
			event := handler(ctx, job.deviceID, job.req)
			if event.Type != "" {
				reply(job.replyTo, event)
			}
		}
		release()
	}
}

func (c *Client) handleControl(envelope protocol.Envelope) string {
	var control protocol.Control
	if json.Unmarshal(envelope.Payload, &control) != nil {
		return ""
	}
	if control.Type == protocol.CtlPairCode && c.OnPairCode != nil {
		c.OnPairCode(control.Code, control.Token, time.UnixMilli(control.ExpiresAt))
	}
	if control.Type == protocol.CtlAppDisconnected &&
		control.DeviceID != "" && c.OnDeviceDisconnected != nil {
		c.OnDeviceDisconnected(control.DeviceID)
		return control.DeviceID
	}
	return ""
}

func (c *Client) sendReplyOn(conn *daemonRelayConn, replyTo string, event protocol.Event) {
	envelope, err := protocol.NewEnvelope(newID(), protocol.PeerApp, event)
	if err != nil {
		return
	}
	envelope.ReplyTo = replyTo
	frame, err := json.Marshal(envelope)
	if err != nil || len(frame) > maxFrame {
		return
	}
	_ = conn.Send(frame)
}

var (
	errRelayConnectionClosed = errors.New("daemon: relay connection closed")
	errRelayQueueFull        = errors.New("daemon: relay outbound queue full")
)

// daemonSocket is the write half used by daemonRelayConn. The narrow contract
// makes slow-writer and ordering behavior deterministic to test.
type daemonSocket interface {
	Write(context.Context, websocket.MessageType, []byte) error
	Ping(context.Context) error
	CloseNow() error
}

// daemonRelayConn is the single writer for one relay websocket. Daemon event
// producers enqueue without performing network I/O; one goroutine preserves
// event/reply order and owns both data writes and keepalive pings.
type daemonRelayConn struct {
	ws          daemonSocket
	outbound    chan []byte
	done        chan struct{}
	close       sync.Once
	queueMu     sync.Mutex
	queuedBytes int
}

func newDaemonRelayConn(ws daemonSocket) *daemonRelayConn {
	return newDaemonRelayConnWithQueue(ws, relayOutboundQueue)
}

func newDaemonRelayConnWithQueue(ws daemonSocket, capacity int) *daemonRelayConn {
	conn := &daemonRelayConn{
		ws: ws, outbound: make(chan []byte, capacity), done: make(chan struct{}),
	}
	go conn.writeLoop()
	return conn
}

func (c *daemonRelayConn) Send(frame []byte) error {
	select {
	case <-c.done:
		return errRelayConnectionClosed
	default:
	}
	c.queueMu.Lock()
	if c.queuedBytes > relayOutboundBytes-len(frame) {
		c.queueMu.Unlock()
		_ = c.Close()
		return errRelayQueueFull
	}
	c.queuedBytes += len(frame)
	select {
	case c.outbound <- frame:
		c.queueMu.Unlock()
		select {
		case <-c.done:
			return errRelayConnectionClosed
		default:
			return nil
		}
	default:
		c.queuedBytes -= len(frame)
		c.queueMu.Unlock()
		_ = c.Close()
		return errRelayQueueFull
	}
}

func (c *daemonRelayConn) Close() error {
	c.close.Do(func() {
		close(c.done)
		_ = c.ws.CloseNow()
	})
	return nil
}

func (c *daemonRelayConn) writeLoop() {
	ticker := time.NewTicker(relayPingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-c.done:
			return
		default:
		}
		select {
		case <-c.done:
			return
		case frame := <-c.outbound:
			ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
			err := c.ws.Write(ctx, websocket.MessageText, frame)
			cancel()
			c.queueMu.Lock()
			c.queuedBytes -= len(frame)
			c.queueMu.Unlock()
			if err != nil {
				_ = c.Close()
				return
			}
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
			err := c.ws.Ping(ctx)
			cancel()
			if err != nil {
				_ = c.Close()
				return
			}
		}
	}
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
