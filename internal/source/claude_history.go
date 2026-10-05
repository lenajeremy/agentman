package source

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Past implements History.
//
// Claude names each project directory after the working directory, replacing
// '/' and '.' with '-', so the subtree under dir is a prefix match on that
// slug. The match is only a filter, not the answer: the slug is lossy — it
// cannot tell '/' from '.' from a literal '-', so /src/agentman-old shares a
// prefix with /src/agentman — and every transcript records its real cwd, so
// that is what decides.
func (s *ClaudeSource) Past(ctx context.Context, dir string, limit int) ([]protocol.Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dir = filepath.Clean(dir)
	if dir == "" || dir == "." {
		return nil, nil
	}
	limit = limitOrDefault(limit)

	entries, err := os.ReadDir(s.projectsDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	slug := claudeProjectSlug(dir)
	candidates := make([]historyEntry, 0, limit)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !claudeSlugMatches(name, slug) {
			continue
		}
		candidates = append(candidates, newestTranscripts(
			filepath.Join(s.projectsDir(), name), ".jsonl", limit)...)
	}
	// Newest first across every candidate directory, so a budget spent on one
	// noisy subfolder cannot hide the root's recent work.
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].modTime > candidates[j].modTime
	})
	if len(candidates) > limit {
		candidates = candidates[:limit]
	}

	found := make([]protocol.Session, 0, len(candidates))
	for _, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		sessionID := strings.TrimSuffix(filepath.Base(candidate.path), ".jsonl")
		if !validClaudeSessionID(sessionID) {
			continue
		}
		facts, ok := s.pastFactsOf(candidate.path)
		if !ok {
			continue
		}
		// A transcript with no cwd of its own is judged by the slug that led
		// here, which is the best available and was already a prefix match.
		if facts.cwd != "" && !underDirectory(facts.cwd, dir) {
			continue
		}
		cwd := facts.cwd
		if cwd == "" {
			cwd = dir
		}
		name := facts.name
		if name == "" {
			name = historyName("", cwd)
		}

		id := string(protocol.KindClaude) + ":" + sessionID
		started := facts.startedAt
		if started == 0 {
			started = candidate.modTime
		}
		found = append(found, protocol.Session{
			ID:             id,
			Kind:           protocol.KindClaude,
			NativeID:       sessionID,
			Name:           name,
			Cwd:            cwd,
			State:          protocol.StateEnded,
			Inject:         protocol.InjectNone,
			StartedAt:      started,
			LastActivityAt: candidate.modTime,
			Model:          facts.model,
		})

		s.past.remember(id, candidate.path)
	}
	return found, nil
}

// claudePastFacts is what a history listing reads from one transcript.
type claudePastFacts struct {
	cwd       string
	startedAt int64
	// name is empty when nothing named the session, and the listing falls
	// back to the folder.
	name  string
	model string
}

// pastFactsOf reads a transcript's directory, name and model, or recalls them
// if the file has not changed since.
func (s *ClaudeSource) pastFactsOf(path string) (claudePastFacts, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return claudePastFacts{}, false
	}
	if facts, ok := s.pastRead.get(path, info); ok {
		return facts, true
	}
	head := claudeTranscriptHead(path)
	facts := claudePastFacts{
		cwd:       head.cwd,
		startedAt: head.startedAt,
		name:      historyName(claudeTranscriptTitle(path, head.titles), ""),
		model:     modelFromTranscript(path, claudeModelOf),
	}
	s.pastRead.put(path, info, facts)
	return facts, true
}

// Directories implements History.
//
// One project directory is one working directory, so the count is the number
// of transcripts in it — a directory listing, with nothing opened. The folder
// name is a lossy slug, so the real path comes from the head of the newest
// transcript, which is one small read per project rather than per session.
func (s *ClaudeSource) Directories(ctx context.Context) ([]protocol.Folder, error) {
	entries, err := os.ReadDir(s.projectsDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	folders := make([]protocol.Folder, 0, len(entries))
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !entry.IsDir() {
			continue
		}
		newest := newestTranscripts(filepath.Join(s.projectsDir(), entry.Name()), ".jsonl", 0)
		if len(newest) == 0 {
			continue
		}
		cwd := claudeTranscriptHead(newest[0].path).cwd
		if cwd == "" {
			continue
		}
		folders = append(folders, protocol.Folder{
			Path:           filepath.Clean(cwd),
			Agents:         len(newest),
			LastActivityAt: newest[0].modTime,
		})
	}
	return folders, nil
}

// claudeProjectSlug is how Claude names a working directory's project folder:
// every character that is not an ASCII letter or digit becomes "-" (2.1.289:
// replace(/[^a-zA-Z0-9]/g,"-")). That runs over JavaScript's UTF-16 string,
// so a character outside the Basic Multilingual Plane, two code units there,
// becomes two dashes. Only "/" and "." used to be replaced here, so any
// folder with a space or an underscore in its path listed no sessions.
//
// Past claudeSlugLimit characters Claude also cuts the name and appends a
// hash of the path; see claudeSlugMatches.
func claudeProjectSlug(cwd string) string {
	var slug strings.Builder
	for _, character := range cwd {
		switch {
		case (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9'):
			slug.WriteRune(character)
		case character > 0xFFFF:
			slug.WriteString("--")
		default:
			slug.WriteByte('-')
		}
	}
	return slug.String()
}

// claudeSlugLimit is the longest project folder name Claude writes before
// cutting it and appending "-" and a hash of the path.
const claudeSlugLimit = 200

// claudeSlugMatches reports whether a project folder can hold sessions run in
// the directory whose slug is given, or beneath it. A folder named past the
// limit is matched on its first claudeSlugLimit characters: the hash is not
// worth reproducing, because each transcript's own cwd decides membership.
func claudeSlugMatches(folder, slug string) bool {
	if len(slug) > claudeSlugLimit {
		return strings.HasPrefix(folder, slug[:claudeSlugLimit])
	}
	return folder == slug || strings.HasPrefix(folder, slug+"-")
}

// transcriptFor resolves a session id to its transcript, live or ended.
func (s *ClaudeSource) transcriptFor(sessionID string) (string, bool) {
	s.mu.RLock()
	session, ok := s.sessions[sessionID]
	s.mu.RUnlock()
	if ok {
		return session.transcript, true
	}
	return s.pastTranscript(sessionID)
}

// pastTranscript resolves a session Past found and Discover cannot see.
func (s *ClaudeSource) pastTranscript(sessionID string) (string, bool) {
	return s.past.path(sessionID)
}

// claudeHead is what the opening lines of a transcript say about a session.
type claudeHead struct {
	cwd       string
	prompt    string
	startedAt int64
	// titles are the names the head gave the session, with the opening
	// prompt as the last resort. See claudeTranscriptTitle.
	titles claudeTitles
}

// claudeTitles are the names a transcript records for its session. Claude
// writes each as a record of its own, and again when a session is resumed.
type claudeTitles struct {
	custom string // set by the person with /rename
	agent  string // the name the live session is listed under
	ai     string // a title Claude made up from the conversation
	prompt string // the opening prompt, when nothing named the session
}

// best is the name a person would recognise: their own first, then the one the
// live row showed, then Claude's, then what they first asked.
func (t claudeTitles) best() string {
	for _, name := range []string{t.custom, t.agent, t.ai, t.prompt} {
		if strings.TrimSpace(name) != "" {
			return name
		}
	}
	return ""
}

// claudeTitleRecord reads a title record, reporting which kind it is.
func claudeTitleRecord(line []byte) (kind, title string) {
	if !bytes.Contains(line, []byte(`-title"`)) && !bytes.Contains(line, []byte(`"agent-name"`)) {
		return "", "" // most lines: skip the decode
	}
	var record struct {
		Type        string `json:"type"`
		CustomTitle string `json:"customTitle"`
		AgentName   string `json:"agentName"`
		AITitle     string `json:"aiTitle"`
	}
	if json.Unmarshal(line, &record) != nil {
		return "", ""
	}
	switch record.Type {
	case "custom-title":
		return record.Type, record.CustomTitle
	case "agent-name":
		return record.Type, record.AgentName
	case "ai-title":
		return record.Type, record.AITitle
	}
	return "", ""
}

// claudeTitleScanBytes is how much of a transcript's end is searched for the
// newest title, the same window the model is read from.
const claudeTitleScanBytes = modelScanBytes

// claudeTranscriptTitle names a past session. The newest title of each kind
// is taken from the end of the transcript, where a rename or a resume writes
// it, and the head's are used for any kind the end does not have.
func claudeTranscriptTitle(path string, head claudeTitles) string {
	titles := claudeTitles{prompt: head.prompt}
	found := map[string]bool{}
	scanTail(path, claudeTitleScanBytes, func(line []byte) bool {
		kind, title := claudeTitleRecord(line)
		if kind == "" || title == "" || found[kind] {
			return false
		}
		found[kind] = true
		switch kind {
		case "custom-title":
			titles.custom = title
		case "agent-name":
			titles.agent = title
		case "ai-title":
			titles.ai = title
		}
		return found["custom-title"] // nothing outranks the person's own
	})
	if titles.custom == "" {
		titles.custom = head.custom
	}
	if titles.agent == "" {
		titles.agent = head.agent
	}
	if titles.ai == "" {
		titles.ai = head.ai
	}
	return titles.best()
}

// claudeTranscriptHead reads a session's working directory, opening prompt and
// start time from the beginning of its transcript.
func claudeTranscriptHead(path string) claudeHead {
	var head claudeHead
	scanHead(path, func(line []byte) bool {
		var record struct {
			Type        string `json:"type"`
			Cwd         string `json:"cwd"`
			Timestamp   string `json:"timestamp"`
			IsSidechain bool   `json:"isSidechain"`
			Message     struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal(line, &record) != nil {
			return false
		}
		switch kind, title := claudeTitleRecord(line); kind {
		case "custom-title":
			head.titles.custom = title
		case "agent-name":
			head.titles.agent = title
		case "ai-title":
			head.titles.ai = title
		}
		if head.cwd == "" && record.Cwd != "" {
			head.cwd = record.Cwd
		}
		// A sidechain is a subagent's conversation, not the one the user
		// opened, so its first message must not become this session's name.
		if head.prompt == "" && record.Type == "user" &&
			!record.IsSidechain && record.Message.Role == "user" {
			head.prompt = claudePromptText(record.Message.Content)
			if head.prompt != "" && record.Timestamp != "" {
				if when, err := time.Parse(time.RFC3339Nano, record.Timestamp); err == nil {
					head.startedAt = when.UnixMilli()
				}
			}
		}
		return head.cwd != "" && head.prompt != ""
	})
	head.titles.prompt = head.prompt
	return head
}

// scheduledTaskName reads the task a scheduled run was started for. Its whole
// prompt is the task's block, which usablePrompt rightly strips as machinery,
// leaving the session named after its folder; the task's name says what it is.
func scheduledTaskName(text string) string {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "<scheduled-task ") {
		return ""
	}
	opening, _, ok := strings.Cut(text, ">")
	if !ok {
		return ""
	}
	_, rest, ok := strings.Cut(opening, ` name="`)
	if !ok {
		return ""
	}
	name, _, ok := strings.Cut(rest, `"`)
	if !ok {
		return ""
	}
	return strings.TrimSpace(name)
}

// claudePromptText pulls the person-written text out of one user message.
//
// Content is either a plain string or a list of typed blocks, and the blocks
// the editor injects — the open file, a slash command's expansion, a system
// reminder — are not what the person typed, so they cannot name the session.
func claudePromptText(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		if name := scheduledTaskName(text); name != "" {
			return name
		}
		return usablePrompt(text)
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return ""
	}
	for _, block := range blocks {
		if block.Type != "text" {
			continue
		}
		if prompt := usablePrompt(block.Text); prompt != "" {
			return prompt
		}
	}
	return ""
}

// injectedPrefixes open the plain-text preambles Claude Code adds to a turn.
// The tag-shaped ones are handled structurally below, because the set of tags
// grows with every feature and a list of them would silently fall behind.
var injectedPrefixes = []string{
	"Caveat: The messages below",
	"This session is being continued",
}

// usablePrompt returns what the person actually typed, or "" if the message is
// entirely machinery.
//
// A user turn is often prefixed with tag-wrapped blocks the CLI injected — the
// open editor file, a slash command's expansion, a scheduled task, a system
// reminder — and none of them name the session. They are stripped by shape
// rather than by name so a tag introduced next release is handled too.
//
// A real prompt that opens with an unclosed angle bracket ("<div> renders
// wrong") is read as such a block and discarded. That costs the row its
// prompt and falls back to the folder name, which is a better failure than
// showing a page of injected XML.
func usablePrompt(text string) string {
	text = strings.TrimSpace(text)
	for strings.HasPrefix(text, "<") {
		close := strings.IndexByte(text, '>')
		if close < 0 {
			return ""
		}
		name := strings.TrimSuffix(strings.TrimSpace(text[1:close]), "/")
		if cut := strings.IndexAny(name, " \t\n"); cut >= 0 {
			name = name[:cut]
		}
		if name == "" {
			return ""
		}
		rest := text[close+1:]
		end := strings.Index(rest, "</"+name+">")
		if end < 0 {
			// An unclosed block runs to the end of the message, leaving
			// nothing a person wrote.
			return ""
		}
		text = strings.TrimSpace(rest[end+len(name)+3:])
	}
	if text == "" {
		return ""
	}
	for _, prefix := range injectedPrefixes {
		if strings.HasPrefix(text, prefix) {
			return ""
		}
	}
	return text
}
