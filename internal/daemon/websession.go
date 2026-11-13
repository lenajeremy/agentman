package daemon

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
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
//
// Sessions outlast a restart of am serve (an upgrade, a login), as the Mac
// app reconnects by itself: they are kept in web-sessions.json in the
// config folder, readable by the user only, as hashes, so the file alone
// signs nothing in. Each lasts 30 days; am web signs in again after.

// WebCookie names the web app's session cookie.
const WebCookie = "agentman_session"

// WebSessionsFile keeps the sessions across restarts, in the config folder.
const WebSessionsFile = "web-sessions.json"

const (
	webSessionLife  = 30 * 24 * time.Hour
	webCodeLife     = time.Minute
	maxWebCodes     = 16
	maxWebSessions  = 64
	webSecretLength = 32
)

// WebSessions keeps the web app's codes, in memory, and its sessions, by
// their hashes, in memory and (once KeepIn names it) in a file.
type WebSessions struct {
	mu    sync.Mutex
	codes map[string]time.Time
	// sessions holds each session's hash and when it was made.
	sessions map[string]time.Time
	file     string
	now      func() time.Time
}

type webSessionsOnDisk struct {
	Sessions map[string]int64 `json:"sessions"` // hash → made, unix ms
}

// KeepIn keeps the sessions in file: those there already, still fresh,
// are taken back; new ones are written to it.
func (w *WebSessions) KeepIn(file string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.file = file
	data, err := os.ReadFile(file)
	if err != nil {
		return
	}
	var kept webSessionsOnDisk
	if json.Unmarshal(data, &kept) != nil {
		return
	}
	now := w.now()
	for hash, made := range kept.Sessions {
		at := time.UnixMilli(made)
		if now.Sub(at) < webSessionLife && len(w.sessions) < maxWebSessions {
			w.sessions[hash] = at
		}
	}
}

// saveLocked writes the sessions to the file, if there is one, readable by
// the user only; w.mu is held.
func (w *WebSessions) saveLocked() {
	if w.file == "" {
		return
	}
	kept := webSessionsOnDisk{Sessions: make(map[string]int64, len(w.sessions))}
	for hash, made := range w.sessions {
		kept.Sessions[hash] = made.UnixMilli()
	}
	data, err := json.Marshal(kept)
	if err != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(w.file), ".web-sessions-*")
	if err != nil {
		return
	}
	_ = tmp.Chmod(0o600)
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if werr != nil || cerr != nil || os.Rename(tmp.Name(), w.file) != nil {
		_ = os.Remove(tmp.Name())
	}
}

func sessionHash(session string) string {
	sum := sha256.Sum256([]byte(session))
	return hex.EncodeToString(sum[:])
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
	now := w.now()
	for hash, made := range w.sessions {
		if now.Sub(made) >= webSessionLife {
			delete(w.sessions, hash)
		}
	}
	if len(w.sessions) >= maxWebSessions {
		oldest(w.sessions)
	}
	w.sessions[sessionHash(session)] = now
	w.saveLocked()
	return session, true
}

// Signed says whether a request carries a session, without looking at where
// it comes from: for pages, which a browser loads with no Origin.
func (w *WebSessions) Signed(r *http.Request) bool {
	cookie, err := r.Cookie(WebCookie)
	if err != nil || cookie.Value == "" {
		return false
	}
	hash := sessionHash(cookie.Value)
	w.mu.Lock()
	defer w.mu.Unlock()
	made, ok := w.sessions[hash]
	return ok && w.now().Sub(made) < webSessionLife
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
