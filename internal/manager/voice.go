package manager

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/secrets"
)

// Voice prompts: a recording from the Mac app or the phone, written out by
// Celery so the user can speak to an agent or to Agentman instead of typing.
// Celery's API transcribes with Gemini, takes out the ums and false starts,
// and refuses anything paraphrased, so what comes back is what was said. The
// words go back into the message box, to read over before sending.
//
// Agentman signs in to Celery as a device of its own, with a code Celery
// emails, and keeps the device's token in the Keychain with the other keys.

// celeryAPI is Celery's server; AGENTMAN_CELERY_URL points elsewhere, and
// tests stand in for it.
var celeryAPI = func() string {
	if url := strings.TrimRight(os.Getenv("AGENTMAN_CELERY_URL"), "/"); url != "" {
		return url
	}
	return "https://celery-api-production-150e.up.railway.app"
}()

// celeryKey names the Keychain item holding the Celery account.
const celeryKey = "celery-account"

const (
	// maxVoiceParts and maxVoiceBase64 bound one recording: Celery takes up
	// to 24 MB of WAV, about twelve minutes.
	maxVoiceParts  = 400
	maxVoiceBase64 = 32 << 20
	// voiceTTL is how long the parts of a recording wait for the rest.
	voiceTTL = 3 * time.Minute
)

// celeryAccount is what Celery gave Agentman when it signed in.
type celeryAccount struct {
	Token string `json:"token"`
	Email string `json:"email"`
	Plan  string `json:"plan"`
}

// voiceUpload is a recording whose parts are arriving.
type voiceUpload struct {
	mime    string
	parts   []string
	got     int
	size    int
	started time.Time
}

type voiceUploads struct {
	mu    sync.Mutex
	clips map[string]*voiceUpload
}

// add keeps one part, and returns the whole recording once every part is in.
func (v *voiceUploads) add(clip *protocol.VoiceClip, now time.Time) (audio string, done bool, err error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.clips == nil {
		v.clips = map[string]*voiceUpload{}
	}
	for id, upload := range v.clips {
		if now.Sub(upload.started) > voiceTTL {
			delete(v.clips, id)
		}
	}
	upload := v.clips[clip.ID]
	if upload == nil {
		if len(v.clips) >= 16 {
			return "", false, errors.New("too many recordings at once")
		}
		upload = &voiceUpload{mime: clip.Mime, parts: make([]string, clip.Parts), started: now}
		v.clips[clip.ID] = upload
	}
	if len(upload.parts) != clip.Parts || upload.mime != clip.Mime {
		delete(v.clips, clip.ID)
		return "", false, errors.New("the parts of that recording don't agree")
	}
	if upload.parts[clip.Part] == "" {
		upload.got++
		upload.size += len(clip.Data)
	}
	upload.parts[clip.Part] = clip.Data
	if upload.size > maxVoiceBase64 {
		delete(v.clips, clip.ID)
		return "", false, errors.New("that recording is too long")
	}
	if upload.got < len(upload.parts) {
		return "", false, nil
	}
	delete(v.clips, clip.ID)
	return strings.Join(upload.parts, ""), true, nil
}

func validVoiceClip(clip *protocol.VoiceClip) error {
	switch {
	case clip == nil:
		return errors.New("no recording")
	case clip.ID == "" || len(clip.ID) > 64:
		return errors.New("a recording needs an id")
	case clip.Parts < 1 || clip.Parts > maxVoiceParts || clip.Part < 0 || clip.Part >= clip.Parts:
		return errors.New("that part of the recording is out of range")
	case strings.ToLower(clip.Mime) != "audio/wav" && strings.ToLower(clip.Mime) != "audio/x-wav":
		return fmt.Errorf("recordings must be WAV, not %q", clip.Mime)
	case clip.Data == "" || len(clip.Data) > 120<<10:
		return errors.New("a part of a recording must hold 1 byte to 120 KB")
	}
	return nil
}

func voiceFailure(message string) protocol.Event {
	return protocol.Event{Type: protocol.EvtError, Error: message}
}

// transcribe takes one part of a recording, and with the last one, has
// Celery write the recording out.
func (m *Manager) transcribe(ctx context.Context, clip *protocol.VoiceClip) protocol.Event {
	if err := validVoiceClip(clip); err != nil {
		return voiceFailure(err.Error())
	}
	encoded, done, err := m.voice.add(clip, time.Now())
	if err != nil {
		return voiceFailure(err.Error())
	}
	if !done {
		return protocol.Event{Type: protocol.EvtTranscript, Transcript: &protocol.Transcript{ID: clip.ID}}
	}
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return voiceFailure("That recording didn't arrive whole. Try again.")
	}
	wav, err := canonicalWAV(raw)
	if err != nil {
		return voiceFailure(err.Error())
	}
	account, err := m.celeryAccount(ctx)
	if err != nil {
		return voiceFailure("Voice prompts are written out by Celery: sign in to Celery in Agentman's settings on your Mac (Keys).")
	}
	text, err := celeryTranscribe(ctx, account.Token, wav)
	if err != nil {
		return voiceFailure(err.Error())
	}
	return protocol.Event{Type: protocol.EvtTranscript, Transcript: &protocol.Transcript{ID: clip.ID, Done: true, Text: text}}
}

// celeryTranscribe sends Celery one recording and returns its cleaned words.
func celeryTranscribe(ctx context.Context, token string, wav []byte) (string, error) {
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("audio", "voice.wav")
	if err != nil {
		return "", err
	}
	if _, err := part.Write(wav); err != nil {
		return "", err
	}
	_ = form.WriteField("app_name", "Agentman")
	if err := form.Close(); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, celeryAPI+"/v1/transcribe", &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", form.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+token)
	var answer struct {
		Text  string `json:"text"`
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	status, err := celeryCall(req, &answer)
	if err != nil {
		return "", fmt.Errorf("Celery can't be reached: %w", err)
	}
	switch {
	case status == http.StatusUnauthorized:
		return "", errors.New("Agentman's Celery sign-in has expired: sign in again in Agentman's settings on your Mac (Keys).")
	case status != http.StatusOK && answer.Error != "":
		return "", errors.New(answer.Error)
	case status != http.StatusOK:
		return "", fmt.Errorf("Celery couldn't write that out (%d). Try again.", status)
	case strings.TrimSpace(answer.Text) == "":
		return "", errors.New("Didn't catch any speech in that one.")
	}
	return strings.TrimSpace(answer.Text), nil
}

// celeryCall makes one request to Celery and reads its JSON answer, whatever
// the status.
func celeryCall(req *http.Request, answer any) (int, error) {
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, err
	}
	_ = json.Unmarshal(raw, answer)
	return resp.StatusCode, nil
}

func celeryJSON(ctx context.Context, path string, payload any, token string, answer any) (int, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, celeryAPI+path, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return celeryCall(req, answer)
}

// celeryAccount is the stored sign-in.
func (m *Manager) celeryAccount(ctx context.Context) (celeryAccount, error) {
	raw, err := m.secrets.Get(ctx, celeryKey)
	if err != nil {
		return celeryAccount{}, err
	}
	var account celeryAccount
	if json.Unmarshal([]byte(raw), &account) != nil || account.Token == "" {
		return celeryAccount{}, secrets.ErrNotFound
	}
	return account, nil
}

func (m *Manager) voiceAccountEvent(ctx context.Context) protocol.Event {
	account, err := m.celeryAccount(ctx)
	if err != nil {
		return protocol.Event{Type: protocol.EvtVoiceAccount, VoiceAccount: &protocol.VoiceAccount{}}
	}
	return protocol.Event{Type: protocol.EvtVoiceAccount, VoiceAccount: &protocol.VoiceAccount{
		SignedIn: true, Email: account.Email, Plan: account.Plan,
	}}
}

// voiceSignInStart has Celery email a sign-in code.
func (m *Manager) voiceSignInStart(ctx context.Context, step *protocol.VoiceSignIn) protocol.Event {
	if step == nil || !strings.Contains(step.Email, "@") || len(step.Email) > 254 {
		return voiceFailure("That doesn't look like an email address.")
	}
	var answer struct {
		RequestID string `json:"request_id"`
		Error     string `json:"error"`
	}
	status, err := celeryJSON(ctx, "/v1/auth/email/start", map[string]string{"email": strings.TrimSpace(step.Email)}, "", &answer)
	switch {
	case err != nil:
		return voiceFailure("Celery can't be reached: " + err.Error())
	case status != http.StatusOK || answer.RequestID == "":
		return voiceFailure(orDefault(answer.Error, fmt.Sprintf("Celery couldn't send a code (%d).", status)))
	}
	return protocol.Event{Type: protocol.EvtVoiceAccount, VoiceAccount: &protocol.VoiceAccount{RequestID: answer.RequestID}}
}

// voiceSignInVerify signs in with the code Celery emailed, as a device named
// for this Mac, and keeps the token.
func (m *Manager) voiceSignInVerify(ctx context.Context, step *protocol.VoiceSignIn) protocol.Event {
	if step == nil || step.RequestID == "" || strings.TrimSpace(step.Code) == "" {
		return voiceFailure("Type the code Celery emailed you.")
	}
	host, _ := os.Hostname()
	label := "Agentman"
	if host = strings.TrimSuffix(host, ".local"); host != "" {
		label += " on " + host
	}
	var answer struct {
		celeryAccount
		Error string `json:"error"`
	}
	status, err := celeryJSON(ctx, "/v1/auth/email/verify", map[string]string{
		"request_id": step.RequestID, "code": strings.TrimSpace(step.Code), "device_label": label,
	}, "", &answer)
	switch {
	case err != nil:
		return voiceFailure("Celery can't be reached: " + err.Error())
	case status != http.StatusOK || answer.Token == "":
		return voiceFailure(orDefault(answer.Error, fmt.Sprintf("Celery couldn't sign in (%d).", status)))
	}
	stored, _ := json.Marshal(answer.celeryAccount)
	if err := m.secrets.Set(ctx, celeryKey, string(stored)); err != nil {
		return voiceFailure("Couldn't keep the sign-in in the Keychain: " + err.Error())
	}
	return m.voiceAccountEvent(ctx)
}

// voiceSignOut signs this Mac out of Celery and forgets the token.
func (m *Manager) voiceSignOut(ctx context.Context) protocol.Event {
	if account, err := m.celeryAccount(ctx); err == nil {
		var ignored map[string]any
		_, _ = celeryJSON(ctx, "/v1/auth/signout", map[string]string{}, account.Token, &ignored)
	}
	if err := m.secrets.Delete(ctx, celeryKey); err != nil && !errors.Is(err, secrets.ErrNotFound) {
		return voiceFailure(err.Error())
	}
	return m.voiceAccountEvent(ctx)
}

func orDefault(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

// canonicalWAV checks a recording is 16 kHz mono 16-bit PCM, as Celery takes
// it, and returns it with the plain 44-byte header Celery reads: recorders
// add chunks of their own (Apple's FLLR padding) that would otherwise be
// read as sound.
func canonicalWAV(raw []byte) ([]byte, error) {
	notWAV := errors.New("that recording isn't 16 kHz mono WAV")
	if len(raw) < 12 || string(raw[0:4]) != "RIFF" || string(raw[8:12]) != "WAVE" {
		return nil, notWAV
	}
	var format, channels, bits uint16
	var rate uint32
	var data []byte
	for at := 12; at+8 <= len(raw); {
		id := string(raw[at : at+4])
		size := int(binary.LittleEndian.Uint32(raw[at+4 : at+8]))
		start := at + 8
		end := min(start+size, len(raw))
		switch id {
		case "fmt ":
			if end-start < 16 {
				return nil, notWAV
			}
			format = binary.LittleEndian.Uint16(raw[start:])
			channels = binary.LittleEndian.Uint16(raw[start+2:])
			rate = binary.LittleEndian.Uint32(raw[start+4:])
			bits = binary.LittleEndian.Uint16(raw[start+14:])
		case "data":
			data = raw[start:end]
		}
		at = start + size + size%2
	}
	if format != 1 || channels != 1 || rate != 16000 || bits != 16 || len(data) == 0 {
		return nil, notWAV
	}
	data = data[:len(data)-len(data)%2]
	out := make([]byte, 44+len(data))
	copy(out[0:], "RIFF")
	binary.LittleEndian.PutUint32(out[4:], uint32(36+len(data)))
	copy(out[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(out[16:], 16)
	binary.LittleEndian.PutUint16(out[20:], 1)
	binary.LittleEndian.PutUint16(out[22:], 1)
	binary.LittleEndian.PutUint32(out[24:], 16000)
	binary.LittleEndian.PutUint32(out[28:], 32000)
	binary.LittleEndian.PutUint16(out[32:], 2)
	binary.LittleEndian.PutUint16(out[34:], 16)
	copy(out[36:], "data")
	binary.LittleEndian.PutUint32(out[40:], uint32(len(data)))
	copy(out[44:], data)
	return out, nil
}
