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
	// OpenCodeDefaultPort is where this adapter looks for OpenCode's API, and
	// therefore the port `am opencode` has to start it on. Exported so the two
	// cannot drift: a wrapper that launched OpenCode anywhere else would
	// produce a session the daemon never sees.
	OpenCodeDefaultPort = 4096
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
	// pinned records that the URL came from the user, so discovery stays on it
	// rather than wandering to another server.
	pinned bool

	// models remembers each session's model; see modelCache. It matters more
	// here than for the file-backed agents, because finding it costs an HTTP
	// request rather than a read of a file already on disk.
	models *modelCache

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
	pinned := baseURL != ""
	if baseURL == "" {
		baseURL = fmt.Sprintf("http://127.0.0.1:%d", OpenCodeDefaultPort)
	}
	return &OpenCodeSource{
		baseURL:  strings.TrimRight(baseURL, "/"),
		client:   &http.Client{Timeout: openCodeTimeout},
		password: os.Getenv("OPENCODE_SERVER_PASSWORD"),
		pinned:   pinned,
		models:   newModelCache(),
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
	return s.doAt(ctx, s.baseURL, method, path, body, out)
}

func (s *OpenCodeSource) doAt(ctx context.Context, base, method, path string, body any, out any) error {
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

	req, err := http.NewRequestWithContext(ctx, method, base+path, reader)
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

// OpenCodePortSpan is how many ports from the default this will look across.
//
// There is one server per directory — `am opencode` starts its own rather than
// attaching, because a session belongs to its server's directory — so the one
// on the default port is simply whichever started first, and it may well have
// exited while others are still running.
const OpenCodePortSpan = 16

// Available reports whether a local opencode server is reachable, adopting one
// on a nearby port if the default is silent.
//
// Any server will do. OpenCode's session storage is global, so every server
// lists every session regardless of which one created it — that is what allows
// one server per directory without the daemon having to track them all.
func (s *OpenCodeSource) Available(ctx context.Context) bool {
	if s.healthyAt(ctx, s.baseURL) {
		return true
	}
	if s.pinned {
		return false // the user named a URL; looking elsewhere would surprise them
	}
	for port := OpenCodeDefaultPort; port < OpenCodeDefaultPort+OpenCodePortSpan; port++ {
		candidate := fmt.Sprintf("http://127.0.0.1:%d", port)
		if candidate == s.baseURL {
			continue
		}
		if s.healthyAt(ctx, candidate) {
			s.baseURL = candidate
			return true
		}
	}
	return false
}

func (s *OpenCodeSource) healthyAt(ctx context.Context, base string) bool {
	var health struct {
		Healthy bool `json:"healthy"`
	}
	if err := s.doAt(ctx, base, http.MethodGet, "/global/health", nil, &health); err != nil {
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

		model, cached := s.models.get(session.ID)
		if !cached {
			model = s.modelOf(ctx, item.ID)
			s.models.put(session.ID, model)
		}
		session.Model = model

		found = append(found, session)
		next[session.ID] = openCodeSession{meta: session, nativeID: item.ID}
	}

	live := make(map[string]bool, len(next))
	for id := range next {
		live[id] = true
	}
	s.models.forget(live)

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
		// Always namespaced by the message id. OpenCode numbers parts within
		// their message, so part.ID is "text-0" for the first text part of
		// *every* assistant message — unique inside one message and colliding
		// across the session.
		//
		// Ids are the identity of a message everywhere downstream: the app
		// merges pages and live updates by id, and Follow decides what is new
		// by id. Session-wide collisions therefore collapsed every assistant
		// reply into one row and stopped new replies from ever streaming.
		id := fmt.Sprintf("%s:%d", message.ID, i)
		if part.ID != "" {
			id = message.ID + ":" + part.ID
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
	// Keyed by id, valued by a fingerprint of the content rather than a bare
	// "seen" flag. OpenCode fills a message in as the model produces it, so the
	// same id legitimately carries different content on successive polls: a
	// reply first appears as a fragment and grows. Treating a known id as
	// nothing-to-report froze every long answer at its first few words.
	seen := map[string]string{}
	// Prime from the current tail so the backlog is not replayed as new.
	if page, err := s.Page(ctx, sessionID, "", 50); err == nil {
		for _, message := range page.Messages {
			seen[message.ID] = messageFingerprint(message)
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
				// Send anything whose content has moved: a new message, a
				// reply that has grown, or a tool that has settled. The app
				// merges by id, so re-sending a changed message replaces the
				// row rather than duplicating it.
				fingerprint := messageFingerprint(message)
				if previous, known := seen[message.ID]; known && previous == fingerprint {
					continue
				}
				seen[message.ID] = fingerprint
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

// modelOf reports the model behind a session's most recent reply.
//
// OpenCode names the model on each assistant message rather than on the
// session, so this asks for the last few messages and takes the newest one that
// carries a name. Cached by the caller — this is an HTTP request, and discovery
// runs every second.
func (s *OpenCodeSource) modelOf(ctx context.Context, nativeID string) string {
	var response struct {
		Data []struct {
			Model struct {
				ID string `json:"id"`
			} `json:"model"`
		} `json:"data"`
	}
	if err := s.do(ctx, http.MethodGet,
		"/api/session/"+nativeID+"/message?limit=6", nil, &response); err != nil {
		return ""
	}
	// Newest first, as everywhere else in this API.
	for _, message := range response.Data {
		if model := cleanModel(message.Model.ID); model != "" {
			return model
		}
	}
	return ""
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

// messageFingerprint captures the parts of a message that can change while its
// id stays the same, so a poll can tell "already sent this" from "this grew".
func messageFingerprint(message protocol.Message) string {
	if message.Tool == nil {
		return message.Text
	}
	return message.Text + "\x00" + message.Tool.Name + "\x00" +
		message.Tool.Summary + "\x00" + string(message.Tool.Status)
}
