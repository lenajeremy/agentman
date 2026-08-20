package source

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
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
	maxOpenCodeSessions         = 200
	maxOpenCodeDirectories      = 64
	maxOpenCodeSnapshotWorkers  = 8
	maxOpenCodeQuestionDetail   = 16 * 1024
	openCodeHealthMissGrace     = 3
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
	pinned             bool
	configurationError error
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
	// A process can miss one health probe while it is busy or restarting. Misses
	// are tracked per server: one healthy OpenCode instance must not make a
	// second, temporarily unresponsive instance's sessions disappear.
	serverMisses map[string]int
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
	password := os.Getenv("OPENCODE_SERVER_PASSWORD")
	configurationError := validateOpenCodeConfiguration(baseURL, password)
	if baseURL == "" {
		baseURL = fmt.Sprintf("http://127.0.0.1:%d", OpenCodeDefaultPort)
	}
	username := strings.TrimSpace(os.Getenv("OPENCODE_SERVER_USERNAME"))
	if username == "" {
		username = "opencode"
	}
	source := &OpenCodeSource{
		baseURL:            strings.TrimRight(baseURL, "/"),
		client:             &http.Client{Timeout: openCodeTimeout},
		password:           password,
		username:           username,
		pinned:             pinned,
		configurationError: configurationError,
		models:             newModelCache(),
		sessions:           map[string]openCodeSession{},
		serverMisses:       map[string]int{},
		questionAnswers:    map[string][][]string{},
	}
	source.findServers = source.scanServers
	return source
}

// Validate reports an actionable configuration error before discovery starts.
func (s *OpenCodeSource) Validate() error { return s.configurationError }

func validateOpenCodeConfiguration(baseURL, password string) error {
	if strings.TrimSpace(baseURL) == "" {
		if password != "" {
			return fmt.Errorf("opencode: AGENTMAN_OPENCODE_URL is required when OPENCODE_SERVER_PASSWORD is set")
		}
		return nil
	}
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Host == "" || parsed.Hostname() == "" {
		return fmt.Errorf("opencode: AGENTMAN_OPENCODE_URL is invalid")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Path != "" && parsed.Path != "/") {
		return fmt.Errorf("opencode: AGENTMAN_OPENCODE_URL must contain only a scheme, host, and optional port")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("opencode: AGENTMAN_OPENCODE_URL must use HTTP or HTTPS")
	}
	if parsed.Scheme == "http" && !openCodeLoopbackHost(parsed.Hostname()) {
		return fmt.Errorf("opencode: remote AGENTMAN_OPENCODE_URL must use HTTPS")
	}
	return nil
}

func openCodeLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
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
	if s.configurationError != nil {
		return nil, s.configurationError
	}
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
	if s.configurationError != nil {
		return false
	}
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
	// A password is a credential, not a discovery token. Sending it to every
	// process that happens to bind one of the watched ports lets an unrelated
	// local process steal it. Authenticated servers must therefore be pinned
	// explicitly with AGENTMAN_OPENCODE_URL; only that endpoint receives auth.
	if s.password != "" {
		return nil
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
	if s.configurationError != nil {
		return nil, s.configurationError
	}
	s.mu.RLock()
	previous := make(map[string]openCodeSession, len(s.sessions))
	previousBases := make(map[string]struct{})
	for id, session := range s.sessions {
		previous[id] = session
		previousBases[session.baseURL] = struct{}{}
	}
	misses := make(map[string]int, len(s.serverMisses))
	for base, count := range s.serverMisses {
		misses[base] = count
	}
	s.mu.RUnlock()

	// No server means OpenCode simply is not being used. Not an error, and it
	// must stay cheap — this runs every second.
	servers := s.findServers(ctx)
	if len(servers) == 0 {
		if len(previous) == 0 {
			return nil, nil
		}
		kept := make(map[string]openCodeSession)
		for base := range previousBases {
			misses[base]++
			if misses[base] > openCodeHealthMissGrace {
				delete(misses, base)
				continue
			}
			for id, session := range previous {
				if session.baseURL == base {
					kept[id] = session
				}
			}
		}
		s.mu.Lock()
		s.sessions = kept
		s.serverMisses = misses
		s.mu.Unlock()
		if len(kept) == 0 {
			return nil, nil
		}
		found := openCodeMetas(kept)
		return found, fmt.Errorf("opencode: all known server health probes missed")
	}

	healthyServers := make(map[string]struct{}, len(servers))
	for _, base := range servers {
		healthyServers[base] = struct{}{}
	}
	// Health scanning returns only responsive servers. Preserve each omitted
	// known server independently for a short grace period, even when another
	// OpenCode process is healthy in the same sweep.
	missedServers := make(map[string]struct{})
	for base := range previousBases {
		if _, healthy := healthyServers[base]; healthy {
			continue
		}
		misses[base]++
		if misses[base] <= openCodeHealthMissGrace {
			missedServers[base] = struct{}{}
		} else {
			delete(misses, base)
		}
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
			misses[base]++
			if misses[base] <= openCodeHealthMissGrace {
				failedServers[base] = struct{}{}
			} else {
				delete(misses, base)
			}
			continue
		}
		delete(misses, base)
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
				model, _ = s.models.get(id)
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
	for base := range missedServers {
		failures = append(failures, fmt.Errorf("opencode: %s health probe missed", base))
	}

	if reached == 0 {
		kept := make(map[string]openCodeSession)
		for id, previousSession := range previous {
			// Both sets are still inside their grace window, exactly as in the
			// partial-failure branch below. Keying on failedServers alone made a
			// total outage discard state that a partial one preserves, which is
			// backwards: the worse the sweep, the less it should be trusted to
			// declare a session gone.
			_, failed := failedServers[previousSession.baseURL]
			_, missed := missedServers[previousSession.baseURL]
			if failed || missed {
				kept[id] = previousSession
			}
		}
		s.mu.Lock()
		s.sessions = kept
		s.serverMisses = misses
		s.mu.Unlock()
		// openCodeMetas returns a non-nil empty slice. That distinction tells the
		// registry a sustained failure has expired rather than asking it to
		// restore its own last snapshot forever.
		return openCodeMetas(kept), errors.Join(failures...)
	}
	if len(failedServers) > 0 || len(missedServers) > 0 {
		// A server answered its health probe but one of the four snapshot calls
		// failed. Preserve that server's last routes for this sweep; otherwise a
		// transient API error announces every session on it as gone and tears
		// down active subscriptions even while other OpenCode servers are fine.
		for id, previousSession := range previous {
			_, failed := failedServers[previousSession.baseURL]
			_, missed := missedServers[previousSession.baseURL]
			if !failed && !missed {
				continue
			}
			current, exists := next[id]
			if !exists || openCodeRoutePriority(previousSession) > openCodeRoutePriority(current) {
				next[id] = previousSession
			}
		}
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
	s.serverMisses = misses
	s.mu.Unlock()

	found := openCodeMetas(next)
	if len(failures) > 0 {
		return found, errors.Join(failures...)
	}
	return found, nil
}

func openCodeMetas(sessions map[string]openCodeSession) []protocol.Session {
	found := make([]protocol.Session, 0, len(sessions))
	for _, session := range sessions {
		found = append(found, session.meta)
	}
	SortSessions(found)
	return found
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
	if len(listed) > maxOpenCodeSessions {
		return nil, nil, nil, nil, fmt.Errorf(
			"%s/session?limit=200: returned %d sessions, maximum is %d",
			base, len(listed), maxOpenCodeSessions,
		)
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
	if len(directories) > maxOpenCodeDirectories {
		return nil, nil, nil, nil, fmt.Errorf(
			"%s/session?limit=200: returned %d active directories, maximum is %d",
			base, len(directories), maxOpenCodeDirectories,
		)
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

	type snapshotJob struct {
		scopeIndex   int
		requestIndex int
		path         string
		out          any
	}
	errs := make([]error, len(scopes)*3)
	jobs := make(chan snapshotJob)
	var wait sync.WaitGroup
	workers := min(maxOpenCodeSnapshotWorkers, len(scopes)*3)
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for job := range jobs {
				if _, err := s.doAt(ctx, base, http.MethodGet, job.path, nil, job.out); err != nil {
					errs[job.scopeIndex*3+job.requestIndex] = fmt.Errorf(
						"%s%s: %w", base, job.path, err,
					)
				}
			}
		}()
	}
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
			request := requests[requestIndex]
			jobs <- snapshotJob{
				scopeIndex: index, requestIndex: requestIndex,
				path: request.path, out: request.out,
			}
		}
	}
	close(jobs)
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
			{Key: "once", Label: "Allow once", Description: "Approve only this request."},
		}
		if len(request.Always) > 0 {
			options = append(options, protocol.QuestionOption{
				Key: "always", Label: "Always allow",
				Description: "Persist for: " + clipRunes(strings.Join(request.Always, ", "), 400),
			})
		}
		options = append(options, protocol.QuestionOption{
			Key: "reject", Label: "Reject", Description: "Deny this request.",
		})
		detail := openCodePermissionDetail(request)
		prompt := "Allow this action?"
		if request.Permission != "" {
			prompt = "Allow " + request.Permission + "?"
		}
		return &protocol.Question{
			ID:    "permission-" + request.ID,
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
				ID:     fmt.Sprintf("question-%s-%d", request.ID, index),
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

func openCodePermissionDetail(request ocPermissionRequest) string {
	parts := make([]string, 0, 3)
	if len(request.Patterns) > 0 {
		parts = append(parts, "Requested scope:\n"+strings.Join(request.Patterns, "\n"))
	}
	if len(request.Metadata) > 0 {
		if encoded, err := json.MarshalIndent(request.Metadata, "", "  "); err == nil {
			parts = append(parts, "Context:\n"+string(encoded))
		}
	}
	if len(request.Always) > 0 {
		parts = append(parts, "Persistent scope:\n"+strings.Join(request.Always, "\n"))
	}
	return clipRunes(strings.Join(parts, "\n\n"), maxOpenCodeQuestionDetail)
}

func clipRunes(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	if limit == 1 {
		return "…"
	}
	return string(runes[:limit-1]) + "…"
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
	if model := openCodeModel(response); model != "" {
		s.models.put(sessionID, model)
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
	seen := map[string]openCodeSeenMessage{}
	var generation uint64
	// Prime from the current tail so the backlog is not replayed as new.
	if page, err := s.Page(ctx, sessionID, "", 50); err == nil {
		generation++
		_ = updateOpenCodeSeen(seen, page.Messages, generation)
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
			generation++
			batch := updateOpenCodeSeen(seen, page.Messages, generation)
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

type openCodeSeenMessage struct {
	fingerprint string
	generation  uint64
}

func updateOpenCodeSeen(
	seen map[string]openCodeSeenMessage,
	messages []protocol.Message,
	generation uint64,
) []protocol.Message {
	var changed []protocol.Message
	for _, message := range messages {
		fingerprint := messageFingerprint(message)
		previous, known := seen[message.ID]
		seen[message.ID] = openCodeSeenMessage{
			fingerprint: fingerprint,
			generation:  generation,
		}
		if !known || previous.fingerprint != fingerprint {
			// Send anything whose content has moved: a new message, a reply
			// that has grown, or a tool that has settled. The app merges by id.
			changed = append(changed, message)
		}
	}
	// Page returns only the recent tail. Keeping two prior windows is enough to
	// deduplicate overlap while bounding memory for a subscription that runs
	// through hundreds of thousands of messages.
	for id, entry := range seen {
		if entry.generation+2 < generation {
			delete(seen, id)
		}
	}
	return changed
}

// openCodeModel reports the model behind the newest reply in an API page.
// Discovery never makes a per-session message request merely to populate this
// cosmetic label: Page and Follow already read messages and update the cache,
// avoiding hundreds of sequential five-second calls during a slow sweep.
func openCodeModel(response []ocMessage) string {
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
	if session.meta.Question == nil || answer.QuestionID == "" ||
		answer.QuestionID != session.meta.Question.ID {
		return fmt.Errorf("source: that question is no longer current; refresh the session")
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
		// recordQuestionAnswer advances immediately, before the next discovery
		// sweep updates session.meta. Reject a second device or double tap still
		// carrying the prior card's revision instead of overwriting its answer.
		if next := s.nextQuestionIndex(pending.answerKey, len(current.Questions)); next != pending.questionIndex {
			return fmt.Errorf("source: that question is no longer current; refresh the session")
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
