package source

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Past implements History.
//
// Cursor's CLI keeps a metadata file per chat holding the working directory,
// a title it wrote itself and both timestamps, so nothing has to be inferred.
// Discovery hides a chat older than cursorCLIWindow unless a pane still has
// its store open; a folder's history is precisely what that hides.
func (s *CursorCLISource) Past(ctx context.Context, dir string, limit int) ([]protocol.Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dir = filepath.Clean(dir)
	if dir == "" || dir == "." {
		return nil, nil
	}
	limit = limitOrDefault(limit)

	chats := s.everyCursorCLIChat()
	// A chat a process still has open is reported by Discover, under the pane's
	// id when a managed pane holds it. Listing it here as well would show one
	// conversation twice, once of them as ended.
	live := make(map[string]bool)
	s.mu.RLock()
	for _, session := range s.sessions {
		live[session.meta.NativeID] = true
	}
	s.mu.RUnlock()
	found := make([]protocol.Session, 0, limit)
	for _, chat := range chats {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(found) == limit {
			break
		}
		if !underDirectory(chat.meta.Cwd, dir) || live[chat.id] {
			continue
		}
		// The same id Discover gives a chat with no pane, so a chat that ends
		// keeps its identity on the phone instead of reappearing as a new row.
		id := cursorCLIChatPrefix + chat.id
		s.modelMu.Lock()
		model := s.models[chat.id].model
		s.modelMu.Unlock()
		found = append(found, protocol.Session{
			ID:             id,
			Kind:           protocol.KindCursorCLI,
			NativeID:       chat.id,
			Name:           historyName(chat.meta.Title, chat.meta.Cwd),
			Cwd:            chat.meta.Cwd,
			State:          protocol.StateEnded,
			Inject:         protocol.InjectNone,
			StartedAt:      chat.meta.CreatedAtMs,
			LastActivityAt: chat.meta.UpdatedAtMs,
			Model:          model,
		})
		s.past.remember(id, chat.store)
	}
	return found, nil
}

// Directories implements History.
func (s *CursorCLISource) Directories(ctx context.Context) ([]protocol.Folder, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	counts := map[string]*protocol.Folder{}
	for _, chat := range s.everyCursorCLIChat() {
		path := filepath.Clean(chat.meta.Cwd)
		folder := counts[path]
		if folder == nil {
			folder = &protocol.Folder{Path: path}
			counts[path] = folder
		}
		folder.Agents++
		if chat.meta.UpdatedAtMs > folder.LastActivityAt {
			folder.LastActivityAt = chat.meta.UpdatedAtMs
		}
	}
	folders := make([]protocol.Folder, 0, len(counts))
	for _, folder := range counts {
		folders = append(folders, *folder)
	}
	return folders, nil
}

// cursorCLIHistoryChat is one chat on disk, newest first.
type cursorCLIHistoryChat struct {
	id    string
	store string
	meta  cursorCLIChat
}

// everyCursorCLIChat reads every chat's metadata, ignoring the recency window
// discovery applies.
func (s *CursorCLISource) everyCursorCLIChat() []cursorCLIHistoryChat {
	paths, err := filepath.Glob(filepath.Join(s.home, ".cursor", "chats", "*", "*", "meta.json"))
	if err != nil {
		return nil
	}
	chats := make([]cursorCLIHistoryChat, 0, len(paths))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil || len(data) > 64*1024 {
			continue
		}
		var meta cursorCLIChat
		if json.Unmarshal(data, &meta) != nil || !meta.HasConversation || meta.Cwd == "" {
			continue
		}
		store := filepath.Join(filepath.Dir(path), "store.db")
		if _, err := os.Stat(store); err != nil {
			continue
		}
		chats = append(chats, cursorCLIHistoryChat{
			id: filepath.Base(filepath.Dir(path)), store: store, meta: meta,
		})
	}
	sort.Slice(chats, func(i, j int) bool {
		if chats[i].meta.UpdatedAtMs != chats[j].meta.UpdatedAtMs {
			return chats[i].meta.UpdatedAtMs > chats[j].meta.UpdatedAtMs
		}
		return chats[i].id < chats[j].id
	})
	return chats
}

// pastCursorCLISession rebuilds the reading shape for an ended chat.
func (s *CursorCLISource) pastCursorCLISession(sessionID string) (cursorCLISession, bool) {
	store, ok := s.past.path(sessionID)
	if !ok {
		return cursorCLISession{}, false
	}
	return cursorCLISession{store: store}, true
}
