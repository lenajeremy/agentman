package main

import (
	"net/http"
	"path/filepath"
	"strconv"
	"sync"
)

// fileIcons serves /fileicon.png?path=<absolute path>&size=18, the icon the
// system shows for a file (NSWorkspace's on macOS), as the Mac app shows a
// sent file's or an attachment's. Off macOS, or for a path with no icon,
// it is a 404 and the page draws its own.
type fileIcons struct {
	next  http.Handler
	mu    sync.Mutex
	cache map[string][]byte
}

func withFileIcons(next http.Handler) http.Handler {
	return &fileIcons{next: next, cache: map[string][]byte{}}
}

func (f *fileIcons) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/fileicon.png" {
		f.next.ServeHTTP(w, r)
		return
	}
	path := r.URL.Query().Get("path")
	size, err := strconv.ParseFloat(r.URL.Query().Get("size"), 64)
	if !filepath.IsAbs(path) || len(path) > 4096 || err != nil || size < 8 || size > 256 {
		http.NotFound(w, r)
		return
	}
	key := path + "|" + r.URL.Query().Get("size")
	f.mu.Lock()
	png, cached := f.cache[key]
	f.mu.Unlock()
	if !cached {
		png = drawFileIcon(path, size, 3)
		f.mu.Lock()
		if len(f.cache) > 512 {
			f.cache = map[string][]byte{}
		}
		f.cache[key] = png
		f.mu.Unlock()
	}
	if png == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "max-age=300")
	_, _ = w.Write(png)
}
