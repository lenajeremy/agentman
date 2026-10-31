package usage

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// Antigravity (agy) keeps each conversation in its own SQLite database. A
// model call's tokens sit in protobuf blobs: once in gen_metadata (a call's
// generation) and again in steps (the step it served), so the two are
// matched by the ids the usage carries and counted once. The layout is
// T3 Code's reading of it, checked against agy's own databases.

const antigravityQueryTimeout = 20 * time.Second

// antigravityDB is what was read from one database, kept until it changes.
type antigravityDB struct {
	fingerprint string
	calls       []antigravityCall
	err         bool
}

// antigravityCall is one usage found in a database, before it is matched
// with its copies.
type antigravityCall struct {
	record Record
	keys   []string
	// timed is how sure the time is: 2 the call's own, 1 the
	// conversation's, 0 the file's.
	timed int
}

type antigravityReader struct {
	mu  sync.Mutex
	dbs map[string]*antigravityDB
}

func newAntigravityReader() *antigravityReader {
	return &antigravityReader{dbs: map[string]*antigravityDB{}}
}

// read returns the calls made since since in every conversation under the
// roots, each counted once, and how many databases couldn't be read.
func (a *antigravityReader) read(ctx context.Context, roots []string, since time.Time) ([]Record, int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	bin, err := exec.LookPath("sqlite3")
	if err != nil {
		return nil, 0
	}
	present := map[string]bool{}
	var calls []antigravityCall
	skipped := 0
	for _, root := range roots {
		_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".db") || present[path] {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return nil
			}
			present[path] = true
			fingerprint, changed := dbFingerprint(path, info)
			if changed.Before(since) {
				return nil
			}
			db := a.dbs[path]
			if db == nil || db.fingerprint != fingerprint {
				db = &antigravityDB{fingerprint: fingerprint}
				db.calls, db.err = readAntigravityDB(ctx, bin, path, info.ModTime())
				a.dbs[path] = db
			}
			if db.err {
				skipped++
			}
			calls = append(calls, db.calls...)
			return nil
		})
	}
	// Databases gone are let go; those only past the range are kept.
	for path := range a.dbs {
		if !present[path] {
			delete(a.dbs, path)
		}
	}
	var out []Record
	for _, r := range mergeAntigravity(calls) {
		if !r.At.Before(since) {
			out = append(out, r)
		}
	}
	return out, skipped
}

// dbFingerprint names a database's state, its write-ahead log's included:
// new rows land in the log first.
func dbFingerprint(path string, info fs.FileInfo) (string, time.Time) {
	changed := info.ModTime()
	fingerprint := info.ModTime().String() + "/" + strconv.FormatInt(info.Size(), 10)
	if wal, err := os.Stat(path + "-wal"); err == nil {
		fingerprint += "/" + wal.ModTime().String() + "/" + strconv.FormatInt(wal.Size(), 10)
		if wal.ModTime().After(changed) {
			changed = wal.ModTime()
		}
	}
	return fingerprint, changed
}

// readAntigravityDB reads one conversation's usage. A database that isn't a
// conversation's is passed over; one that can't be read says so.
func readAntigravityDB(ctx context.Context, bin, path string, modified time.Time) ([]antigravityCall, bool) {
	tables, err := querySQLite(ctx, bin, path, `select name as n from sqlite_master where type = 'table'`)
	if err != nil {
		return nil, true
	}
	has := map[string]bool{}
	for _, row := range tables {
		has[row.Name] = true
	}
	if !has["gen_metadata"] && !has["steps"] {
		return nil, false
	}
	var parts []string
	if has["gen_metadata"] {
		parts = append(parts, `select 'g' as k, idx as i, hex(data) as h from gen_metadata where data is not null`)
	}
	if has["steps"] {
		parts = append(parts, `select 's', idx, hex(metadata) from steps where metadata is not null`)
	}
	if has["trajectory_metadata_blob"] {
		parts = append(parts, `select 't', 0, hex(data) from trajectory_metadata_blob where data is not null`)
	}
	rows, err := querySQLite(ctx, bin, path, strings.Join(parts, " union all "))
	if err != nil {
		return nil, true
	}
	session := strings.TrimSuffix(filepath.Base(path), ".db")
	var conversationAt time.Time
	type entry struct {
		idx  int64
		meta antigravityMeta
	}
	var generations, steps []entry
	bad := false
	for _, row := range rows {
		blob, err := hex.DecodeString(row.Hex)
		if err != nil {
			bad = true
			continue
		}
		switch row.Kind {
		case "t":
			if conversationAt.IsZero() {
				if fields, err := protoFields(blob); err == nil {
					conversationAt = protoTime(fields.nested(2))
				}
			}
		case "g", "s":
			meta, err := antigravityMetadata(blob, row.Kind == "s")
			if err != nil {
				bad = true
				continue
			}
			if row.Kind == "g" {
				generations = append(generations, entry{row.Index, meta})
			} else {
				steps = append(steps, entry{row.Index, meta})
			}
		}
	}
	models := map[int64]string{}
	for _, g := range generations {
		models[g.idx] = g.meta.model
	}
	var calls []antigravityCall
	for _, source := range []struct {
		step    bool
		entries []entry
	}{{true, steps}, {false, generations}} {
		for _, e := range source.entries {
			for _, u := range e.meta.usages {
				output := max(u.number(3), u.number(9)+u.number(10))
				tokens := protocol.Tokens{Input: u.number(2), CacheRead: u.number(5), CacheWrite: u.number(4), Output: output}
				if tokens.Total() == 0 {
					continue
				}
				model := antigravityModelIDs[u.number(1)]
				if model == "" {
					model = e.meta.model
				}
				if model == "" && source.step {
					model = models[e.idx]
				}
				if model == "" {
					model = antigravityModel("", u.number(1))
				}
				call := antigravityCall{timed: 2}
				at := e.meta.at
				if at.IsZero() {
					at, call.timed = conversationAt, 1
				}
				if at.IsZero() {
					at, call.timed = modified, 0
				}
				for _, key := range []int{11, 12, 7} {
					if id := u.text(key); id != "" {
						call.keys = append(call.keys, strconv.Itoa(key)+":"+id)
					}
				}
				call.record = Record{
					Agent: protocol.KindAntigravity, Session: session, Model: model, Provider: antigravityProvider(model), At: at,
					Tokens: tokens, Context: tokens.Input + tokens.CacheRead + tokens.CacheWrite,
				}
				calls = append(calls, call)
			}
		}
	}
	return calls, bad
}

// mergeAntigravity counts each call once: usages that share an id are one
// call, kept with the larger of each count and the surest time.
func mergeAntigravity(calls []antigravityCall) []Record {
	parent := make([]int, len(calls))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(i int) int {
		for parent[i] != i {
			parent[i] = parent[parent[i]]
			i = parent[i]
		}
		return i
	}
	owner := map[string]int{}
	for i, c := range calls {
		for _, key := range c.keys {
			if j, ok := owner[key]; ok {
				a, b := find(i), find(j)
				if a != b {
					parent[max(a, b)] = min(a, b)
				}
			} else {
				owner[key] = i
			}
		}
	}
	merged := map[int]*antigravityCall{}
	var order []int
	for i := range calls {
		root := find(i)
		m := merged[root]
		if m == nil {
			c := calls[i]
			merged[root] = &c
			order = append(order, root)
			continue
		}
		c := calls[i]
		t := &m.record.Tokens
		t.Input, t.CacheRead = max(t.Input, c.record.Tokens.Input), max(t.CacheRead, c.record.Tokens.CacheRead)
		t.CacheWrite, t.Output = max(t.CacheWrite, c.record.Tokens.CacheWrite), max(t.Output, c.record.Tokens.Output)
		m.record.Context = t.Input + t.CacheRead + t.CacheWrite
		if m.record.Model == "antigravity-unknown" {
			m.record.Model, m.record.Provider = c.record.Model, c.record.Provider
		}
		if c.timed > m.timed || c.timed == m.timed && c.record.At.Before(m.record.At) {
			m.record.At, m.timed = c.record.At, c.timed
		}
	}
	out := make([]Record, 0, len(order))
	for _, root := range order {
		out = append(out, merged[root].record)
	}
	return out
}

// antigravityMeta is what a generation or a step says: its model, when it
// ran, and its usage, a retry's included.
type antigravityMeta struct {
	model  string
	at     time.Time
	usages []protoMessage
}

// antigravityMetadata reads a generation's blob (its message in field 1) or a
// step's.
func antigravityMetadata(blob []byte, step bool) (antigravityMeta, error) {
	root, err := protoFields(blob)
	if err != nil {
		return antigravityMeta{}, err
	}
	data := root
	if !step {
		if root.bytes(1) == nil {
			return antigravityMeta{}, errors.New("usage: antigravity generation without its message")
		}
		if data, err = protoFields(root.bytes(1)); err != nil {
			return antigravityMeta{}, err
		}
	}
	usageField, retryField, modelName, modelLabel, modelID := 4, 17, 19, 21, 3
	model := data
	if step {
		usageField, retryField, modelName, modelLabel, modelID = 9, 28, 12, 8, 1
		model = data.nested(24)
	}
	var meta antigravityMeta
	if u := data.bytes(usageField); u != nil {
		fields, err := protoFields(u)
		if err != nil {
			return antigravityMeta{}, err
		}
		meta.usages = append(meta.usages, fields)
	}
	for _, retry := range data[retryField] {
		if retry.bytes == nil {
			return antigravityMeta{}, errors.New("usage: antigravity retry isn't a message")
		}
		r, err := protoFields(retry.bytes)
		if err != nil {
			return antigravityMeta{}, err
		}
		if u := r.bytes(2); u != nil {
			fields, err := protoFields(u)
			if err != nil {
				return antigravityMeta{}, err
			}
			meta.usages = append(meta.usages, fields)
		}
	}
	name := model.text(modelName)
	if name == "" {
		name = model.text(modelLabel)
	}
	meta.model = antigravityModel(name, model.number(modelID))
	if step {
		if meta.at = protoTime(data.nested(8)); meta.at.IsZero() {
			meta.at = protoTime(data.nested(1))
		}
	} else {
		meta.at = protoTime(data.nested(9).nested(4))
	}
	return meta, nil
}

// antigravityModelIDs names the models agy knows by number alone.
var antigravityModelIDs = map[int64]string{
	246: "gemini-2.5-pro", 312: "gemini-2.5-flash", 313: "gemini-2.5-flash-thinking", 329: "gemini-2.5-flash-thinking",
	330: "gemini-2.5-flash-lite", 281: "claude-sonnet-4", 282: "claude-sonnet-4", 290: "claude-opus-4", 291: "claude-opus-4",
	333: "claude-sonnet-4-5", 334: "claude-sonnet-4-5", 340: "claude-haiku-4-5", 341: "claude-haiku-4-5",
	1026: "claude-opus-4-6", 1035: "claude-sonnet-4-6", 1016: "gemini-3.1-pro", 1036: "gemini-3.1-pro", 1037: "gemini-3.1-pro",
	1018: "gemini-3-flash-preview", 1084: "gemini-3-flash-preview", 1047: "gemini-3-flash-preview",
}

var (
	parenthesised = regexp.MustCompile(`\s*\([^)]*\)\s*$`)
	claudeVersion = regexp.MustCompile(`^claude-(4(?:\.\d+)?)-(sonnet|opus|haiku)`)
	effort        = regexp.MustCompile(`-(?:low|medium|high)$`)
)

// antigravityModel makes agy's display name a model id: "Claude Sonnet 4.6
// (Thinking)" is claude-sonnet-4-6, "Gemini 3.1 Pro (High)" gemini-3.1-pro.
func antigravityModel(name string, id int64) string {
	if name != "" {
		model := strings.ReplaceAll(parenthesised.ReplaceAllString(strings.ToLower(name), ""), " ", "-")
		if strings.HasPrefix(model, "claude-") {
			model = strings.ReplaceAll(claudeVersion.ReplaceAllString(model, "claude-$2-$1"), ".", "-")
		}
		return effort.ReplaceAllString(model, "")
	}
	if model, ok := antigravityModelIDs[id]; ok {
		return model
	}
	if id > 0 {
		return "antigravity-model-" + strconv.FormatInt(id, 10)
	}
	return "antigravity-unknown"
}

// antigravityProvider is whose prices a model of agy's goes by.
func antigravityProvider(model string) string {
	switch {
	case strings.HasPrefix(model, "claude-"):
		return "anthropic"
	case strings.HasPrefix(model, "gpt-"), strings.HasPrefix(model, "o3"), strings.HasPrefix(model, "o4"):
		return "openai"
	default:
		return "google"
	}
}

type sqliteRow struct {
	Name  string `json:"n"`
	Kind  string `json:"k"`
	Index int64  `json:"i"`
	Hex   string `json:"h"`
}

// querySQLite runs a query on a database read-only. agy keeps its databases
// in WAL mode: while it runs, the -wal and -shm files let a reader in; once
// it exits they are folded in and removed, and the database is complete,
// which immutable=1 tells SQLite.
func querySQLite(ctx context.Context, bin, path, query string) ([]sqliteRow, error) {
	ctx, cancel := context.WithTimeout(ctx, antigravityQueryTimeout)
	defer cancel()
	uri := "file:" + path + "?mode=ro"
	if _, err := os.Stat(path + "-wal"); os.IsNotExist(err) {
		uri += "&immutable=1"
	}
	out, err := exec.CommandContext(ctx, bin, "-json", "-readonly", "-cmd", ".timeout 1000", uri, query).Output()
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(out)) == 0 {
		return nil, nil
	}
	var rows []sqliteRow
	if err := json.Unmarshal(out, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

// A protobuf message read without its schema: each field number's values,
// a number or bytes.

type protoValue struct {
	number uint64
	bytes  []byte
}

type protoMessage map[int][]protoValue

func protoFields(b []byte) (protoMessage, error) {
	out := protoMessage{}
	for len(b) > 0 {
		tag, n := protoVarint(b)
		if n <= 0 {
			return nil, errors.New("usage: bad protobuf tag")
		}
		b = b[n:]
		field, wire := int(tag>>3), tag&7
		if field == 0 {
			return nil, errors.New("usage: bad protobuf field")
		}
		switch wire {
		case 0:
			v, n := protoVarint(b)
			if n <= 0 {
				return nil, errors.New("usage: bad protobuf varint")
			}
			b = b[n:]
			out[field] = append(out[field], protoValue{number: v})
		case 1, 5:
			size := 8
			if wire == 5 {
				size = 4
			}
			if len(b) < size {
				return nil, errors.New("usage: truncated protobuf field")
			}
			b = b[size:]
		case 2:
			size, n := protoVarint(b)
			if n <= 0 || size > uint64(len(b)-n) {
				return nil, errors.New("usage: truncated protobuf field")
			}
			b = b[n:]
			out[field] = append(out[field], protoValue{bytes: b[:size]})
			b = b[size:]
		default:
			return nil, errors.New("usage: unsupported protobuf wire type")
		}
	}
	return out, nil
}

func protoVarint(b []byte) (uint64, int) {
	var v uint64
	for i := 0; i < len(b) && i < 10; i++ {
		v |= uint64(b[i]&0x7f) << (7 * i)
		if b[i] < 0x80 {
			return v, i + 1
		}
	}
	return 0, -1
}

func (m protoMessage) number(field int) int64 {
	if values := m[field]; len(values) > 0 && values[0].bytes == nil {
		return int64(values[0].number)
	}
	return 0
}

func (m protoMessage) bytes(field int) []byte {
	if values := m[field]; len(values) > 0 {
		return values[0].bytes
	}
	return nil
}

func (m protoMessage) nested(field int) protoMessage {
	b := m.bytes(field)
	if b == nil {
		return protoMessage{}
	}
	fields, err := protoFields(b)
	if err != nil {
		return protoMessage{}
	}
	return fields
}

func (m protoMessage) text(field int) string {
	b := m.bytes(field)
	if b == nil || !utf8.Valid(b) {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// protoTime reads a google.protobuf.Timestamp: seconds, then nanoseconds.
func protoTime(m protoMessage) time.Time {
	seconds := m.number(1)
	if seconds <= 0 {
		return time.Time{}
	}
	return time.Unix(seconds, m.number(2)).Round(time.Millisecond)
}
