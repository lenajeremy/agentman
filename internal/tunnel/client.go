package tunnel

import (
	"context"
	crand "crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"
)

const (
	helloTimeout = 15 * time.Second
	// Reconnect backoff, matching the daemon's relay client: a laptop lid
	// closing or the relay redeploying is routine, not an error.
	minBackoff = time.Second
	maxBackoff = 30 * time.Second
	// localDialTimeout covers a loopback dial, which either connects or is
	// refused almost instantly. The ceiling only matters for a server so
	// overloaded it has stopped accepting.
	localDialTimeout = 5 * time.Second
	// unavailableDrain bounds how long a stream answered with an error page
	// waits for the relay to finish sending the request it no longer needs.
	unavailableDrain = 5 * time.Second
)

// PermanentError is a refusal that retrying cannot fix: a bad token, a relay
// without previews enabled, or an account already sharing too many ports.
type PermanentError struct {
	Status  int
	Message string
}

func (e *PermanentError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("relay refused the tunnel (HTTP %d)", e.Status)
	}
	return e.Message
}

// Client shares one local port through a relay until its context ends.
type Client struct {
	// RelayURL is the relay's base URL, as http(s) or ws(s).
	RelayURL string
	// Token is the daemon token; it decides which account owns the link.
	Token string
	// Port is the local TCP port to share. It never changes after start.
	Port int
	// Nonce keeps the link stable across reconnects. Left empty, one is
	// generated on first use, so the link lasts as long as this Client does.
	Nonce string

	// OnReady is called after every successful (re)connect with the link.
	OnReady func(Hello)
	// OnDisconnect reports a dropped connection that is about to be retried.
	OnDisconnect func(err error)

	nonceOnce sync.Once
}

// Run keeps the tunnel open, reconnecting with jittered backoff, until ctx is
// cancelled. It returns nil on cancellation and a *PermanentError when the
// relay refuses in a way that retrying cannot fix.
func (c *Client) Run(ctx context.Context) error {
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("tunnel: invalid port %d", c.Port)
	}
	if strings.TrimSpace(c.Token) == "" {
		return errors.New("tunnel: missing daemon token")
	}
	var nonceErr error
	c.nonceOnce.Do(func() {
		if c.Nonce == "" {
			c.Nonce, nonceErr = newNonce()
		}
	})
	if nonceErr != nil {
		return nonceErr
	}

	backoff := minBackoff
	for {
		started := time.Now()
		err := c.connectOnce(ctx)
		if ctx.Err() != nil {
			return nil
		}
		var permanent *PermanentError
		if errors.As(err, &permanent) {
			return err
		}
		if c.OnDisconnect != nil {
			c.OnDisconnect(err)
		}
		// A connection that stayed up has paid off earlier failures.
		if time.Since(started) >= 30*time.Second {
			backoff = minBackoff
		}
		// Jitter keeps every tunnel from reconnecting in lockstep after a
		// relay redeploy.
		jitter := time.Duration(rand.Int64N(int64(backoff / 2)))
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff + jitter):
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

func (c *Client) connectOnce(ctx context.Context) error {
	dialCtx, cancel := context.WithTimeout(ctx, helloTimeout)
	defer cancel()

	ws, resp, err := websocket.Dial(dialCtx, wsBase(c.RelayURL)+Path, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Authorization": []string{"Bearer " + c.Token},
			NonceHeader:     []string{c.Nonce},
			PortHeader:      []string{strconv.Itoa(c.Port)},
		},
	})
	if err != nil {
		if refusal := refusalFrom(resp); refusal != nil {
			return refusal
		}
		return err
	}

	// The hello is the only message read whole, so it is the only one that
	// needs a limit. NetConn below streams and disables the limit itself.
	ws.SetReadLimit(maxHelloBytes)
	kind, data, err := ws.Read(dialCtx)
	if err != nil {
		_ = ws.CloseNow()
		return fmt.Errorf("tunnel: waiting for relay hello: %w", err)
	}
	var hello Hello
	if kind != websocket.MessageText || json.Unmarshal(data, &hello) != nil || hello.URL == "" {
		_ = ws.CloseNow()
		return errors.New("tunnel: relay sent an invalid hello")
	}

	connCtx, stop := context.WithCancel(ctx)
	defer stop()
	conn := websocket.NetConn(connCtx, ws, websocket.MessageBinary)
	// This side is the yamux server: the relay opens a stream per browser
	// connection, and this side accepts them.
	session, err := yamux.Server(conn, MuxConfig())
	if err != nil {
		_ = conn.Close()
		return err
	}
	defer session.Close()
	// Cancelling ctx must unblock Accept below.
	go func() {
		select {
		case <-connCtx.Done():
		case <-session.CloseChan():
		}
		_ = session.Close()
	}()

	if c.OnReady != nil {
		c.OnReady(hello)
	}

	var streams sync.WaitGroup
	defer streams.Wait()
	for {
		stream, err := session.AcceptStream()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("tunnel: relay connection lost: %w", err)
		}
		streams.Add(1)
		go func() {
			defer streams.Done()
			c.serveStream(connCtx, stream)
		}()
	}
}

// serveStream connects one browser connection to the local port.
func (c *Client) serveStream(ctx context.Context, stream *yamux.Stream) {
	defer stream.Close()

	local, err := dialLoopback(ctx, c.Port)
	if err != nil {
		writeUnavailable(stream, c.Port)
		return
	}
	defer local.Close()
	pipe(stream, local)
}

// dialLoopback reaches the shared port on this machine only.
//
// Both loopback families are tried because dev servers disagree about which
// one "localhost" means: recent Node versions resolve it to ::1 first, so Vite
// and friends often listen on IPv6 alone, while plenty of servers bind only
// 127.0.0.1. A literal address, not the name, keeps /etc/hosts out of it.
func dialLoopback(ctx context.Context, port int) (net.Conn, error) {
	dialer := net.Dialer{Timeout: localDialTimeout}
	var firstErr error
	for _, host := range []string{"127.0.0.1", "::1"} {
		conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
		if err == nil {
			return conn, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return nil, firstErr
}

// writeUnavailable answers a stream with an explanatory error page.
//
// The relay is waiting to read an HTTP response from the stream, so replying
// in HTTP turns a bare "bad gateway" into a message that tells the person
// holding the link what is actually wrong.
func writeUnavailable(stream *yamux.Stream, port int) {
	body := fmt.Sprintf("Nothing is answering on localhost:%d on the machine sharing this link.\n"+
		"Start the server there, then reload this page.\n", port)
	_, _ = fmt.Fprintf(stream, "HTTP/1.1 502 Bad Gateway\r\n"+
		"Content-Type: text/plain; charset=utf-8\r\n"+
		"Content-Length: %d\r\n"+
		"Cache-Control: no-store\r\n"+
		"Connection: close\r\n\r\n%s", len(body), body)
	// Consume whatever request is still arriving. Closing with unread data
	// would leave the relay blocked on a full flow-control window until the
	// stream times out; "Connection: close" makes it hang up promptly instead.
	_ = stream.SetReadDeadline(time.Now().Add(unavailableDrain))
	_, _ = io.Copy(io.Discard, stream)
}

// pipe copies both directions until each side has finished sending.
//
// Each direction half-closes its destination when its source ends, rather
// than tearing everything down at the first EOF. HTTP needs that: a client may
// finish sending its request long before the response has been written.
func pipe(stream *yamux.Stream, local net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(local, stream)
		closeWrite(local)
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(stream, local)
		// yamux's Close is a half-close: it sends FIN but keeps reading.
		_ = stream.Close()
	}()
	wg.Wait()
}

func closeWrite(conn net.Conn) {
	if half, ok := conn.(interface{ CloseWrite() error }); ok {
		_ = half.CloseWrite()
		return
	}
	_ = conn.Close()
}

// refusalFrom turns a pre-upgrade HTTP rejection into a PermanentError when it
// is one that retrying cannot change.
func refusalFrom(resp *http.Response) error {
	if resp == nil {
		return nil
	}
	switch resp.StatusCode {
	case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden,
		http.StatusNotFound, http.StatusTooManyRequests:
	default:
		return nil
	}
	message := ""
	if resp.Body != nil {
		// The websocket library keeps only the first KiB of a failed body.
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		message = strings.TrimSpace(string(raw))
	}
	return &PermanentError{Status: resp.StatusCode, Message: message}
}

func newNonce() (string, error) {
	buf := make([]byte, 16)
	if _, err := crand.Read(buf); err != nil {
		return "", fmt.Errorf("tunnel: generate nonce: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func wsBase(raw string) string {
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
