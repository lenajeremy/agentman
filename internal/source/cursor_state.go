package source

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// The IDE keeps its own composer index in state.vscdb, next to the CLI
// transcripts the adapter already reads. It is strictly an enrichment layer,
// never the source of truth:
//
//   - The transcript JSONL is append-only with stable byte offsets, readable
//     while the agent writes, and present even when the IDE is closed. The
//     database is written by the UI process in an undocumented schema whose
//     _v version fields show it evolving — one Cursor update could reshape it.
//   - Composer IDs match transcript UUIDs, so rows join cleanly onto the
//     sessions discovery already found. A missing, unreadable, or stale
//     database degrades to transcript-only discovery instead of failing it.
//
// What the index adds over transcripts: exact created/updated timestamps,
// Cursor's own subtitle label, the exact workspace path (no lossy
// dash-decoding), archived filtering, and hasBlockingPendingActions — the
// waiting-input signal transcripts never record.
type cursorHeader struct {
	composerID string
	createdAt  int64
	updatedAt  int64
	archived   bool
	subtitle   string
	blocking   bool
	cwd        string
}

// cursorHeaderNameChars bounds Cursor's own subtitle for phone display.
// Subtitles arrive pre-truncated by the IDE; this is only a backstop.
const cursorHeaderNameChars = 120

// cursorStateDBTimeout bounds one sqlite3 spawn so a stalled database never
// stalls a one-second discovery sweep.
const cursorStateDBTimeout = 5 * time.Second

// maxCursorStateDBOutput caps the composer index read. The table holds one
// small row per composer; anything larger is corruption, not history.
const maxCursorStateDBOutput = 16 * 1024 * 1024

// cursorStateDBPath locates the IDE's global storage for a home directory.
// Empty means the platform layout is unknown and enrichment is skipped.
func cursorStateDBPath(home string) string {
	const tail = "globalStorage/state.vscdb"
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Cursor", "User", tail)
	case "windows":
		if base := os.Getenv("APPDATA"); base != "" {
			return filepath.Join(base, "Cursor", "User", tail)
		}
		return ""
	default:
		// Linux and anything else follow XDG, falling back to ~/.config.
		base := os.Getenv("XDG_CONFIG_HOME")
		if base == "" {
			base = filepath.Join(home, ".config")
		}
		return filepath.Join(base, "Cursor", "User", tail)
	}
}

// queryCursorHeaders reads the composer index by shelling out to the sqlite3
// CLI. There is deliberately no driver dependency: a CGO SQLite binding
// would complicate the cross-compiled releases, and a pure-Go one is heavy
// machinery for one small read per database change. The database is opened
// read-only over a URI so the IDE's writer is never blocked, and output is
// bounded before parsing because the value column is unbounded JSON.
func queryCursorHeaders(ctx context.Context, dbPath string) (map[string]cursorHeader, error) {
	bin, err := exec.LookPath("sqlite3")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, cursorStateDBTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin,
		"-json", "-readonly", "file:"+dbPath+"?mode=ro",
		"SELECT composerId, createdAt, lastUpdatedAt, isArchived, value FROM composerHeaders")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("source: cursor state db: %v: %s",
			err, bytes.TrimSpace(stderr.Bytes()))
	}
	if stdout.Len() > maxCursorStateDBOutput {
		return nil, fmt.Errorf("source: cursor state db: response exceeds %d bytes",
			maxCursorStateDBOutput)
	}

	var rows []struct {
		ComposerID string          `json:"composerId"`
		CreatedAt  *int64          `json:"createdAt"`
		UpdatedAt  *int64          `json:"lastUpdatedAt"`
		Archived   int             `json:"isArchived"`
		Value      json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &rows); err != nil {
		return nil, fmt.Errorf("source: cursor state db: invalid JSON: %w", err)
	}

	out := make(map[string]cursorHeader, len(rows))
	for _, row := range rows {
		if row.ComposerID == "" {
			continue
		}
		var value struct {
			Subtitle  string `json:"subtitle"`
			Blocking  bool   `json:"hasBlockingPendingActions"`
			Workspace struct {
				URI struct {
					FsPath string `json:"fsPath"`
				} `json:"uri"`
			} `json:"workspaceIdentifier"`
		}
		// A row whose value does not parse is skipped, not fatal: one
		// future-shaped composer must not hide every other session.
		if len(row.Value) > 0 {
			if err := json.Unmarshal(row.Value, &value); err != nil {
				// sqlite3 -json transport encodes TEXT columns as JSON
				// strings, so the stored document arrives double-encoded.
				// Unwrap one layer and retry before giving up on the row.
				var nested string
				if err := json.Unmarshal(row.Value, &nested); err != nil {
					continue
				}
				if err := json.Unmarshal([]byte(nested), &value); err != nil {
					continue
				}
			}
		}
		header := cursorHeader{
			composerID: row.ComposerID,
			archived:   row.Archived != 0,
			subtitle:   value.Subtitle,
			blocking:   value.Blocking,
			cwd:        value.Workspace.URI.FsPath,
		}
		if row.CreatedAt != nil {
			header.createdAt = *row.CreatedAt
		}
		if row.UpdatedAt != nil {
			header.updatedAt = *row.UpdatedAt
		}
		out[row.ComposerID] = header
	}
	return out, nil
}

// sameCursorDBFile reports whether a database file (or its WAL) has kept
// the same identity, size, and modification time since the last read.
func sameCursorDBFile(cached, current os.FileInfo) bool {
	if cached == nil || current == nil {
		return cached == nil && current == nil
	}
	return os.SameFile(cached, current) && cached.Size() == current.Size() &&
		cached.ModTime().Equal(current.ModTime())
}

// cursorHeaders returns the IDE composer index, or nil when it is
// unavailable. Cursor writes to SQLite's WAL while the main database stays
// unchanged, so both files must be watched. A failed query retains the last
// good index and is retried on the next sweep.
func (s *CursorSource) cursorHeaders(ctx context.Context) map[string]cursorHeader {
	dbPath := cursorStateDBPath(s.home)
	if dbPath == "" {
		return nil
	}
	info, err := os.Stat(dbPath)
	if err != nil {
		return nil
	}
	walInfo, err := os.Stat(dbPath + "-wal")
	if err != nil && !os.IsNotExist(err) {
		// Do not cache a snapshot when the WAL cannot be inspected.
		s.stateMu.Lock()
		defer s.stateMu.Unlock()
		return s.stateHeaders
	}
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if sameCursorDBFile(s.stateDBInfo, info) &&
		sameCursorDBFile(s.stateWALInfo, walInfo) {
		return s.stateHeaders
	}
	query := s.queryHeaders
	if query == nil {
		query = queryCursorHeaders
	}
	headers, err := query(ctx, dbPath)
	if err != nil {
		return s.stateHeaders
	}
	s.stateDBInfo = info
	s.stateWALInfo = walInfo
	s.stateHeaders = headers
	return headers
}
