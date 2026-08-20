package speech

import (
	"strings"
	"testing"
)

// Agent answers are written to be read, not heard. This is the rule that
// decides whether the feature is pleasant or unbearable.
func TestPrepareStripsWhatOnlyMakesSenseOnScreen(t *testing.T) {
	in := "Fixed the leak in `AudioRecorder.start()`.\n\n" +
		"```swift\nlet engine = AVAudioEngine()\ntry engine.start()\n```\n\n" +
		"See internal/hook/server.go:247 and https://example.com/docs for the rest.\n" +
		"## What changed\n" +
		"- **Bold** point\n" +
		"- Another one\n"

	out := Prepare(in)

	for _, banned := range []string{"```", "AVAudioEngine", "https://", "server.go:247", "##", "**"} {
		if strings.Contains(out, banned) {
			t.Errorf("spoken text still contains %q:\n%s", banned, out)
		}
	}
	for _, wanted := range []string{"Fixed the leak", "What changed", "Bold point"} {
		if !strings.Contains(out, wanted) {
			t.Errorf("spoken text lost %q:\n%s", wanted, out)
		}
	}
}

// A turn that is nothing but code has nothing worth saying. Silence beats
// reading punctuation aloud.
func TestPrepareReturnsNothingWhenThereIsNothingToSay(t *testing.T) {
	for _, in := range []string{
		"",
		"   \n\t ",
		"```go\nfunc main() {}\n```",
		"`just-inline-code`",
	} {
		if out := Prepare(in); out != "" {
			t.Errorf("Prepare(%q) should be empty, got %q", in, out)
		}
	}
}

// Long answers get clipped, and clipping mid-word sounds like a dropped call.
func TestPrepareClipsAtASentence(t *testing.T) {
	long := strings.Repeat("This is a complete sentence about the deploy. ", 40)
	out := Prepare(long)

	if len([]rune(out)) > maxSpoken {
		t.Errorf("clipped text is %d runes, over the %d cap", len([]rune(out)), maxSpoken)
	}
	if !strings.HasSuffix(out, ".") && !strings.HasSuffix(out, "…") {
		t.Errorf("clip should land on a sentence end or an ellipsis, got %q", out[max(0, len(out)-40):])
	}
}

// Off unless asked for, and useless without a token — either alone must not
// produce audio.
func TestEnabledRequiresBothSwitchAndToken(t *testing.T) {
	cases := []struct {
		cfg  Config
		want bool
	}{
		{Config{}, false},
		{Config{Enabled: true}, false},
		{Config{Token: "cel_x"}, false},
		{Config{Enabled: true, Token: "cel_x"}, true},
	}
	for _, c := range cases {
		if got := New(c.cfg, nil).Enabled(); got != c.want {
			t.Errorf("Enabled() for %+v = %v, want %v", c.cfg, got, c.want)
		}
	}

	// A nil speaker must be safe to call: serve.go only installs one when it
	// is enabled, and a nil check here is cheaper than a panic later.
	var nilSpeaker *Speaker
	if nilSpeaker.Enabled() {
		t.Error("a nil Speaker should never report enabled")
	}
}

// Defaults have to be filled in, or the request is rejected upstream.
func TestNewFillsDefaults(t *testing.T) {
	s := New(Config{Enabled: true, Token: "cel_x"}, nil)
	if s.cfg.Endpoint == "" || s.cfg.Voice == "" || s.cfg.Instructions == "" || s.cfg.Speed != 1.0 {
		t.Errorf("defaults not filled: %+v", s.cfg)
	}
	custom := New(Config{Voice: "coral", Speed: 1.2}, nil)
	if custom.cfg.Voice != "coral" || custom.cfg.Speed != 1.2 {
		t.Errorf("explicit settings were overwritten: %+v", custom.cfg)
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
