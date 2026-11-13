// Package webapp serves Agentman's web app (web/, built by `npm run build`
// into this package's dist/) from am serve. A released am fetches the
// files for its version the first time `am web` asks (fetch.go); they can
// also be carried inside the binary, built with `-tags webapp`, or read
// from a folder (AGENTMAN_WEB_DIR) while developing.
package webapp

import (
	"io/fs"
	"net/http"
	"os"
)

// files is the built app, set by embed.go in builds with the webapp tag.
var files fs.FS

// Handler serves the app's files: from AGENTMAN_WEB_DIR=<folder> when set
// (internal/webapp/dist, as Vite builds it), so a test daemon shows each
// new build without am being rebuilt; else those inside the binary, if it
// carries them; else the store's, once fetched.
func Handler(store *Store) http.Handler {
	return withoutShellScript(app(store))
}

// Ready says whether the app's files can be served now, without fetching.
func Ready(store *Store) bool {
	if os.Getenv("AGENTMAN_WEB_DIR") != "" || files != nil {
		return true
	}
	_, ok := store.Dir()
	return ok
}

func app(store *Store) http.Handler {
	if dir := os.Getenv("AGENTMAN_WEB_DIR"); dir != "" {
		return http.FileServer(http.Dir(dir))
	}
	if files != nil {
		return http.FileServer(http.FS(files))
	}
	if dir, ok := store.Dir(); ok {
		return http.FileServer(http.Dir(dir))
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		if store.Fetchable() {
			_, _ = w.Write([]byte("The web app isn't here yet: run `am web`, which fetches it the first time.\n"))
			return
		}
		_, _ = w.Write([]byte(ErrNotFetchable.Error() + "\n"))
	})
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
