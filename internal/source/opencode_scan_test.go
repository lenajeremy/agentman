package source

import (
	"context"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/tmux"
)

// Discovery looked for OpenCode servers by asking all sixteen ports for their
// health every second, almost always to hear that nothing was there. Ports
// that answered stay checked every sweep; the rest are scanned every few
// seconds. A server agentman launched is checked from its first sweep, since
// the phone is waiting to open the session it started.
func TestOpenCodeScansEveryPortOnlyEveryFewSeconds(t *testing.T) {
	src := NewOpenCodeSource("")
	var mu sync.Mutex
	healthy := map[string]bool{"127.0.0.1:4097": true}
	probed := map[string]int{}
	src.client.Transport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		mu.Lock()
		defer mu.Unlock()
		probed[request.URL.Host]++
		if !healthy[request.URL.Host] {
			return nil, errors.New("connection refused")
		}
		return &http.Response{
			StatusCode: http.StatusOK, Header: http.Header{}, Request: request,
			Body: io.NopCloser(strings.NewReader(`{"healthy":true}`)),
		}, nil
	})
	clock := time.Unix(1_000_000, 0)
	src.now = func() time.Time { return clock }
	var panes []tmux.Session
	src.listPanes = func(context.Context) ([]tmux.Session, error) { return panes, nil }

	scan := func(want ...string) []string {
		t.Helper()
		mu.Lock()
		clear(probed)
		mu.Unlock()
		got := src.scanServers(context.Background())
		if !slices.Equal(got, want) {
			t.Errorf("servers = %v, want %v", got, want)
		}
		mu.Lock()
		defer mu.Unlock()
		var ports []string
		for host := range probed {
			ports = append(ports, strings.TrimPrefix(host, "127.0.0.1:"))
		}
		slices.Sort(ports)
		return ports
	}

	if ports := scan("http://127.0.0.1:4097"); len(ports) != OpenCodePortSpan {
		t.Errorf("the first scan probed %v, want every port", ports)
	}
	clock = clock.Add(time.Second)
	if ports := scan("http://127.0.0.1:4097"); !slices.Equal(ports, []string{"4097"}) {
		t.Errorf("a sweep between scans probed %v, want only the live server", ports)
	}

	panes = []tmux.Session{{Name: tmux.Prefix + "opencode-server-4100-1759000000000-ab12"}}
	healthy["127.0.0.1:4100"] = true
	clock = clock.Add(time.Second)
	if ports := scan("http://127.0.0.1:4097", "http://127.0.0.1:4100"); !slices.Equal(ports, []string{"4097", "4100"}) {
		t.Errorf("a server agentman launched was not checked at once: probed %v", ports)
	}

	// One started some other way waits for the next full scan.
	healthy["127.0.0.1:4105"] = true
	clock = clock.Add(time.Second)
	scan("http://127.0.0.1:4097", "http://127.0.0.1:4100")
	clock = clock.Add(openCodeFullScanEvery)
	if ports := scan("http://127.0.0.1:4097", "http://127.0.0.1:4100", "http://127.0.0.1:4105"); len(ports) != OpenCodePortSpan {
		t.Errorf("the full scan probed %v, want every port", ports)
	}
}
