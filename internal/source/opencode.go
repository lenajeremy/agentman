package source

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
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
	// API responses are local but not inherently trusted: a mismatched service
	// on a scanned port must not be able to make the daemon allocate without a
	// bound. Metadata is much smaller than transcript pages, so keeping separate
	// ceilings prevents sixteen concurrent health probes from each allocating a
	// transcript-sized buffer.
	maxOpenCodeMetadataResponse = 4 * 1024 * 1024
	maxOpenCodeMessageResponse  = 16 * 1024 * 1024
	maxOpenCodeHealthResponse   = 4 * 1024
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
	// username defaults to "opencode", but OpenCode lets users override it.
	username string
	// pinned records that the URL came from the user, so discovery stays on it
	// rather than wandering to another server.
	pinned bool
	// findServers is replaceable in tests. In production it scans the small
	// port range used by `am opencode` and returns every live server, not just
	// the first one: concurrent OpenCode TUIs each own their own API process.
	findServers func(context.Context) []string

	// models remembers each session's model; see modelCache. It matters more
	// here than for the file-backed agents, because finding it costs an HTTP
	// request rather than a read of a file already on disk.
	models *modelCache
	// questionAnswers accumulates answers when OpenCode asks several questions
	// in one request. The mobile protocol presents one choice card at a time;
	// once the final card is answered, the whole ordered answer matrix is sent.
	answerMu        sync.Mutex
	questionAnswers map[string][][]string

	mu       sync.RWMutex
	sessions map[string]openCodeSession
}

type openCodeSession struct {
	meta      protocol.Session
	nativeID  string
	baseURL   string
	directory string
	pending   openCodePending
}

type openCodePendingKind string

const (
	openCodeQuestionPending   openCodePendingKind = "question"
	openCodePermissionPending openCodePendingKind = "permission"
)

type openCodePending struct {
	kind          openCodePendingKind
	requestID     string
	answerKey     string
	questionCount int
	questionIndex int
}

// NewOpenCodeSource creates an adapter. An empty baseURL uses the default
// local port, and OPENCODE_SERVER_PASSWORD is picked up when set.
func NewOpenCodeSource(baseURL string) *OpenCodeSource {
	pinned := baseURL != ""
	if baseURL == "" {
		baseURL = fmt.Sprintf("http://127.0.0.1:%d", OpenCodeDefaultPort)
	}
	username := strings.TrimSpace(os.Getenv("OPENCODE_SERVER_USERNAME"))
	if username == "" {
		username = "opencode"
	}
	source := &OpenCodeSource{
		baseURL:         strings.TrimRight(baseURL, "/"),
		client:          &http.Client{Timeout: openCodeTimeout},
		password:        os.Getenv("OPENCODE_SERVER_PASSWORD"),
		username:        username,
		pinned:          pinned,
		models:          newModelCache(),
		sessions:        map[string]openCodeSession{},
		questionAnswers: map[string][][]string{},
	}
	source.findServers = source.scanServers
	return source
}

// Kind implements Source.
func (s *OpenCodeSource) Kind() protocol.Kind { return protocol.KindOpenCode }

/* ------------------------------ wire types ------------------------------- */

// These types mirror OpenCode's published OpenAPI 3.1 contract. Keeping the
// wire structs local prevents its release cadence from leaking into the rest
// of agentman, whose protocol is intentionally stable.
type ocSession struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Directory string `json:"directory"`
	Model     struct {
		ID         string `json:"id"`
		ProviderID string `json:"providerID"`
	} `json:"model"`
	// Location is retained as a compatibility fallback for the short-lived
	// experimental /api/session response used by older OpenCode builds.
	Location struct {
		Directory string `json:"directory"`
	} `json:"location"`
	Time struct {
		Created int64 `json:"created"`
		Updated int64 `json:"updated"`
	} `json:"time"`
}

func (s ocSession) directory() string {
	if s.Directory != "" {
		return s.Directory
	}
	return s.Location.Directory
}

type ocMessage struct {
	Info  ocMessageInfo `json:"info"`
	Parts []ocPart      `json:"parts"`
}

type ocMessageInfo struct {
	ID         string `json:"id"`
	Role       string `json:"role"`
	ModelID    string `json:"modelID"`
	ProviderID string `json:"providerID"`
	Model      struct {
		ModelID    string `json:"modelID"`
		ProviderID string `json:"providerID"`
	} `json:"model"`
	Time struct {
		Created int64 `json:"created"`
	} `json:"time"`
	Error json.RawMessage `json:"error"`
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
		Error  string `json:"error"`
	} `json:"state"`
}

type ocQuestionRequest struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionID"`
	Questions []struct {
		Question string `json:"question"`
		Header   string `json:"header"`
		Multiple bool   `json:"multiple"`
		// OpenCode enables its synthetic custom-answer row by default and omits
		// this field in that case. A pointer preserves the distinction between
		// an omitted/default-true value and an explicit false.
		Custom  *bool `json:"custom"`
		Options []struct {
			Label       string `json:"label"`
			Description string `json:"description"`
		} `json:"options"`
	} `json:"questions"`
}

type ocPermissionRequest struct {
	ID         string         `json:"id"`
	SessionID  string         `json:"sessionID"`
	Permission string         `json:"permission"`
	Patterns   []string       `json:"patterns"`
	Metadata   map[string]any `json:"metadata"`
	Always     []string       `json:"always"`
}

/* -------------------------------- requests ------------------------------- */

func (s *OpenCodeSource) doAt(ctx context.Context, base, method, path string, body any, out any) (http.Header, error) {
	return s.doAtLimit(ctx, base, method, path, body, out, maxOpenCodeMetadataResponse)
}

func (s *OpenCodeSource) doAtLimit(
	ctx context.Context,
	base, method, path string,
	body any,
	out any,
	limit int64,
) (http.Header, error) {
	var reader *bytes.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(encoded)
	} else {
		reader = bytes.NewReader(nil)
	}

	req, err := http.NewRequestWithContext(ctx, method, base+path, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.password != "" {
		req.SetBasicAuth(s.username, s.password)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		message := strings.TrimSpace(string(detail))
		if message != "" {
			return resp.Header, fmt.Errorf("opencode: %s %s: %s: %s", method, path, resp.Status, message)
		}
		return resp.Header, fmt.Errorf("opencode: %s %s: %s", method, path, resp.Status)
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return resp.Header, nil
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return resp.Header, err
	}
	if int64(len(raw)) > limit {
		return resp.Header, fmt.Errorf("opencode: %s %s: response exceeds %d bytes", method, path, limit)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return resp.Header, fmt.Errorf("opencode: %s %s: invalid JSON: %w", method, path, err)
	}
	return resp.Header, nil
}

// OpenCodePortSpan is how many ports from the default this will look across.
//
// There is one server per directory — `am opencode` starts its own rather than
// attaching, because a session belongs to its server's directory — so the one
// on the default port is simply whichever started first, and it may well have
// exited while others are still running.
const OpenCodePortSpan = 16

// Available reports whether at least one local OpenCode server is reachable.
func (s *OpenCodeSource) Available(ctx context.Context) bool {
	return len(s.findServers(ctx)) > 0
}

// scanServers returns every server the wrapper may have started.
//
// OpenCode's supported API is instance-scoped: two TUIs in two projects have
// two servers, and only the owning server reliably reports live status,
// questions, and permissions. Adopting the first healthy port made the others
// silently disappear or route commands through the wrong process.
func (s *OpenCodeSource) scanServers(ctx context.Context) []string {
	if s.pinned {
		if s.healthyAt(ctx, s.baseURL) {
			return []string{s.baseURL}
		}
		return nil // the user named a URL; looking elsewhere would surprise them
	}

	// Probe concurrently. A different process can occupy one candidate port and
	// accept a connection without answering; doing sixteen five-second probes
	// serially made every discovery sweep stall for over a minute.
	healthy := make([]bool, OpenCodePortSpan)
	var wait sync.WaitGroup
	for index := range OpenCodePortSpan {
		wait.Add(1)
		go func() {
			defer wait.Done()
			candidate := fmt.Sprintf("http://127.0.0.1:%d", OpenCodeDefaultPort+index)
			healthy[index] = s.healthyAt(ctx, candidate)
		}()
	}
	wait.Wait()

	servers := make([]string, 0, OpenCodePortSpan)
	for index, ok := range healthy {
		if ok {
			servers = append(servers, fmt.Sprintf("http://127.0.0.1:%d", OpenCodeDefaultPort+index))
		}
	}
	return servers
}

func (s *OpenCodeSource) healthyAt(ctx context.Context, base string) bool {
	var health struct {
		Healthy bool `json:"healthy"`
	}
	if _, err := s.doAtLimit(ctx, base, http.MethodGet, "/global/health", nil, &health,
		maxOpenCodeHealthResponse); err != nil {
		return false
	}
	return health.Healthy
}

func openCodePath(path string, query url.Values) string {
	if len(query) == 0 {
		return path
	}
	return path + "?" + query.Encode()
}

func openCodeSegment(value string) string { return url.PathEscape(value) }

func openCodeAnswerKey(base, nativeID, requestID string) string {
	return base + "\x00" + nativeID + "\x00" + requestID
}

/* -------------------------------- Source --------------------------------- */

// Discover implements Source.
func (s *OpenCodeSource) Discover(ctx context.Context) ([]protocol.Session, error) {
	// No server means OpenCode simply is not being used. Not an error, and it
	// must stay cheap — this runs every second.
	servers := s.findServers(ctx)
	if len(servers) == 0 {
		s.mu.Lock()
		s.sessions = map[string]openCodeSession{}
		s.mu.Unlock()
		return nil, nil
	}

	cutoff := time.Now().Add(-openCodeIdleWindow).UnixMilli()
	next := make(map[string]openCodeSession)
	activeQuestionAnswers := make(map[string]struct{})
	var failures []error
	failedServers := make(map[string]struct{})
	var reached int

	for _, base := range servers {
		listed, statuses, questions, permissions, err := s.snapshotAt(ctx, base, cutoff)
		if err != nil {
			failures = append(failures, err)
			failedServers[base] = struct{}{}
			continue
		}
		reached++
		for _, request := range questions {
			activeQuestionAnswers[openCodeAnswerKey(base, request.SessionID, request.ID)] = struct{}{}
		}

		for _, item := range listed {
			updated := item.Time.Updated
			if updated == 0 {
				updated = item.Time.Created
			}
			if updated < cutoff {
				continue
			}

			directory := item.directory()
			state := protocol.StateIdle
			if status := statuses[item.ID].Type; status == "busy" || status == "retry" {
				state = protocol.StateBusy
			}

			question, pending := s.openCodePendingFor(base, item.ID, questions, permissions)
			if question != nil {
				state = protocol.StateWaitingInput
			}

			id := string(protocol.KindOpenCode) + ":" + item.ID
			model := cleanModel(item.Model.ID)
			if model == "" {
				if cached, ok := s.models.get(id); ok {
					model = cached
				} else {
					model = s.modelOfAt(ctx, base, item.ID, directory)
					s.models.put(id, model)
				}
			} else {
				s.models.put(id, model)
			}

			meta := protocol.Session{
				ID: id, Kind: protocol.KindOpenCode, NativeID: item.ID,
				Name: openCodeName(item), Cwd: directory, State: state,
				Inject: protocol.InjectAPI, StartedAt: item.Time.Created,
				LastActivityAt: updated, Model: model, Question: question,
			}
			candidate := openCodeSession{
				meta: meta, nativeID: item.ID, baseURL: base,
				directory: directory, pending: pending,
			}

			// The same project may have several OpenCode servers. Each lists
			// the same history, but only the server that owns the live TUI
			// reports its busy state or pending decision. Prefer that route.
			current, exists := next[id]
			if !exists || openCodeRoutePriority(candidate) > openCodeRoutePriority(current) ||
				(openCodeRoutePriority(candidate) == openCodeRoutePriority(current) &&
					candidate.meta.LastActivityAt > current.meta.LastActivityAt) {
				next[id] = candidate
			}
		}
	}

	if reached == 0 {
		return nil, errors.Join(failures...)
	}
	if len(failedServers) > 0 {
		// A server answered its health probe but one of the four snapshot calls
		// failed. Preserve that server's last routes for this sweep; otherwise a
		// transient API error announces every session on it as gone and tears
		// down active subscriptions even while other OpenCode servers are fine.
		s.mu.RLock()
		for id, previous := range s.sessions {
			if _, failed := failedServers[previous.baseURL]; !failed {
				continue
			}
			current, exists := next[id]
			if !exists || openCodeRoutePriority(previous) > openCodeRoutePriority(current) {
				next[id] = previous
			}
		}
		s.mu.RUnlock()
	}
	// Partial multi-question answers are useful only while the exact request is
	// still pending. Prune after a completely healthy sweep; if one server was
	// temporarily unreachable, preserve its state so a reconnect does not make
	// the user answer earlier cards again.
	if len(failures) == 0 {
		s.answerMu.Lock()
		for key := range s.questionAnswers {
			if _, live := activeQuestionAnswers[key]; !live {
				delete(s.questionAnswers, key)
			}
		}
		s.answerMu.Unlock()
	}

	live := make(map[string]bool, len(next))
	for id := range next {
		live[id] = true
	}
	s.models.forget(live)

	s.mu.Lock()
	s.sessions = next
	s.mu.Unlock()

	found := make([]protocol.Session, 0, len(next))
	for _, session := range next {
		found = append(found, session.meta)
	}
	SortSessions(found)
	if len(failures) > 0 {
		return found, errors.Join(failures...)
	}
	return found, nil
}

type ocSessionStatus struct {
	Type string `json:"type"`
}

func (s *OpenCodeSource) snapshotAt(ctx context.Context, base string, cutoff int64) (
	[]ocSession,
	map[string]ocSessionStatus,
	[]ocQuestionRequest,
	[]ocPermissionRequest,
	error,
) {
	var listed []ocSession
	if _, err := s.doAt(ctx, base, http.MethodGet, "/session?limit=200", nil, &listed); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("%s/session?limit=200: %w", base, err)
	}

	// OpenCode's pending-state endpoints are scoped to a project directory.
	// Calling them without ?directory= only reports the server's launch
	// directory, even though /session lists sessions from every project. This
	// made sessions in any other directory look idle and hid their questions.
	directories := make(map[string]struct{})
	for _, item := range listed {
		updated := item.Time.Updated
		if updated == 0 {
			updated = item.Time.Created
		}
		if updated >= cutoff {
			directories[item.directory()] = struct{}{}
		}
	}
	type scopedSnapshot struct {
		directory   string
		statuses    map[string]ocSessionStatus
		questions   []ocQuestionRequest
		permissions []ocPermissionRequest
	}
	scopes := make([]scopedSnapshot, 0, len(directories))
	for directory := range directories {
		scopes = append(scopes, scopedSnapshot{
			directory: directory,
			statuses:  map[string]ocSessionStatus{},
		})
	}

	errs := make([]error, len(scopes)*3)
	var wait sync.WaitGroup
	for index := range scopes {
		query := url.Values{}
		if scopes[index].directory != "" {
			query.Set("directory", scopes[index].directory)
		}
		requests := []struct {
			path string
			out  any
		}{
			{path: openCodePath("/session/status", query), out: &scopes[index].statuses},
			{path: openCodePath("/question", query), out: &scopes[index].questions},
			{path: openCodePath("/permission", query), out: &scopes[index].permissions},
		}
		for requestIndex := range requests {
			wait.Add(1)
			go func(scopeIndex, requestIndex int) {
				defer wait.Done()
				request := requests[requestIndex]
				if _, err := s.doAt(ctx, base, http.MethodGet, request.path, nil, request.out); err != nil {
					errs[scopeIndex*3+requestIndex] = fmt.Errorf("%s%s: %w", base, request.path, err)
				}
			}(index, requestIndex)
		}
	}
	wait.Wait()
	if err := errors.Join(errs...); err != nil {
		return nil, nil, nil, nil, err
	}

	statuses := map[string]ocSessionStatus{}
	questionsByID := make(map[string]ocQuestionRequest)
	permissionsByID := make(map[string]ocPermissionRequest)
	for _, scope := range scopes {
		for sessionID, status := range scope.statuses {
			statuses[sessionID] = status
		}
		for _, question := range scope.questions {
			questionsByID[question.ID] = question
		}
		for _, permission := range scope.permissions {
			permissionsByID[permission.ID] = permission
		}
	}
	questions := make([]ocQuestionRequest, 0, len(questionsByID))
	for _, question := range questionsByID {
		questions = append(questions, question)
	}
	permissions := make([]ocPermissionRequest, 0, len(permissionsByID))
	for _, permission := range permissionsByID {
		permissions = append(permissions, permission)
	}
	return listed, statuses, questions, permissions, nil
}

func openCodeRoutePriority(session openCodeSession) int {
	switch session.meta.State {
	case protocol.StateWaitingInput:
		return 3
	case protocol.StateBusy:
		return 2
	default:
		return 1
	}
}

func openCodeName(item ocSession) string {
	title := strings.TrimSpace(item.Title)
	// A new session is titled "New session - <timestamp>" until the model
	// summarizes it; the directory is more use on a phone until then.
	if title != "" && !strings.HasPrefix(title, "New session") {
		return title
	}
	if base := filepath.Base(item.directory()); base != "." && base != string(filepath.Separator) {
		return base
	}
	return "opencode"
}

func (s *OpenCodeSource) openCodePendingFor(
	base string,
	nativeID string,
	questions []ocQuestionRequest,
	permissions []ocPermissionRequest,
) (*protocol.Question, openCodePending) {
	// Permissions take priority because they usually block a tool already in
	// flight. They were previously ignored entirely by the OpenCode adapter.
	for _, request := range permissions {
		if request.SessionID != nativeID {
			continue
		}
		options := []protocol.QuestionOption{
			{Key: "once", Label: "Allow once"},
		}
		if len(request.Always) > 0 {
			options = append(options, protocol.QuestionOption{Key: "always", Label: "Always allow"})
		}
		options = append(options, protocol.QuestionOption{Key: "reject", Label: "Reject"})
		detail := strings.Join(request.Patterns, "\n")
		prompt := "Allow this action?"
		if request.Permission != "" {
			prompt = "Allow " + request.Permission + "?"
		}
		return &protocol.Question{
			Title: "Permission", Prompt: prompt, Detail: detail, Options: options,
		}, openCodePending{kind: openCodePermissionPending, requestID: request.ID}
	}

	for _, request := range questions {
		if request.SessionID != nativeID || len(request.Questions) == 0 {
			continue
		}
		answerKey := openCodeAnswerKey(base, nativeID, request.ID)
		index := s.nextQuestionIndex(answerKey, len(request.Questions))
		first := request.Questions[index]
		custom := first.Custom == nil || *first.Custom
		options := make([]protocol.QuestionOption, 0, len(first.Options))
		for _, option := range first.Options {
			options = append(options, protocol.QuestionOption{
				Key: option.Label, Label: option.Label, Description: option.Description,
			})
		}
		if len(options) == 0 && !custom {
			continue
		}
		detail := ""
		if len(request.Questions) > 1 {
			detail = fmt.Sprintf("Question %d of %d", index+1, len(request.Questions))
		}
		return &protocol.Question{
				Prompt: first.Question, Title: first.Header, Detail: detail, Options: options,
				Multiple: first.Multiple, Custom: custom,
			}, openCodePending{
				kind: openCodeQuestionPending, requestID: request.ID,
				answerKey:     answerKey,
				questionCount: len(request.Questions), questionIndex: index,
			}
	}
	return nil, openCodePending{}
}

func (s *OpenCodeSource) nextQuestionIndex(answerKey string, count int) int {
	s.answerMu.Lock()
	defer s.answerMu.Unlock()
	answers := s.questionAnswers[answerKey]
	for index := 0; index < count && index < len(answers); index++ {
		if len(answers[index]) == 0 {
			return index
		}
	}
	if len(answers) < count {
		return len(answers)
	}
	// A completed answer set is kept only while its POST is in flight. Keep
	// showing the last question rather than indexing past the request.
	return max(0, count-1)
}

func (s *OpenCodeSource) recordQuestionAnswer(pending openCodePending, values []string) ([][]string, bool) {
	s.answerMu.Lock()
	defer s.answerMu.Unlock()
	answers := s.questionAnswers[pending.answerKey]
	if len(answers) != pending.questionCount {
		answers = make([][]string, pending.questionCount)
	}
	if pending.questionIndex >= 0 && pending.questionIndex < len(answers) {
		answers[pending.questionIndex] = append([]string(nil), values...)
	}
	s.questionAnswers[pending.answerKey] = answers
	for _, answer := range answers {
		if len(answer) == 0 {
			return answers, false
		}
	}
	return answers, true
}

func (s *OpenCodeSource) clearQuestionAnswers(answerKey string) {
	s.answerMu.Lock()
	delete(s.questionAnswers, answerKey)
	s.answerMu.Unlock()
}

// Page implements Source.
func (s *OpenCodeSource) Page(ctx context.Context, sessionID, before string, limit int) (protocol.Page, error) {
	s.mu.RLock()
	session, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if !ok {
		return protocol.Page{}, fmt.Errorf("source: unknown opencode session %q", sessionID)
	}

	query := url.Values{"limit": {strconv.Itoa(limit)}}
	if before != "" {
		query.Set("before", before)
	}
	if session.directory != "" {
		query.Set("directory", session.directory)
	}
	path := openCodePath(fmt.Sprintf("/session/%s/message", openCodeSegment(session.nativeID)), query)

	var response []ocMessage
	headers, err := s.doAtLimit(ctx, session.baseURL, http.MethodGet, path, nil, &response,
		maxOpenCodeMessageResponse)
	if err != nil {
		return protocol.Page{}, err
	}

	// OpenCode returns the newest page in chronological order. Older pages are
	// requested with the opaque X-Next-Cursor value in the `before` parameter.
	messages := make([]protocol.Message, 0, len(response))
	for _, message := range response {
		messages = append(messages, openCodeMessages(sessionID, message)...)
	}

	cursor := headers.Get("X-Next-Cursor")
	return protocol.NewPage(sessionID, messages, cursor, cursor != ""), nil
}

// openCodeMessages flattens one API message into the normalized feed.
func openCodeMessages(sessionID string, message ocMessage) []protocol.Message {
	ts := message.Info.Time.Created
	role := protocol.RoleAssistant
	if message.Info.Role == "user" {
		role = protocol.RoleUser
	}

	out := make([]protocol.Message, 0, len(message.Parts)+1)
	for i, part := range message.Parts {
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
		id := fmt.Sprintf("%s:%d", message.Info.ID, i)
		if part.ID != "" {
			id = message.Info.ID + ":" + part.ID
		}

		switch part.Type {
		case "text":
			text := strings.TrimSpace(part.Text)
			if text == "" {
				continue
			}
			out = append(out, protocol.Message{
				ID: id, SessionID: sessionID,
				Role: role, Ts: ts, Text: text,
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
			text := part.State.Output
			if part.State.Error != "" {
				text = part.State.Error
			}
			out = append(out, protocol.Message{
				ID: id, SessionID: sessionID, Role: protocol.RoleTool, Ts: ts,
				Text: clipOutput(text),
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
	if text := openCodeErrorText(message.Info.Error); text != "" {
		out = append(out, protocol.Message{
			ID: message.Info.ID + ":error", SessionID: sessionID,
			Role: protocol.RoleSystem, Ts: ts,
			Text: clipOutput(text),
		})
	}
	return out
}

func openCodeErrorText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var parsed struct {
		Name    string `json:"name"`
		Message string `json:"message"`
		Data    struct {
			Message      string `json:"message"`
			ResponseBody string `json:"responseBody"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &parsed) == nil {
		if parsed.Data.Message != "" {
			return parsed.Data.Message
		}
		if parsed.Message != "" {
			return parsed.Message
		}
		if parsed.Data.ResponseBody != "" {
			return parsed.Data.ResponseBody
		}
		if parsed.Name != "" {
			return parsed.Name
		}
	}
	return "OpenCode turn failed"
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
func (s *OpenCodeSource) modelOfAt(ctx context.Context, base, nativeID, directory string) string {
	query := url.Values{"limit": {"6"}}
	if directory != "" {
		query.Set("directory", directory)
	}
	path := openCodePath("/session/"+openCodeSegment(nativeID)+"/message", query)
	var response []ocMessage
	if _, err := s.doAtLimit(ctx, base, http.MethodGet, path, nil, &response,
		maxOpenCodeMessageResponse); err != nil {
		return ""
	}
	for i := len(response) - 1; i >= 0; i-- {
		modelID := response[i].Info.ModelID
		if modelID == "" {
			modelID = response[i].Info.Model.ModelID
		}
		if model := cleanModel(modelID); model != "" {
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
		"parts": []map[string]any{{"type": "text", "text": text}},
	}
	query := url.Values{}
	if session.directory != "" {
		query.Set("directory", session.directory)
	}
	path := openCodePath("/session/"+openCodeSegment(session.nativeID)+"/prompt_async", query)
	if _, err := s.doAt(ctx, session.baseURL, http.MethodPost, path, body, nil); err != nil {
		return protocol.InjectNone, err
	}
	return protocol.InjectAPI, nil
}

// Answer implements Answerer.
func (s *OpenCodeSource) Answer(ctx context.Context, sessionID string, answer protocol.QuestionAnswer) error {
	s.mu.RLock()
	session, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("source: unknown opencode session %q", sessionID)
	}

	// Re-read the pending decision: it can disappear or change between the app
	// rendering it and the user tapping an option.
	query := url.Values{}
	if session.directory != "" {
		query.Set("directory", session.directory)
	}
	var questions []ocQuestionRequest
	if _, err := s.doAt(ctx, session.baseURL, http.MethodGet,
		openCodePath("/question", query), nil, &questions); err != nil {
		return err
	}
	var permissions []ocPermissionRequest
	if _, err := s.doAt(ctx, session.baseURL, http.MethodGet,
		openCodePath("/permission", query), nil, &permissions); err != nil {
		return err
	}
	pending := session.pending
	if pending.requestID == "" {
		return fmt.Errorf("source: that question is no longer waiting for an answer")
	}

	switch pending.kind {
	case openCodePermissionPending:
		var current *ocPermissionRequest
		for index := range permissions {
			request := &permissions[index]
			if request.ID == pending.requestID && request.SessionID == session.nativeID {
				current = request
				break
			}
		}
		if current == nil {
			return fmt.Errorf("source: that question is no longer waiting for an answer")
		}
		if len(answer.Options) > 0 || answer.Text != "" {
			return fmt.Errorf("source: OpenCode permissions accept one listed option")
		}
		if answer.OptionKey != "once" && answer.OptionKey != "always" && answer.OptionKey != "reject" {
			return fmt.Errorf("source: invalid OpenCode permission answer %q", answer.OptionKey)
		}
		if answer.OptionKey == "always" && len(current.Always) == 0 {
			return fmt.Errorf("source: OpenCode did not offer an always-allow answer")
		}
		path := openCodePath("/permission/"+openCodeSegment(pending.requestID)+"/reply", query)
		_, err := s.doAt(ctx, session.baseURL, http.MethodPost, path,
			map[string]any{"reply": answer.OptionKey}, nil)
		return err

	case openCodeQuestionPending:
		var current *ocQuestionRequest
		for index := range questions {
			request := &questions[index]
			if request.ID == pending.requestID && request.SessionID == session.nativeID {
				current = request
				break
			}
		}
		if current == nil || pending.questionIndex < 0 || pending.questionIndex >= len(current.Questions) {
			return fmt.Errorf("source: that question is no longer waiting for an answer")
		}
		question := current.Questions[pending.questionIndex]
		values := append([]string(nil), answer.Options...)
		if answer.OptionKey != "" {
			values = append(values, answer.OptionKey)
		}
		customText := strings.TrimSpace(answer.Text)
		customAllowed := question.Custom == nil || *question.Custom
		if customText != "" {
			if !customAllowed {
				return fmt.Errorf("source: OpenCode did not offer a custom answer")
			}
			values = append(values, customText)
		}
		if len(values) == 0 || (!question.Multiple && len(values) != 1) {
			return fmt.Errorf("source: this OpenCode question requires %s answer",
				map[bool]string{true: "at least one", false: "exactly one"}[question.Multiple])
		}
		listed := make(map[string]struct{}, len(question.Options))
		for _, option := range question.Options {
			listed[option.Label] = struct{}{}
		}
		seen := make(map[string]struct{}, len(values))
		for _, value := range values {
			if _, duplicate := seen[value]; duplicate {
				return fmt.Errorf("source: duplicate OpenCode question answer %q", value)
			}
			seen[value] = struct{}{}
			if value == customText && customText != "" && customAllowed {
				continue
			}
			if _, valid := listed[value]; !valid {
				return fmt.Errorf("source: invalid OpenCode question answer %q", value)
			}
		}

		pending.questionCount = len(current.Questions)
		answers, complete := s.recordQuestionAnswer(pending, values)
		if !complete {
			return nil
		}
		path := openCodePath("/question/"+openCodeSegment(pending.requestID)+"/reply", query)
		if _, err := s.doAt(ctx, session.baseURL, http.MethodPost, path,
			map[string]any{"answers": answers}, nil); err != nil {
			return err
		}
		s.clearQuestionAnswers(pending.answerKey)
		return nil
	default:
		return fmt.Errorf("source: unsupported OpenCode decision")
	}
}

// Interrupt stops a running turn.
func (s *OpenCodeSource) Interrupt(ctx context.Context, sessionID string) error {
	s.mu.RLock()
	session, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if !ok {
		return fmt.Errorf("source: unknown opencode session %q", sessionID)
	}
	query := url.Values{}
	if session.directory != "" {
		query.Set("directory", session.directory)
	}
	path := openCodePath("/session/"+openCodeSegment(session.nativeID)+"/abort", query)
	_, err := s.doAt(ctx, session.baseURL, http.MethodPost, path, nil, nil)
	return err
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
