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
	// trustProxy says whether X-Forwarded-For may be believed. See clientKey.
	trustProxy bool
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
		trustProxy:        trustProxy,
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
	token := bearer(r)
	if token == "" {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "missing daemon token"})
		return
	}
	// Derived, not looked up: the relay can issue a code for a daemon it has
	// never seen, with nothing stored on either side.
	account := DeriveAccount(token)

	secrets, err := s.hub.NewPairing(account)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not generate a code"})
		return
	}
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

	client := s.clientKey(r)
	if s.perClientFailures.over(client) || s.globalFailures.over("") {
		writeJSON(w, http.StatusTooManyRequests, map[string]string{
			"error": "too many pairing attempts — wait a minute and try a fresh code",
		})
		return
	}

	account, ok := s.hub.RedeemPairingCode(code)
	if !ok {
		// Only failures are charged, and only to the caller who made them. A
		// user who types their code correctly never spends budget, and one
		// person guessing badly never affects anybody else.
		now := time.Now()
		s.perClientFailures.record(client, now)
		s.globalFailures.record("", now)

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
	token, err := MintDeviceToken(s.secret, account, deviceID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not issue token"})
		return
	}
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
	token := bearer(r)
	if token == "" {
		http.Error(w, "missing bearer token", http.StatusUnauthorized)
		return
	}
	// No lookup: the account is derived from the token itself, so the relay
	// serves a daemon it has never seen without storing anything about it.
	account := DeriveAccount(token)

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
		s.hub.RemoveDaemon(account, conn)
		s.notifyApps(account, protocol.Control{
			Type:       protocol.CtlDaemonOffline,
			LastSeenAt: time.Now().UnixMilli(),
		})
		s.log.Info("daemon disconnected", "account", account)
	}()

	s.pump(r.Context(), ws, conn, account, protocol.PeerDaemon)
}

// handleApp serves a phone connection.
func (s *Server) handleApp(w http.ResponseWriter, r *http.Request) {
	token := bearer(r)
	account, err := VerifyDeviceToken(s.secret, token)
	if err != nil {
		http.Error(w, "invalid device token", http.StatusUnauthorized)
		return
	}

	ws, err := s.accept(w, r)
	if err != nil {
		return
	}
	conn := &wsConn{ws: ws}

	deviceID := fmt.Sprintf("%s-%d", account, time.Now().UnixNano())
	s.hub.AddApp(account, deviceID, conn)
	defer s.hub.RemoveApp(account, deviceID)

	online, lastSeen := s.hub.DaemonOnline(account)
	hello := protocol.Control{Type: protocol.CtlHello, DaemonOnline: online}
	if !online && !lastSeen.IsZero() {
		hello.LastSeenAt = lastSeen.UnixMilli()
	}
	_ = sendControl(conn, hello)

	s.pump(r.Context(), ws, conn, account, protocol.PeerApp)
}

// pump reads frames from one side and routes them to the other until the
// connection closes.
func (s *Server) pump(ctx context.Context, ws *websocket.Conn, conn Conn, account AccountID, from protocol.Peer) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go keepAlive(ctx, ws)

	for {
		_, data, err := ws.Read(ctx)
		if err != nil {
			return
		}

		var envelope protocol.Envelope
		if err := json.Unmarshal(data, &envelope); err != nil {
			continue // malformed frames are dropped, not fatal
		}

		switch envelope.To {
		case protocol.PeerRelay:
			s.handleControl(conn, account, from, envelope)

		case protocol.PeerDaemon:
			if err := s.hub.ToDaemon(account, data); errors.Is(err, ErrNoPeer) {
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
		code, err := s.hub.NewPairingCode(account)
		if err != nil {
			_ = sendControlReply(conn, envelope.ID, protocol.Control{
				Type: protocol.CtlError, Message: "could not generate a code",
			})
			return
		}
		_ = sendControlReply(conn, envelope.ID, protocol.Control{
			Type:      protocol.CtlPairCode,
			Code:      code,
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

func keepAlive(ctx context.Context, ws *websocket.Conn) {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			pingCtx, cancel := context.WithTimeout(ctx, writeTimeout)
			err := ws.Ping(pingCtx)
			cancel()
			if err != nil {
				return
			}
		}
	}
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

func bearer(r *http.Request) string {
	header := r.Header.Get("Authorization")
	if after, ok := strings.CutPrefix(header, "Bearer "); ok {
		return strings.TrimSpace(after)
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
