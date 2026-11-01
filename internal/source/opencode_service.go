package source

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// OpenCode 2 replaced the server each `opencode` ran on a port of its own
// with one background service for every project (`opencode service`). It
// says where that service is, and its password, in a file of its own; it
// serves its API under /api, behind the password; and its sessions,
// messages, questions ("forms") and permissions have shapes of their own.
//
// The adapter speaks OpenCode 1's API. A request to a 2 service is
// translated here, there and back, so all of the adapter above the requests
// works the same for both.

// maxOpenCodeServiceFile bounds service.json, a few lines of JSON.
const maxOpenCodeServiceFile = 8 << 10

// openCodeServiceUser is the name OpenCode 2's service takes with its
// password.
const openCodeServiceUser = "opencode"

// openCodeService is what service.json says.
type openCodeService struct {
	URL      string `json:"url"`
	Password string `json:"password"`
}

// openCodeServiceFile is where OpenCode 2 writes service.json:
// $XDG_STATE_HOME/opencode, or ~/.local/state/opencode.
func openCodeServiceFile() string {
	if state := os.Getenv("XDG_STATE_HOME"); state != "" {
		return filepath.Join(state, "opencode", "service.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "state", "opencode", "service.json")
}

// readOpenCodeService reads service.json. Its password goes only to the URL
// beside it, and only when that URL is this Mac's own.
func readOpenCodeService(path string) (openCodeService, bool) {
	if path == "" {
		return openCodeService{}, false
	}
	file, err := os.Open(path)
	if err != nil {
		return openCodeService{}, false
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maxOpenCodeServiceFile+1))
	if err != nil || len(raw) > maxOpenCodeServiceFile {
		return openCodeService{}, false
	}
	var service openCodeService
	if json.Unmarshal(raw, &service) != nil || service.Password == "" {
		return openCodeService{}, false
	}
	parsed, err := url.Parse(service.URL)
	if err != nil || parsed.Scheme != "http" || !openCodeLoopbackHost(parsed.Hostname()) || parsed.Port() == "" ||
		parsed.User != nil || parsed.RawQuery != "" || (parsed.Path != "" && parsed.Path != "/") {
		return openCodeService{}, false
	}
	service.URL = strings.TrimRight(service.URL, "/")
	return service, true
}

// findServices returns OpenCode 2's service when one is running here.
func (s *OpenCodeSource) findServices(ctx context.Context) []string {
	service, ok := readOpenCodeService(s.serviceFile)
	if ok {
		var info struct {
			Version string `json:"version"`
		}
		_, err := s.send(ctx, http.MethodGet, service.URL+"/api/info", openCodeServiceUser, service.Password,
			nil, &info, maxOpenCodeHealthResponse)
		ok = err == nil && info.Version != ""
	}
	s.scanMu.Lock()
	defer s.scanMu.Unlock()
	if !ok {
		s.services = nil
		return nil
	}
	s.services = map[string]string{service.URL: service.Password}
	return []string{service.URL}
}

// servicePassword is the password of the OpenCode 2 service at base, when
// base is one.
// serviceSkills is the skills OpenCode 2's service has for a folder, by id.
// A folder it hasn't seen yet can list none at first, so an empty list is
// asked for again, briefly.
func (s *OpenCodeSource) serviceSkills(ctx context.Context, base, password string, location url.Values) map[string]string {
	known := map[string]string{}
	for attempt := 0; attempt < 3; attempt++ {
		var listed struct {
			Data []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"data"`
		}
		// Each skill comes with its whole text: a long list.
		if _, err := s.send(ctx, http.MethodGet, base+openCodePath("/api/skill", location), openCodeServiceUser, password,
			nil, &listed, maxOpenCodeSkillList); err != nil {
			return known
		}
		for _, skill := range listed.Data {
			known[skill.ID] = skill.Name
		}
		if len(known) > 0 {
			return known
		}
		select {
		case <-ctx.Done():
			return known
		case <-time.After(700 * time.Millisecond):
		}
	}
	return known
}

// maxOpenCodeSkillList bounds the service's skill list, every skill's text
// in it.
const maxOpenCodeSkillList = 16 << 20

func (s *OpenCodeSource) servicePassword(base string) (string, bool) {
	s.scanMu.Lock()
	defer s.scanMu.Unlock()
	password, ok := s.services[base]
	return password, ok
}

// errOpenCodeService is the answer to a request OpenCode 2 has nothing for.
var errOpenCodeService = errors.New("opencode: not available from OpenCode 2's service")

// doService sends an OpenCode 1 request to an OpenCode 2 service, and
// answers in OpenCode 1's shapes.
func (s *OpenCodeSource) doService(
	ctx context.Context, base, password, method, path string, body, out any, limit int64,
) (http.Header, error) {
	parsed, err := url.Parse(path)
	if err != nil {
		return nil, err
	}
	query := parsed.Query()
	// OpenCode 1's ?directory= is OpenCode 2's location.
	location := url.Values{}
	if directory := query.Get("directory"); directory != "" {
		location.Set("location[directory]", directory)
	}
	call := func(method, path string, query url.Values, body, out any) error {
		_, err := s.send(ctx, method, base+openCodePath(path, query), openCodeServiceUser, password, body, out, limit)
		return err
	}
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")
	is := func(want string, parts ...string) bool { return method == want && slices.Equal(segments, parts) }
	sessionCall := func(want, last string) (string, bool) {
		if method != want || len(segments) != 3 || segments[0] != "session" || segments[2] != last {
			return "", false
		}
		id, err := url.PathUnescape(segments[1])
		return id, err == nil && id != ""
	}
	replyCall := func(kind string) (string, bool) {
		if method != http.MethodPost || len(segments) != 3 || segments[0] != kind || segments[2] != "reply" {
			return "", false
		}
		id, err := url.PathUnescape(segments[1])
		return id, err == nil && id != ""
	}

	switch {
	case is(http.MethodGet, "global", "health"):
		var info struct {
			Version string `json:"version"`
		}
		if err := call(http.MethodGet, "/api/info", nil, nil, &info); err != nil {
			return nil, err
		}
		return nil, answerAs(out, map[string]bool{"healthy": info.Version != ""})

	case is(http.MethodGet, "session"):
		page := url.Values{}
		if value := query.Get("limit"); value != "" {
			page.Set("limit", value)
		}
		var listed struct {
			Data []json.RawMessage `json:"data"`
		}
		if err := call(http.MethodGet, "/api/session", page, nil, &listed); err != nil {
			return nil, err
		}
		return nil, answerAs(out, openCodeServiceSessions(listed.Data))

	case is(http.MethodGet, "session", "status"):
		var active struct {
			Data map[string]struct {
				Type string `json:"type"`
			} `json:"data"`
		}
		if err := call(http.MethodGet, "/api/session/active", nil, nil, &active); err != nil {
			return nil, err
		}
		statuses := make(map[string]ocSessionStatus, len(active.Data))
		for id := range active.Data {
			statuses[id] = ocSessionStatus{Type: "busy"}
		}
		return nil, answerAs(out, statuses)

	case is(http.MethodGet, "question"):
		forms, err := s.serviceForms(call, location)
		if err != nil {
			return nil, err
		}
		questions := make([]ocQuestionRequest, 0, len(forms))
		for _, form := range forms {
			questions = append(questions, form.request())
		}
		return nil, answerAs(out, questions)

	case is(http.MethodGet, "permission"):
		requests, err := s.servicePermissions(call, location)
		if err != nil {
			return nil, err
		}
		permissions := make([]ocPermissionRequest, 0, len(requests))
		for _, request := range requests {
			permissions = append(permissions, request.request())
		}
		return nil, answerAs(out, permissions)

	case is(http.MethodGet, "command"):
		var listed struct {
			Data []ocCommand `json:"data"`
		}
		if err := call(http.MethodGet, "/api/command", location, nil, &listed); err != nil {
			return nil, err
		}
		return nil, answerAs(out, listed.Data)

	case is(http.MethodGet, "config", "providers"):
		var models struct {
			Data []struct {
				ID         string `json:"id"`
				ModelID    string `json:"modelID"`
				ProviderID string `json:"providerID"`
				Status     string `json:"status"`
			} `json:"data"`
		}
		if err := call(http.MethodGet, "/api/model", nil, nil, &models); err != nil {
			return nil, err
		}
		type model struct {
			ID     string `json:"id"`
			Status string `json:"status,omitempty"`
		}
		type provider struct {
			ID     string           `json:"id"`
			Models map[string]model `json:"models"`
		}
		var providers []provider
		index := map[string]int{}
		for _, item := range models.Data {
			id := item.ModelID
			if id == "" {
				id = item.ID
			}
			if item.ProviderID == "" || id == "" {
				continue
			}
			at, ok := index[item.ProviderID]
			if !ok {
				at = len(providers)
				index[item.ProviderID] = at
				providers = append(providers, provider{ID: item.ProviderID, Models: map[string]model{}})
			}
			providers[at].Models[id] = model{ID: id, Status: item.Status}
		}
		return nil, answerAs(out, map[string]any{"providers": providers})
	}

	if id, ok := sessionCall(http.MethodGet, "message"); ok {
		// OpenCode 2 pages newest first, and on with the cursor it gave; the
		// adapter wants a page in time order and the cursor to the one before.
		page := url.Values{}
		if value := query.Get("limit"); value != "" {
			page.Set("limit", value)
		}
		if before := query.Get("before"); before != "" {
			page.Set("cursor", before)
		} else {
			page.Set("order", "desc")
		}
		var listed struct {
			Data   []json.RawMessage `json:"data"`
			Cursor struct {
				Next *string `json:"next"`
			} `json:"cursor"`
		}
		if err := call(http.MethodGet, "/api/session/"+openCodeSegment(id)+"/message", page, nil, &listed); err != nil {
			return nil, err
		}
		messages := make([]ocMessage, 0, len(listed.Data))
		for index := len(listed.Data) - 1; index >= 0; index-- {
			if message, ok := openCodeServiceMessage(listed.Data[index]); ok {
				messages = append(messages, message)
			}
		}
		headers := http.Header{}
		if listed.Cursor.Next != nil && *listed.Cursor.Next != "" && len(listed.Data) > 0 {
			headers.Set("X-Next-Cursor", *listed.Cursor.Next)
		}
		return headers, answerAs(out, messages)
	}

	if id, ok := sessionCall(http.MethodPost, "prompt_async"); ok {
		var prompt struct {
			Parts []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"parts"`
			Model *struct {
				ProviderID string `json:"providerID"`
				ModelID    string `json:"modelID"`
			} `json:"model"`
			// The ids of the skills the message names (InjectWithSkills).
			Skills []string `json:"skills"`
		}
		if err := reshape(body, &prompt); err != nil {
			return nil, err
		}
		var texts []string
		for _, part := range prompt.Parts {
			if part.Type == "text" {
				texts = append(texts, part.Text)
			}
		}
		// The model chosen on the phone is the session's in OpenCode 2, not
		// each message's.
		if prompt.Model != nil && prompt.Model.ModelID != "" {
			if err := call(http.MethodPost, "/api/session/"+openCodeSegment(id)+"/model", nil, map[string]any{
				"model": map[string]string{"id": prompt.Model.ModelID, "providerID": prompt.Model.ProviderID},
			}, nil); err != nil {
				return nil, err
			}
		}
		payload := map[string]any{"text": strings.Join(texts, "\n")}
		if len(prompt.Skills) > 0 {
			// Only skills the service has for the folder: one it doesn't
			// know fails the whole message. The words name it anyway.
			known := s.serviceSkills(ctx, base, password, location)
			var attach []map[string]string
			for _, skill := range prompt.Skills {
				if name, ok := known[skill]; ok {
					attach = append(attach, map[string]string{"id": skill, "name": name})
				}
			}
			if len(attach) > 0 {
				payload["skills"] = attach
			}
		}
		return nil, call(http.MethodPost, "/api/session/"+openCodeSegment(id)+"/prompt", nil, payload, nil)
	}

	if id, ok := sessionCall(http.MethodPost, "command"); ok {
		var command struct {
			Command   string `json:"command"`
			Arguments string `json:"arguments"`
		}
		if err := reshape(body, &command); err != nil {
			return nil, err
		}
		return nil, call(http.MethodPost, "/api/session/"+openCodeSegment(id)+"/command", nil,
			map[string]string{"name": command.Command, "text": command.Arguments}, nil)
	}

	if id, ok := sessionCall(http.MethodPost, "summarize"); ok {
		return nil, call(http.MethodPost, "/api/session/"+openCodeSegment(id)+"/compact", nil, map[string]any{}, nil)
	}

	if id, ok := sessionCall(http.MethodPost, "abort"); ok {
		return nil, call(http.MethodPost, "/api/session/"+openCodeSegment(id)+"/interrupt", nil, nil, nil)
	}

	if id, ok := replyCall("permission"); ok {
		var reply struct {
			Reply string `json:"reply"`
		}
		if err := reshape(body, &reply); err != nil {
			return nil, err
		}
		requests, err := s.servicePermissions(call, location)
		if err != nil {
			return nil, err
		}
		for _, request := range requests {
			if request.ID == id {
				return nil, call(http.MethodPost, "/api/session/"+openCodeSegment(request.SessionID)+"/permission/"+
					openCodeSegment(id)+"/reply", nil, map[string]string{"decision": reply.Reply}, nil)
			}
		}
		return nil, errors.New("opencode: that permission is no longer waiting for an answer")
	}

	if id, ok := replyCall("question"); ok {
		var reply struct {
			Answers [][]string `json:"answers"`
		}
		if err := reshape(body, &reply); err != nil {
			return nil, err
		}
		forms, err := s.serviceForms(call, location)
		if err != nil {
			return nil, err
		}
		for _, form := range forms {
			if form.ID != id {
				continue
			}
			answer, err := form.answer(reply.Answers)
			if err != nil {
				return nil, err
			}
			return nil, call(http.MethodPost, "/api/session/"+openCodeSegment(form.SessionID)+"/form/"+
				openCodeSegment(id)+"/reply", nil, map[string]any{"answer": answer}, nil)
		}
		return nil, errors.New("opencode: that question is no longer waiting for an answer")
	}

	return nil, fmt.Errorf("%w: %s %s", errOpenCodeService, method, parsed.Path)
}

// answerAs hands a translated answer to the caller, as if it had come from
// the server.
func answerAs(out, value any) error {
	if out == nil {
		return nil
	}
	return reshape(value, out)
}

// reshape copies value into out through JSON.
func reshape(value, out any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

// openCodeServiceSessions are the sessions in OpenCode 1's shape, which
// OpenCode 2's decodes into: its location stands for the directory. A
// subagent's session is its parent's work, not a session of its own.
func openCodeServiceSessions(listed []json.RawMessage) []ocSession {
	sessions := make([]ocSession, 0, len(listed))
	for _, raw := range listed {
		var parent struct {
			ParentID string `json:"parentID"`
		}
		var session ocSession
		if json.Unmarshal(raw, &parent) != nil || parent.ParentID != "" || json.Unmarshal(raw, &session) != nil || session.ID == "" {
			continue
		}
		sessions = append(sessions, session)
	}
	return sessions
}

// openCodeServiceMessage is one of OpenCode 2's messages in OpenCode 1's
// shape: what the person sent, what the model answered and the tools it
// ran, a shell command run from the prompt, and a finished compaction (as
// the role "compaction", which only OpenCode 2 has). The rest (switches of
// model or agent, system notes) is OpenCode's bookkeeping.
func openCodeServiceMessage(raw json.RawMessage) (ocMessage, bool) {
	var message struct {
		ID   string `json:"id"`
		Type string `json:"type"`
		Text string `json:"text"`
		Time struct {
			Created   float64 `json:"created"`
			Completed float64 `json:"completed"`
		} `json:"time"`
		Model struct {
			ID         string `json:"id"`
			ProviderID string `json:"providerID"`
		} `json:"model"`
		Content []struct {
			Type  string          `json:"type"`
			ID    string          `json:"id"`
			Text  string          `json:"text"`
			Name  string          `json:"name"`
			State json.RawMessage `json:"state"`
		} `json:"content"`
		Error   json.RawMessage `json:"error"`
		Command string          `json:"command"`
		Status  string          `json:"status"`
		Output  json.RawMessage `json:"output"`
	}
	if json.Unmarshal(raw, &message) != nil || message.ID == "" {
		return ocMessage{}, false
	}
	var out ocMessage
	out.Info.ID = message.ID
	out.Info.Time.Created = int64(message.Time.Created)
	out.Info.Time.Completed = int64(message.Time.Completed)
	switch message.Type {
	case "user":
		out.Info.Role = "user"
		out.Parts = []ocPart{{ID: "text", Type: "text", Text: message.Text}}
	case "assistant":
		out.Info.Role = "assistant"
		out.Info.ModelID, out.Info.ProviderID = message.Model.ID, message.Model.ProviderID
		out.Info.Error = message.Error
		for index, content := range message.Content {
			part := ocPart{ID: content.ID, Type: content.Type, Text: content.Text}
			if part.ID == "" {
				part.ID = strconv.Itoa(index)
			}
			if content.Type == "tool" {
				part.Tool = content.Name
				openCodeServiceToolState(&part, content.State)
			}
			out.Parts = append(out.Parts, part)
		}
	case "compaction":
		// /compact, or OpenCode compacting on its own, once it is done.
		if message.Status != "completed" {
			return ocMessage{}, false
		}
		out.Info.Role = "compaction"
	case "shell":
		// A command run from the prompt with !, as a tool the person ran.
		out.Info.Role = "assistant"
		part := ocPart{ID: "shell", Type: "tool", Tool: "shell"}
		part.State.Status = map[string]string{"running": "running", "completed": "completed"}[message.Status]
		if part.State.Status == "" {
			part.State.Status = "completed"
		}
		part.State.Input, _ = json.Marshal(map[string]string{"command": message.Command})
		part.State.Output = openCodeServiceText(message.Output)
		out.Parts = []ocPart{part}
	default:
		return ocMessage{}, false
	}
	return out, true
}

// openCodeServiceToolState reads an OpenCode 2 tool's state into OpenCode
// 1's: streaming and running are still under way, and what a tool returned
// is a list of contents rather than one output.
func openCodeServiceToolState(part *ocPart, raw json.RawMessage) {
	var state struct {
		Status   string          `json:"status"`
		Input    json.RawMessage `json:"input"`
		Content  json.RawMessage `json:"content"`
		Metadata struct {
			Title string `json:"title"`
		} `json:"metadata"`
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(raw, &state) != nil {
		return
	}
	part.State.Status = map[string]string{
		"streaming": "pending", "running": "running", "completed": "completed", "error": "error",
	}[state.Status]
	part.State.Input = state.Input
	part.State.Title = state.Metadata.Title
	part.State.Output = openCodeServiceText(state.Content)
	if len(state.Error) > 0 && string(state.Error) != "null" {
		part.State.Error = openCodeErrorText(state.Error)
	}
}

// openCodeServiceText is the text in an OpenCode 2 list of contents, or in
// a value that is text itself.
func openCodeServiceText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var contents []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &contents) == nil {
		var texts []string
		for _, content := range contents {
			if content.Text != "" {
				texts = append(texts, content.Text)
			}
		}
		return strings.Join(texts, "\n")
	}
	var object struct {
		Text   string `json:"text"`
		Output string `json:"output"`
		Stdout string `json:"stdout"`
	}
	if json.Unmarshal(raw, &object) == nil {
		for _, value := range []string{object.Text, object.Output, object.Stdout} {
			if value != "" {
				return value
			}
		}
	}
	return ""
}

/* --------------------------- permissions --------------------------------- */

// ocServicePermission is one of OpenCode 2's permission requests.
type ocServicePermission struct {
	ID        string         `json:"id"`
	SessionID string         `json:"sessionID"`
	Action    string         `json:"action"`
	Resources []string       `json:"resources"`
	Save      []string       `json:"save"`
	Metadata  map[string]any `json:"metadata"`
}

func (p ocServicePermission) request() ocPermissionRequest {
	return ocPermissionRequest{
		ID: p.ID, SessionID: p.SessionID, Permission: p.Action, Patterns: p.Resources,
		Metadata: p.Metadata, Always: p.Save,
	}
}

func (s *OpenCodeSource) servicePermissions(
	call func(method, path string, query url.Values, body, out any) error, location url.Values,
) ([]ocServicePermission, error) {
	var listed struct {
		Data []ocServicePermission `json:"data"`
	}
	if err := call(http.MethodGet, "/api/permission/request", location, nil, &listed); err != nil {
		return nil, err
	}
	return listed.Data, nil
}

/* ------------------------------ questions -------------------------------- */

// ocServiceForm is one of OpenCode 2's forms: what OpenCode 1 asked as
// questions, now fields of a form, each answered by its key.
type ocServiceForm struct {
	ID        string               `json:"id"`
	SessionID string               `json:"sessionID"`
	Title     string               `json:"title"`
	Fields    []ocServiceFormField `json:"fields"`
}

type ocServiceFormField struct {
	Key         string `json:"key"`
	Type        string `json:"type"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Hidden      bool   `json:"hidden"`
	Custom      *bool  `json:"custom"`
	Options     []struct {
		Value       string `json:"value"`
		Label       string `json:"label"`
		Description string `json:"description"`
	} `json:"options"`
}

func (s *OpenCodeSource) serviceForms(
	call func(method, path string, query url.Values, body, out any) error, location url.Values,
) ([]ocServiceForm, error) {
	var listed struct {
		Data []ocServiceForm `json:"data"`
	}
	if err := call(http.MethodGet, "/api/form", location, nil, &listed); err != nil {
		return nil, err
	}
	return listed.Data, nil
}

// fields are the fields asked one at a time on the phone: the visible ones
// a person can answer there.
func (f ocServiceForm) fields() []ocServiceFormField {
	var out []ocServiceFormField
	for _, field := range f.Fields {
		if field.Hidden || field.Key == "" || field.Type == "external" {
			continue
		}
		out = append(out, field)
	}
	return out
}

// request is the form as OpenCode 1's questions: one a field, its options
// by label, a yes or no for a boolean, and the person's own words where the
// field takes them.
func (f ocServiceForm) request() ocQuestionRequest {
	type option struct {
		Label       string `json:"label"`
		Description string `json:"description,omitempty"`
	}
	type question struct {
		Question string   `json:"question"`
		Header   string   `json:"header"`
		Multiple bool     `json:"multiple"`
		Custom   bool     `json:"custom"`
		Options  []option `json:"options"`
	}
	questions := []question{}
	for _, field := range f.fields() {
		asked := question{Header: f.Title, Multiple: field.Type == "multiselect", Options: []option{}}
		asked.Question = strings.TrimSpace(strings.Join(nonEmpty(field.Title, field.Description), "\n"))
		if asked.Question == "" {
			asked.Question = f.Title
		}
		switch field.Type {
		case "boolean":
			asked.Options = append(asked.Options, option{Label: "Yes"}, option{Label: "No"})
		case "string", "multiselect":
			for _, choice := range field.Options {
				label := choice.Label
				if label == "" {
					label = choice.Value
				}
				asked.Options = append(asked.Options, option{Label: label, Description: choice.Description})
			}
			asked.Custom = len(field.Options) == 0 || (field.Custom != nil && *field.Custom)
		default: // number, integer: written in
			asked.Custom = true
		}
		questions = append(questions, asked)
	}
	var request ocQuestionRequest
	_ = reshape(map[string]any{"id": f.ID, "sessionID": f.SessionID, "questions": questions}, &request)
	return request
}

// answer is the form's answer from OpenCode 1's: the values chosen for each
// question, in order, by label or in the person's words.
func (f ocServiceForm) answer(answers [][]string) (map[string]any, error) {
	fields := f.fields()
	if len(answers) != len(fields) {
		return nil, fmt.Errorf("opencode: the form has %d questions, %d were answered", len(fields), len(answers))
	}
	answer := make(map[string]any, len(fields))
	for index, field := range fields {
		values := answers[index]
		byLabel := map[string]string{}
		for _, option := range field.Options {
			label := option.Label
			if label == "" {
				label = option.Value
			}
			byLabel[label] = option.Value
		}
		value := func(chosen string) string {
			if picked, ok := byLabel[chosen]; ok {
				return picked
			}
			return chosen
		}
		switch field.Type {
		case "multiselect":
			picked := make([]string, 0, len(values))
			for _, chosen := range values {
				picked = append(picked, value(chosen))
			}
			answer[field.Key] = picked
		case "boolean":
			if len(values) != 1 {
				return nil, fmt.Errorf("opencode: %q takes yes or no", field.Title)
			}
			answer[field.Key] = values[0] == "Yes"
		case "number", "integer":
			if len(values) != 1 {
				return nil, fmt.Errorf("opencode: %q takes one number", field.Title)
			}
			number, err := strconv.ParseFloat(strings.TrimSpace(values[0]), 64)
			if err != nil {
				return nil, fmt.Errorf("opencode: %q takes a number", field.Title)
			}
			answer[field.Key] = number
		default:
			if len(values) != 1 {
				return nil, fmt.Errorf("opencode: %q takes one answer", field.Title)
			}
			answer[field.Key] = value(values[0])
		}
	}
	return answer, nil
}

func nonEmpty(values ...string) []string {
	out := values[:0:0]
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, value)
		}
	}
	return out
}

/* ------------------------- starting a session ---------------------------- */

// OpenCodeMajorVersion is the major version of the opencode on PATH, or 0
// when there is none or it cannot say.
func OpenCodeMajorVersion(ctx context.Context) int {
	binary, err := exec.LookPath("opencode")
	if err != nil {
		return 0
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, binary, "--version").Output()
	if err != nil {
		return 0
	}
	// "opencode v2.2.0", or a bare "1.4.3".
	fields := strings.Fields(strings.TrimSpace(string(out)))
	if len(fields) == 0 {
		return 0
	}
	major, _, _ := strings.Cut(strings.TrimPrefix(fields[len(fields)-1], "v"), ".")
	version, _ := strconv.Atoi(major)
	return version
}

// StartOpenCodeServiceSession starts a session in dir with prompt on
// OpenCode 2's service, starting the service first when OpenCode 2 is
// installed but it is not running. ok is false when this Mac has no
// OpenCode 2, for OpenCode 1's way instead.
func StartOpenCodeServiceSession(ctx context.Context, dir, prompt string) (id string, ok bool, err error) {
	s := &OpenCodeSource{client: &http.Client{Timeout: openCodeTimeout}, serviceFile: openCodeServiceFile()}
	services := s.findServices(ctx)
	if len(services) == 0 {
		if OpenCodeMajorVersion(ctx) < 2 {
			return "", false, nil
		}
		start, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if out, err := exec.CommandContext(start, "opencode", "service", "start").CombinedOutput(); err != nil {
			return "", true, fmt.Errorf("source: OpenCode's service did not start: %s", strings.TrimSpace(string(out)))
		}
		for attempt := 0; len(services) == 0; attempt++ {
			if attempt == 50 {
				return "", true, errors.New("source: OpenCode's service did not start")
			}
			select {
			case <-ctx.Done():
				return "", true, ctx.Err()
			case <-time.After(200 * time.Millisecond):
			}
			services = s.findServices(ctx)
		}
	}
	base := services[0]
	password, _ := s.servicePassword(base)
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if _, err := s.send(ctx, http.MethodPost, base+"/api/session", openCodeServiceUser, password,
		map[string]any{"location": map[string]string{"directory": dir}}, &created, maxOpenCodeMetadataResponse); err != nil {
		return "", true, err
	}
	if created.Data.ID == "" {
		return "", true, errors.New("source: OpenCode returned no session ID")
	}
	if _, err := s.send(ctx, http.MethodPost, base+"/api/session/"+openCodeSegment(created.Data.ID)+"/prompt",
		openCodeServiceUser, password, map[string]string{"text": prompt}, nil, maxOpenCodeMetadataResponse); err != nil {
		return "", true, err
	}
	return created.Data.ID, true, nil
}
