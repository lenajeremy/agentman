package source

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// OpenCode keeps every session in a SQLite database of its own, and its server
// answers from it. With no `opencode serve` running there was no OpenCode
// history at all on the phone: no folders, no past sessions, no transcripts,
// although every one of them was still on disk. This reads that database,
// read-only, for exactly that case. A running server stays the source of
// truth whenever there is one.
//
// The rows hold the same JSON the API returns, so the transcript goes through
// the same conversion as a live one.

// openCodeDBTimeout bounds one query. The database is local; a long wait means
// OpenCode holds a write lock, and the phone is better told nothing than kept
// waiting.
const openCodeDBTimeout = 3 * time.Second

// maxOpenCodeStoredSessions bounds a history listing read from the database.
const maxOpenCodeStoredSessions = 2000

// maxOpenCodeDBOutput bounds what one query may return.
const maxOpenCodeDBOutput = 32 << 20

type openCodeStore struct {
	// path is where the database is, or "" when OpenCode has none here.
	path func() string
}

// openCodeDatabase is where OpenCode keeps its store: $XDG_DATA_HOME/opencode,
// or ~/.local/share/opencode, on every platform.
func openCodeDatabase() string {
	data := os.Getenv("XDG_DATA_HOME")
	if data == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		data = filepath.Join(home, ".local", "share")
	}
	path := filepath.Join(data, "opencode", "opencode.db")
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	return path
}

var errNoOpenCodeStore = errors.New("source: OpenCode keeps no store on this machine")

// query runs one read-only statement and decodes its rows.
func (st *openCodeStore) query(ctx context.Context, statement string, rows any) error {
	path := ""
	if st != nil && st.path != nil {
		path = st.path()
	}
	if path == "" {
		return errNoOpenCodeStore
	}
	bin, err := exec.LookPath("sqlite3")
	if err != nil {
		return errNoOpenCodeStore
	}
	ctx, cancel := context.WithTimeout(ctx, openCodeDBTimeout)
	defer cancel()
	output, err := exec.CommandContext(ctx, bin, "-json", "-readonly",
		"-cmd", ".timeout 1000", "file:"+path+"?mode=ro", statement).Output()
	if err != nil {
		return fmt.Errorf("source: reading OpenCode's store: %w", err)
	}
	if len(output) > maxOpenCodeDBOutput {
		return errors.New("source: OpenCode's store answered with too much")
	}
	if len(strings.TrimSpace(string(output))) == 0 {
		return nil // sqlite3 prints nothing for no rows
	}
	return json.Unmarshal(output, rows)
}

// sessions lists the conversations a person started, newest first. A child
// session is a subagent's, shown by its parent rather than as a row of its
// own, and an archived one is one OpenCode itself no longer shows.
func (st *openCodeStore) sessions(ctx context.Context) ([]ocSession, error) {
	var rows []struct {
		ID        string `json:"id"`
		Title     string `json:"title"`
		Directory string `json:"directory"`
		Model     string `json:"model"`
		Created   int64  `json:"time_created"`
		Updated   int64  `json:"time_updated"`
	}
	statement := "SELECT id, title, directory, model, time_created, time_updated FROM session " +
		"WHERE parent_id IS NULL AND time_archived IS NULL ORDER BY time_updated DESC LIMIT " +
		strconv.Itoa(maxOpenCodeStoredSessions)
	if err := st.query(ctx, statement, &rows); err != nil {
		return nil, err
	}
	sessions := make([]ocSession, 0, len(rows))
	for _, row := range rows {
		var session ocSession
		session.ID, session.Title, session.Directory = row.ID, row.Title, row.Directory
		session.Time.Created, session.Time.Updated = row.Created, row.Updated
		_ = json.Unmarshal([]byte(row.Model), &session.Model)
		sessions = append(sessions, session)
	}
	return sessions, nil
}

// messages reads one page of a session's transcript: the newest limit messages
// before the cursor, in chronological order, and the cursor for the page
// before them ("" when there is none). A cursor is the creation time and id of
// the oldest message a page holds.
func (st *openCodeStore) messages(ctx context.Context, nativeID, before string, limit int) ([]ocMessage, string, error) {
	if !validOpenCodeStoreID(nativeID) {
		return nil, "", fmt.Errorf("source: invalid opencode session %q", nativeID)
	}
	bound := ""
	if before != "" {
		created, id, ok := strings.Cut(before, ":")
		at, err := strconv.ParseInt(created, 10, 64)
		if !ok || err != nil || !validOpenCodeStoreID(id) {
			return nil, "", fmt.Errorf("source: bad cursor %q", before)
		}
		bound = fmt.Sprintf(" AND (time_created < %d OR (time_created = %d AND id < '%s'))", at, at, id)
	}
	var rows []struct {
		ID      string `json:"id"`
		Data    string `json:"data"`
		Created int64  `json:"time_created"`
	}
	statement := fmt.Sprintf("SELECT id, data, time_created FROM message WHERE session_id = '%s'%s "+
		"ORDER BY time_created DESC, id DESC LIMIT %d", nativeID, bound, limit+1)
	if err := st.query(ctx, statement, &rows); err != nil {
		return nil, "", err
	}
	cursor := ""
	if len(rows) > limit {
		rows = rows[:limit]
		oldest := rows[len(rows)-1]
		cursor = fmt.Sprintf("%d:%s", oldest.Created, oldest.ID)
	}
	if len(rows) == 0 {
		return nil, "", nil
	}

	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		if !validOpenCodeStoreID(row.ID) {
			return nil, "", fmt.Errorf("source: OpenCode's store holds an unexpected message id %q", row.ID)
		}
		ids = append(ids, "'"+row.ID+"'")
	}
	var parts []struct {
		ID      string `json:"id"`
		Message string `json:"message_id"`
		Data    string `json:"data"`
	}
	statement = "SELECT id, message_id, data FROM part WHERE message_id IN (" +
		strings.Join(ids, ",") + ") ORDER BY message_id, id"
	if err := st.query(ctx, statement, &parts); err != nil {
		return nil, "", err
	}
	byMessage := map[string][]ocPart{}
	for _, row := range parts {
		var part ocPart
		if json.Unmarshal([]byte(row.Data), &part) != nil {
			continue
		}
		if part.ID == "" {
			part.ID = row.ID
		}
		byMessage[row.Message] = append(byMessage[row.Message], part)
	}

	messages := make([]ocMessage, 0, len(rows))
	for i := len(rows) - 1; i >= 0; i-- { // oldest first, as the API pages
		var info ocMessageInfo
		if json.Unmarshal([]byte(rows[i].Data), &info) != nil {
			continue
		}
		if info.ID == "" {
			info.ID = rows[i].ID
		}
		messages = append(messages, ocMessage{Info: info, Parts: byMessage[rows[i].ID]})
	}
	return messages, cursor, nil
}

// validOpenCodeStoreID accepts OpenCode's ids (ses_…, msg_…, prt_…) and
// nothing that could change the meaning of a query it is spliced into.
func validOpenCodeStoreID(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, character := range id {
		if !((character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '_' || character == '-') {
			return false
		}
	}
	return true
}
