package usage

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// OpenCode 2 writes its replies to session_message, and copied the ones
// from before into it under the same ids; a reply still only in OpenCode 1's
// message table is read from there, and none twice.
func TestOpenCode2RepliesComeFromItsNewTable(t *testing.T) {
	bin, err := exec.LookPath("sqlite3")
	if err != nil {
		t.Skip("no sqlite3")
	}
	db := filepath.Join(t.TempDir(), "opencode.db")
	now := itoa(int(time.Now().UnixMilli()))
	sql := `create table message (id text primary key, session_id text not null, time_created integer not null, time_updated integer not null, data text not null);` +
		`create table session_message (id text primary key, session_id text not null, type text not null, seq integer not null, time_created integer not null, time_updated integer not null, data text not null);` +
		// Copied by OpenCode 2: in both tables.
		`insert into message values ('m1','ses_1',` + now + `,0,'{"role":"assistant","modelID":"gpt-6.1-sol","providerID":"openai","tokens":{"input":10,"output":1,"reasoning":0,"cache":{"write":0,"read":0}}}');` +
		`insert into session_message values ('m1','ses_1','assistant',1,` + now + `,0,'{"model":{"id":"gpt-6.1-sol","providerID":"openai"},"tokens":{"input":10,"output":1,"reasoning":0,"cache":{"write":0,"read":0}}}');` +
		// Only in the old table.
		`insert into message values ('m0','ses_0',` + now + `,0,'{"role":"assistant","modelID":"gpt-6.1-sol","providerID":"openai","tokens":{"input":7,"output":2,"reasoning":0,"cache":{"write":0,"read":0}}}');` +
		// Written by OpenCode 2.
		`insert into session_message values ('m2','ses_2','assistant',2,` + now + `,0,'{"model":{"id":"gpt-6.1-sol","providerID":"openai"},"tokens":{"input":338,"output":1511,"reasoning":1049,"cache":{"read":69120,"write":0}}}');` +
		`insert into session_message values ('m3','ses_2','user',3,` + now + `,0,'{"text":"hi"}');`
	if out, err := exec.Command(bin, db, sql).CombinedOutput(); err != nil {
		t.Fatalf("sqlite3: %v %s", err, out)
	}
	records, skipped := readOpenCode(context.Background(), db, time.Now().Add(-time.Hour))
	if skipped != 0 || len(records) != 3 {
		t.Fatalf("records = %+v, skipped %d", records, skipped)
	}
	bySession := map[string]Record{}
	for _, r := range records {
		bySession[r.Session] = r
	}
	if r := bySession["ses_2"]; r.Model != "gpt-6.1-sol" || r.Provider != "openai" ||
		r.Tokens != (protocol.Tokens{Input: 338, CacheRead: 69120, Output: 2560}) {
		t.Errorf("OpenCode 2's reply = %+v", r)
	}
	if bySession["ses_0"].Tokens.Input != 7 || bySession["ses_1"].Tokens.Input != 10 {
		t.Errorf("records = %+v", records)
	}
}
