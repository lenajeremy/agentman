package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/lenajeremy/agentman/internal/apphost"
	"github.com/lenajeremy/agentman/internal/daemon"
	"github.com/lenajeremy/agentman/internal/hook"
	"github.com/lenajeremy/agentman/internal/webapp"
)

// The web app: am serve serves it on its loopback listener, and `am web`
// opens it in the browser, signed in (internal/daemon's websession.go).
//
//   GET /app/web-ready?token=<app token>    the web app's files, fetched the
//                                           first time (internal/webapp), for am web
//   GET /app/login-code?token=<app token>   a one-time code, for am web
//   GET /login?code=<code>                  the code for a session cookie
//   GET /…                                  the app, to a signed-in browser
//   POST /app/host/<name>                   what a page can't do (internal/apphost)
//   GET /app/watch?root=<folder>            the editor's folder, its changes streamed
//   GET /__editor/file?path=<picture>       a picture from the disk, for the editor

// loginCode answers am web with a one-time code. Only the app token proves
// the caller: a browser is never given it.
func (g *appGateway) loginCode(w http.ResponseWriter, r *http.Request) {
	if g.gate.Allow(r) != nil || r.Header.Get("Origin") != "" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	code, err := g.web.NewCode()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"code": code})
}

// webReady answers am web once the web app's files are here: at once if
// they are, or after fetching this version's (internal/webapp/fetch.go),
// about 9 MB, the first time. Only the app token proves the caller.
func (g *appGateway) webReady(w http.ResponseWriter, r *http.Request) {
	if g.gate.Allow(r) != nil || r.Header.Get("Origin") != "" {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if webapp.Ready(g.webStore) {
		_ = json.NewEncoder(w).Encode(map[string]bool{"ready": true})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	_, fetched, err := g.webStore.Ensure(ctx)
	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]bool{"ready": true, "fetched": fetched})
}

// login swaps the code for a session and goes to the app.
func (g *appGateway) login(w http.ResponseWriter, r *http.Request) {
	session, ok := g.web.Login(r.URL.Query().Get("code"))
	if !ok {
		webPage(w, http.StatusForbidden, "This sign-in link has expired",
			"Sign-in links work once, for a minute. Run <code>am web</code> again to open Agentman.")
		return
	}
	daemon.SetWebCookie(w, session)
	// next, a path on this same address (/?agent=<id>), is where to land;
	// anything else goes to the start, so the link can't send the browser
	// elsewhere.
	next := r.URL.Query().Get("next")
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.Contains(next, "\\") {
		next = "/"
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

// page serves the app's files. The page itself goes only to a signed-in
// browser: anyone else is told how to open it.
func (g *appGateway) page(w http.ResponseWriter, r *http.Request) {
	if !webappHostOK(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if (r.URL.Path == "/" || r.URL.Path == "/index.html") && !g.web.Signed(r) {
		webPage(w, http.StatusUnauthorized, "Open Agentman with am web",
			"Run <code>am web</code> in a terminal: it opens Agentman here, signed in.")
		return
	}
	webapp.Handler(g.webStore).ServeHTTP(w, r)
}

// hostMethods are the calls /app/host/ answers.
var hostMethods = apphost.Methods()

// host answers the screens' calls to the computer: POST /app/host/<name>
// with the arguments as a JSON array, answered {"result": …} or
// {"error": "…"}. The same sign-in as the app endpoints guards it.
func (g *appGateway) host(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !g.signedIn(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	method, ok := hostMethods[strings.TrimPrefix(r.URL.Path, "/app/host/")]
	if !ok {
		http.Error(w, apphost.ErrNoMethod.Error(), http.StatusNotFound)
		return
	}
	var args []json.RawMessage
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<20)).Decode(&args); err != nil {
		http.Error(w, "the arguments aren't a JSON array", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	result, err := method(args)
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"result": result})
}

// signedIn says whether a request comes from the app: the desktop shell's
// token, or the browser's session from the app's own page.
func (g *appGateway) signedIn(r *http.Request) bool {
	return g.gate.Allow(r) == nil || g.web.Allow(r)
}

// editorFile serves the editor a picture from the disk (its image viewer,
// its Markdown preview), as the desktop shell's /__editor/file did.
func (g *appGateway) editorFile(w http.ResponseWriter, r *http.Request) {
	if !g.signedIn(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	apphost.ServeEditorFile(w, r)
}

// watch streams an editor tab's folder changes, as server-sent events,
// for as long as the tab keeps the stream open: what the shell's watcher
// told its windows ("agentman:editor-files").
func (g *appGateway) watch(w http.ResponseWriter, r *http.Request) {
	if !g.signedIn(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	root := r.URL.Query().Get("root")
	if info, err := os.Stat(root); !filepath.IsAbs(root) || err != nil || !info.IsDir() {
		http.Error(w, "no such folder", http.StatusNotFound)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming isn't possible here", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	var mu sync.Mutex
	send := func(line string) error {
		mu.Lock()
		defer mu.Unlock()
		if _, err := io.WriteString(w, line); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	}
	if send(": watching\n\n") != nil {
		return
	}
	// A comment now and then keeps the stream from looking idle.
	go func() {
		tick := time.NewTicker(25 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-r.Context().Done():
				return
			case <-tick.C:
				_ = send(": still\n\n")
			}
		}
	}()
	apphost.Watch(r.Context(), root, func(changed apphost.FilesChanged) error {
		data, err := json.Marshal(changed)
		if err != nil {
			return err
		}
		return send("data: " + string(data) + "\n\n")
	})
}

// webappHostOK keeps DNS rebinding out: the app answers only to loopback.
func webappHostOK(r *http.Request) bool {
	host := r.Host
	if i := strings.LastIndex(host, ":"); i >= 0 {
		host = host[:i]
	}
	return host == "127.0.0.1" || host == "localhost" || host == "[::1]"
}

// webPage is a small page for the browser, in the app's own words.
func webPage(w http.ResponseWriter, status int, title, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><title>%s</title>
<style>body{font:15px -apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif;margin:0;display:grid;place-items:center;height:100vh;background:#fafafa;color:#101010}
main{max-width:420px;padding:24px}h1{font-size:19px;font-weight:600;margin:0 0 8px}p{margin:0;color:#3c3c3c;line-height:1.5}
code{font:13px ui-monospace,Menlo,monospace;background:#efefef;padding:1px 5px;border-radius:4px}
@media(prefers-color-scheme:dark){body{background:#000;color:#f2f2f2}p{color:#d0d0d0}code{background:#202020}}</style>
<main><h1>%s</h1><p>%s</p></main>`, html.EscapeString(title), html.EscapeString(title), body)
}

// runWeb is `am web`: Agentman in the browser, signed in.
func runWeb(args []string) error {
	fs := flag.NewFlagSet("web", flag.ContinueOnError)
	configHome := fs.String("config-home", "", "separate home for Agentman state")
	printOnly := fs.Bool("print", false, "print the link instead of opening the browser")
	if err := fs.Parse(args); err != nil {
		return err
	}
	dir, err := hook.ConfigDir(*configHome)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(dir, daemon.AppEndpointFile))
	if errors.Is(err, os.ErrNotExist) {
		return errors.New("am serve isn't running: start it with `am serve` (or `am service install`), then run am web again")
	}
	if err != nil {
		return err
	}
	var endpoint daemon.AppEndpoint
	if err := json.Unmarshal(data, &endpoint); err != nil || endpoint.URL == "" || endpoint.Token == "" {
		return errors.New("am serve's app endpoint is unreadable: restart am serve")
	}
	base := strings.TrimSuffix(strings.Replace(endpoint.URL, "ws://", "http://", 1), "/app")

	if err := webReady(base, endpoint.Token); err != nil {
		return err
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(base + "/app/login-code?token=" + endpoint.Token)
	if err != nil {
		return fmt.Errorf("am serve isn't answering at %s: %w", base, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("am serve refused a sign-in (%s): it may be older than am web; upgrade it", resp.Status)
	}
	var answer struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&answer); err != nil || answer.Code == "" {
		return errors.New("am serve sent no sign-in code")
	}
	link := base + "/login?code=" + answer.Code
	if *printOnly {
		fmt.Println(link)
		return nil
	}
	if err := openBrowser(link); err != nil {
		fmt.Printf("Open this link in your browser (it works once, for a minute):\n  %s\n", link)
		return nil
	}
	fmt.Println(dim("Agentman is open in your browser at " + base))
	return nil
}

// webReady asks am serve to have the web app's files, which it fetches the
// first time for each version, and says so while it does. An am serve from
// before 0.47.0 doesn't know the question (404): it serves what it carries,
// as it did.
func webReady(base, token string) error {
	client := &http.Client{Timeout: 6 * time.Minute}
	done := make(chan struct{})
	go func() {
		select {
		case <-done:
		case <-time.After(700 * time.Millisecond):
			// On stderr: `am web -print` prints only the link, for a script.
			fmt.Fprintln(os.Stderr, dim("Getting Agentman's web app, once for this version (about 9 MB)…"))
		}
	}()
	resp, err := client.Get(base + "/app/web-ready?token=" + token)
	close(done)
	if err != nil {
		return fmt.Errorf("am serve isn't answering at %s: %w", base, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	var answer struct {
		Error string `json:"error"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&answer)
	if resp.StatusCode != http.StatusOK {
		if answer.Error == "" {
			answer.Error = "am serve couldn't get the web app (" + resp.Status + ")"
		}
		return errors.New(answer.Error)
	}
	return nil
}

func openBrowser(link string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", link).Run()
	case "linux":
		return exec.Command("xdg-open", link).Run()
	default:
		return errors.New("no browser opener on " + runtime.GOOS)
	}
}
