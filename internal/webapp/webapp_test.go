package webapp

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The shell runtime's optional script is answered with nothing, so a page
// in a browser logs no 404 for it; the runtime loads only a 2xx with a
// JavaScript type, and this has none.
func TestTheShellsOptionalScriptIsAnsweredWithNothing(t *testing.T) {
	t.Setenv("AGENTMAN_WEB_DIR", t.TempDir())
	for _, method := range []string{http.MethodHead, http.MethodGet} {
		w := httptest.NewRecorder()
		Handler().ServeHTTP(w, httptest.NewRequest(method, "/wails/custom.js", nil))
		if w.Code != http.StatusNoContent || w.Header().Get("Content-Type") != "" || w.Body.Len() != 0 {
			t.Fatalf("%s: %d %q %q", method, w.Code, w.Header().Get("Content-Type"), w.Body.String())
		}
	}
	w := httptest.NewRecorder()
	Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/missing.js", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("other files: %d", w.Code)
	}
}
