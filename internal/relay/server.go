package relay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	// writeTimeout stops one unresponsive peer from pinning a goroutine.
	writeTimeout = 10 * time.Second
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
		hub:               NewHub(),
		secret:            secret,
		log:               log,
		version:           version,
		perClientFailures: newLimiter(10, time.Minute),
		globalFailures:    newLimiter(2000, time.Minute),
		perClientPairings: newLimiter(30, time.Minute),
		globalPairings:    newLimiter(5000, time.Minute),
		trustProxy:        trustProxy,
		connections:       make(chan struct{}, maxRelayConnections),
		clientConnections: map[string]int{},
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
	var body struct {
		Code     string `json:"code"`
		DeviceID string `json:"deviceId"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}

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

// wsConn adapts a websocket to the hub's Conn interface.
type wsConn struct {
	ws *websocket.Conn
	// Writes are serialized: the websocket library allows only one writer at a
	// time, and both the hub fan-out and the ping loop can write concurrently.
	mu sync.Mutex
}

func (c *wsConn) Send(frame []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	return c.ws.Write(ctx, websocket.MessageText, frame)
}

func (c *wsConn) Close() error {
	return c.ws.Close(websocket.StatusNormalClosure, "")
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
	conn := &wsConn{ws: ws}

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
	conn := &wsConn{ws: ws}

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
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		if keepAlive(ctx, ws) != nil {
			cancel()
		}
	}()

	for {
		_, data, err := ws.Read(ctx)
		if err != nil {
			return
		}

		var envelope protocol.Envelope
		if err := json.Unmarshal(data, &envelope); err != nil {
			continue // malformed frames are dropped, not fatal
		}
		if envelope.V != protocol.Version {
			_ = sendControlReply(conn, envelope.ID, protocol.Control{
				Type: protocol.CtlError, Message: "unsupported protocol version",
			})
			continue
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
			if err := s.hub.ToDaemon(account, forwarded); errors.Is(err, ErrNoPeer) {
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

func keepAlive(ctx context.Context, ws *websocket.Conn) error {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			pingCtx, cancel := context.WithTimeout(ctx, writeTimeout)
			err := ws.Ping(pingCtx)
			cancel()
			if err != nil {
				return err
			}
		}
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
	envelope, err := protocol.NewEnvelope(newFrameID(), protocol.PeerRelay, control)
	if err != nil {
		return err
	}
	envelope.ReplyTo = replyTo
	frame, err := json.Marshal(envelope)
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
