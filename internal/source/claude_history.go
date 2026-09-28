package source

import (
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
		if name != slug && !strings.HasPrefix(name, slug+"-") {
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
		head := claudeTranscriptHead(candidate.path)
		// A transcript with no cwd of its own is judged by the slug that led
		// here, which is the best available and was already a prefix match.
		if head.cwd != "" && !underDirectory(head.cwd, dir) {
			continue
		}
		cwd := head.cwd
		if cwd == "" {
			cwd = dir
		}

		id := string(protocol.KindClaude) + ":" + sessionID
		model, cached := s.models.get(id)
		if !cached {
			model = modelFromTranscript(candidate.path, claudeModelOf)
			s.models.put(id, model)
		}

		started := head.startedAt
		if started == 0 {
			started = candidate.modTime
		}
		found = append(found, protocol.Session{
			ID:             id,
			Kind:           protocol.KindClaude,
			NativeID:       sessionID,
			Name:           historyName(head.prompt, cwd),
			Cwd:            cwd,
			State:          protocol.StateEnded,
			Inject:         protocol.InjectNone,
			StartedAt:      started,
			LastActivityAt: candidate.modTime,
			Model:          model,
		})

		s.past.remember(id, candidate.path)
	}
	return found, nil
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

// claudeProjectSlug is how Claude names a working directory's project folder.
func claudeProjectSlug(cwd string) string {
	return strings.NewReplacer("/", "-", ".", "-").Replace(cwd)
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
	return head
}

// claudePromptText pulls the person-written text out of one user message.
//
// Content is either a plain string or a list of typed blocks, and the blocks
// the editor injects — the open file, a slash command's expansion, a system
// reminder — are not what the person typed, so they cannot name the session.
func claudePromptText(raw json.RawMessage) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
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
