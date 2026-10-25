package manager

import (
	"context"
	"errors"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// Model is the part of the Claude API Agentman uses: send one request and
// get the whole reply back, with its text passed to onText as it streams.
// An interface, so tests can script replies without calling the API.
type Model interface {
	Stream(ctx context.Context, params anthropic.BetaMessageNewParams, onText func(string)) (*anthropic.BetaMessage, error)
}

// claudeModel calls the Claude API with the user's key.
type claudeModel struct {
	client anthropic.Client
}

func newClaudeModel(apiKey string) Model {
	return &claudeModel{client: anthropic.NewClient(option.WithAPIKey(apiKey))}
}

func (m *claudeModel) Stream(
	ctx context.Context,
	params anthropic.BetaMessageNewParams,
	onText func(string),
) (*anthropic.BetaMessage, error) {
	stream := m.client.Beta.Messages.NewStreaming(ctx, params)
	defer stream.Close()
	message := anthropic.BetaMessage{}
	for stream.Next() {
		event := stream.Current()
		if err := message.Accumulate(event); err != nil {
			return nil, err
		}
		if onText == nil {
			continue
		}
		if delta, ok := event.AsAny().(anthropic.BetaRawContentBlockDeltaEvent); ok {
			if text, ok := delta.Delta.AsAny().(anthropic.BetaTextDelta); ok {
				onText(text.Text)
			}
		}
	}
	if err := stream.Err(); err != nil {
		return nil, err
	}
	return &message, nil
}

// describeAPIError turns an API failure into a line the user can act on.
func describeAPIError(err error) string {
	var gemini *geminiError
	if errors.As(err, &gemini) {
		return describeGeminiError(gemini)
	}
	var apiErr *anthropic.Error
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case 401:
			return "The API key was rejected. Check it in Settings."
		case 403:
			return "The API key isn't allowed to use this model."
		case 404:
			return "That model isn't available to this API key."
		case 429:
			return "The API is rate limiting this key. Try again in a moment."
		case 529, 503:
			return "The Claude API is overloaded right now. Try again shortly."
		}
		if apiErr.StatusCode >= 500 {
			return "The Claude API had a problem. Try again."
		}
		return "The Claude API refused the request (" + apiErr.Error() + ")."
	}
	if errors.Is(err, context.Canceled) {
		return "Stopped."
	}
	return "Couldn't reach the API: " + err.Error()
}

// describeGeminiError is describeAPIError for the Gemini API.
func describeGeminiError(err *geminiError) string {
	switch err.Status {
	case 400:
		if strings.Contains(err.Message, "API key") {
			return "The Gemini API key was rejected. Check it in Settings."
		}
		return "The Gemini API refused the request (" + err.Message + ")."
	case 401, 403:
		return "The Gemini API key was rejected, or isn't allowed this model. Check it in Settings."
	case 404:
		return "That Gemini model isn't available to this API key."
	case 429:
		return "The Gemini API is rate limiting this key, or its quota is used up. Try again in a moment."
	}
	if err.Status >= 500 {
		return "The Gemini API had a problem. Try again."
	}
	return "The Gemini API refused the request (" + err.Message + ")."
}
