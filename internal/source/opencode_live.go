package source

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// OpenCode 2's service tells every client what a turn is doing as it
// happens: GET /api/event streams the reply a few words at a time, and each
// tool from the moment the model starts writing its call. What it saves is
// far behind. A reply's text is saved empty until the reply is finished, so
// a follow that reads only saved messages gets every reply whole, at the end.
//
// openCodeLive holds one event stream per service, open while discovery
// finds the service, and keeps the parts of the replies being written as
// rows under the ids their saved message will give them (see
// openCodeMessages and openCodeServiceMessage), so the saved rows replace
// them in place. It covers every session on the service: the OpenCode
// terminal app's as well as Agentman's.
//
// The service saves a message's parts in the order they start: reasoning,
// text and tool calls each take the next place, and a text part with no id
// of its own is known by its place. The stream says when each starts, which
// is how a preview knows its place. A message whose start the stream missed
// (Agentman started mid-reply, or the stream dropped) cannot know them, so
// its text is left to the saved message, as before; its tools are known by
// their call ids and still show.

// openCodeLiveTypes are the events a stream asks for. Reasoning deltas are
// left out: thinking is not shown, and they are most of the traffic. Its
// start still counts, because it takes a place in the message.
var openCodeLiveTypes = []string{
	"session.step.started",
	"session.reasoning.started",
	"session.text.started", "session.text.delta", "session.text.ended",
	"session.tool.input.started", "session.tool.input.ended",
	"session.tool.called", "session.tool.success", "session.tool.failed",
	"session.execution.succeeded", "session.execution.failed", "session.execution.interrupted",
}

const (
	// openCodeLiveLinger is how long a finished part stays a preview: long
	// enough for a follow to read the saved message that replaces it, after
	// which a preview nothing replaced is taken back.
	openCodeLiveLinger = 3 * time.Second
	// openCodeLiveForget is how long a stream outlives the last discovery
	// sweep that found its service.
	openCodeLiveForget = 30 * time.Second
	// maxOpenCodeLiveLine bounds one line of the stream. A longer one (a
	// tool's whole output) is skipped: previews never need it.
	maxOpenCodeLiveLine = 8 << 20
	// maxOpenCodeLiveText bounds one part's preview.
	maxOpenCodeLiveText = 1 << 20
	openCodeLiveRetry   = time.Second
	openCodeLiveBackoff = 15 * time.Second
)

// openCodeLive is every service's stream.
type openCodeLive struct {
	mu      sync.Mutex
	streams map[string]*openCodeLiveStream
	// connect opens a service's event stream; replaceable in tests.
	connect func(ctx context.Context, base, password string) (io.ReadCloser, error)
	now     func() time.Time
}

type openCodeLiveStream struct {
	cancel context.CancelFunc
	seen   time.Time
	// sessions are the replies being written, by OpenCode's session id.
	sessions map[string]*openCodeLiveSession
	// watchers are told when a session's previews change.
	watchers map[string]map[chan struct{}]struct{}
}

type openCodeLiveSession struct {
	order    []string
	messages map[string]*openCodeLiveMessage
}

// openCodeLiveMessage is one assistant message being written.
type openCodeLiveMessage struct {
	id string
	ts int64
	// partial says the stream missed its start, so its places are unknown.
	partial bool
	parts   []*openCodeLivePart
	// ended is when its turn ended, for a message that got no parts.
	ended time.Time
}

type openCodeLivePart struct {
	kind    string // "reasoning", "text" or "tool"
	ordinal int    // a text or reasoning part's number among its kind
	text    strings.Builder
	callID  string
	tool    string
	input   json.RawMessage
	status  protocol.ToolStatus
	ended   time.Time
}

func newOpenCodeLive() *openCodeLive {
	return &openCodeLive{
		streams: map[string]*openCodeLiveStream{},
		connect: openCodeConnectEvents,
		now:     time.Now,
	}
}

// openCodeLiveClient has no timeout: the stream lasts as long as the service.
var openCodeLiveClient = &http.Client{}

func openCodeConnectEvents(ctx context.Context, base, password string) (io.ReadCloser, error) {
	query := url.Values{"types": openCodeLiveTypes}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/event?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream")
	req.SetBasicAuth(openCodeServiceUser, password)
	resp, err := openCodeLiveClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("opencode: GET /api/event: %s", resp.Status)
	}
	return resp.Body, nil
}

// keepLive holds a stream open to each OpenCode 2 service discovery found.
func (s *OpenCodeSource) keepLive() {
	s.scanMu.Lock()
	services := make(map[string]string, len(s.services))
	for base, password := range s.services {
		services[base] = password
	}
	s.scanMu.Unlock()
	s.live.keep(services)
}

// keep holds a stream open to each service found, by base URL to password,
// and closes the ones not found for a while.
func (l *openCodeLive) keep(services map[string]string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	for base, password := range services {
		if stream, ok := l.streams[base]; ok {
			stream.seen = now
			continue
		}
		ctx, cancel := context.WithCancel(context.Background())
		l.streams[base] = &openCodeLiveStream{
			cancel: cancel, seen: now,
			sessions: map[string]*openCodeLiveSession{},
			watchers: map[string]map[chan struct{}]struct{}{},
		}
		go l.run(ctx, base, password)
	}
	for base, stream := range l.streams {
		if _, found := services[base]; !found && now.Sub(stream.seen) > openCodeLiveForget {
			stream.cancel()
			delete(l.streams, base)
		}
	}
}

// stop closes every stream.
func (l *openCodeLive) stop() {
	l.mu.Lock()
	defer l.mu.Unlock()
	for base, stream := range l.streams {
		stream.cancel()
		delete(l.streams, base)
	}
}

// run reads one service's stream until ctx ends, reconnecting when it drops.
func (l *openCodeLive) run(ctx context.Context, base, password string) {
	wait := openCodeLiveRetry
	for ctx.Err() == nil {
		body, err := l.connect(ctx, base, password)
		if err == nil {
			// What was being written when the stream dropped has a gap.
			l.reset(base)
			l.read(ctx, base, body)
			body.Close()
			wait = openCodeLiveRetry
		} else {
			wait = min(wait*2, openCodeLiveBackoff)
		}
		select {
		case <-ctx.Done():
		case <-time.After(wait):
		}
	}
}

// read hands each event on the stream to apply. They are server-sent
// events, one JSON object per data line.
func (l *openCodeLive) read(ctx context.Context, base string, body io.Reader) {
	reader := bufio.NewReaderSize(body, 64<<10)
	var line []byte
	tooLong := false
	for ctx.Err() == nil {
		chunk, err := reader.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			// Part of a long line. One too long to keep (a tool's whole
			// output) is skipped: previews never need it.
			if !tooLong && len(line)+len(chunk) <= maxOpenCodeLiveLine {
				line = append(line, chunk...)
			} else {
				tooLong, line = true, nil
			}
			continue
		}
		if err != nil {
			return
		}
		if !tooLong {
			line = append(line, chunk...)
			if payload, ok := strings.CutPrefix(string(line), "data:"); ok && len(line) <= maxOpenCodeLiveLine {
				l.apply(base, []byte(strings.TrimSpace(payload)))
			}
		}
		line, tooLong = line[:0], false
	}
}

type ocLiveEvent struct {
	Type string `json:"type"`
	Data struct {
		SessionID          string          `json:"sessionID"`
		AssistantMessageID string          `json:"assistantMessageID"`
		Started            float64         `json:"started"`
		Ordinal            int             `json:"ordinal"`
		Delta              string          `json:"delta"`
		Text               string          `json:"text"`
		ID                 string          `json:"id"`
		Name               string          `json:"name"`
		Input              json.RawMessage `json:"input"`
	} `json:"data"`
}

// apply folds one event into the previews.
func (l *openCodeLive) apply(base string, raw []byte) {
	var event ocLiveEvent
	if json.Unmarshal(raw, &event) != nil || event.Data.SessionID == "" {
		return
	}
	data := event.Data
	l.mu.Lock()
	defer l.mu.Unlock()
	stream, ok := l.streams[base]
	if !ok {
		return
	}
	now := l.now()
	session := stream.sessions[data.SessionID]
	if session == nil {
		if strings.HasPrefix(event.Type, "session.execution.") {
			return
		}
		session = &openCodeLiveSession{messages: map[string]*openCodeLiveMessage{}}
		stream.sessions[data.SessionID] = session
	}
	if strings.HasPrefix(event.Type, "session.execution.") {
		// The turn is over: whatever was still being written stops here. A
		// session no follow watches has nothing left worth keeping.
		if len(stream.watchers[data.SessionID]) == 0 {
			delete(stream.sessions, data.SessionID)
			return
		}
		for _, message := range session.messages {
			if message.ended.IsZero() {
				message.ended = now
			}
			for _, part := range message.parts {
				if part.ended.IsZero() {
					part.ended = now
				}
			}
		}
		stream.wake(data.SessionID)
		return
	}
	if data.AssistantMessageID == "" {
		return
	}
	message := session.messages[data.AssistantMessageID]
	if message == nil {
		message = &openCodeLiveMessage{
			id: data.AssistantMessageID, ts: now.UnixMilli(),
			partial: event.Type != "session.step.started",
		}
		session.messages[message.id] = message
		session.order = append(session.order, message.id)
	}
	switch event.Type {
	case "session.step.started":
		if data.Started > 0 {
			message.ts = int64(data.Started)
		}
	case "session.reasoning.started":
		message.parts = append(message.parts, &openCodeLivePart{kind: "reasoning", ordinal: data.Ordinal})
	case "session.text.started":
		message.parts = append(message.parts, &openCodeLivePart{kind: "text", ordinal: data.Ordinal})
	case "session.text.delta", "session.text.ended":
		part := message.find("text", data.Ordinal, "")
		if part == nil {
			// Its start was missed, so its place is unknown.
			message.partial = true
			part = &openCodeLivePart{kind: "text", ordinal: data.Ordinal}
			message.parts = append(message.parts, part)
		}
		if event.Type == "session.text.ended" {
			part.text.Reset()
			part.text.WriteString(clipRunes(data.Text, maxOpenCodeLiveText))
			part.ended = now
		} else if part.ended.IsZero() && part.text.Len()+len(data.Delta) <= maxOpenCodeLiveText {
			part.text.WriteString(data.Delta)
		}
	case "session.tool.input.started", "session.tool.input.ended", "session.tool.called",
		"session.tool.success", "session.tool.failed":
		if data.ID == "" {
			return
		}
		part := message.find("tool", 0, data.ID)
		if part == nil {
			part = &openCodeLivePart{kind: "tool", callID: data.ID, status: protocol.ToolRunning}
			message.parts = append(message.parts, part)
		}
		if data.Name != "" {
			part.tool = data.Name
		}
		switch event.Type {
		case "session.tool.input.ended":
			if json.Valid([]byte(data.Text)) {
				part.input = json.RawMessage(data.Text)
			}
		case "session.tool.called":
			if len(data.Input) > 0 {
				part.input = data.Input
			}
		case "session.tool.success":
			part.status, part.ended = protocol.ToolOK, now
		case "session.tool.failed":
			part.status, part.ended = protocol.ToolError, now
		}
	default:
		return
	}
	stream.wake(data.SessionID)
}

func (m *openCodeLiveMessage) find(kind string, ordinal int, callID string) *openCodeLivePart {
	for _, part := range m.parts {
		if part.kind == kind && part.ordinal == ordinal && part.callID == callID {
			return part
		}
	}
	return nil
}

// wake tells a session's watchers its previews changed. Called with l.mu.
func (st *openCodeLiveStream) wake(nativeID string) {
	for ch := range st.watchers[nativeID] {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// reset forgets what a stream was showing, when it connects afresh.
func (l *openCodeLive) reset(base string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if stream, ok := l.streams[base]; ok {
		for nativeID := range stream.sessions {
			delete(stream.sessions, nativeID)
			stream.wake(nativeID)
		}
	}
}

// watch returns a channel that is told when a session's previews change, and
// the function that stops it. A service with no stream never tells it.
func (l *openCodeLive) watch(base, nativeID string) (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	l.mu.Lock()
	defer l.mu.Unlock()
	stream, ok := l.streams[base]
	if !ok {
		return nil, func() {}
	}
	if stream.watchers[nativeID] == nil {
		stream.watchers[nativeID] = map[chan struct{}]struct{}{}
	}
	stream.watchers[nativeID][ch] = struct{}{}
	return ch, func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		delete(stream.watchers[nativeID], ch)
		if len(stream.watchers[nativeID]) == 0 {
			delete(stream.watchers, nativeID)
		}
	}
}

// rows are a session's previews, oldest first. Parts finished longer ago
// than openCodeLiveLinger are dropped, and with them messages and sessions
// left with none.
func (l *openCodeLive) rows(base, nativeID, sessionID string) []protocol.Message {
	l.mu.Lock()
	defer l.mu.Unlock()
	stream, ok := l.streams[base]
	if !ok {
		return nil
	}
	session := stream.sessions[nativeID]
	if session == nil {
		return nil
	}
	now := l.now()
	expired := func(at time.Time) bool { return !at.IsZero() && now.Sub(at) > openCodeLiveLinger }
	var rows []protocol.Message
	order := session.order[:0]
	for _, id := range session.order {
		message := session.messages[id]
		alive := len(message.parts) == 0 && !expired(message.ended)
		// A part's place counts every part before it, finished ones too.
		for place, part := range message.parts {
			if expired(part.ended) {
				continue
			}
			alive = true
			switch part.kind {
			case "text":
				text := strings.TrimSpace(part.text.String())
				if message.partial || text == "" {
					continue
				}
				rows = append(rows, protocol.Message{
					ID: message.id + ":" + strconv.Itoa(place), SessionID: sessionID,
					Role: protocol.RoleAssistant, Ts: message.ts, Text: text,
				})
			case "tool":
				name := part.tool
				if name == "" {
					name = "tool"
				}
				call := ocPart{Tool: name}
				call.State.Input = part.input
				rows = append(rows, protocol.Message{
					ID: message.id + ":" + part.callID, SessionID: sessionID,
					Role: protocol.RoleTool, Ts: message.ts,
					Tool: &protocol.Tool{Name: name, Summary: openCodeToolSummary(call), Status: part.status},
				})
			}
		}
		if !alive {
			delete(session.messages, id)
			continue
		}
		order = append(order, id)
	}
	session.order = order
	if len(session.order) == 0 {
		delete(stream.sessions, nativeID)
	}
	return rows
}

// openCodeLiveFollow is one follow's previews: which it has sent, and which
// rows the saved messages already have.
type openCodeLiveFollow struct {
	sessionID string
	saved     map[string]bool
	sent      map[string]string
}

func newOpenCodeLiveFollow(sessionID string) *openCodeLiveFollow {
	return &openCodeLiveFollow{sessionID: sessionID, saved: map[string]bool{}, sent: map[string]string{}}
}

// read notes the rows a read of the saved messages found.
func (f *openCodeLiveFollow) read(saved []protocol.Message) {
	f.saved = make(map[string]bool, len(saved))
	for _, row := range saved {
		f.saved[row.ID] = true
	}
}

// merge adds the previews that changed to batch. A saved row settles its
// preview; a preview that ends with no saved row under its id is taken back.
func (f *openCodeLiveFollow) merge(batch, live []protocol.Message) []protocol.Message {
	current := make(map[string]bool, len(live))
	for _, row := range live {
		if f.saved[row.ID] {
			continue
		}
		current[row.ID] = true
		fingerprint := messageFingerprint(row)
		if f.sent[row.ID] == fingerprint {
			continue
		}
		f.sent[row.ID] = fingerprint
		batch = append(batch, row)
	}
	for id := range f.sent {
		if current[id] {
			continue
		}
		if !f.saved[id] {
			batch = append(batch, withdrawnPreview(f.sessionID, id))
		}
		delete(f.sent, id)
	}
	return batch
}
