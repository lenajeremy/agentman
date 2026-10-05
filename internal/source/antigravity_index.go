package source

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// agy keeps an index of its conversations in conversation_summaries.db, one
// row per conversation with the workspace it ran in, the title its /resume
// picker shows and, for a subagent, its parent:
//
//	conversation_id | title | preview | step_count | last_modified_time |
//	workspace_uris  | status | agent_name | parent_conversation_id | …
//
// It is read with the sqlite3 CLI, read-only, the way the Cursor adapter reads
// its stores: no driver to complicate the cross-compiled releases, and a
// read-only open never blocks agy's own writer.

const (
	antigravityIndexTimeout   = 5 * time.Second
	antigravityIndexMaxOutput = 16 << 20
)

// antigravityIndexQuery lists every conversation agy knows.
const antigravityIndexQuery = "SELECT conversation_id, title, last_modified_time, workspace_uris, " +
	"parent_conversation_id FROM conversation_summaries"

// antigravityIndex caches one read of the database, kept until it or its
// write-ahead log changes.
type antigravityIndex struct {
	mu           sync.Mutex
	db, wal      os.FileInfo
	read         bool
	conversation []antigravityConversation
	subagents    map[string]bool
}

// indexedConversations returns agy's indexed conversations, without
// subagents, and the set of subagent ids so the prompt log's copy of one can
// be left out too.
func (s *AntigravitySource) indexedConversations(ctx context.Context) ([]antigravityConversation, map[string]bool) {
	path := filepath.Join(s.root(), "conversation_summaries.db")
	db, err := os.Stat(path)
	if err != nil {
		return nil, nil
	}
	wal, _ := os.Stat(path + "-wal")

	s.index.mu.Lock()
	defer s.index.mu.Unlock()
	if s.index.read && sameCursorDBFile(s.index.db, db) && sameCursorDBFile(s.index.wal, wal) {
		return s.index.conversation, s.index.subagents
	}
	conversations, subagents, err := queryAntigravityIndex(ctx, path)
	if err != nil {
		// Unreadable now — sqlite3 missing, the file mid-rewrite — is not
		// the same as empty: keep the last good read, and try again next time.
		return s.index.conversation, s.index.subagents
	}
	s.index.db, s.index.wal, s.index.read = db, wal, true
	s.index.conversation, s.index.subagents = conversations, subagents
	return conversations, subagents
}

func queryAntigravityIndex(ctx context.Context, path string) ([]antigravityConversation, map[string]bool, error) {
	bin, err := exec.LookPath("sqlite3")
	if err != nil {
		return nil, nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, antigravityIndexTimeout)
	defer cancel()
	// agy keeps the database in WAL mode. While it runs, the -wal and -shm
	// files beside it let a read-only reader in. Once it exits it folds the
	// log back into the database and removes both, and a read-only open of a
	// WAL database with no -shm fails outright ("unable to open database
	// file"), because the reader would have to create one. The database is
	// then complete and nobody is writing it, which is exactly what
	// immutable=1 promises SQLite.
	uri := "file:" + path + "?mode=ro"
	if _, err := os.Stat(path + "-wal"); os.IsNotExist(err) {
		uri += "&immutable=1"
	}
	cmd := exec.CommandContext(ctx, bin, "-json", "-readonly", "-cmd", ".timeout 1000", uri, antigravityIndexQuery)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, nil, fmt.Errorf("source: antigravity index: %v: %s", err, bytes.TrimSpace(stderr.Bytes()))
	}
	if stdout.Len() > antigravityIndexMaxOutput {
		return nil, nil, fmt.Errorf("source: antigravity index exceeds %d bytes", antigravityIndexMaxOutput)
	}
	return parseAntigravityIndex(stdout.Bytes())
}

func parseAntigravityIndex(raw []byte) ([]antigravityConversation, map[string]bool, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, map[string]bool{}, nil // sqlite3 prints nothing for no rows
	}
	var rows []struct {
		ID         string `json:"conversation_id"`
		Title      string `json:"title"`
		Modified   string `json:"last_modified_time"`
		Workspaces string `json:"workspace_uris"`
		Parent     string `json:"parent_conversation_id"`
	}
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, nil, fmt.Errorf("source: antigravity index: %w", err)
	}
	conversations := make([]antigravityConversation, 0, len(rows))
	subagents := map[string]bool{}
	for _, row := range rows {
		if !isUUID(row.ID) {
			continue
		}
		if row.Parent != "" {
			subagents[row.ID] = true
			continue
		}
		conversations = append(conversations, antigravityConversation{
			id:        row.ID,
			workspace: antigravityWorkspace(row.Workspaces),
			title:     strings.Join(strings.Fields(row.Title), " "),
			lastAt:    antigravityIndexTime(row.Modified),
		})
	}
	return conversations, subagents, nil
}

// antigravityWorkspace is the first folder in workspace_uris, a JSON list of
// file URLs: ["file:///Users/me/work/api"]. A conversation started where agy
// had not been trusted has none.
func antigravityWorkspace(field string) string {
	var uris []string
	if json.Unmarshal([]byte(field), &uris) != nil {
		return ""
	}
	for _, uri := range uris {
		parsed, err := url.Parse(uri)
		if err != nil || parsed.Scheme != "file" || !filepath.IsAbs(parsed.Path) {
			continue
		}
		return filepath.Clean(parsed.Path)
	}
	return ""
}

// antigravityIndexTime reads the index's timestamps, which agy writes as
// "2026-10-05 09:37:52.321166+00:00".
func antigravityIndexTime(value string) int64 {
	for _, layout := range []string{"2006-01-02 15:04:05.999999999-07:00", time.RFC3339Nano} {
		if t, err := time.Parse(layout, value); err == nil {
			return t.UnixMilli()
		}
	}
	return 0
}
