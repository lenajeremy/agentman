package main

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// symbols serves /sf/<name>.png?size=13&weight=regular, an SF Symbol drawn
// by macOS (drawSymbol), and hands everything else to the page's assets.
// The page uses the picture as a mask, so it takes the colour of the text
// around it. A name the system doesn't know, or any request off macOS, is a
// 404, and the page draws its Lucide icon instead.
type symbols struct {
	next  http.Handler
	mu    sync.Mutex
	cache map[string][]byte
}

var symbolName = regexp.MustCompile(`^[a-z0-9.]{1,64}$`)

var symbolWeights = map[string]int{"regular": 0, "medium": 1, "semibold": 2, "bold": 3}

func withSymbols(next http.Handler) http.Handler {
	return &symbols{next: next, cache: map[string][]byte{}}
}

func (s *symbols) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name, ok := strings.CutPrefix(r.URL.Path, "/sf/")
	if !ok {
		s.next.ServeHTTP(w, r)
		return
	}
	name = strings.TrimSuffix(name, ".png")
	size, err := strconv.ParseFloat(r.URL.Query().Get("size"), 64)
	weight, known := symbolWeights[r.URL.Query().Get("weight")]
	if !symbolName.MatchString(name) || err != nil || size < 4 || size > 64 || !known {
		http.NotFound(w, r)
		return
	}
	key := name + "|" + r.URL.RawQuery
	s.mu.Lock()
	png, cached := s.cache[key]
	s.mu.Unlock()
	if !cached {
		// A square of 1.5 times the point size holds any symbol, wide ones
		// (ellipsis, arrow.triangle.branch) too; 3x keeps it sharp on any
		// screen.
		png = drawSymbol(name, size, weight, size*1.5, 3)
		s.mu.Lock()
		s.cache[key] = png
		s.mu.Unlock()
	}
	if png == nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "max-age=31536000, immutable")
	_, _ = w.Write(png)
}
