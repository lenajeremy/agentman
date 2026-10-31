package usage

import (
	"context"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// A protobuf message written field by field, as agy writes its metadata.
type pb []byte

func (m pb) varint(field int, v uint64) pb {
	m = appendVarint(m, uint64(field)<<3)
	return appendVarint(m, v)
}

func (m pb) bytes(field int, b []byte) pb {
	m = appendVarint(m, uint64(field)<<3|2)
	m = appendVarint(m, uint64(len(b)))
	return append(m, b...)
}

func appendVarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

func timestamp(at time.Time) pb { return pb{}.varint(1, uint64(at.Unix())) }

// agy's usage: the model's number, fresh input, output, cache writes, cache
// reads, and the response's id.
func agyUsage(model, input, output, write, read uint64, id string) pb {
	return pb{}.varint(1, model).varint(2, input).varint(3, output).varint(4, write).varint(5, read).bytes(11, []byte(id))
}

func sqliteDB(t *testing.T, path string, statements string) {
	t.Helper()
	bin, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("no sqlite3")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command(bin, path, statements).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3: %v %s", err, out)
	}
}

// A call is in agy's database twice, as its generation and as the step it
// served; it is counted once, at the generation's model and time. A retry's
// usage counts too, and a database that isn't a conversation is passed over.
func TestAntigravityCallsAreReadOnceEach(t *testing.T) {
	home := t.TempDir()
	at := time.Now().Add(-2 * time.Hour).Truncate(time.Second)
	generation := pb{}.bytes(1, pb{}.
		varint(3, 1417).
		bytes(4, agyUsage(1417, 2000, 300, 0, 9000, "resp-1")).
		bytes(9, pb{}.bytes(4, timestamp(at))).
		bytes(17, pb{}.bytes(2, agyUsage(1417, 100, 0, 0, 0, "resp-retry"))).
		bytes(19, []byte("Gemini 3.8 Flash (High)")))
	step := pb{}.
		bytes(8, timestamp(at.Add(time.Second))).
		bytes(9, agyUsage(1417, 2000, 300, 0, 9000, "resp-1")).
		bytes(24, pb{}.bytes(12, []byte("Gemini 3.8 Flash")))
	// A step on its own, its model only in the usage's number.
	alone := pb{}.
		bytes(8, timestamp(at.Add(time.Minute))).
		bytes(9, agyUsage(1035, 50, 5, 0, 0, "resp-2"))
	conversations := filepath.Join(home, ".gemini", "antigravity-cli", "conversations")
	sqliteDB(t, filepath.Join(conversations, "conv-1.db"), `
		create table gen_metadata (idx integer, data blob);
		create table steps (idx integer, metadata blob);
		insert into gen_metadata values (0, X'`+hex.EncodeToString(generation)+`');
		insert into steps values (0, X'`+hex.EncodeToString(step)+`'), (1, X'`+hex.EncodeToString(alone)+`'), (2, null);`)
	sqliteDB(t, filepath.Join(home, ".gemini", "antigravity-cli", "index.db"), `create table conversations (id text);`)

	s := NewScanner(home, WithCatalog(prices))
	records, skipped := s.Records(context.Background(), time.Now().Add(-24*time.Hour))
	if skipped != 0 {
		t.Fatalf("skipped %d", skipped)
	}
	if len(records) != 3 {
		t.Fatalf("want the call, its retry and the lone step: %+v", records)
	}
	byInput := map[int64]Record{}
	for _, r := range records {
		if r.Agent != protocol.KindAntigravity || r.Session != "conv-1" {
			t.Fatalf("record %+v", r)
		}
		byInput[r.Tokens.Input] = r
	}
	call := byInput[2000]
	if call.Model != "gemini-3.8-flash" || call.Provider != "google" || !call.At.Equal(at) ||
		call.Tokens != (protocol.Tokens{Input: 2000, CacheRead: 9000, Output: 300}) {
		t.Fatalf("the call, once, at its generation's time: %+v", call)
	}
	if retry := byInput[100]; retry.Model != "gemini-3.8-flash" {
		t.Fatalf("the retry: %+v", retry)
	}
	if lone := byInput[50]; lone.Model != "claude-sonnet-4-6" || lone.Provider != "anthropic" || lone.Tokens.Output != 5 {
		t.Fatalf("the lone step, its model by number: %+v", lone)
	}
}

func TestAntigravityModelNames(t *testing.T) {
	for name, want := range map[string]string{
		"Claude Sonnet 4.6 (Thinking)": "claude-sonnet-4-6",
		"Claude 4.5 Opus":              "claude-opus-4-5",
		"Gemini 3.1 Pro (High)":        "gemini-3.1-pro",
		"Gemini 3.8 Flash-low":         "gemini-3.8-flash",
	} {
		if got := antigravityModel(name, 0); got != want {
			t.Errorf("%q: %q, want %q", name, got, want)
		}
	}
	if got := antigravityModel("", 246); got != "gemini-2.5-pro" {
		t.Errorf("by number: %q", got)
	}
	if got := antigravityModel("", 0); got != "antigravity-unknown" {
		t.Errorf("nothing: %q", got)
	}
}
