package manager

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// The models Agentman can run on. Opus is the default for both the chat and
// the briefs; the user picks others in Settings.
var models = []string{"claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-5-5", "claude-fable-5-1"}

var efforts = []string{"low", "medium", "high", "xhigh", "max"}

const (
	defaultModel  = "claude-opus-5-5"
	defaultEffort = "medium"
	// keyName is the API key's name in the secret store.
	keyName = "anthropic-api-key"
	// maxStyle bounds what the user can put into the prompt. It is read on
	// every request, so it is kept to what fits comfortably.
	maxStyle = 8000
)

// defaultStyle is how Agentman talks until the user says otherwise: calm and
// concise, which is what they chose.
const defaultStyle = "Calm and concise. Lead with what matters, in as few words as it takes. " +
	"No preamble, no filler, no exclamation marks. Name agents by name. " +
	"When nothing needs the user, say so in one line."

func defaultSettings() protocol.ManagerSettings {
	return protocol.ManagerSettings{
		ChatModel:  defaultModel,
		BriefModel: defaultModel,
		Effort:     defaultEffort,
		Briefs:     true,
		// The user chose this: Agentman handles the simple, low-risk requests,
		// edits in the agent's own folder included.
		AutoApprove: true,
		Style:       defaultStyle,
	}
}

func loadSettings(path string) protocol.ManagerSettings {
	settings := defaultSettings()
	raw, err := os.ReadFile(path)
	if err != nil {
		return settings
	}
	// Read over the defaults, so a setting added since the file was saved
	// takes its default rather than its zero value.
	stored := defaultSettings()
	if json.Unmarshal(raw, &stored) != nil {
		return settings
	}
	if validateSettings(stored) == nil {
		stored.KeySet = false
		return stored
	}
	return settings
}

func saveSettings(path string, settings protocol.ManagerSettings) error {
	settings.KeySet = false
	raw, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func validateSettings(s protocol.ManagerSettings) error {
	if !slices.Contains(models, s.ChatModel) {
		return fmt.Errorf("unknown chat model %q", s.ChatModel)
	}
	if !slices.Contains(models, s.BriefModel) {
		return fmt.Errorf("unknown brief model %q", s.BriefModel)
	}
	if !slices.Contains(efforts, s.Effort) {
		return fmt.Errorf("effort must be one of %s", strings.Join(efforts, ", "))
	}
	if len(s.Style) > maxStyle || len(s.Instructions) > maxStyle {
		return errors.New("style and instructions are limited to 8000 characters each")
	}
	return nil
}

// supportsFallbacks reports the models that take server-side refusal
// fallbacks in their "default" form. Haiku has no server-side fallback.
func supportsFallbacks(model string) bool {
	return model == "claude-opus-5-5" || model == "claude-fable-5-1" || model == "claude-sonnet-5-5"
}
