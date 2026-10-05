package relay

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"runtime"
	"strings"
	"testing"
	"time"
)

// stalledUpload starts an upload that declares a full-size image, sends one
// byte of it, and then goes quiet: what a hostile client does, and what a
// phone that loses signal mid-send looks like.
func stalledUpload(t *testing.T, base, token string) net.Conn {
	t.Helper()
	conn, err := net.Dial("tcp", strings.TrimPrefix(base, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	fmt.Fprintf(conn, "POST /upload HTTP/1.1\r\nHost: relay\r\nAuthorization: Bearer %s\r\n"+
		"Content-Type: image/png\r\nContent-Length: %d\r\n\r\n\x89", token, maxUploadBytes)
	return conn
}

// Any caller can pair a throwaway daemon of its own and so hold a device
// token. An upload that stopped sending used to hold its connection and the
// full declared 4 MiB for as long as the client liked: 64 of them pinned
// 259 MiB, and a couple of thousand would exhaust the relay for everyone.
func TestAStalledUploadIsCutOff(t *testing.T) {
	server, ts := newTestServer(t)
	server.uploadReadTimeout = 200 * time.Millisecond
	conn := stalledUpload(t, ts.URL, deviceTokenFor(t, "daemon-token"))

	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		// A closed connection is an acceptable answer too; still waiting is not.
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			t.Fatal("the relay is still waiting on an upload that stopped sending")
		}
		return
	}
	response.Body.Close()
	if response.StatusCode < 400 {
		t.Fatalf("a stalled upload was answered %s", response.Status)
	}
}

// How many uploads may be read at once is bounded, so stalled ones cannot
// add up however many accounts send them.
func TestUploadsInProgressAreBounded(t *testing.T) {
	server, ts := newTestServer(t)
	server.uploadRequests = make(chan struct{}, 2)
	token := deviceTokenFor(t, "daemon-token")
	stalledUpload(t, ts.URL, token)
	stalledUpload(t, ts.URL, token)
	// Let both reach the handler.
	deadline := time.Now().Add(5 * time.Second)
	for len(server.uploadRequests) < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	resp := postUpload(t, ts.URL, token, pngBytes(t, 4))
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("a third upload while two were in progress got %s", resp.Status)
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Error("the refusal does not say when to try again")
	}
}

// Memory is spent on bytes that arrived, not on the length a client claims.
func TestAStalledUploadHoldsOnlyWhatItSent(t *testing.T) {
	server, ts := newTestServer(t)
	server.uploadRequests = make(chan struct{}, 64)
	token := deviceTokenFor(t, "daemon-token")
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	const stalled = 32
	for range stalled {
		stalledUpload(t, ts.URL, token)
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(server.uploadRequests) < stalled && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	if grew := int64(after.HeapInuse) - int64(before.HeapInuse); grew > stalled*maxUploadBytes/4 {
		t.Errorf("%d one-byte uploads hold %d MiB: the declared length is being allocated up front",
			stalled, grew>>20)
	}
}
