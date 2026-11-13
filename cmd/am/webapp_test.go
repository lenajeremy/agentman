package main

import (
	"archive/tar"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/apphost"
	"github.com/lenajeremy/agentman/internal/webapp"
)

// am web's path into the browser: a code for the app token, the code for a
// cookie once, and the page only for a browser that holds one.
func TestTheWebAppSignsInWithAOneTimeCode(t *testing.T) {
	g := newAppGateway("secret")
	do := func(h http.HandlerFunc, target string, cookie *http.Cookie, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, target, nil)
		r.Host = "127.0.0.1:8788"
		if cookie != nil {
			r.AddCookie(cookie)
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		rec := httptest.NewRecorder()
		h(rec, r)
		return rec
	}

	if rec := do(g.loginCode, "/app/login-code?token=guess", nil, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("a code for a wrong token: %d", rec.Code)
	}
	if rec := do(g.loginCode, "/app/login-code?token=secret", nil, "http://127.0.0.1:8788"); rec.Code != http.StatusForbidden {
		t.Fatalf("a code asked for from a page: %d; only am web may ask", rec.Code)
	}
	rec := do(g.loginCode, "/app/login-code?token=secret", nil, "")
	var answer struct{ Code string }
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &answer) != nil || answer.Code == "" {
		t.Fatalf("login code: %d %s", rec.Code, rec.Body)
	}

	if rec := do(g.page, "/", nil, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("the page before signing in: %d", rec.Code)
	}
	rec = do(g.login, "/login?code="+answer.Code, nil, "")
	cookies := rec.Result().Cookies()
	if rec.Code != http.StatusSeeOther || len(cookies) != 1 {
		t.Fatalf("login: %d, %d cookies", rec.Code, len(cookies))
	}
	if rec := do(g.login, "/login?code="+answer.Code, nil, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("the same code again: %d", rec.Code)
	}
	if rec := do(g.page, "/", cookies[0], ""); rec.Code == http.StatusUnauthorized || rec.Code == http.StatusForbidden {
		t.Fatalf("the page after signing in: %d", rec.Code)
	}
	// The app endpoints take the cookie from the app's own page only.
	if !g.web.Allow(withCookie(cookies[0], "http://127.0.0.1:8788")) {
		t.Fatal("the app's own page was refused")
	}
	if g.web.Allow(withCookie(cookies[0], "http://127.0.0.1:3000")) {
		t.Fatal("a page from another local server got in with the cookie")
	}
}

func withCookie(c *http.Cookie, origin string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/app", nil)
	r.Host = "127.0.0.1:8788"
	r.AddCookie(c)
	r.Header.Set("Origin", origin)
	return r
}

func TestTheHostCallsNeedASignedInApp(t *testing.T) {
	g := newAppGateway("secret")
	post := func(target, body, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
		r.Host = "127.0.0.1:8788"
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		rec := httptest.NewRecorder()
		g.host(rec, r)
		return rec
	}
	if rec := post("/app/host/Shell.Home", "[]", "https://example.com"); rec.Code != http.StatusForbidden {
		t.Fatalf("a call with no sign-in: %d", rec.Code)
	}
	rec := post("/app/host/Shell.Home?token=secret", "[]", "")
	var answer struct{ Result string }
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &answer) != nil || answer.Result == "" {
		t.Fatalf("Shell.Home: %d %s", rec.Code, rec.Body)
	}
	if rec := post("/app/host/Nope.Nothing?token=secret", "[]", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("an unknown method: %d", rec.Code)
	}
	rec = post("/app/host/Files.Read?token=secret", `["relative", 10]`, "")
	var failed struct{ Error string }
	if json.Unmarshal(rec.Body.Bytes(), &failed) != nil || failed.Error == "" {
		t.Fatalf("a failing call: %s", rec.Body)
	}
}

func TestTheEditorsPicturesAndWatchNeedASignedInApp(t *testing.T) {
	g := newAppGateway("secret")
	dir := t.TempDir()
	png := filepath.Join(dir, "shot.png")
	if err := os.WriteFile(png, []byte("\x89PNG\r\n\x1a\nrest"), 0o644); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(dir, "notes.txt")
	_ = os.WriteFile(secret, []byte("private"), 0o644)
	get := func(handler http.HandlerFunc, target string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, target, nil)
		r.Host = "127.0.0.1:8788"
		rec := httptest.NewRecorder()
		handler(rec, r)
		return rec
	}
	if rec := get(g.editorFile, "/__editor/file?path="+png); rec.Code != http.StatusForbidden {
		t.Fatalf("a picture with no sign-in: %d", rec.Code)
	}
	if rec := get(g.editorFile, "/__editor/file?token=secret&path="+png); rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("a picture: %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	if rec := get(g.editorFile, "/__editor/file?token=secret&path="+secret); rec.Code != http.StatusNotFound {
		t.Fatalf("a file that isn't a picture: %d", rec.Code)
	}
	if rec := get(g.watch, "/app/watch?root="+dir); rec.Code != http.StatusForbidden {
		t.Fatalf("a watch with no sign-in: %d", rec.Code)
	}
	if rec := get(g.watch, "/app/watch?token=secret&root="+filepath.Join(dir, "gone")); rec.Code != http.StatusNotFound {
		t.Fatalf("a watch of no folder: %d", rec.Code)
	}
}

func TestAWatchStreamsTheFolderChanges(t *testing.T) {
	g := newAppGateway("secret")
	dir := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(g.watch))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/app/watch?token=secret&root="+url.QueryEscape(dir), nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("Content-Type = %q", resp.Header.Get("Content-Type"))
	}
	lines := bufio.NewScanner(resp.Body)
	lines.Scan() // ": watching", once the watcher has looked
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}
	for lines.Scan() {
		line := lines.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var changed apphost.FilesChanged
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &changed); err != nil {
			t.Fatal(err)
		}
		if changed.Root != dir || len(changed.Changes) != 1 || changed.Changes[0].Type != 1 {
			t.Fatalf("changes = %+v", changed)
		}
		return
	}
	t.Fatal("the stream ended with nothing told")
}

// am web asks am serve for the web app's files first: only with the app
// token, never from a page; ready at once when they're here, fetched (and
// checked) the first time when they aren't.
func TestAmWebHasTheScreensFetchedFirst(t *testing.T) {
	t.Setenv("AGENTMAN_WEB_DIR", "")
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	page := "<!doctype html><title>Agentman</title>"
	_ = tw.WriteHeader(&tar.Header{Name: "index.html", Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(page))})
	_, _ = tw.Write([]byte(page))
	_ = tw.Close()
	_ = gz.Close()
	sum := sha256.Sum256(buf.Bytes())
	release := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(buf.Bytes()) }))
	defer release.Close()

	g := newAppGateway("secret")
	g.webStore = webapp.NewStore(filepath.Join(t.TempDir(), "web"), "0.47.0", hex.EncodeToString(sum[:]), release.URL)
	ask := func(target, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, target, nil)
		r.Host = "127.0.0.1:8788"
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		rec := httptest.NewRecorder()
		g.webReady(rec, r)
		return rec
	}
	if rec := ask("/app/web-ready?token=guess", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("a wrong token: %d", rec.Code)
	}
	if rec := ask("/app/web-ready?token=secret", "http://127.0.0.1:8788"); rec.Code != http.StatusForbidden {
		t.Fatalf("from a page: %d", rec.Code)
	}
	if rec := ask("/app/web-ready?token=secret", ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"fetched":true`) {
		t.Fatalf("first: %d %s", rec.Code, rec.Body.String())
	}
	if rec := ask("/app/web-ready?token=secret", ""); rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "fetched") {
		t.Fatalf("second: %d %s", rec.Code, rec.Body.String())
	}

	// A development build says how to get the screens instead.
	g.webStore = webapp.NewStore(t.TempDir(), "dev", "", "")
	if rec := ask("/app/web-ready?token=secret", ""); rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "development build") {
		t.Fatalf("dev: %d %s", rec.Code, rec.Body.String())
	}
}

// am web's side: an older am serve that doesn't know the question serves
// what it carries, as before; a refusal is said in am serve's words.
func TestAmWebReadsTheAnswer(t *testing.T) {
	answer := func(status int, body string) string {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/app/web-ready" || r.URL.Query().Get("token") != "secret" {
				t.Errorf("asked %s", r.URL)
			}
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		}))
		defer server.Close()
		if err := webReady(server.URL, "secret"); err != nil {
			return err.Error()
		}
		return ""
	}
	if got := answer(http.StatusOK, `{"ready":true,"fetched":true}`); got != "" {
		t.Fatal(got)
	}
	if got := answer(http.StatusNotFound, "This am was built without the web app."); got != "" {
		t.Fatalf("an older am serve: %s", got)
	}
	if got := answer(http.StatusServiceUnavailable, `{"error":"the web app couldn't be downloaded: offline"}`); got != "the web app couldn't be downloaded: offline" {
		t.Fatalf("refused: %q", got)
	}
	if got := answer(http.StatusInternalServerError, "boom"); !strings.Contains(got, "500") {
		t.Fatalf("no words: %q", got)
	}
}
