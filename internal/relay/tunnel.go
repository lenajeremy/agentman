package relay

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"

	"github.com/lenajeremy/agentman/internal/tunnel"
)

// Previews: public links to a port on a user's machine.
//
// A request for https://<id>.<preview-domain>/ is matched to the tunnel whose
// link that is, and reverse-proxied down a fresh yamux stream on that tunnel's
// websocket. The link itself is the only credential, by design: anyone holding
// it can open the app, which is what makes it shareable. That puts the weight
// on the link being unguessable and short-lived — it is derived with the relay
// secret, and it stops working the moment `am expose` exits.

const (
	// tunnelIDLength is 26 base32 characters, 130 bits: beyond guessing, and
	// still a single DNS label (at most 63 characters).
	tunnelIDLength = 26
	// Bounds on the client's nonce. It is not secret, only unique per process.
	minTunnelNonce = 16
	maxTunnelNonce = 64
	// Enough for a frontend, an API and a few more ports; far short of letting
	// one account use the relay as general hosting.
	maxTunnelsPerAccount = 8
	// Each browser connection holds one stream. The cap keeps one busy link
	// from monopolising a tunnel; further requests wait for a free connection.
	maxStreamsPerTunnel = 64
	// A request rate far above any person browsing a dev server, low enough to
	// stop a shared link being hammered through the relay.
	previewRequestsPerMinute = 1200
	// Dev servers can take a long time to answer the first request while they
	// compile, so this is generous. It still ends a request the local server
	// has abandoned.
	previewResponseTimeout = 2 * time.Minute
)

var tunnelIDEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// previewOrigin is where links live, parsed from AGENTMAN_PREVIEW_ORIGIN.
type previewOrigin struct {
	scheme string // "https", or "http" for local development only
	domain string // lowercased hostname suffix, e.g. "preview.agentman.dev"
	port   string // empty for the scheme's default port
}

// parsePreviewOrigin validates the operator's preview origin.
//
// Links should live on a registrable domain of their own, not under the
// relay's: pages on one link can then never read another link's cookies or the
// relay's. That cannot be checked from here, so it is documented instead.
func parsePreviewOrigin(raw string) (*previewOrigin, error) {
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(raw), "/"))
	if err != nil || parsed.Hostname() == "" {
		return nil, fmt.Errorf("invalid preview origin %q", raw)
	}
	if parsed.Path != "" || parsed.RawQuery != "" || parsed.User != nil || parsed.Fragment != "" {
		return nil, fmt.Errorf("preview origin must contain only a scheme, host and optional port")
	}
	domain := strings.ToLower(parsed.Hostname())
	switch parsed.Scheme {
	case "https":
	case "http":
		// Links are handed to other people and carry whatever the app sends,
		// so plaintext is allowed only for a relay running on this machine.
		if domain != "localhost" {
			return nil, fmt.Errorf("preview origin must use https (http is allowed only for localhost)")
		}
	default:
		return nil, fmt.Errorf("preview origin must use https")
	}
	return &previewOrigin{scheme: parsed.Scheme, domain: domain, port: parsed.Port()}, nil
}

// linkURL is the public address for one tunnel.
func (o *previewOrigin) linkURL(id string) string {
	host := id + "." + o.domain
	if o.port != "" {
		host = net.JoinHostPort(host, o.port)
	}
	return o.scheme + "://" + host
}

// match reports whether host belongs to the preview domain and, if it names a
// well-formed link, which one.
func (o *previewOrigin) match(host string) (id string, isPreview bool) {
	hostname := strings.ToLower(host)
	if h, _, err := net.SplitHostPort(hostname); err == nil {
		hostname = h
	}
	label, found := strings.CutSuffix(hostname, "."+o.domain)
	if !found {
		return "", false
	}
	if !validTunnelID(label) {
		return "", true
	}
	return label, true
}

// deriveTunnelID computes a link from the account and the client's nonce.
//
// Derived rather than stored, like accounts and device tokens: a client that
// reconnects with the same nonce gets the same link, even from a relay that
// restarted in between. Keyed with the relay secret, so nobody without it can
// predict a link from an account and nonce, and bound to the account, so
// reusing someone else's nonce yields a different link.
func deriveTunnelID(secret string, account AccountID, nonce string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("agentman-tunnel\x00" + string(account) + "\x00" + nonce))
	return strings.ToLower(tunnelIDEncoding.EncodeToString(mac.Sum(nil)))[:tunnelIDLength]
}

func validTunnelID(id string) bool {
	if len(id) != tunnelIDLength {
		return false
	}
	for _, c := range id {
		if (c < 'a' || c > 'z') && (c < '2' || c > '7') {
			return false
		}
	}
	return true
}

func validTunnelNonce(nonce string) bool {
	if len(nonce) < minTunnelNonce || len(nonce) > maxTunnelNonce {
		return false
	}
	for _, c := range nonce {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

/* ------------------------------- registry -------------------------------- */

// liveTunnel is one connected `am expose`.
type liveTunnel struct {
	id        string
	account   AccountID
	session   *yamux.Session
	transport *http.Transport
	proxy     *httputil.ReverseProxy
}

func (t *liveTunnel) close() {
	_ = t.session.Close()
	t.transport.CloseIdleConnections()
}

// tunnelRegistry maps links to live tunnels. Like the hub, it holds only
// what is connected right now.
type tunnelRegistry struct {
	mu         sync.Mutex
	byID       map[string]*liveTunnel
	perAccount map[AccountID]int
}

func newTunnelRegistry() *tunnelRegistry {
	return &tunnelRegistry{byID: map[string]*liveTunnel{}, perAccount: map[AccountID]int{}}
}

var errTooManyTunnels = errors.New("relay: too many tunnels for this account")

// hasRoom says whether add would succeed, so a refusal can be a plain HTTP
// error before the websocket upgrade. add re-checks, since two connections can
// race between the two calls.
func (r *tunnelRegistry) hasRoom(account AccountID, id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.byID[id] != nil || r.perAccount[account] < maxTunnelsPerAccount
}

// add registers a tunnel. A reconnect for the same link replaces the old
// connection, which may be a half-dead socket from a laptop that went to sleep.
func (r *tunnelRegistry) add(t *liveTunnel) (replaced *liveTunnel, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if old := r.byID[t.id]; old != nil {
		r.byID[t.id] = t
		return old, nil
	}
	if r.perAccount[t.account] >= maxTunnelsPerAccount {
		return nil, errTooManyTunnels
	}
	r.byID[t.id] = t
	r.perAccount[t.account]++
	return nil, nil
}

// remove unregisters t only if it is still the current tunnel for its link,
// so a replaced connection closing late cannot take down its replacement.
func (r *tunnelRegistry) remove(t *liveTunnel) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.byID[t.id] != t {
		return
	}
	delete(r.byID, t.id)
	if remaining := r.perAccount[t.account] - 1; remaining > 0 {
		r.perAccount[t.account] = remaining
	} else {
		delete(r.perAccount, t.account)
	}
}

func (r *tunnelRegistry) get(id string) *liveTunnel {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.byID[id]
}

/* -------------------------------- handlers ------------------------------- */

// EnablePreviews turns on public links under origin, for example
// "https://preview.agentman.dev". The domain needs a wildcard DNS record and a
// wildcard certificate pointing at this relay.
func (s *Server) EnablePreviews(origin string) error {
	parsed, err := parsePreviewOrigin(origin)
	if err != nil {
		return err
	}
	s.previews = parsed
	return nil
}

// handleTunnel accepts an `am expose` connection.
func (s *Server) handleTunnel(w http.ResponseWriter, r *http.Request) {
	if s.previews == nil {
		http.Error(w, "this relay does not offer preview links", http.StatusNotFound)
		return
	}
	// Same rule as the daemon: the root token only ever travels in a header.
	token := bearerHeader(r)
	if token == "" {
		http.Error(w, "missing bearer token", http.StatusUnauthorized)
		return
	}
	nonce := r.Header.Get(tunnel.NonceHeader)
	if !validTunnelNonce(nonce) {
		http.Error(w, "invalid tunnel nonce", http.StatusBadRequest)
		return
	}
	port, err := strconv.Atoi(r.Header.Get(tunnel.PortHeader))
	if err != nil || port < 1 || port > 65535 {
		http.Error(w, "invalid tunnel port", http.StatusBadRequest)
		return
	}

	account := DeriveAccount(token)
	id := deriveTunnelID(s.secret, account, nonce)
	if !s.tunnels.hasRoom(account, id) {
		http.Error(w, fmt.Sprintf("this account is already sharing %d ports — stop one first",
			maxTunnelsPerAccount), http.StatusTooManyRequests)
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
	link := s.previews.linkURL(id)

	// The hello goes out before any yamux traffic, so the client can read it
	// as a plain text message and only then switch the socket to binary.
	hello, err := json.Marshal(tunnel.Hello{ID: id, URL: link})
	if err != nil {
		_ = ws.CloseNow()
		return
	}
	writeCtx, cancel := context.WithTimeout(r.Context(), writeTimeout)
	err = ws.Write(writeCtx, websocket.MessageText, hello)
	cancel()
	if err != nil {
		_ = ws.CloseNow()
		return
	}

	ctx, stop := context.WithCancel(r.Context())
	defer stop()
	conn := websocket.NetConn(ctx, ws, websocket.MessageBinary)
	// The relay is the yamux client because it is the side that opens
	// streams: one per browser connection.
	session, err := yamux.Client(conn, tunnel.MuxConfig())
	if err != nil {
		_ = conn.Close()
		return
	}

	live := newLiveTunnel(id, account, port, link, session)
	replaced, err := s.tunnels.add(live)
	if err != nil {
		// Lost the race hasRoom could not rule out.
		live.close()
		return
	}
	if replaced != nil {
		replaced.close()
	}
	defer func() {
		s.tunnels.remove(live)
		live.close()
	}()
	s.log.Info("tunnel opened")

	select {
	case <-session.CloseChan():
	case <-ctx.Done():
	}
	s.log.Info("tunnel closed")
}

// newLiveTunnel builds the reverse proxy that carries browser traffic down
// the tunnel's session.
func newLiveTunnel(id string, account AccountID, port int, link string, session *yamux.Session) *liveTunnel {
	local := net.JoinHostPort("localhost", strconv.Itoa(port))
	transport := &http.Transport{
		// Every "connection" the proxy makes is a new stream on the one
		// websocket. The address is ignored: the far end decides where a
		// stream goes, and it only ever goes to the shared port.
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			return session.OpenStream()
		},
		MaxConnsPerHost:       maxStreamsPerTunnel,
		MaxIdleConnsPerHost:   maxStreamsPerTunnel / 4,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: previewResponseTimeout,
		// Pass compressed bodies through untouched rather than re-encoding.
		DisableCompression: true,
	}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			// Rewrite, unlike Director, drops any X-Forwarded-* the visitor
			// sent before this sets them, so the app cannot be fed a forged
			// client address.
			pr.SetXForwarded()
			pr.Out.URL.Scheme = "http"
			pr.Out.URL.Host = local
			// Dev servers commonly reject unfamiliar Host headers (Vite's
			// allowedHosts, Django's ALLOWED_HOSTS), and they already accept
			// the name they are listening under.
			pr.Out.Host = local
			presentLocalOrigin(pr.Out.Header, link, "http://"+local)
		},
		Transport: transport,
		ModifyResponse: func(resp *http.Response) error {
			setPreviewHeaders(resp.Header, link)
			rewriteLocalRedirect(resp.Header, port, link)
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			http.Error(w, "The machine sharing this link did not respond. "+
				"It may be offline, or `am expose` may have stopped.", http.StatusBadGateway)
		},
	}
	return &liveTunnel{id: id, account: account, session: session, transport: transport, proxy: proxy}
}

// setPreviewHeaders adds the relay's own headers to every link response.
func setPreviewHeaders(header http.Header, link string) {
	// Links are shared by hand, never meant to be found by search.
	header.Set("X-Robots-Tag", "noindex, nofollow")
	// Browsers enforce HTTPS on some endings (.app, .dev) but not most, so
	// the relay asks for it itself: after one visit, a later http:// copy of
	// the link is upgraded before anything is sent in the clear. It replaces
	// whatever the dev server sent, since a local app has no business setting
	// transport policy for a public domain.
	if strings.HasPrefix(link, "https://") {
		header.Set("Strict-Transport-Security", "max-age=31536000")
	}
}

// presentLocalOrigin makes the page's own requests look same-origin to the
// dev server.
//
// Browsers send Origin (and Referer) naming the link, and dev servers check it:
// Expo's Metro answers "Unauthorized request" to every bundle and asset fetch
// from an unfamiliar origin, and Vite does the same for its hot-reload socket.
// Only the link's own origin is rewritten. A request from any other site keeps
// its real Origin, so the dev server can still refuse cross-site requests,
// which is what those checks exist to do.
func presentLocalOrigin(header http.Header, link, local string) {
	if header.Get("Origin") == link {
		header.Set("Origin", local)
	}
	if referer := header.Get("Referer"); referer == link || strings.HasPrefix(referer, link+"/") {
		header.Set("Referer", local+strings.TrimPrefix(referer, link))
	}
}

// rewriteLocalRedirect points a redirect to http://localhost:<port>/... at the
// public link instead. Apps that build absolute URLs from their own listen
// address would otherwise send the visitor to their own device's localhost.
func rewriteLocalRedirect(header http.Header, port int, link string) {
	location := header.Get("Location")
	if location == "" {
		return
	}
	target, err := url.Parse(location)
	if err != nil || target.Host == "" {
		return // relative redirects already resolve against the link
	}
	host := target.Hostname()
	loopback := strings.EqualFold(host, "localhost") || host == "127.0.0.1" || host == "::1"
	if !loopback || target.Port() != strconv.Itoa(port) {
		return
	}
	public, err := url.Parse(link)
	if err != nil {
		return
	}
	target.Scheme = public.Scheme
	target.Host = public.Host
	header.Set("Location", target.String())
}

// servePreview answers a request for a link.
func (s *Server) servePreview(w http.ResponseWriter, r *http.Request, id string) {
	live := s.tunnels.get(id)
	if id == "" || live == nil {
		w.Header().Set("Cache-Control", "no-store")
		setPreviewHeaders(w.Header(), s.previews.linkURL(id))
		http.Error(w, "This preview link is not active. The person who shared it may have "+
			"stopped sharing, or their machine is offline.", http.StatusNotFound)
		return
	}
	if !s.previewRequests.allow(id, time.Now()) {
		http.Error(w, "Too many requests to this preview — slow down.", http.StatusTooManyRequests)
		return
	}
	live.proxy.ServeHTTP(w, r)
}
