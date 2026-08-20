package relay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/lenajeremy/agentman/internal/protocol"
)

const (
	// maxFrame bounds a single message. Generous enough for a page of
	// scrollback with long tool output, small enough to bound memory per
	// connection.
	maxFrame = 4 << 20
	// App requests contain at most a 64 KiB message plus envelope metadata.
	// Daemon events may legitimately be much larger, but accepting 4 MiB from
	// an app only increases relay/daemon allocation exposure.
	maxAppFrame = 128 << 10
	// writeTimeout stops one unresponsive peer from pinning a goroutine.
	writeTimeout = 10 * time.Second
	// Each connection gets a short burst buffer. When it fills, the connection
	// is dropped and can recover through reconnect/history instead of retaining
	// an unbounded transcript in the relay.
	outboundQueueSize     = 16
	maxOutboundQueueBytes = 8 << 20
	// pingInterval keeps NAT and load-balancer idle timers from silently
	// dropping a connection that is simply quiet — common on mobile networks.
	pingInterval = 30 * time.Second
	// A hard ceiling turns connection floods into a bounded 503 response rather
	// than an out-of-memory crash. Normal deployments remain far below it.
	maxRelayConnections = 10_000
	// Daemon authentication is intentionally self-sovereign: any random token
	// creates an isolated account. Without a per-client ceiling that also means
	// one IP can occupy every global websocket slot with throwaway accounts.
	maxConnectionsPerClient = 128
	// Pairing requests are unauthenticated until their body has been read. Keep
	// both their lifetime and aggregate resource use bounded so slow clients
	// cannot pin an arbitrary number of HTTP connections and goroutines.
	maxConcurrentPairingRequests = 64
	pairingBodyReadTimeout       = 5 * time.Second
)

// Server is the relay's HTTP surface.
type Server struct {
	hub *Hub
	// secret signs device tokens. It is the only persistent configuration the
	// relay needs, and it is supplied by the environment, never stored here.
	secret string
	log    *slog.Logger
	// version is reported by /health for debugging deployments.
	version string

	// Failed pairing attempts are charged to the caller who made them, so one
	// person guessing badly never affects anyone else. The global ceiling is a
	// backstop against sheer volume from a large botnet, set high enough that
	// real pairing never meets it.
	perClientFailures *limiter
	globalFailures    *limiter
	// Pairing creation is authenticated only by possession of a daemon token,
	// and accounts are intentionally self-sovereign rather than registered.
	// That means any caller can mint an account of its own, so creation needs a
	// separate resource-abuse limit even though redeeming codes is protected
	// against guessing below.
	perClientPairings *limiter
	globalPairings    *limiter
	// pairingRequests limits the unauthenticated POST /pair handlers, including
	// requests that are still trickling in their bodies. pairingReadTimeout is
	// a field so focused tests can exercise the deadline without sleeping for
	// the production timeout.
	pairingRequests    chan struct{}
	pairingReadTimeout time.Duration
	// trustProxy says whether X-Forwarded-For may be believed. See clientKey.
	trustProxy        bool
	connections       chan struct{}
	connectionMu      sync.Mutex
	clientConnections map[string]int
}

// NewServer builds a relay.
//
// Set trustProxy only when something in front of this process overwrites
// X-Forwarded-For — a managed host like Railway, or a reverse proxy configured
// to do so. Setting it on a directly exposed relay lets every caller forge
// their own rate-limit bucket.
func NewServer(secret, version string, log *slog.Logger, trustProxy bool) *Server {
	if log == nil {
		log = slog.Default()
	}
	return &Server{
		hub:                NewHub(),
		secret:             secret,
		log:                log,
		version:            version,
		perClientFailures:  newLimiter(10, time.Minute),
		globalFailures:     newLimiter(2000, time.Minute),
		perClientPairings:  newLimiter(30, time.Minute),
		globalPairings:     newLimiter(5000, time.Minute),
		pairingRequests:    make(chan struct{}, maxConcurrentPairingRequests),
		pairingReadTimeout: pairingBodyReadTimeout,
		trustProxy:         trustProxy,
		connections:        make(chan struct{}, maxRelayConnections),
		clientConnections:  map[string]int{},
	}
}

// Handler returns the routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /ws/daemon", s.handleDaemon)
	mux.HandleFunc("GET /ws/app", s.handleApp)
	mux.HandleFunc("POST /pair", s.handlePair)
	mux.HandleFunc("POST /pair/code", s.handlePairCode)
	return withCORS(mux)
}

// withCORS allows browser clients to reach the HTTP endpoints.
//
// Permissive by design and safe here: every endpoint authenticates with a
// bearer token or a single-use pairing code, and the relay sets no cookies, so
// there is no ambient authority for another origin to ride on. Without this a
// web client cannot pair at all, since the browser blocks the cross-origin
// request before the relay ever sees it.
func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Access-Control-Max-Age", "86400")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// handlePairCode issues a pairing code to a daemon.
//
// This is HTTP rather than a websocket control frame on purpose: a daemon that
// had to open a socket to request a code would replace its own live connection
// (one daemon per account, newest wins), knocking the real daemon offline every
// time the user ran `am pair`.
func (s *Server) handlePairCode(w http.ResponseWriter, r *http.Request) {
	token := bearerHeader(r)
	if token == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing daemon token"})
		return
	}
	client := s.clientKey(r)
	now := time.Now()
	if !s.perClientPairings.allow("ip:"+client, now) || !s.globalPairings.allow("", now) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{
			"error": "too many pairing codes requested — wait a minute and try again",
		})
		return
	}
	// Derived, not looked up: the relay can issue a code for a daemon it has
	// never seen, with nothing stored on either side.
	account := DeriveAccount(token)

	secrets, err := s.hub.NewPairing(account)
	if err != nil {
		if errors.Is(err, ErrPairingCapacity) {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"error": "the relay is temporarily at pairing capacity — try again shortly",
			})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not generate a code"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"code":      secrets.Code,
		"token":     secrets.Token,
		"expiresAt": time.Now().Add(PairingCodeTTL).UnixMilli(),
	})
}

// handleHealth reports liveness and connection counts.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	daemons, apps, pending := s.hub.Stats()
	writeJSON(w, http.StatusOK, map[string]any{
		"status":          "ok",
		"version":         s.version,
		"daemons":         daemons,
		"apps":            apps,
		"pendingPairings": pending,
		"storage":         "none",
	})
}

// handlePair exchanges a pairing code for a device token.
func (s *Server) handlePair(w http.ResponseWriter, r *http.Request) {
	select {
	case s.pairingRequests <- struct{}{}:
		defer func() { <-s.pairingRequests }()
	default:
		w.Header().Set("Retry-After", "1")
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "the relay is temporarily at pairing capacity — try again shortly",
		})
		return
	}

	// A server-wide ReadTimeout would also expire long-lived websocket
	// connections. Scope the deadline to this unauthenticated request body and
	// clear it as soon as the complete JSON document has been consumed.
	controller := http.NewResponseController(w)
	if err := controller.SetReadDeadline(time.Now().Add(s.pairingReadTimeout)); err != nil {
		w.Header().Set("Connection", "close")
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "could not safely read pairing request",
		})
		return
	}

	var body struct {
		Code     string `json:"code"`
		DeviceID string `json:"deviceId"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	if err := decoder.Decode(&body); err != nil {
		w.Header().Set("Connection", "close")
		if isTimeout(err) {
			writeJSON(w, http.StatusRequestTimeout, map[string]string{"error": "pairing request timed out"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	// Reaching EOF proves a chunked client finished its body and prevents a
	// valid JSON prefix followed by an indefinitely slow suffix from escaping
	// the deadline. It also rejects multiple JSON documents.
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != io.EOF {
		w.Header().Set("Connection", "close")
		if isTimeout(err) {
			writeJSON(w, http.StatusRequestTimeout, map[string]string{"error": "pairing request timed out"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	_ = controller.SetReadDeadline(time.Time{})

	code := strings.ReplaceAll(strings.TrimSpace(body.Code), " ", "")

	// A scanned token carries 128 bits, so guessing it is not a threat and
	// there is nothing to rate limit. Recognising it by shape rather than by a
	// flag from the caller matters: otherwise anyone could claim this path and
	// skip the limits that protect the typed code.
	if IsPairingToken(code) {
		account, ok := s.hub.RedeemPairingCode(code)
		if !ok {
			writeJSON(w, http.StatusForbidden, map[string]string{
				"error": "this pairing has expired — scan a fresh code",
			})
			return
		}
		s.issueDeviceToken(w, account, body.DeviceID)
		return
	}

	client := "ip:" + s.clientKey(r)
	now := time.Now()
	if !s.perClientFailures.reserve(client, now) {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{
			"error": "too many pairing attempts — wait a minute and try a fresh code",
		})
		return
	}
	if !s.globalFailures.reserve("", now) {
		s.perClientFailures.release(client, now, false)
		writeJSON(w, http.StatusTooManyRequests, map[string]string{
			"error": "too many pairing attempts — wait a minute and try a fresh code",
		})
		return
	}

	account, ok := s.hub.RedeemPairingCode(code)
	s.perClientFailures.release(client, time.Now(), !ok)
	s.globalFailures.release("", time.Now(), !ok)
	if !ok {
		// Deliberately vague: distinguishing "wrong" from "expired" would help
		// someone probing the code space more than it helps a real user.
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "pairing code is not valid — generate a fresh one with `am pair`",
		})
		return
	}

	s.issueDeviceToken(w, account, body.DeviceID)
}

func isTimeout(err error) bool {
	var timeout interface{ Timeout() bool }
	return errors.As(err, &timeout) && timeout.Timeout()
}

// issueDeviceToken completes a successful pairing.
func (s *Server) issueDeviceToken(w http.ResponseWriter, account AccountID, deviceID string) {
	if deviceID == "" {
		deviceID = "device"
	}
	nonce, err := randomToken(12)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not issue token"})
		return
	}
	if len(deviceID) > 80 {
		deviceID = deviceID[:80]
	}
	token, err := MintDeviceToken(s.secret, account, deviceID+":"+nonce)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not issue token"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]string{"token": token})
}

/* ------------------------------- websockets ------------------------------ */

var (
	errOutboundClosed    = errors.New("relay: outbound connection closed")
	errOutboundQueueFull = errors.New("relay: outbound queue full")
)

// websocketWriter is the write side of coder/websocket.Conn. Keeping it
// narrow makes queue overflow and write failures deterministic to test without
// weakening the real socket integration tests.
type websocketWriter interface {
	Write(context.Context, websocket.MessageType, []byte) error
	Ping(context.Context) error
	CloseNow() error
}

// outboundFrame is one queued write. wrote, when non-nil, is closed once the
// writer has finished with the frame — successfully or not — so a caller that
// must get a final message out before hanging up can wait for it.
type outboundFrame struct {
	data  []byte
	wrote chan struct{}
}

// wsConn adapts a websocket to the hub's Conn interface. Send only enqueues;
// writeLoop is the sole owner of websocket writes and preserves frame order.
type wsConn struct {
	ws          websocketWriter
	outbound    chan outboundFrame
	done        chan struct{}
	close       sync.Once
	closeErr    error
	queueMu     sync.Mutex
	queuedBytes int
}

func newWSConn(ws websocketWriter) *wsConn {
	return newWSConnWithQueue(ws, outboundQueueSize)
}

func newWSConnWithQueue(ws websocketWriter, capacity int) *wsConn {
	c := &wsConn{
		ws:       ws,
		outbound: make(chan outboundFrame, capacity),
		done:     make(chan struct{}),
	}
	go c.writeLoop()
	return c
}

func (c *wsConn) Send(frame []byte) error {
	return c.enqueue(outboundFrame{data: frame})
}

// sendAndFlush queues a frame and waits for the writer to be done with it, so
// an immediately following close cannot discard it. Bounded by writeTimeout:
// a peer that has stopped reading must not pin the caller here.
func (c *wsConn) sendAndFlush(frame []byte) {
	wrote := make(chan struct{})
	if err := c.enqueue(outboundFrame{data: frame, wrote: wrote}); err != nil {
		return
	}
	timeout := time.NewTimer(writeTimeout)
	defer timeout.Stop()
	select {
	case <-wrote:
	case <-c.done:
	case <-timeout.C:
	}
}

func (c *wsConn) enqueue(frame outboundFrame) error {
	// Prefer the closed result even when buffer space happens to be available;
	// the second check covers a close racing the enqueue.
	select {
	case <-c.done:
		return errOutboundClosed
	default:
	}
	c.queueMu.Lock()
	if c.queuedBytes > maxOutboundQueueBytes-len(frame.data) {
		c.queueMu.Unlock()
		_ = c.Close()
		return errOutboundQueueFull
	}
	c.queuedBytes += len(frame.data)
	select {
	case c.outbound <- frame:
		c.queueMu.Unlock()
		select {
		case <-c.done:
			return errOutboundClosed
		default:
			return nil
		}
	default:
		c.queuedBytes -= len(frame.data)
		c.queueMu.Unlock()
		select {
		case <-c.done:
			return errOutboundClosed
		default:
			_ = c.Close()
			return errOutboundQueueFull
		}
	}
}

func (c *wsConn) Close() error {
	c.close.Do(func() {
		close(c.done)
		c.closeErr = c.ws.CloseNow()
	})
	return c.closeErr
}

func (c *wsConn) writeLoop() {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-c.done:
			return
		case frame := <-c.outbound:
			ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
			err := c.ws.Write(ctx, websocket.MessageText, frame.data)
			cancel()
			c.queueMu.Lock()
			c.queuedBytes -= len(frame.data)
			c.queueMu.Unlock()
			if frame.wrote != nil {
				close(frame.wrote)
			}
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

func (s *Server) accept(w http.ResponseWriter, r *http.Request) (*websocket.Conn, error) {
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// The app is a native client, not a browser page, so there is no
		// origin to check against.
		InsecureSkipVerify: true,
	})
	if err != nil {
		return nil, err
	}
	ws.SetReadLimit(maxFrame)
	return ws, nil
}

// handleDaemon serves a daemon connection.
func (s *Server) handleDaemon(w http.ResponseWriter, r *http.Request) {
	// A daemon can always set an Authorization header. Never accept its
	// long-lived root token in the query string, where proxies commonly log it.
	token := bearerHeader(r)
	if token == "" {
		http.Error(w, "missing bearer token", http.StatusUnauthorized)
		return
	}
	// No lookup: the account is derived from the token itself, so the relay
	// serves a daemon it has never seen without storing anything about it.
	account := DeriveAccount(token)
	client := s.clientKey(r)
	if !s.acquireConnection(w, client) {
		return
	}
	defer s.releaseConnection(client)

	ws, err := s.accept(w, r)
	if err != nil {
		return
	}
	// Keep the daemon at accept's maxFrame. It publishes history pages and
	// session snapshots that are legitimately far larger than any app request.
	conn := newWSConn(ws)
	defer conn.Close()

	// A reconnecting daemon replaces its previous socket, so a half-dead
	// connection from a suspended laptop cannot keep owning the account.
	if replaced := s.hub.AddDaemon(account, conn); replaced != nil {
		_ = replaced.Close()
	}
	s.log.Info("daemon connected", "account", account)

	s.notifyApps(account, protocol.Control{Type: protocol.CtlDaemonOnline, DaemonOnline: true})

	defer func() {
		// Replacing a stale socket closes its reader. That old handler must not
		// announce "offline" after the replacement already announced "online".
		if s.hub.RemoveDaemon(account, conn) {
			s.notifyApps(account, protocol.Control{
				Type:       protocol.CtlDaemonOffline,
				LastSeenAt: time.Now().UnixMilli(),
			})
			s.log.Info("daemon disconnected", "account", account)
		}
	}()

	s.pump(r.Context(), ws, conn, account, protocol.PeerDaemon, "")
}

// handleApp serves a phone connection.
func (s *Server) handleApp(w http.ResponseWriter, r *http.Request) {
	token := bearer(r)
	account, err := VerifyDeviceToken(s.secret, token)
	if err != nil {
		http.Error(w, "invalid device token", http.StatusUnauthorized)
		return
	}
	client := s.clientKey(r)
	if !s.acquireConnection(w, client) {
		return
	}
	defer s.releaseConnection(client)

	ws, err := s.accept(w, r)
	if err != nil {
		return
	}
	// An app only ever sends requests, so hold it to the smaller ceiling at the
	// socket itself. pump's length check stays as a second line of defence.
	ws.SetReadLimit(maxAppFrame)
	conn := newWSConn(ws)
	defer conn.Close()

	deviceID := fmt.Sprintf("%s-%d", account, time.Now().UnixNano())
	s.hub.AddApp(account, deviceID, conn)
	defer func() {
		s.hub.RemoveApp(account, deviceID)
		s.notifyDaemon(account, protocol.Control{
			Type: protocol.CtlAppDisconnected, DeviceID: deviceID,
		})
	}()

	online, lastSeen := s.hub.DaemonOnline(account)
	hello := protocol.Control{Type: protocol.CtlHello, DaemonOnline: online}
	if !online && !lastSeen.IsZero() {
		hello.LastSeenAt = lastSeen.UnixMilli()
	}
	_ = sendControl(conn, hello)

	s.pump(r.Context(), ws, conn, account, protocol.PeerApp, deviceID)
}

// pump reads frames from one side and routes them to the other until the
// connection closes.
func (s *Server) pump(
	ctx context.Context,
	ws *websocket.Conn,
	conn Conn,
	account AccountID,
	from protocol.Peer,
	deviceID string,
) {
	for {
		_, data, err := ws.Read(ctx)
		if err != nil {
			return
		}
		if from == protocol.PeerApp && len(data) > maxAppFrame {
			_ = conn.Close()
			return
		}

		var envelope protocol.Envelope
		if err := json.Unmarshal(data, &envelope); err != nil {
			continue // malformed frames are dropped, not fatal
		}
		if envelope.V != protocol.Version {
			// Say why, in the peer's own version, before hanging up. Without this
			// the close is indistinguishable from a flaky network: the app retries
			// forever showing "offline" and never learns it needs updating.
			// Ordinary Send only queues, and the close below would discard it.
			sendFinalControl(conn, envelope.ID, envelope.V, protocol.Control{
				Type:    protocol.CtlError,
				Message: "unsupported protocol version",
			})
			// Never leave an incompatible daemon registered as online. A policy
			// close makes the deferred hub removal publish offline and prevents the
			// much worse half-compatible state where forms render but answers fail.
			_ = ws.Close(websocket.StatusPolicyViolation, "unsupported protocol version")
			return
		}
		if !allowedDestination(from, envelope.To) {
			_ = sendControlReply(conn, envelope.ID, protocol.Control{
				Type: protocol.CtlError, Message: "frame is addressed to an invalid peer",
			})
			continue
		}

		switch envelope.To {
		case protocol.PeerRelay:
			s.handleControl(conn, account, from, envelope)

		case protocol.PeerDaemon:
			// The sender identity is relay-owned. Overwrite anything the app
			// supplied so one device cannot impersonate another subscription.
			envelope.From = deviceID
			forwarded, err := json.Marshal(envelope)
			if err != nil {
				continue
			}
			if err := s.hub.ToDaemon(account, forwarded); err != nil {
				// Answer immediately rather than buffering: the relay stores
				// nothing, and a prompt failure is more honest than a message
				// that silently arrives an hour later.
				online, lastSeen := s.hub.DaemonOnline(account)
				_ = online
				notice := protocol.Control{Type: protocol.CtlDaemonOffline}
				if !lastSeen.IsZero() {
					notice.LastSeenAt = lastSeen.UnixMilli()
				}
				_ = sendControlReply(conn, envelope.ID, notice)
			}

		case protocol.PeerApp:
			s.hub.ToApps(account, data)
		}
	}
}

func allowedDestination(from, to protocol.Peer) bool {
	switch from {
	case protocol.PeerDaemon:
		return to == protocol.PeerApp || to == protocol.PeerRelay
	case protocol.PeerApp:
		return to == protocol.PeerDaemon || to == protocol.PeerRelay
	default:
		return false
	}
}

// handleControl answers the small set of frames the relay owns.
func (s *Server) handleControl(conn Conn, account AccountID, from protocol.Peer, envelope protocol.Envelope) {
	var control protocol.Control
	if json.Unmarshal(envelope.Payload, &control) != nil {
		return
	}

	if control.Type == protocol.CtlPairRequest {
		// Only a daemon may mint a pairing code. Otherwise anyone holding a
		// device token could issue new ones and widen their own access.
		if from != protocol.PeerDaemon {
			_ = sendControlReply(conn, envelope.ID, protocol.Control{
				Type: protocol.CtlError, Message: "only a daemon can request pairing codes",
			})
			return
		}
		now := time.Now()
		if !s.perClientPairings.allow("account:"+string(account), now) ||
			!s.globalPairings.allow("", now) {
			_ = sendControlReply(conn, envelope.ID, protocol.Control{
				Type: protocol.CtlError, Message: "too many pairing codes requested",
			})
			return
		}
		secrets, err := s.hub.NewPairing(account)
		if err != nil {
			_ = sendControlReply(conn, envelope.ID, protocol.Control{
				Type: protocol.CtlError, Message: "could not generate a code",
			})
			return
		}
		_ = sendControlReply(conn, envelope.ID, protocol.Control{
			Type:      protocol.CtlPairCode,
			Code:      secrets.Code,
			Token:     secrets.Token,
			ExpiresAt: time.Now().Add(PairingCodeTTL).UnixMilli(),
		})
	}
}

func (s *Server) notifyApps(account AccountID, control protocol.Control) {
	envelope, err := protocol.NewEnvelope(newFrameID(), protocol.PeerApp, control)
	if err != nil {
		return
	}
	// Status frames are addressed to the app but originate here, so they are
	// marked as relay control for the client to distinguish.
	envelope.To = protocol.PeerRelay
	if frame, err := json.Marshal(envelope); err == nil {
		s.hub.ToApps(account, frame)
	}
}

func (s *Server) notifyDaemon(account AccountID, control protocol.Control) {
	envelope, err := protocol.NewEnvelope(newFrameID(), protocol.PeerRelay, control)
	if err != nil {
		return
	}
	if frame, err := json.Marshal(envelope); err == nil {
		_ = s.hub.ToDaemon(account, frame)
	}
}

func (s *Server) acquireConnection(w http.ResponseWriter, client string) bool {
	select {
	case s.connections <- struct{}{}:
	default:
		http.Error(w, "relay connection capacity reached", http.StatusServiceUnavailable)
		return false
	}

	s.connectionMu.Lock()
	defer s.connectionMu.Unlock()
	if s.clientConnections[client] >= maxConnectionsPerClient {
		<-s.connections
		http.Error(w, "client connection capacity reached", http.StatusTooManyRequests)
		return false
	}
	s.clientConnections[client]++
	return true
}

func (s *Server) releaseConnection(client string) {
	s.connectionMu.Lock()
	if remaining := s.clientConnections[client] - 1; remaining > 0 {
		s.clientConnections[client] = remaining
	} else {
		delete(s.clientConnections, client)
	}
	s.connectionMu.Unlock()
	<-s.connections
}

func sendControl(conn Conn, control protocol.Control) error {
	return sendControlReply(conn, "", control)
}

func sendControlReply(conn Conn, replyTo string, control protocol.Control) error {
	return sendControlReplyVersion(conn, replyTo, protocol.Version, control)
}

func controlReplyFrame(replyTo string, version int, control protocol.Control) ([]byte, error) {
	envelope, err := protocol.NewEnvelope(newFrameID(), protocol.PeerRelay, control)
	if err != nil {
		return nil, err
	}
	envelope.V = version
	envelope.ReplyTo = replyTo
	return json.Marshal(envelope)
}

// sendFinalControl delivers one last control frame and waits for it to reach
// the socket, for callers that are about to close. Connections that cannot
// guarantee a flush fall back to a plain queued send.
func sendFinalControl(conn Conn, replyTo string, version int, control protocol.Control) {
	frame, err := controlReplyFrame(replyTo, version, control)
	if err != nil {
		return
	}
	if flusher, ok := conn.(interface{ sendAndFlush([]byte) }); ok {
		flusher.sendAndFlush(frame)
		return
	}
	_ = conn.Send(frame)
}

func sendControlReplyVersion(
	conn Conn,
	replyTo string,
	version int,
	control protocol.Control,
) error {
	frame, err := controlReplyFrame(replyTo, version, control)
	if err != nil {
		return err
	}
	return conn.Send(frame)
}

// clientKey identifies the caller for rate limiting.
//
// Behind a proxy the socket address is the proxy for everyone, so without
// X-Forwarded-For every user shares one bucket and per-caller limiting does
// nothing. Railway overwrites that header with the true peer address —
// verified by sending two different forged values and watching both land in
// the same bucket — so reading it there is both safe and necessary.
//
// It is only safe when something overwrites it. A relay exposed directly, as
// `docker run -p 8080:8080` gives you, receives whatever the client typed, and
// an attacker varying it per request would mint unlimited buckets. Same
// binary, opposite trust environments, so the operator declares which one this
// is rather than the code guessing.
func (s *Server) clientKey(r *http.Request) string {
	if s.trustProxy {
		if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
			// Left-most entry is the original client; a trusted proxy appends
			// itself to the right.
			first, _, found := strings.Cut(forwarded, ",")
			if !found {
				first = forwarded
			}
			if key := normalizeIP(strings.TrimSpace(first)); key != "" {
				return key
			}
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if key := normalizeIP(host); key != "" {
		return key
	}
	return host
}

// normalizeIP collapses an address to the unit worth rate limiting.
//
// IPv6 is grouped by /64, the smallest block routinely assigned to a single
// subscriber. Keying on a full v6 address would hand one attacker 2^64 free
// buckets, which is not rate limiting at all.
func normalizeIP(raw string) string {
	addr, err := netip.ParseAddr(raw)
	if err != nil {
		return ""
	}
	if addr.Is4() || addr.Is4In6() {
		return addr.Unmap().String()
	}
	prefix, err := addr.Prefix(64)
	if err != nil {
		return addr.String()
	}
	return prefix.String()
}

func bearerHeader(r *http.Request) string {
	header := r.Header.Get("Authorization")
	if after, ok := strings.CutPrefix(header, "Bearer "); ok {
		return strings.TrimSpace(after)
	}
	return ""
}

func bearer(r *http.Request) string {
	if token := bearerHeader(r); token != "" {
		return token
	}
	// Native websocket clients can set headers, but browsers and some mobile
	// stacks cannot, so a query parameter is accepted as a fallback.
	return r.URL.Query().Get("token")
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
