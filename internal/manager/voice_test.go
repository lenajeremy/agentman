package manager

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// fakeCelery stands in for Celery's API: an emailed code signs in, and a
// signed-in device gets its recording written out.
type fakeCelery struct {
	audio []byte
	label string
	app   string
}

func (f *fakeCelery) serve(t *testing.T) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/auth/email/start":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["email"] != "me@example.com" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `{"error":"Enter a valid email address."}`)
				return
			}
			_, _ = io.WriteString(w, `{"request_id":"req-1","expires_at":"soon"}`)
		case "/v1/auth/email/verify":
			var body map[string]string
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["request_id"] != "req-1" || body["code"] != "123456" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = io.WriteString(w, `{"error":"That code isn't right, or it has expired."}`)
				return
			}
			f.label = body["device_label"]
			_, _ = io.WriteString(w, `{"token":"device-token","email":"me@example.com","plan":"pro"}`)
		case "/v1/transcribe":
			if r.Header.Get("Authorization") != "Bearer device-token" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = io.WriteString(w, `{"error":"That sign-in has expired. Sign in again."}`)
				return
			}
			file, _, err := r.FormFile("audio")
			if err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			f.audio, _ = io.ReadAll(file)
			f.app = r.FormValue("app_name")
			_, _ = io.WriteString(w, `{"text":"Run the tests in main.go.","raw":"um run the tests in main dot go"}`)
		case "/v1/auth/signout":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	previous := celeryAPI
	celeryAPI = server.URL
	t.Cleanup(func() { celeryAPI = previous })
}

// wav is a recording as Apple's recorder writes one: a FLLR padding chunk
// before the sound, which Celery would otherwise hear as noise.
func wav(samples int) []byte {
	pcm := make([]byte, samples*2)
	for i := range pcm {
		pcm[i] = byte(i)
	}
	var out []byte
	chunk := func(id string, body []byte) {
		out = append(out, id...)
		out = binary.LittleEndian.AppendUint32(out, uint32(len(body)))
		out = append(out, body...)
	}
	format := make([]byte, 16)
	binary.LittleEndian.PutUint16(format[0:], 1)
	binary.LittleEndian.PutUint16(format[2:], 1)
	binary.LittleEndian.PutUint32(format[4:], 16000)
	binary.LittleEndian.PutUint32(format[8:], 32000)
	binary.LittleEndian.PutUint16(format[12:], 2)
	binary.LittleEndian.PutUint16(format[14:], 16)
	body := []byte("WAVE")
	out = body
	chunk("fmt ", format)
	chunk("FLLR", make([]byte, 4040))
	chunk("data", pcm)
	return append(append([]byte("RIFF"), binary.LittleEndian.AppendUint32(nil, uint32(len(out)))...), out...)
}

// send sends a recording in parts, as the apps do, and returns the answer to
// the last part.
func send(t *testing.T, m *Manager, recording []byte, parts int) protocol.Event {
	t.Helper()
	encoded := base64.StdEncoding.EncodeToString(recording)
	size := (len(encoded) + parts - 1) / parts
	var last protocol.Event
	for part := range parts {
		end := min((part+1)*size, len(encoded))
		last = m.Handle(context.Background(), protocol.Request{Type: protocol.ReqTranscribe, Voice: &protocol.VoiceClip{
			ID: "rec-1", Part: part, Parts: parts, Mime: "audio/wav", Data: encoded[part*size : end],
		}})
		if part < parts-1 && (last.Transcript == nil || last.Transcript.Done) {
			t.Fatalf("part %d: %+v", part, last)
		}
	}
	return last
}

func TestVoiceNeedsACelerySignIn(t *testing.T) {
	h := newHarness(t, &scriptedModel{}, true)
	answer := send(t, h.m, wav(16000), 1)
	if answer.Type != protocol.EvtError || !strings.Contains(answer.Error, "sign in to Celery") {
		t.Fatalf("got %+v", answer)
	}
}

func TestSigningInToCeleryAndSpeaking(t *testing.T) {
	celery := &fakeCelery{}
	celery.serve(t)
	h := newHarness(t, &scriptedModel{}, true)

	if account := h.m.Handle(context.Background(), protocol.Request{Type: protocol.ReqVoiceAccount}); account.VoiceAccount == nil || account.VoiceAccount.SignedIn {
		t.Fatalf("should start signed out: %+v", account)
	}
	bad := h.m.Handle(context.Background(), protocol.Request{Type: protocol.ReqVoiceSignInStart, VoiceSignIn: &protocol.VoiceSignIn{Email: "nobody"}})
	if bad.Type != protocol.EvtError {
		t.Fatalf("an address without @ should be refused: %+v", bad)
	}
	started := h.m.Handle(context.Background(), protocol.Request{Type: protocol.ReqVoiceSignInStart, VoiceSignIn: &protocol.VoiceSignIn{Email: "me@example.com"}})
	if started.VoiceAccount == nil || started.VoiceAccount.RequestID != "req-1" {
		t.Fatalf("start: %+v", started)
	}
	wrong := h.m.Handle(context.Background(), protocol.Request{Type: protocol.ReqVoiceSignInVerify, VoiceSignIn: &protocol.VoiceSignIn{RequestID: "req-1", Code: "000000"}})
	if wrong.Type != protocol.EvtError || !strings.Contains(wrong.Error, "code isn't right") {
		t.Fatalf("a wrong code should say so, in Celery's words: %+v", wrong)
	}
	signed := h.m.Handle(context.Background(), protocol.Request{Type: protocol.ReqVoiceSignInVerify, VoiceSignIn: &protocol.VoiceSignIn{RequestID: "req-1", Code: " 123456 "}})
	if signed.VoiceAccount == nil || !signed.VoiceAccount.SignedIn || signed.VoiceAccount.Email != "me@example.com" || signed.VoiceAccount.Plan != "pro" {
		t.Fatalf("verify: %+v", signed)
	}
	if !strings.HasPrefix(celery.label, "Agentman") {
		t.Fatalf("the device should be named for Agentman, not %q", celery.label)
	}
	// The token is kept in the Keychain, never handed to an app.
	if strings.Contains(signed.VoiceAccount.Email+signed.VoiceAccount.Plan, "device-token") {
		t.Fatal("the token reached the app")
	}

	answer := send(t, h.m, wav(16000), 3)
	if answer.Type != protocol.EvtTranscript || !answer.Transcript.Done || answer.Transcript.Text != "Run the tests in main.go." {
		t.Fatalf("transcribe: %+v", answer)
	}
	if celery.app != "Agentman" {
		t.Fatalf("app_name = %q", celery.app)
	}
	// Celery gets the plain 44-byte header and every sample, without Apple's
	// padding chunk.
	if len(celery.audio) != 44+32000 || string(celery.audio[36:40]) != "data" {
		t.Fatalf("Celery got %d bytes with %q at 36", len(celery.audio), celery.audio[36:40])
	}

	out := h.m.Handle(context.Background(), protocol.Request{Type: protocol.ReqVoiceSignOut})
	if out.VoiceAccount == nil || out.VoiceAccount.SignedIn {
		t.Fatalf("sign out: %+v", out)
	}
}

func TestRecordingsCeleryCantTakeAreRefused(t *testing.T) {
	h := newHarness(t, &scriptedModel{}, true)
	for _, clip := range []*protocol.VoiceClip{
		{ID: "a", Part: 0, Parts: 1, Mime: "audio/mp4", Data: "AAAA"},
		{ID: "", Part: 0, Parts: 1, Mime: "audio/wav", Data: "AAAA"},
		{ID: "b", Part: 2, Parts: 2, Mime: "audio/wav", Data: "AAAA"},
		{ID: "c", Part: 0, Parts: 1, Mime: "audio/wav", Data: strings.Repeat("A", 121<<10)},
	} {
		if answer := h.m.Handle(context.Background(), protocol.Request{Type: protocol.ReqTranscribe, Voice: clip}); answer.Type != protocol.EvtError {
			t.Errorf("%+v was taken", clip)
		}
	}
	// 44.1 kHz stereo isn't what Celery reads.
	stereo := wav(100)
	binary.LittleEndian.PutUint16(stereo[22:], 2)
	if _, err := canonicalWAV(stereo); err == nil {
		t.Error("stereo should be refused")
	}
}
