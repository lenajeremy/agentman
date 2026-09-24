package relay

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func pngBytes(t *testing.T, size int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, size, size))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// deviceTokenFor mints an app token the way pairing would.
func deviceTokenFor(t *testing.T, daemonToken string) string {
	t.Helper()
	token, err := MintDeviceToken(testSecret, DeriveAccount(daemonToken), "nonce")
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func postUpload(t *testing.T, base, token string, body []byte) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, base+"/upload", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func uploadID(t *testing.T, resp *http.Response) string {
	t.Helper()
	var payload struct {
		ID   string `json:"id"`
		MIME string `json:"mime"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	return payload.ID
}

func fetchUpload(t *testing.T, base, token, id string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, base+"/upload/"+id, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestUploadRoundTripsToTheDaemonThatOwnsIt(t *testing.T) {
	server, http_ := newTestServer(t)
	daemon := "daemon-token"
	body := pngBytes(t, 4)

	resp := postUpload(t, http_.URL, deviceTokenFor(t, daemon), body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload: %s", resp.Status)
	}
	id := uploadID(t, resp)

	got := fetchUpload(t, http_.URL, daemon, id)
	if got.StatusCode != http.StatusOK {
		t.Fatalf("fetch: %s", got.Status)
	}
	if got.Header.Get("Content-Type") != "image/png" {
		t.Errorf("Content-Type = %q", got.Header.Get("Content-Type"))
	}
	data, err := io.ReadAll(got.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(data, body) {
		t.Error("the bytes came back changed")
	}
	if count, total := server.uploads.pending(); count != 0 || total != 0 {
		t.Errorf("collecting left %d uploads holding %d bytes", count, total)
	}
}

// A ticket is good exactly once, so a replay cannot pull the image again.
func TestUploadCannotBeCollectedTwice(t *testing.T) {
	_, http_ := newTestServer(t)
	daemon := "daemon-token"
	id := uploadID(t, postUpload(t, http_.URL, deviceTokenFor(t, daemon), pngBytes(t, 2)))

	fetchUpload(t, http_.URL, daemon, id)
	if again := fetchUpload(t, http_.URL, daemon, id); again.StatusCode != http.StatusNotFound {
		t.Fatalf("second fetch: %s", again.Status)
	}
}

// Containment: the ticket alone is not enough, the account must match.
func TestUploadCannotBeCollectedByAnotherAccount(t *testing.T) {
	_, http_ := newTestServer(t)
	id := uploadID(t, postUpload(t, http_.URL, deviceTokenFor(t, "mine"), pngBytes(t, 2)))

	if resp := fetchUpload(t, http_.URL, "someone-else", id); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("a stranger collected it: %s", resp.Status)
	}
	if resp := fetchUpload(t, http_.URL, "mine", id); resp.StatusCode != http.StatusOK {
		t.Fatalf("the owner was refused: %s", resp.Status)
	}
}

func TestUploadRequiresADeviceToken(t *testing.T) {
	_, http_ := newTestServer(t)
	for _, token := range []string{"", "not-a-device-token", "daemon-token"} {
		if resp := postUpload(t, http_.URL, token, pngBytes(t, 2)); resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("token %q got %s", token, resp.Status)
		}
	}
}

// The declared type is never consulted; the bytes decide. A script named .png
// must not become a file the daemon writes as an image.
func TestUploadRefusesAnythingThatIsNotAnImage(t *testing.T) {
	_, http_ := newTestServer(t)
	token := deviceTokenFor(t, "daemon-token")
	for _, body := range []string{"#!/bin/sh\nrm -rf /\n", "<svg onload=alert(1)>", "%PDF-1.4"} {
		resp := postUpload(t, http_.URL, token, []byte(body))
		if resp.StatusCode != http.StatusUnsupportedMediaType {
			t.Errorf("%q got %s", body[:8], resp.Status)
		}
	}
}

func TestUploadRefusesAnImageOverTheCap(t *testing.T) {
	_, http_ := newTestServer(t)
	oversized := append(pngBytes(t, 2), bytes.Repeat([]byte{0}, maxUploadBytes)...)
	resp := postUpload(t, http_.URL, deviceTokenFor(t, "daemon-token"), oversized)
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized upload got %s", resp.Status)
	}
}

func TestUploadCapsWhatOneAccountCanHold(t *testing.T) {
	_, http_ := newTestServer(t)
	token := deviceTokenFor(t, "daemon-token")
	for i := 0; i < maxAccountUploads; i++ {
		if resp := postUpload(t, http_.URL, token, pngBytes(t, 2)); resp.StatusCode != http.StatusOK {
			t.Fatalf("upload %d: %s", i, resp.Status)
		}
	}
	resp := postUpload(t, http_.URL, token, pngBytes(t, 2))
	if resp.StatusCode != http.StatusInsufficientStorage {
		t.Fatalf("the cap did not bite: %s", resp.Status)
	}
	// A different account is unaffected by a noisy one.
	other := deviceTokenFor(t, "another-daemon")
	if resp := postUpload(t, http_.URL, other, pngBytes(t, 2)); resp.StatusCode != http.StatusOK {
		t.Fatalf("a second account was blocked: %s", resp.Status)
	}
}

// Nobody collected it, so it must not sit in memory indefinitely.
func TestUploadExpires(t *testing.T) {
	server, _ := newTestServer(t)
	account := DeriveAccount("daemon-token")
	now := time.Now()
	id, err := server.uploads.put(account, "image/png", pngBytes(t, 2), now)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := server.uploads.take(account, id, now.Add(uploadTTL+time.Second)); ok {
		t.Fatal("an expired upload was still collectable")
	}
	if count, total := server.uploads.pending(); count != 0 || total != 0 {
		t.Errorf("expiry left %d uploads holding %d bytes", count, total)
	}
}

func TestSniffImageNamesTheExtensionFromTheBytes(t *testing.T) {
	mime, ext, ok := sniffImage(pngBytes(t, 2))
	if !ok || mime != "image/png" || ext != ".png" {
		t.Fatalf("png sniffed as %q %q %v", mime, ext, ok)
	}
	if _, _, ok := sniffImage([]byte(strings.Repeat("a", 600))); ok {
		t.Fatal("plain text passed as an image")
	}
}
