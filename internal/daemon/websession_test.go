package daemon

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAWebCodeSignsInOnceWhileFresh(t *testing.T) {
	w := NewWebSessions()
	code, err := w.NewCode()
	if err != nil {
		t.Fatal(err)
	}
	session, ok := w.Login(code)
	if !ok || session == "" || session == code {
		t.Fatalf("Login = %q, %v; want a new session", session, ok)
	}
	if _, ok := w.Login(code); ok {
		t.Fatal("a code signed in twice")
	}

	stale, _ := w.NewCode()
	w.now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	if _, ok := w.Login(stale); ok {
		t.Fatal("a code two minutes old signed in")
	}
	if _, ok := w.Login("guess"); ok {
		t.Fatal("a made-up code signed in")
	}
}

// The cookie counts only from the app's own page: a page on another local
// port gets the same cookie from the browser and must still be refused.
func TestASessionIsAcceptedOnlyFromTheAppsOwnPage(t *testing.T) {
	w := NewWebSessions()
	code, _ := w.NewCode()
	session, _ := w.Login(code)

	request := func(host, origin, cookie string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/app", nil)
		r.Host = host
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: WebCookie, Value: cookie})
		}
		return r
	}
	cases := []struct {
		name               string
		host, origin, cook string
		ok                 bool
	}{
		{"the app's page", "127.0.0.1:8788", "http://127.0.0.1:8788", session, true},
		{"the app's page by name", "localhost:8788", "http://localhost:8788", session, true},
		{"no Origin (a page load)", "127.0.0.1:8788", "", session, true},
		{"another local server's page", "127.0.0.1:8788", "http://127.0.0.1:3000", session, false},
		{"a web site", "127.0.0.1:8788", "https://example.com", session, false},
		{"DNS rebinding", "evil.example:8788", "http://evil.example:8788", session, false},
		{"no cookie", "127.0.0.1:8788", "http://127.0.0.1:8788", "", false},
		{"a made-up cookie", "127.0.0.1:8788", "http://127.0.0.1:8788", "guess", false},
	}
	for _, c := range cases {
		if got := w.Allow(request(c.host, c.origin, c.cook)); got != c.ok {
			t.Errorf("%s: Allow = %v, want %v", c.name, got, c.ok)
		}
	}
}

func TestTheSessionCookieIsHttpOnlyAndStrict(t *testing.T) {
	rec := httptest.NewRecorder()
	SetWebCookie(rec, "s")
	cookie := rec.Result().Cookies()[0]
	if cookie.Name != WebCookie || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" {
		t.Fatalf("cookie = %+v", cookie)
	}
}

func TestASessionOutlastsARestartButNotAMonth(t *testing.T) {
	file := filepath.Join(t.TempDir(), WebSessionsFile)
	first := NewWebSessions()
	first.KeepIn(file)
	code, _ := first.NewCode()
	session, _ := first.Login(code)

	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("the file is %v; want it readable by the user only", info.Mode().Perm())
	}
	data, _ := os.ReadFile(file)
	if strings.Contains(string(data), session) {
		t.Fatal("the file holds the session itself")
	}

	signed := func(w *WebSessions) bool {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.AddCookie(&http.Cookie{Name: WebCookie, Value: session})
		return w.Signed(r)
	}
	again := NewWebSessions()
	again.KeepIn(file)
	if !signed(again) {
		t.Fatal("a restarted am serve forgot the session")
	}
	again.now = func() time.Time { return time.Now().Add(31 * 24 * time.Hour) }
	if signed(again) {
		t.Fatal("a session a month old still signs in")
	}
	later := NewWebSessions()
	later.now = again.now
	later.KeepIn(file)
	if len(later.sessions) != 0 {
		t.Fatal("a month-old session was taken back from the file")
	}
}
