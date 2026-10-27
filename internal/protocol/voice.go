package protocol

// Voice prompts: the apps record the user, and the daemon on their Mac has
// Celery (github.com/lenajeremy/celery) write it out: Gemini's transcription,
// then Celery's cleanup of ums and false starts, with its guard against
// anything paraphrased. Agentman signs in to Celery as a device of its own,
// with a code Celery emails, and keeps the token in the Mac's Keychain.
//
// A phone's request may be at most 128 KiB at the relay, so a recording
// travels in parts: each carries a slice of the base64, the daemon joins them
// in order, and the last one is answered with the words.

const (
	// ReqTranscribe sends one part of a recording (Voice), answered with an
	// EvtTranscript: Done and Text once the last part is in.
	ReqTranscribe RequestType = "transcribe"
	EvtTranscript EventType   = "transcript"

	// ReqVoiceAccount asks whether Agentman is signed in to Celery;
	// ReqVoiceSignInStart (VoiceSignIn.Email) has Celery email a code, and
	// ReqVoiceSignInVerify (VoiceSignIn.RequestID and Code) signs in with it;
	// ReqVoiceSignOut signs out. Each is answered with an EvtVoiceAccount.
	ReqVoiceAccount      RequestType = "voice_account"
	ReqVoiceSignInStart  RequestType = "voice_signin_start"
	ReqVoiceSignInVerify RequestType = "voice_signin_verify"
	ReqVoiceSignOut      RequestType = "voice_signout"
	EvtVoiceAccount      EventType   = "voice_account"
)

// VoiceClip is one part of a recording: 16 kHz mono WAV, as Celery takes it.
type VoiceClip struct {
	// ID names the recording, the same in every part.
	ID    string `json:"id"`
	Part  int    `json:"part"`
	Parts int    `json:"parts"`
	// Mime is the recording's type: audio/wav.
	Mime string `json:"mime"`
	// Data is this part's slice of the recording's base64.
	Data string `json:"data"`
}

// Transcript answers a part: received, or, for the last, the words.
type Transcript struct {
	ID   string `json:"id"`
	Done bool   `json:"done"`
	Text string `json:"text,omitempty"`
}

// VoiceSignIn is a step of signing in to Celery.
type VoiceSignIn struct {
	Email     string `json:"email,omitempty"`
	RequestID string `json:"requestId,omitempty"`
	Code      string `json:"code,omitempty"`
}

// VoiceAccount is Agentman's Celery account.
type VoiceAccount struct {
	SignedIn bool   `json:"signedIn"`
	Email    string `json:"email,omitempty"`
	Plan     string `json:"plan,omitempty"`
	// RequestID answers ReqVoiceSignInStart: the code is on its way.
	RequestID string `json:"requestId,omitempty"`
}
