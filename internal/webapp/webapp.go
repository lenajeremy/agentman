// Package webapp serves Agentman's web app (web/, built by `npm run build`
// into this package's dist/) from am serve. The files are carried inside
// the am binary when it is built with `-tags webapp`; without the tag,
// as in ordinary builds and tests, a page says how to build it.
package webapp

import (
	"io/fs"
	"net/http"
	"os"
)

// files is the built app, set by embed.go in builds with the webapp tag.
var files fs.FS

// Available says whether this am carries the web app.
func Available() bool { return files != nil }

// Handler serves the app's files. AGENTMAN_WEB_DIR=<folder> serves them from
// that folder instead (internal/webapp/dist, as Vite builds it), so a test
// daemon shows each new build without am being rebuilt.
func Handler() http.Handler {
	return withoutShellScript(app())
}

func app() http.Handler {
	if dir := os.Getenv("AGENTMAN_WEB_DIR"); dir != "" {
		return http.FileServer(http.Dir(dir))
	}
	if files == nil {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("This am was built without the web app.\n\n" +
				"Build it with:\n  (cd web && npm ci && npm run build)\n  go build -tags webapp ./cmd/am\n"))
		})
	}
	return http.FileServer(http.FS(files))
}

// shellScript is what the desktop shell's runtime (@wailsio/runtime) looks
// for on every page, a script the shell may add. In a browser there is none:
// it's answered with nothing, rather than a 404 in every page's console.
const shellScript = "/wails/custom.js"

func withoutShellScript(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == shellScript {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
