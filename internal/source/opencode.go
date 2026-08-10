package source

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// OpenCode is the one agent that needs no tricks.
//
// Claude Code and Codex are terminal programs with no way in, which is why
// they need a tmux wrapper to receive a message and a pane scrape to reveal a
// pending question. OpenCode ships a real HTTP API that answers all of it:
// sessions, messages with a cursor, mid-turn prompt delivery, and — the part
// the other two cannot do at all — questions as structured data with a reply
// endpoint.
//
// So this adapter is the shape the Source interface was designed for, and a
// useful reference for anyone adding a fourth agent.
const (
	openCodeDefaultPort = 4096
	openCodeTimeout     = 5 * time.Second
	// openCodeIdleWindow decides how long after its last message a session
	// still counts as live. The API reports which sessions are *running*, but
	// an idle session is still one the user may want to look at, so recency
	// fills the gap.
	openCodeIdleWindow = 12 * time.Hour
)

// OpenCodeSource talks to a local `opencode serve` instance.
type OpenCodeSource struct {
	baseURL string
	client  *http.Client
	// password is OPENCODE_SERVER_PASSWORD when the server requires auth.
	password string

	mu       sync.RWMutex
	sessions map[string]openCodeSession
}

type openCodeSession struct {
	meta     protocol.Session
	nativeID string
}

// NewOpenCodeSource creates an adapter. An empty baseURL uses the default
// local port, and OPENCODE_SERVER_PASSWORD is picked up when set.
func NewOpenCodeSource(baseURL string) *OpenCodeSource {
	if baseURL == "" {
		baseURL = fmt.Sprintf("http://127.0.0.1:%d", openCodeDefaultPort)
	}
	return &OpenCodeSource{
		baseURL:  strings.TrimRight(baseURL, "/"),
		client:   &http.Client{Timeout: openCodeTimeout},
		password: os.Getenv("OPENCODE_SERVER_PASSWORD"),
		sessions: map[string]openCodeSession{},
	}
}

// Kind implements Source.
func (s *OpenCodeSource) Kind() protocol.Kind { return protocol.KindOpenCode }

/* ------------------------------ wire types ------------------------------- */

// ocSession mirrors what the server actually returns, which differs from its
// own OpenAPI spec in two ways found by calling it: the list is wrapped in
// {data, cursor} rather than being a bare array, and the working directory
// lives under `location`, not at the top level as `directory`.
type ocSession struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Location struct {
		Directory string `json:"directory"`
	} `json:"location"`
	Time struct {
		Created int64 `json:"created"`
		Updated int64 `json:"updated"`
	} `json:"time"`
}

type ocSessionsResponse struct {
	Data []ocSession `json:"data"`
}

// ocMessagesResponse mirrors what the server returns, which again differs
// from its OpenAPI spec — the spec describes {info, parts} per entry, but the
// wire format is a flat message whose shape depends on its type: a user
// message carries `text` directly, an assistant message carries `content`.
type ocMessagesResponse struct {
	Data   []ocMessage `json:"data"`
	Cursor struct {
		Previous string `json:"previous"`
		Next     string `json:"next"`
	} `json:"cursor"`
}

type ocMessage struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Time struct {
		Created int64 `json:"created"`
	} `json:"time"`
	// User messages.
	Text string `json:"text"`
	// Assistant messages.
	Content []ocPart `json:"content"`
	Error   *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// ocPart is one element of an assistant message's content.
type ocPart struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Text string `json:"text"`
	// Synthetic parts are injected scaffolding, not anything a human wrote.
	Synthetic bool   `json:"synthetic"`
	Ignored   bool   `json:"ignored"`
	Tool      string `json:"tool"`
	State     struct {
		Status string `json:"status"`
		Title  string `json:"title"`
		Output string `json:"output"`
	} `json:"state"`
}

type ocQuestionsResponse struct {
	Data []struct {
		ID        string `json:"id"`
		SessionID string `json:"sessionID"`
		Questions []struct {
			Question string `json:"question"`
			Header   string `json:"header"`
			Multiple bool   `json:"multiple"`
			Options  []struct {
				Label       string `json:"label"`
				Description string `json:"description"`
			} `json:"options"`
		} `json:"questions"`
	} `json:"data"`
}

/* -------------------------------- requests ------------------------------- */

func (s *OpenCodeSource) do(ctx context.Context, method, path string, body any, out any) error {
	var reader *bytes.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	} else {
		reader = bytes.NewReader(nil)
	}

	req, err := http.NewRequestWithContext(ctx, method, s.baseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.password != "" {
		req.SetBasicAuth("opencode", s.password)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("opencode: %s %s: %s", method, path, resp.Status)
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Available reports whether a local opencode server is reachable.
func (s *OpenCodeSource) Available(ctx context.Context) bool {
	var health struct {
		Healthy bool `json:"healthy"`
	}
	if err := s.do(ctx, http.MethodGet, "/global/health", nil, &health); err != nil {
		return false
	}
	return health.Healthy
}

/* -------------------------------- Source --------------------------------- */

// Discover implements Source.
func (s *OpenCodeSource) Discover(ctx context.Context) ([]protocol.Session, error) {
	// No server means OpenCode simply is not being used. Not an error, and it
	// must stay cheap — this runs every second.
	if !s.Available(ctx) {
		s.mu.Lock()
		s.sessions = map[string]openCodeSession{}
		s.mu.Unlock()
		return nil, nil
	}

	var listed ocSessionsResponse
	if err := s.do(ctx, http.MethodGet, "/api/session", nil, &listed); err != nil {
		return nil, err
	}
	list := listed.Data

	// The API says which sessions are mid-turn, so busy/idle is exact here
	// rather than inferred from file mtimes as it is for the other agents.
	var active struct {
		Data map[string]struct {
			Type string `json:"type"`
		} `json:"data"`
	}
	_ = s.do(ctx, http.MethodGet, "/api/session/active", nil, &active)

	cutoff := time.Now().Add(-openCodeIdleWindow).UnixMilli()
	found := make([]protocol.Session, 0, len(list))
	next := make(map[string]openCodeSession, len(list))

	for _, item := range list {
		updated := item.Time.Updated
		if updated == 0 {
			updated = item.Time.Created
		}
		if updated < cutoff {
			continue
		}

		state := protocol.StateIdle
		if entry, ok := active.Data[item.ID]; ok && entry.Type == "running" {
			state = protocol.StateBusy
		}

		session := protocol.Session{
			ID:       string(protocol.KindOpenCode) + ":" + item.ID,
			Kind:     protocol.KindOpenCode,
			NativeID: item.ID,
			Name:     openCodeName(item),
			Cwd:      item.Location.Directory,
			State:    state,
			// A real API, so messages land immediately and mid-turn — the
			// same quality as the tmux path but without the wrapper.
			Inject:         protocol.InjectAPI,
			StartedAt:      item.Time.Created,
			LastActivityAt: updated,
		}

		// Questions are structured data here, not something scraped off a
		// terminal — so this works for any OpenCode session, however started.
		if q := s.pendingQuestion(ctx, item.ID); q != nil {
			session.Question = q
			session.State = protocol.StateWaitingInput
		}

		found = append(found, session)
		next[session.ID] = openCodeSession{meta: session, nativeID: item.ID}
	}

	s.mu.Lock()
	s.sessions = next
	s.mu.Unlock()
	return found, nil
}

func openCodeName(item ocSession) string {
	title := strings.TrimSpace(item.Title)
	// A new session is titled "New session - <timestamp>" until the model
	// summarizes it; the directory is more use on a phone until then.
	if title != "" && !strings.HasPrefix(title, "New session") {
		return title
	}
	if base := filepath.Base(item.Location.Directory); base != "." && base != string(filepath.Separator) {
		return base
	}
	return "opencode"
}

// pendingQuestion returns the first unanswered question for a session.
func (s *OpenCodeSource) pendingQuestion(ctx context.Context, nativeID string) *protocol.Question {
	var response ocQuestionsResponse
	if err := s.do(ctx, http.MethodGet, "/api/session/"+nativeID+"/question", nil, &response); err != nil {
		return nil
	}
	for _, request := range response.Data {
		if len(request.Questions) == 0 {
			continue
		}
		first := request.Questions[0]
		options := make([]protocol.QuestionOption, 0, len(first.Options))
		for _, option := range first.Options {
			// The reply endpoint identifies a choice by its label, so the key
			// is the label itself rather than an index.
			options = append(options, protocol.QuestionOption{
				Key:   option.Label,
				Label: option.Label,
			})
		}
		if len(options) == 0 {
			continue
		}
		return &protocol.Question{
			Prompt: first.Question,
			Title:  first.Header,
			// The request id has to survive the round trip so the reply can
			// name which question is being answered.
			Detail:  "",
			Options: options,
			// Carried out-of-band; see questionRequestID.
		}
	}
	return nil
}

// Page implements Source.
func (s *OpenCodeSource) Page(ctx context.Context, sessionID, before string, limit int) (protocol.Page, error) {
	s.mu.RLock()
	session, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if !ok {
		return protocol.Page{}, fmt.Errorf("source: unknown opencode session %q", sessionID)
	}

	path := fmt.Sprintf("/api/session/%s/message?limit=%d", session.nativeID, limit)
	if before != "" {
		// The cursor is opaque to every caller; only this adapter knows it is
		// OpenCode's own pagination token rather than a byte offset.
		path += "&cursor=" + before
	}

	var response ocMessagesResponse
	if err := s.do(ctx, http.MethodGet, path, nil, &response); err != nil {
		return protocol.Page{}, err
	}

	// The API returns newest-first (its cursor says order=desc), but a Page is
	// contractually oldest-first — without this the reply to a message sorts
	// above the message itself.
	messages := make([]protocol.Message, 0, len(response.Data))
	for i := len(response.Data) - 1; i >= 0; i-- {
		messages = append(messages, openCodeMessages(sessionID, response.Data[i])...)
	}

	return protocol.NewPage(sessionID, messages, response.Cursor.Previous, response.Cursor.Previous != ""), nil
}

// openCodeMessages flattens one API message into the normalized feed.
func openCodeMessages(sessionID string, message ocMessage) []protocol.Message {
	ts := message.Time.Created

	// A user message holds its text directly, with no parts to walk.
	if message.Type == "user" {
		text := strings.TrimSpace(message.Text)
		if text == "" {
			return nil
		}
		return []protocol.Message{{
			ID: message.ID, SessionID: sessionID,
			Role: protocol.RoleUser, Ts: ts, Text: text,
		}}
	}

	out := make([]protocol.Message, 0, len(message.Content)+1)
	for i, part := range message.Content {
		// Synthetic and ignored parts are scaffolding the UI never shows.
		if part.Synthetic || part.Ignored {
			continue
		}
		id := part.ID
		if id == "" {
			id = fmt.Sprintf("%s:%d", message.ID, i)
		}

		switch part.Type {
		case "text":
			text := strings.TrimSpace(part.Text)
			if text == "" {
				continue
			}
			out = append(out, protocol.Message{
				ID: id, SessionID: sessionID,
				Role: protocol.RoleAssistant, Ts: ts, Text: text,
			})

		case "tool":
			status := protocol.ToolOK
			switch part.State.Status {
			case "pending", "running":
				status = protocol.ToolRunning
			case "error":
				status = protocol.ToolError
			}
			name := part.Tool
			if name == "" {
				name = "tool"
			}
			out = append(out, protocol.Message{
				ID: id, SessionID: sessionID, Role: protocol.RoleTool, Ts: ts,
				Text: clipOutput(part.State.Output),
				Tool: &protocol.Tool{
					Name:    name,
					Summary: clipOutput(part.State.Title),
					Status:  status,
				},
			})
		}
		// Reasoning and step parts are dropped, matching how the other
		// adapters treat thinking blocks.
	}

	// A failed turn produces no content at all, so without this the agent
	// would appear to have silently done nothing.
	if message.Error != nil && message.Error.Message != "" {
		out = append(out, protocol.Message{
			ID: message.ID + ":error", SessionID: sessionID,
			Role: protocol.RoleSystem, Ts: ts,
			Text: clipOutput(message.Error.Message),
		})
	}
	return out
}

func clipOutput(text string) string {
	flat := strings.Join(strings.Fields(text), " ")
	const max = 400
	runes := []rune(flat)
	if len(runes) <= max {
		return flat
	}
	return string(runes[:max-1]) + "…"
}

// Follow implements Source.
//
// Polls rather than holding the SSE stream: the daemon already runs a
// once-a-second loop, one extra local HTTP call is cheaper than managing a
// long-lived stream's lifecycle, and messages are deduplicated by id anyway.
func (s *OpenCodeSource) Follow(ctx context.Context, sessionID string, out chan<- []protocol.Message) error {
	seen := map[string]bool{}
	// Prime from the current tail so the backlog is not replayed as new.
	if page, err := s.Page(ctx, sessionID, "", 50); err == nil {
		for _, message := range page.Messages {
			seen[message.ID] = true
		}
	}

	ticker := time.NewTicker(followInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			page, err := s.Page(ctx, sessionID, "", 20)
			if err != nil {
				continue
			}
			var batch []protocol.Message
			for _, message := range page.Messages {
				// A running tool re-emits under the same id as it settles, so
				// only a genuinely new id counts as new.
				if seen[message.ID] && message.Tool == nil {
					continue
				}
				seen[message.ID] = true
				batch = append(batch, message)
			}
			if len(batch) == 0 {
				continue
			}
			select {
			case out <- batch:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

// Inject implements Injector.
func (s *OpenCodeSource) Inject(ctx context.Context, sessionID, text string) (protocol.InjectMode, error) {
	s.mu.RLock()
	session, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if !ok {
		return protocol.InjectNone, fmt.Errorf("source: unknown opencode session %q", sessionID)
	}

	body := map[string]any{
		"prompt": map[string]any{"text": text},
		// "steer" delivers into a turn already in progress, which is what the
		// tmux path achieves by typing. Native, and without the wrapper.
		"delivery": "steer",
	}
	if err := s.do(ctx, http.MethodPost, "/api/session/"+session.nativeID+"/prompt", body, nil); err != nil {
		return protocol.InjectNone, err
	}
	return protocol.InjectAPI, nil
}

// Answer implements Answerer.
func (s *OpenCodeSource) Answer(ctx context.Context, sessionID, optionKey string) error {
	s.mu.RLock()
	session, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("source: unknown opencode session %q", sessionID)
	}

	// Re-read the pending question: its request id is what the reply must
	// name, and it can change between the app rendering and the user tapping.
	var response ocQuestionsResponse
	if err := s.do(ctx, http.MethodGet,
		"/api/session/"+session.nativeID+"/question", nil, &response); err != nil {
		return err
	}
	if len(response.Data) == 0 {
		return fmt.Errorf("source: that question is no longer waiting for an answer")
	}
	request := response.Data[0]

	// Answers are per question, each a list of selected labels.
	body := map[string]any{"answers": []any{[]string{optionKey}}}
	return s.do(ctx, http.MethodPost,
		fmt.Sprintf("/api/session/%s/question/%s/reply", session.nativeID, request.ID), body, nil)
}

// Interrupt stops a running turn.
func (s *OpenCodeSource) Interrupt(ctx context.Context, sessionID string) error {
	s.mu.RLock()
	session, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("source: unknown opencode session %q", sessionID)
	}
	return s.do(ctx, http.MethodPost, "/api/session/"+session.nativeID+"/interrupt", nil, nil)
}
