// Package search finds words in what agents and the people using them said
// to each other, across every conversation on this Mac.
//
// Brute force read 1.9 GB of transcripts in 11 to 17 seconds; by November
// 2026 there were 7.9 GB. Nearly all of that is tool output, though: the
// conversation itself, what was typed and what the agents answered, was 60
// million characters in 45,000 messages. So that alone is indexed, in SQLite
// with FTS5's trigram tokenizer, which finds any run of three or more
// characters without reading every message: a query takes milliseconds. The
// first build reads every transcript once; after that only what was added.
//
// Messages are read through each agent's own adapter, the same reading the
// apps get, so a hit names the message by the id the apps already know and
// can be scrolled to.
package search

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// schemaVersion is bumped whenever what is stored changes; an index of
// another version is thrown away and read again.
const schemaVersion = 2

const (
	// hitLimit bounds the messages one search grades. FTS5 finds them in
	// milliseconds even for "the"; reading thousands of texts back is what
	// costs, and a word in more messages than this is not narrowing much.
	hitLimit = 2000
	// inSessionLimit bounds the matches in one conversation, for its find bar.
	inSessionLimit = 1000
	// DefaultLimit is how many conversations a search returns.
	DefaultLimit = 50
	maxLimit     = 200
	// recencyHalfLife is how fast recent work stops counting for more: a
	// conversation from two weeks ago gets half the lift of today's.
	recencyHalfLife = 14 * 24 * time.Hour
)

// Index is the store of messages and their trigrams.
type Index struct {
	db *sql.DB
}

// Open opens the index at path, creating it, or starting it afresh when it
// was written by another version.
func Open(path string) (*Index, error) {
	db, err := open(path)
	if err != nil {
		return nil, err
	}
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		db.Close()
		return nil, err
	}
	if version != schemaVersion && version != 0 {
		db.Close()
		for _, suffix := range []string{"", "-wal", "-shm"} {
			if err := os.Remove(path + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
				return nil, err
			}
		}
		if db, err = open(path); err != nil {
			return nil, err
		}
	}
	for _, statement := range schema {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			return nil, fmt.Errorf("search: %w", err)
		}
	}
	return &Index{db: db}, nil
}

func open(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", "file:"+path+
		"?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(10000)")
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

var schema = []string{
	// One row per conversation. session is what the apps are sent with a
	// hit, so one that has ended can still be shown and opened; indexed is
	// the last activity the conversation was read up to.
	`CREATE TABLE IF NOT EXISTS sessions (
		key TEXT PRIMARY KEY,
		session TEXT NOT NULL,
		indexed INTEGER NOT NULL DEFAULT 0
	)`,
	// A message's id is its time, in microseconds, give or take a few
	// for messages of one millisecond: the index lists matches by id, so
	// newest first is its own order, and a search reads only the newest
	// matches rather than all of them to sort.
	`CREATE TABLE IF NOT EXISTS messages (
		id INTEGER PRIMARY KEY,
		key TEXT NOT NULL,
		entry TEXT NOT NULL,
		role TEXT NOT NULL,
		at INTEGER NOT NULL,
		text TEXT NOT NULL,
		UNIQUE (key, entry)
	)`,
	// Contentless: the text is in messages already, and the index only
	// needs its trigrams. remove_diacritics finds "café" for "cafe".
	`CREATE VIRTUAL TABLE IF NOT EXISTS message_text USING fts5(
		text, content = '', contentless_delete = 1,
		tokenize = 'trigram remove_diacritics 1'
	)`,
	fmt.Sprintf(`PRAGMA user_version = %d`, schemaVersion),
}

// Close closes the index.
func (x *Index) Close() error {
	return x.db.Close()
}

// Key is what one conversation is stored under: its agent's own id for it.
//
// Not the session id. Kiro, Codex and Antigravity name a session running in
// an Agentman pane after the pane, and the same conversation after its
// transcript once it has ended; read under both, it would be indexed twice.
func Key(session protocol.Session) string {
	if session.NativeID != "" {
		return string(session.Kind) + ":" + session.NativeID
	}
	return session.ID
}

// indexed reports how far a conversation has been read: the last activity
// it was read up to, or 0 when it never was all the way.
func (x *Index) indexed(ctx context.Context, key string) (int64, error) {
	var at int64
	err := x.db.QueryRowContext(ctx, `SELECT indexed FROM sessions WHERE key = ?`, key).Scan(&at)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return at, err
}

// known returns which of ids are stored for a conversation already.
func (x *Index) known(ctx context.Context, key string, ids []string) (map[string]bool, error) {
	known := map[string]bool{}
	if len(ids) == 0 {
		return known, nil
	}
	list, _ := json.Marshal(ids)
	rows, err := x.db.QueryContext(ctx,
		`SELECT entry FROM messages WHERE key = ? AND entry IN (SELECT value FROM json_each(?))`, key, string(list))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		known[id] = true
	}
	return known, rows.Err()
}

// add stores messages and the conversation they belong to. A message
// already stored is left as it was.
func (x *Index) add(ctx context.Context, session protocol.Session, messages []protocol.Message) error {
	blob, err := json.Marshal(session)
	if err != nil {
		return err
	}
	tx, err := x.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO sessions (key, session) VALUES (?, ?)
		 ON CONFLICT (key) DO UPDATE SET session = excluded.session`, Key(session), string(blob)); err != nil {
		return err
	}
	for _, message := range messages {
		var exists int
		err := tx.QueryRowContext(ctx, `SELECT count(*) FROM messages WHERE key = ? AND entry = ?`,
			Key(session), message.ID).Scan(&exists)
		if err != nil {
			return err
		}
		if exists > 0 {
			continue
		}
		rowid, err := insertMessage(ctx, tx, Key(session), message)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO message_text (rowid, text) VALUES (?, ?)`,
			rowid, straightApostrophes(readable(message.Text))); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// insertMessage stores a message under the first free id at its time.
func insertMessage(ctx context.Context, tx *sql.Tx, key string, message protocol.Message) (int64, error) {
	id := max(message.Ts, 0) * 1000
	for {
		var taken int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM messages WHERE id = ?`, id).Scan(&taken); err != nil {
			return 0, err
		}
		if taken == 0 {
			break
		}
		id++
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO messages (id, key, entry, role, at, text) VALUES (?, ?, ?, ?, ?, ?)`,
		id, key, message.ID, string(message.Role), message.Ts, message.Text)
	return id, err
}

// done records that a conversation has been read up to its last activity.
func (x *Index) done(ctx context.Context, key string, lastActivity int64) error {
	_, err := x.db.ExecContext(ctx, `UPDATE sessions SET indexed = ? WHERE key = ?`, lastActivity, key)
	return err
}

// Counts reports how many conversations and messages are stored.
func (x *Index) Counts(ctx context.Context) (sessions, messages int, err error) {
	if err = x.db.QueryRowContext(ctx, `SELECT count(*) FROM sessions`).Scan(&sessions); err != nil {
		return
	}
	err = x.db.QueryRowContext(ctx, `SELECT count(*) FROM messages`).Scan(&messages)
	return
}

// Query is one search.
type Query struct {
	Text string
	// Key keeps the search to one conversation, and returns every message
	// that matched there, oldest first, for its find bar.
	Key   string
	Limit int
}

// Hit is a message that matched.
type Hit struct {
	Key     string
	Session protocol.Session
	Message protocol.Message
	Snippet string
	Quality float64
	// Matches is how many of the conversation's messages matched as well.
	Matches int

	// text and at make the snippet, once the hit is kept.
	text string
	at   int
}

type row struct {
	key, entry, role, text string
	at                     int64
}

// Search finds a query's words. Across conversations it returns the best
// message of each, the best conversations first; in one conversation, every
// message that matched, in order.
func (x *Index) Search(ctx context.Context, q Query, now time.Time) ([]Hit, error) {
	t := splitQuery(q.Text)
	if len(t.long) == 0 {
		return nil, nil
	}
	rows, err := x.candidates(ctx, t, q.Key)
	if err != nil {
		return nil, err
	}
	m := newMatcher(t)
	var hits []Hit
	for _, r := range rows {
		text := readable(r.text)
		g, ok := m.grade(text)
		if !ok {
			continue
		}
		hits = append(hits, Hit{
			Key: r.key,
			Message: protocol.Message{
				ID: r.entry, Role: protocol.Role(r.role), Ts: r.at,
			},
			Quality: g.quality,
			text:    text,
			at:      g.at,
		})
	}
	if q.Key == "" {
		if hits, err = x.rank(ctx, hits, q.Limit, now); err != nil {
			return nil, err
		}
	} else {
		sort.SliceStable(hits, func(i, j int) bool { return hits[i].Message.Ts < hits[j].Message.Ts })
	}
	for i := range hits {
		hits[i].Snippet = snippet(hits[i].text, hits[i].at)
		hits[i].text = ""
	}
	return hits, nil
}

// candidates finds the messages holding every long word, newest first.
//
// Words of three or more characters are looked up as quoted phrases, which
// the trigram index matches anywhere in a message. A word the index cannot
// look up (two CJK characters) means scanning the newest messages instead.
func (x *Index) candidates(ctx context.Context, t terms, key string) ([]row, error) {
	limit := hitLimit
	if key != "" {
		limit = inSessionLimit * 4
	}
	indexed := true
	for _, word := range t.long {
		if len([]rune(word)) < searchableLength {
			indexed = false
		}
	}
	var (
		rows *sql.Rows
		err  error
	)
	if indexed {
		phrases := make([]string, len(t.long))
		for i, word := range t.long {
			phrases[i] = `"` + strings.ReplaceAll(word, `"`, `""`) + `"`
		}
		rows, err = x.db.QueryContext(ctx, `
			SELECT m.key, m.entry, m.role, m.at, m.text
			FROM message_text JOIN messages AS m ON m.id = message_text.rowid
			WHERE message_text MATCH ? AND (? = '' OR m.key = ?)
			ORDER BY message_text.rowid DESC LIMIT ?`, strings.Join(phrases, " "), key, key, limit)
	} else {
		rows, err = x.db.QueryContext(ctx, `
			SELECT key, entry, role, at, text FROM (
				SELECT * FROM messages WHERE (? = '' OR key = ?) ORDER BY id DESC LIMIT 20000
			) WHERE instr(lower(text), ?) > 0
			ORDER BY id DESC LIMIT ?`, key, key, t.long[0], limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var found []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.key, &r.entry, &r.role, &r.at, &r.text); err != nil {
			return nil, err
		}
		found = append(found, r)
	}
	return found, rows.Err()
}

// rank keeps each conversation's best message and orders the conversations.
//
// A conversation that matched a word as a word, or its start, comes before
// one that only had it inside other words. Among equals, one that keeps
// returning to the word outweighs a passing mention, and recent work counts
// for more; the newest message breaks a tie.
func (x *Index) rank(ctx context.Context, hits []Hit, limit int, now time.Time) ([]Hit, error) {
	if limit <= 0 {
		limit = DefaultLimit
	}
	limit = min(limit, maxLimit)
	best := map[string]*Hit{}
	var order []string
	for i := range hits {
		hit := hits[i]
		current, seen := best[hit.Key]
		if !seen {
			hit.Matches = 1
			best[hit.Key] = &hit
			order = append(order, hit.Key)
			continue
		}
		switch {
		case hit.Quality > current.Quality:
			hit.Matches = 1
			*current = hit
		case hit.Quality == current.Quality:
			current.Matches++
			// Rows come newest first, so a tie keeps the newer message —
			// unless this one is what the user typed, which is what they
			// will remember.
			if hit.Message.Role == protocol.RoleUser && current.Message.Role != protocol.RoleUser {
				matches := current.Matches
				*current = hit
				current.Matches = matches
			}
		}
	}
	sessions, err := x.sessions(ctx, order)
	if err != nil {
		return nil, err
	}
	ranked := make([]Hit, 0, len(order))
	score := map[string]float64{}
	for _, key := range order {
		session, ok := sessions[key]
		if !ok {
			continue
		}
		hit := *best[key]
		hit.Session = session
		ranked = append(ranked, hit)
		age := now.Sub(time.UnixMilli(session.LastActivityAt))
		recency := math.Pow(2, -max(0, age.Hours())/recencyHalfLife.Hours())
		score[key] = (1 + math.Log2(1+float64(hit.Matches))) * (1 + recency)
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		a, b := ranked[i], ranked[j]
		if strong := a.Quality >= wordStart; strong != (b.Quality >= wordStart) {
			return strong
		}
		if a.Quality != b.Quality {
			return a.Quality > b.Quality
		}
		if score[a.Key] != score[b.Key] {
			return score[a.Key] > score[b.Key]
		}
		return a.Message.Ts > b.Message.Ts
	})
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	return ranked, nil
}

// sessions reads the stored conversations under keys.
func (x *Index) sessions(ctx context.Context, keys []string) (map[string]protocol.Session, error) {
	found := map[string]protocol.Session{}
	if len(keys) == 0 {
		return found, nil
	}
	list, _ := json.Marshal(keys)
	rows, err := x.db.QueryContext(ctx,
		`SELECT key, session FROM sessions WHERE key IN (SELECT value FROM json_each(?))`, string(list))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var key, blob string
		if err := rows.Scan(&key, &blob); err != nil {
			return nil, err
		}
		var session protocol.Session
		if json.Unmarshal([]byte(blob), &session) == nil {
			found[key] = session
		}
	}
	return found, rows.Err()
}

// Session reads one stored conversation.
func (x *Index) Session(ctx context.Context, key string) (protocol.Session, bool) {
	found, err := x.sessions(ctx, []string{key})
	if err != nil {
		return protocol.Session{}, false
	}
	session, ok := found[key]
	return session, ok
}
