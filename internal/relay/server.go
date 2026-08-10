package relay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
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
}

// NewServer builds a relay.
func NewServer(secret, version string, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	return &Server{hub: NewHub(), secret: secret, log: log, version: version}
}

// Handler returns the routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /ws/daemon", s.handleDaemon)
	mux.HandleFunc("GET /ws/app", s.handleApp)
	mux.HandleFunc("POST /pair", s.handlePair)
	mux.HandleFunc("POST /pair/code", s.handlePairCode)
	return mux
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

	code, err := s.hub.NewPairingCode(account)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not generate a code"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"code":      code,
		"expiresAt": time.Now().Add(PairingCodeTTL).UnixMilli(),
	})
}

// handleHealth reports liveness and connection counts.
//
// Counts only. The relay has no sessions, messages, or user records to expose
// here, which is the point — there is no endpoint that could leak them.
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
	account, ok := s.hub.RedeemPairingCode(code)
	if !ok {
		// Deliberately vague: distinguishing "wrong" from "expired" would help
		// someone probing the code space more than it helps a real user.
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "pairing code is not valid — generate a fresh one with `am pair`",
		})
		return
	}

	deviceID := body.DeviceID
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
