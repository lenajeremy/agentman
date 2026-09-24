package attachments_test

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/lenajeremy/agentman/internal/attachments"
	"github.com/lenajeremy/agentman/internal/relay"
)

// The whole path, with a real relay: a phone uploads, a daemon collects, the
// bytes land on disk unchanged, and the relay is empty afterwards.
func TestPhoneToDiskThroughARealRelay(t *testing.T) {
	const secret = "test-secret-test-secret-test-sec"
	server := httptest.NewServer(relay.NewServer(secret, "test", slog.New(slog.DiscardHandler), false).Handler())
	defer server.Close()

	const daemonToken = "a-daemon-token"
	deviceToken, err := relay.MintDeviceToken(secret, relay.DeriveAccount(daemonToken), "nonce")
	if err != nil {
		t.Fatal(err)
	}

	var original bytes.Buffer
	if err := png.Encode(&original, image.NewRGBA(image.Rect(0, 0, 64, 64))); err != nil {
		t.Fatal(err)
	}

	// The phone.
	req, err := http.NewRequest(http.MethodPost, server.URL+"/upload", bytes.NewReader(original.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+deviceToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload: %s", resp.Status)
	}
	var payload struct {
		ID string `json:"id"`
	}
	if err := decodeJSON(resp, &payload); err != nil {
		t.Fatal(err)
	}

	// The Mac.
	dir := t.TempDir()
	store := attachments.New(dir, server.URL, daemonToken)
	path, err := store.Save(context.Background(), "claude:abc-123", payload.ID)
	if err != nil {
		t.Fatal(err)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(written, original.Bytes()) {
		t.Error("the image changed somewhere between the phone and the disk")
	}

	// The locker is empty: collecting is what empties it.
	if _, err := store.Save(context.Background(), "claude:abc-123", payload.ID); err == nil {
		t.Error("the same ticket worked twice")
	}
}

func decodeJSON(resp *http.Response, target any) error {
	return json.NewDecoder(resp.Body).Decode(target)
}
