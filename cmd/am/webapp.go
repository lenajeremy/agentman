package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/lenajeremy/agentman/internal/daemon"
	"github.com/lenajeremy/agentman/internal/hook"
	"github.com/lenajeremy/agentman/internal/webapp"
)

// The web app: am serve serves it on its loopback listener, and `am web`
// opens it in the browser, signed in (internal/daemon's websession.go).
//
//   GET /app/login-code?token=<app token>   a one-time code, for am web
//   GET /login?code=<code>                  the code for a session cookie
//   GET /…                                  the app, to a signed-in browser

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

// login swaps the code for a session and goes to the app.
func (g *appGateway) login(w http.ResponseWriter, r *http.Request) {
	session, ok := g.web.Login(r.URL.Query().Get("code"))
	if !ok {
		webPage(w, http.StatusForbidden, "This sign-in link has expired",
			"Sign-in links work once, for a minute. Run <code>am web</code> again to open Agentman.")
		return
	}
	daemon.SetWebCookie(w, session)
	http.Redirect(w, r, "/", http.StatusSeeOther)
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
	webapp.Handler().ServeHTTP(w, r)
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
