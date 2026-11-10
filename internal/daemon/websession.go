package daemon

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"sync"
	"time"
)

// The web app's sign-in. `am web` asks am serve for a one-time code (proving
// it is this user by the app token in ~/.agentman/app.json), and opens the
// browser on /login?code=…, which swaps the code for a session cookie. The
// app token itself never reaches the browser, and a code is good once, for a
// minute, so one left in the browser's history opens nothing.
//
// A session cookie is accepted only from the app's own page: the request's
// Host is loopback and its Origin, when the browser sends one, is exactly
// that address. A cookie for 127.0.0.1 is also sent by pages on other ports
// of 127.0.0.1 (cookies ignore ports, and those count as the same site), so
// the Origin is what keeps a page from another local server out.

// WebCookie names the web app's session cookie.
const WebCookie = "agentman_session"

const (
	webCodeLife     = time.Minute
	maxWebCodes     = 16
	maxWebSessions  = 64
	webSecretLength = 32
)

// WebSessions keeps the web app's codes and sessions, in memory: a restart
// of am serve signs every browser out, and `am web` signs one back in.
type WebSessions struct {
	mu       sync.Mutex
	codes    map[string]time.Time
	sessions map[string]time.Time
	now      func() time.Time
}

func NewWebSessions() *WebSessions {
	return &WebSessions{codes: map[string]time.Time{}, sessions: map[string]time.Time{}, now: time.Now}
}

// NewCode returns a one-time sign-in code.
func (w *WebSessions) NewCode() (string, error) {
	code, err := webSecret()
	if err != nil {
		return "", err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	now := w.now()
	for c, made := range w.codes {
		if now.Sub(made) > webCodeLife {
			delete(w.codes, c)
		}
	}
	if len(w.codes) >= maxWebCodes {
		oldest(w.codes)
	}
	w.codes[code] = now
	return code, nil
}

// Login swaps a code, once and while it is fresh, for a session.
func (w *WebSessions) Login(code string) (string, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	var found string
	for c := range w.codes {
		if subtle.ConstantTimeCompare([]byte(c), []byte(code)) == 1 {
			found = c
		}
	}
	if found == "" {
		return "", false
	}
	made := w.codes[found]
	delete(w.codes, found)
	if w.now().Sub(made) > webCodeLife {
		return "", false
	}
	session, err := webSecret()
	if err != nil {
		return "", false
	}
	if len(w.sessions) >= maxWebSessions {
		oldest(w.sessions)
	}
	w.sessions[session] = w.now()
	return session, true
}

// Signed says whether a request carries a session, without looking at where
// it comes from: for pages, which a browser loads with no Origin.
func (w *WebSessions) Signed(r *http.Request) bool {
	cookie, err := r.Cookie(WebCookie)
	if err != nil || cookie.Value == "" {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for s := range w.sessions {
		if subtle.ConstantTimeCompare([]byte(s), []byte(cookie.Value)) == 1 {
			return true
		}
	}
	return false
}

// Allow says whether a request may use the app endpoints on a session: it
// carries one, its Host is loopback, and its Origin is the app's own page.
func (w *WebSessions) Allow(r *http.Request) bool {
	if !loopbackHost(r.Host) || !w.Signed(r) {
		return false
	}
	origin := r.Header.Get("Origin")
	return origin == "" || origin == "http://"+r.Host
}

// SetCookie hands the browser its session.
func SetWebCookie(rw http.ResponseWriter, session string) {
	http.SetCookie(rw, &http.Cookie{
		Name:     WebCookie,
		Value:    session,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
}

func webSecret() (string, error) {
	b := make([]byte, webSecretLength)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// oldest drops the oldest entry, to keep a map bounded.
func oldest(m map[string]time.Time) {
	var key string
	var at time.Time
	for k, t := range m {
		if key == "" || t.Before(at) {
			key, at = k, t
		}
	}
	delete(m, key)
}
