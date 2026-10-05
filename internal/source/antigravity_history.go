package source

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

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
// what an ended session no longer has. Two records keep one. agy's own index of
// conversations, conversation_summaries.db, holds every conversation with its
// workspace and the title its /resume picker shows; history.jsonl, one line
// per typed prompt, holds the workspace of every conversation a prompt was
// typed into. The index is preferred and the prompt log fills in what it lacks
// — conversations from before it, or a database that could not be read.
//
// Subagents are conversations too, listed in the index with their parent's id.
// They are the parent's work, not sessions of their own, and are left out the
// way Claude's workers are.
func (s *AntigravitySource) Past(ctx context.Context, dir string, limit int) ([]protocol.Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dir = filepath.Clean(dir)
	if dir == "" || dir == "." {
		return nil, nil
	}
	limit = limitOrDefault(limit)

	conversations := s.antigravityConversations(ctx)
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
		name := conversation.title
		if name == "" {
			// The transcript's own first request reads better than the prompt
			// log when both exist, and matches what a live session is named.
			name = conversation.firstPrompt
			if _, err := os.Stat(transcript); err == nil {
				name = s.conversationName(conversation.id, transcript, conversation.workspace)
			}
		}
		started := conversation.firstAt
		if started == 0 {
			// The presence lock is made when the conversation is.
			started = fileMillis(filepath.Join(s.root(), "presence", conversation.id+".lock"), conversation.lastAt)
		}
		found = append(found, protocol.Session{
			ID:             id,
			Kind:           protocol.KindAntigravity,
			NativeID:       conversation.id,
			Name:           historyName(name, conversation.workspace),
			Cwd:            conversation.workspace,
			State:          protocol.StateEnded,
			Inject:         protocol.InjectNone,
			StartedAt:      started,
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
	for _, conversation := range s.antigravityConversations(ctx) {
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

// antigravityConversation is one conversation as agy's records describe it.
type antigravityConversation struct {
	id          string
	workspace   string
	title       string
	firstPrompt string
	firstAt     int64
	lastAt      int64
}

// antigravityConversations merges agy's conversation index with its prompt
// log. See Past.
func (s *AntigravitySource) antigravityConversations(ctx context.Context) []antigravityConversation {
	fromLog := s.loggedConversations()
	indexed, subagents := s.indexedConversations(ctx)
	seen := map[string]bool{}
	found := make([]antigravityConversation, 0, len(indexed)+len(fromLog))
	logged := map[string]antigravityConversation{}
	for _, conversation := range fromLog {
		logged[conversation.id] = conversation
	}
	for _, conversation := range indexed {
		if prompts, ok := logged[conversation.id]; ok {
			if conversation.workspace == "" {
				conversation.workspace = prompts.workspace
			}
			conversation.firstPrompt = prompts.firstPrompt
			conversation.firstAt = prompts.firstAt
			conversation.lastAt = max(conversation.lastAt, prompts.lastAt)
		}
		seen[conversation.id] = true
		found = append(found, conversation)
	}
	for _, conversation := range fromLog {
		if !seen[conversation.id] && !subagents[conversation.id] {
			found = append(found, conversation)
		}
	}
	return found
}

// loggedConversations groups history.jsonl by conversation.
//
// Lines with no conversation id are slash commands typed outside a
// conversation — "/model" and the like — and belong to no session.
func (s *AntigravitySource) loggedConversations() []antigravityConversation {
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
	// The conversation's id is what its artifacts are found by.
	native := strings.TrimPrefix(sessionID, string(protocol.KindAntigravity)+":")
	return antigravitySession{transcript: transcript, meta: protocol.Session{
		ID: sessionID, Kind: protocol.KindAntigravity, NativeID: native, State: protocol.StateEnded,
	}}, true
}
