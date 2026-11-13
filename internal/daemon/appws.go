package daemon

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/coder/websocket"
)

// The app protocol over WebSocket.
//
// Agentman for Mac opens the Unix socket. The cross-platform app runs its
// screens in a web view, which can open a WebSocket but not a socket, and on
// Windows it reaches an am serve running inside WSL, where a Unix socket
// cannot follow. So the same protocol is also served on the hook listener,
// which is loopback only: one envelope per line, exactly as on the socket,
// carried in text messages. Lines may be split across messages or share one,
// so a client reads the messages as one stream and splits it at newlines.
//
// Anyone who can open the socket can already drive every agent, so the same
// user is trusted here too. What this keeps out is everyone else on the
// machine and every web page in a browser: a connection needs the token,
// which is in a file only this user can read (AppEndpointFile), its Host must
// be loopback (no DNS rebinding), and a browser's Origin must be the app's.

// AppEndpointFile is where am serve writes the app endpoint's URL and token,
// in the config directory, readable by this user only.
const AppEndpointFile = "app.json"

// AppEndpoint is what AppEndpointFile holds.
type AppEndpoint struct {
	URL      string `json:"url"`
	Terminal string `json:"terminal"`
	Token    string `json:"token"`
}

// NewAppToken returns a fresh token for the app endpoint. A new one is made
// at every start, so a token that leaked dies with the daemon that issued it.
func NewAppToken() (string, error) {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return "", err
	}
	return hex.EncodeToString(secret), nil
}

// WriteAppEndpoint saves endpoint to AppEndpointFile in dir, 0600, replacing
// the file whole so an app never reads half of it.
func WriteAppEndpoint(dir string, endpoint AppEndpoint) (string, error) {
	data, err := json.MarshalIndent(endpoint, "", "  ")
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, AppEndpointFile)
	tmp, err := os.CreateTemp(dir, ".app-*.json")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	return path, os.Rename(tmp.Name(), path)
}

// AppGate decides whether a request may use the app endpoints.
type AppGate struct {
	Token string
}

// ErrAppForbidden is every refusal: which check failed is not the caller's
// business.
var ErrAppForbidden = errors.New("forbidden")

// Allow checks the token, the Host and the Origin.
func (g AppGate) Allow(r *http.Request) error {
	given := r.URL.Query().Get("token")
	if g.Token == "" || subtle.ConstantTimeCompare([]byte(given), []byte(g.Token)) != 1 {
		return ErrAppForbidden
	}
	if !loopbackHost(r.Host) {
		return ErrAppForbidden
	}
	if origin := r.Header.Get("Origin"); origin != "" && !AppOrigin(origin) {
		return ErrAppForbidden
	}
	return nil
}

// AppOrigin says whether a browser Origin is the app's own page: the Wails
// web view's (wails:// on macOS and Linux, wails.localhost on Windows), or a
// page served from loopback while the app's screens are being developed.
// Other pages cannot have the token anyway; this is a second lock.
func AppOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	switch u.Scheme {
	case "wails":
		return true
	case "http", "https":
		host := u.Hostname()
		return host == "wails.localhost" || host == "localhost" || isLoopbackIP(host)
	}
	return false
}

func loopbackHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	return host == "localhost" || isLoopbackIP(host)
}

func isLoopbackIP(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// ServeWebSocket serves one app over a WebSocket, as if it had opened the
// socket: the same requests, and every event the daemon sends. The caller
// has already checked AppGate. It answers 503 until Serve has started.
func (s *LocalServer) ServeWebSocket(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	ctx, handler, disconnected := s.ctx, s.handler, s.disconnected
	s.mu.Unlock()
	if handler == nil {
		http.Error(w, "am serve is starting", http.StatusServiceUnavailable)
		return
	}
	// The Origin was checked by AppGate, which knows the app's own.
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	// A frame may be as large as on the socket, and arrive as one message.
	c.SetReadLimit(maxLocalFrame + 1)
	connCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	conn := websocket.NetConn(connCtx, c, websocket.MessageText)
	s.serveConn(connCtx, conn, handler, disconnected)
}
