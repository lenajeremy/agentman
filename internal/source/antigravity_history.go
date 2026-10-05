package source

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// maxAntigravityHistoryBytes bounds the prompt log one Past call reads.
//
// agy appends one small line per prompt, so this is room for a very long
// history rather than a typical one.
const maxAntigravityHistoryBytes = 32 << 20

// Past implements History.
//
// Antigravity records no working directory anywhere near its transcripts: a
// live session's cwd comes from lsof on the running process, which is exactly
// what an ended session no longer has. What it does keep is history.jsonl, one
// line per prompt carrying the workspace and the conversation it belonged to,
// so grouping that log by conversation is what makes a finished session
// findable at all.
func (s *AntigravitySource) Past(ctx context.Context, dir string, limit int) ([]protocol.Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dir = filepath.Clean(dir)
	if dir == "" || dir == "." {
		return nil, nil
	}
	limit = limitOrDefault(limit)

	conversations := s.antigravityConversations()
	sort.Slice(conversations, func(i, j int) bool {
		return conversations[i].lastAt > conversations[j].lastAt
	})

	model := s.defaultModel()
	found := make([]protocol.Session, 0, limit)
	for _, conversation := range conversations {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(found) == limit {
			break
		}
		if !underDirectory(conversation.workspace, dir) {
			continue
		}
		transcript := s.transcriptPath(conversation.id)
		id := string(protocol.KindAntigravity) + ":" + conversation.id
		// The transcript's own first request reads better than the prompt log
		// when both exist, and matches what a live session is named.
		name := conversation.firstPrompt
		if _, err := os.Stat(transcript); err == nil {
			name = s.conversationName(conversation.id, transcript, conversation.workspace)
		}
		found = append(found, protocol.Session{
			ID:             id,
			Kind:           protocol.KindAntigravity,
			NativeID:       conversation.id,
			Name:           historyName(name, conversation.workspace),
			Cwd:            conversation.workspace,
			State:          protocol.StateEnded,
			Inject:         protocol.InjectNone,
			StartedAt:      conversation.firstAt,
			LastActivityAt: conversation.lastAt,
			Model:          model,
		})
		s.past.remember(id, transcript)
	}
	return found, nil
}

// Directories implements History.
func (s *AntigravitySource) Directories(ctx context.Context) ([]protocol.Folder, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	counts := map[string]*protocol.Folder{}
	for _, conversation := range s.antigravityConversations() {
		if conversation.workspace == "" {
			continue
		}
		path := filepath.Clean(conversation.workspace)
		folder := counts[path]
		if folder == nil {
			folder = &protocol.Folder{Path: path}
			counts[path] = folder
		}
		folder.Agents++
		if conversation.lastAt > folder.LastActivityAt {
			folder.LastActivityAt = conversation.lastAt
		}
	}
	folders := make([]protocol.Folder, 0, len(counts))
	for _, folder := range counts {
		folders = append(folders, *folder)
	}
	return folders, nil
}

// antigravityConversation is one conversation as the prompt log describes it.
type antigravityConversation struct {
	id          string
	workspace   string
	firstPrompt string
	firstAt     int64
	lastAt      int64
}

// antigravityConversations groups history.jsonl by conversation.
//
// Lines with no conversation id are slash commands typed outside a
// conversation — "/model" and the like — and belong to no session.
func (s *AntigravitySource) antigravityConversations() []antigravityConversation {
	raw, err := readBoundedFile(filepath.Join(s.root(), "history.jsonl"), maxAntigravityHistoryBytes)
	if err != nil {
		return nil
	}
	byID := map[string]*antigravityConversation{}
	order := make([]string, 0, 16)
	for _, line := range splitJSONLines(raw) {
		var record struct {
			Display        string `json:"display"`
			Timestamp      int64  `json:"timestamp"`
			Workspace      string `json:"workspace"`
			ConversationID string `json:"conversationId"`
			Type           string `json:"type"`
		}
		if json.Unmarshal(line, &record) != nil || record.ConversationID == "" {
			continue
		}
		conversation := byID[record.ConversationID]
		if conversation == nil {
			conversation = &antigravityConversation{
				id:        record.ConversationID,
				workspace: record.Workspace,
				firstAt:   record.Timestamp,
			}
			byID[record.ConversationID] = conversation
			order = append(order, record.ConversationID)
		}
		if conversation.workspace == "" {
			conversation.workspace = record.Workspace
		}
		if conversation.firstPrompt == "" && record.Type != "slash_command" {
			conversation.firstPrompt = usablePrompt(record.Display)
		}
		if record.Timestamp > conversation.lastAt {
			conversation.lastAt = record.Timestamp
		}
	}
	found := make([]antigravityConversation, 0, len(order))
	for _, id := range order {
		found = append(found, *byID[id])
	}
	return found
}

// splitJSONLines splits a JSONL buffer, skipping blank lines.
func splitJSONLines(raw []byte) [][]byte {
	lines := make([][]byte, 0, 64)
	for start := 0; start < len(raw); {
		end := start
		for end < len(raw) && raw[end] != '\n' {
			end++
		}
		if end > start {
			lines = append(lines, raw[start:end])
		}
		start = end + 1
	}
	return lines
}

// pastAntigravitySession rebuilds the reading shape for an ended session.
func (s *AntigravitySource) pastAntigravitySession(sessionID string) (antigravitySession, bool) {
	transcript, ok := s.past.path(sessionID)
	if !ok {
		return antigravitySession{}, false
	}
	if _, err := os.Stat(transcript); err != nil {
		// A conversation can be in the prompt log before agy has written its
		// brain directory. It is still a session, with nothing to show yet.
		return antigravitySession{}, true
	}
	return antigravitySession{transcript: transcript}, true
}
