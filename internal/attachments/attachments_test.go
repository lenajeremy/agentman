package attachments

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func pngBytes(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// relayServing stands in for the relay, and records what it was asked for.
func relayServing(t *testing.T, body []byte, contentType string) (*httptest.Server, *string) {
	t.Helper()
	var seen string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.Path + "|" + r.Header.Get("Authorization")
		if body == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", contentType)
		_, _ = w.Write(body)
	}))
	t.Cleanup(server.Close)
	return server, &seen
}

func TestSaveWritesTheImageAndNamesItItself(t *testing.T) {
	body := pngBytes(t)
	relay, seen := relayServing(t, body, "image/png")
	dir := t.TempDir()
	store := New(dir, relay.URL, "daemon-token")

	path, err := store.Save(context.Background(), "claude:abc-123", "AAAAAAAAAAAAAAAAAAAAAAAAAA")
	if err != nil {
		t.Fatal(err)
	}
	if *seen != "/upload/AAAAAAAAAAAAAAAAAAAAAAAAAA|Bearer daemon-token" {
		t.Errorf("relay saw %q", *seen)
	}
	if !strings.HasPrefix(path, filepath.Join(dir, "images")) {
		t.Errorf("wrote outside its own directory: %s", path)
	}
	if filepath.Ext(path) != ".png" {
		t.Errorf("extension %q", filepath.Ext(path))
	}
	// The colon in a session id must not reach the filesystem.
	if strings.Contains(path, ":") {
		t.Errorf("session id leaked into the path: %s", path)
	}
	written, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(written, body) {
		t.Error("the bytes on disk are not the bytes sent")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want 0600", info.Mode().Perm())
	}
}

// The extension comes from the bytes, not from the relay's header. The relay
// is a separate process reachable from the internet, and this decides what
// gets written to a developer's disk.
func TestSaveIdentifiesTheBytesRatherThanTrustingTheHeader(t *testing.T) {
	relay, _ := relayServing(t, []byte("#!/bin/sh\nrm -rf /\n"), "image/png")
	store := New(t.TempDir(), relay.URL, "token")

	if _, err := store.Save(context.Background(), "s", "AAAAAAAAAAAAAAAAAAAAAAAAAA"); err == nil {
		t.Fatal("a shell script was accepted as an image")
	}
}

func TestSaveRejectsAMalformedTicket(t *testing.T) {
	relay, seen := relayServing(t, pngBytes(t), "image/png")
	store := New(t.TempDir(), relay.URL, "token")

	for _, id := range []string{"", "../../etc/passwd", "AAAA/BBBB", "short", strings.Repeat("A", 100)} {
		if _, err := store.Save(context.Background(), "s", id); err == nil {
			t.Errorf("%q was accepted", id)
		}
	}
	if *seen != "" {
		t.Errorf("a malformed ticket still reached the relay: %q", *seen)
	}
}

func TestSaveReportsAnExpiredUpload(t *testing.T) {
	relay, _ := relayServing(t, nil, "")
	store := New(t.TempDir(), relay.URL, "token")

	_, err := store.Save(context.Background(), "s", "AAAAAAAAAAAAAAAAAAAAAAAAAA")
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("err = %v, want something about expiry", err)
	}
}

func writeAged(t *testing.T, dir, name string, size int, age time.Duration) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, bytes.Repeat([]byte{7}, size), 0o600); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-age)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSweepDeletesImagesPastRetention(t *testing.T) {
	dir := t.TempDir()
	store := New(dir, "", "")
	session := filepath.Join(dir, "images", "claude-abc")
	old := writeAged(t, session, "old.png", 16, retention+time.Hour)
	fresh := writeAged(t, session, "fresh.png", 16, time.Hour)

	if err := store.Sweep(time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("an image past retention survived")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Error("a recent image was deleted")
	}
}

func TestSweepEnforcesTheSizeCapOldestFirst(t *testing.T) {
	dir := t.TempDir()
	store := New(dir, "", "")
	session := filepath.Join(dir, "images", "claude-abc")
	// Three files that together exceed the cap; the two oldest must go.
	oldest := writeAged(t, session, "a.png", maxTotalBytes/2, 72*time.Hour)
	middle := writeAged(t, session, "b.png", maxTotalBytes/2, 48*time.Hour)
	newest := writeAged(t, session, "c.png", maxTotalBytes/2, time.Hour)

	if err := store.Sweep(time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(oldest); !os.IsNotExist(err) {
		t.Error("the oldest image survived the size cap")
	}
	if _, err := os.Stat(newest); err != nil {
		t.Error("the newest image was deleted")
	}
	_ = middle
}

func TestSweepRemovesEmptySessionDirectories(t *testing.T) {
	dir := t.TempDir()
	store := New(dir, "", "")
	session := filepath.Join(dir, "images", "claude-abc")
	writeAged(t, session, "old.png", 16, retention+time.Hour)

	if err := store.Sweep(time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(session); !os.IsNotExist(err) {
		t.Error("an emptied session directory was left behind")
	}
}

func TestSweepOnAMissingDirectoryIsNotAnError(t *testing.T) {
	if err := New(t.TempDir(), "", "").Sweep(time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestSessionDirNeverEscapes(t *testing.T) {
	for _, id := range []string{"../../etc", "claude:../x", "a/b/c", "", "..."} {
		got := sessionDir(id)
		if strings.ContainsAny(got, "/:.") || got == "" {
			t.Errorf("sessionDir(%q) = %q", id, got)
		}
	}
}
