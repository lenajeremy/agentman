package source

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// writeClaudeTranscript lays out one Claude transcript the way Claude does:
// under a project directory named after the working directory with '/' and
// '.' replaced by '-'. No registry file is written, so the session is one that
// has already exited — the whole point of Past.
func writeClaudeTranscript(t *testing.T, home, id, cwd string, lines ...string) string {
	t.Helper()
	dir := filepath.Join(home, ".claude", "projects", claudeProjectSlug(cwd))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, id+".jsonl")
	body := strings.Join(lines, "\n")
	if body != "" {
		body += "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// claudeUserLine is one real user turn, carrying the working directory the way
// every Claude transcript line does.
func claudeUserLine(cwd, text string) string {
	return fmt.Sprintf(`{"type":"user","isSidechain":false,"cwd":%q,"timestamp":"2026-03-05T22:52:36.211Z",`+
		`"message":{"role":"user","content":[{"type":"text","text":%q}]}}`, cwd, text)
}

// A session whose process is gone is invisible to Discover by design. It is
// still the thing the folder filter exists to show.
func TestClaudePastFindsSessionsThatHaveExited(t *testing.T) {
	home := t.TempDir()
	work := filepath.Join(home, "code", "api")
	writeClaudeTranscript(t, home, "11111111-1111-4111-8111-111111111111", work,
		claudeUserLine(work, "fix the retry backoff"))

	s, err := NewClaudeSource(home)
	if err != nil {
		t.Fatal(err)
	}
	live, err := s.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 0 {
		t.Fatalf("Discover found %d sessions; a transcript with no process is not live", len(live))
	}

	past, err := s.Past(context.Background(), work, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(past) != 1 {
		t.Fatalf("Past found %d sessions, want 1", len(past))
	}
	if past[0].State != protocol.StateEnded {
		t.Errorf("state = %q, want %q", past[0].State, protocol.StateEnded)
	}
	if past[0].Inject != protocol.InjectNone {
		t.Errorf("inject = %q; nothing can be typed into a session that has exited", past[0].Inject)
	}
	if past[0].Name != "fix the retry backoff" {
		t.Errorf("name = %q, want the opening prompt", past[0].Name)
	}
	if past[0].Cwd != work {
		t.Errorf("cwd = %q, want %q", past[0].Cwd, work)
	}
}

// Selecting a folder means the project, not one directory of it.
func TestClaudePastIncludesSubdirectories(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "code", "api")
	nested := filepath.Join(root, "mobile")
	writeClaudeTranscript(t, home, "11111111-1111-4111-8111-111111111111", root,
		claudeUserLine(root, "root work"))
	writeClaudeTranscript(t, home, "22222222-2222-4222-8222-222222222222", nested,
		claudeUserLine(nested, "nested work"))

	s, err := NewClaudeSource(home)
	if err != nil {
		t.Fatal(err)
	}
	past, err := s.Past(context.Background(), root, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(past) != 2 {
		t.Fatalf("Past found %d sessions, want both the root and its subdirectory", len(past))
	}
}

// The project directory name replaces '/' and '.' with '-', so it cannot tell
// a separator from a literal dash: /code/api-old shares a prefix with
// /code/api. The transcript's own cwd is what has to decide.
func TestClaudePastRejectsASiblingSharingASlugPrefix(t *testing.T) {
	home := t.TempDir()
	wanted := filepath.Join(home, "code", "api")
	sibling := filepath.Join(home, "code", "api-old")
	writeClaudeTranscript(t, home, "11111111-1111-4111-8111-111111111111", wanted,
		claudeUserLine(wanted, "wanted"))
	writeClaudeTranscript(t, home, "22222222-2222-4222-8222-222222222222", sibling,
		claudeUserLine(sibling, "sibling"))

	s, err := NewClaudeSource(home)
	if err != nil {
		t.Fatal(err)
	}
	past, err := s.Past(context.Background(), wanted, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(past) != 1 {
		t.Fatalf("Past found %d sessions, want only the one in %s", len(past), wanted)
	}
	if past[0].Name != "wanted" {
		t.Errorf("name = %q; api-old leaked into api", past[0].Name)
	}
}

// A blocked slug prefix match would still be wrong if the sibling were merely
// skipped by name, so this asserts the count the index reports too.
func TestClaudeDirectoriesCountsWithoutParsingEverySession(t *testing.T) {
	home := t.TempDir()
	work := filepath.Join(home, "code", "api")
	for i := range 5 {
		writeClaudeTranscript(t, home, fmt.Sprintf("%08d-1111-4111-8111-111111111111", i), work,
			claudeUserLine(work, "turn"))
	}

	s, err := NewClaudeSource(home)
	if err != nil {
		t.Fatal(err)
	}
	folders, err := s.Directories(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(folders) != 1 {
		t.Fatalf("got %d folders, want 1", len(folders))
	}
	if folders[0].Path != work {
		t.Errorf("path = %q, want %q", folders[0].Path, work)
	}
	if folders[0].Agents != 5 {
		t.Errorf("agents = %d, want 5", folders[0].Agents)
	}
}

// An ended session is opened from the folder list like any other, so Page has
// to resolve an id no sweep ever put in the live map.
func TestClaudePageReadsASessionOnlyPastKnows(t *testing.T) {
	home := t.TempDir()
	work := filepath.Join(home, "code", "api")
	id := "11111111-1111-4111-8111-111111111111"
	writeClaudeTranscript(t, home, id, work, claudeUserLine(work, "what changed?"))

	s, err := NewClaudeSource(home)
	if err != nil {
		t.Fatal(err)
	}
	sessionID := string(protocol.KindClaude) + ":" + id
	if _, err := s.Page(context.Background(), sessionID, "", 10); err == nil {
		t.Fatal("Page succeeded before Past ran; the session should be unknown")
	}
	if _, err := s.Past(context.Background(), work, 0); err != nil {
		t.Fatal(err)
	}
	page, err := s.Page(context.Background(), sessionID, "", 10)
	if err != nil {
		t.Fatalf("Page after Past: %v", err)
	}
	if len(page.Messages) == 0 {
		t.Error("page is empty; the ended session's transcript was not read")
	}
}

// A sweep replaces the live map wholesale. An ended session opened from the
// folder list must not vanish because of a sweep that has no reason to know
// about it.
func TestClaudePastSurvivesADiscoverySweep(t *testing.T) {
	home := t.TempDir()
	work := filepath.Join(home, "code", "api")
	id := "11111111-1111-4111-8111-111111111111"
	writeClaudeTranscript(t, home, id, work, claudeUserLine(work, "still here?"))

	s, err := NewClaudeSource(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Past(context.Background(), work, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Discover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Page(context.Background(), string(protocol.KindClaude)+":"+id, "", 10); err != nil {
		t.Fatalf("Page after a sweep: %v", err)
	}
}

// The blocks Claude Code injects ahead of a turn are not what anyone typed, so
// they cannot name a session. The set of them grows, so the stripper works by
// shape rather than by a list of tags.
func TestPromptStrippingIgnoresInjectedBlocks(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string
	}{
		{"plain", "fix the retry backoff", "fix the retry backoff"},
		{"opened file", "<ide_opened_file>The user opened x</ide_opened_file>", ""},
		{"reminder then prompt", "<system-reminder>be nice</system-reminder>\nwhat changed?", "what changed?"},
		{"tag with attributes", `<scheduled-task name="daily" freq="1d">run it</scheduled-task>`, ""},
		{"unknown future tag", "<some-tag-shipped-next-release>x</some-tag-shipped-next-release>", ""},
		{"compaction preamble", "This session is being continued from a previous conversation", ""},
		{"caveat", "Caveat: The messages below were generated by the user", ""},
		{"empty", "   ", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := usablePrompt(c.text); got != c.want {
				t.Errorf("usablePrompt(%q) = %q, want %q", c.text, got, c.want)
			}
		})
	}
}

func TestUnderDirectoryMatchesTheSubtreeAndNothingElse(t *testing.T) {
	cases := []struct {
		cwd, dir string
		want     bool
	}{
		{"/code/api", "/code/api", true},
		{"/code/api/mobile", "/code/api", true},
		{"/code/api/", "/code/api", true},
		{"/code/api-old", "/code/api", false},
		{"/code", "/code/api", false},
		{"/other/api", "/code/api", false},
		{"", "/code/api", false},
		{"/code/api", "", false},
	}
	for _, c := range cases {
		if got := underDirectory(c.cwd, c.dir); got != c.want {
			t.Errorf("underDirectory(%q, %q) = %v, want %v", c.cwd, c.dir, got, c.want)
		}
	}
}

// writeCodexHistoryRollout lays out one rollout the way Codex does: filed by
// date, with the working directory only in its session_meta header. Distinct
// from writeCodexRollout in codex_lifetime_test.go, which is about the live
// window and so takes times rather than a source and body lines.
func writeCodexHistoryRollout(t *testing.T, home, id, cwd, source string, lines ...string) string {
	t.Helper()
	dir := filepath.Join(home, ".codex", "sessions", "2026", "03", "05")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "rollout-2026-03-05T22-52-36-"+id+".jsonl")
	meta := fmt.Sprintf(`{"timestamp":"2026-03-05T23:01:15.820Z","type":"session_meta","payload":`+
		`{"id":%q,"session_id":%q,"cwd":%q,"timestamp":"2026-03-05T22:52:36.211Z","source":%s}}`,
		id, id, cwd, source)
	body := strings.Join(append([]string{meta}, lines...), "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// codexUserTurn is the completed-item event that marks a real user turn, as
// opposed to the developer preamble and AGENTS.md that open every rollout.
func codexUserTurn(text string) string {
	return fmt.Sprintf(`{"type":"event_msg","payload":{"type":"item_completed",`+
		`"item":{"type":"UserMessage","id":"item-1","content":[{"type":"text","text":%q}]}}}`, text)
}

func TestCodexPastFindsSessionsThatHaveExited(t *testing.T) {
	home := t.TempDir()
	work := filepath.Join(home, "code", "api")
	writeCodexHistoryRollout(t, home, "019cc033-f3f2-7c32-9215-a860efb9f9fd", work, `"cli"`,
		`{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"# AGENTS.md instructions for /code/api"}]}}`,
		codexUserTurn("why is the relay flaky?"))

	s, err := NewCodexSource(home)
	if err != nil {
		t.Fatal(err)
	}
	past, err := s.Past(context.Background(), work, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(past) != 1 {
		t.Fatalf("Past found %d sessions, want 1", len(past))
	}
	if past[0].State != protocol.StateEnded {
		t.Errorf("state = %q, want %q", past[0].State, protocol.StateEnded)
	}
	// AGENTS.md arrives under the user role and is not what anyone typed.
	if past[0].Name != "why is the relay flaky?" {
		t.Errorf("name = %q, want the first real prompt", past[0].Name)
	}
}

// Most rollouts in a busy project belong to subagents the session spawned.
// They are not sessions anyone opened, and Discover leaves them out, so
// history has to agree or a folder's count is mostly noise.
func TestCodexPastLeavesOutSubagentRollouts(t *testing.T) {
	home := t.TempDir()
	work := filepath.Join(home, "code", "api")
	writeCodexHistoryRollout(t, home, "019cc033-f3f2-7c32-9215-a860efb9f9fd", work, `"cli"`,
		codexUserTurn("the real session"))
	writeCodexHistoryRollout(t, home, "019cc044-f3f2-7c32-9215-a860efb9f9fd", work,
		`{"subagent":{"other":"guardian"}}`, codexUserTurn("a spawned helper"))

	s, err := NewCodexSource(home)
	if err != nil {
		t.Fatal(err)
	}
	past, err := s.Past(context.Background(), work, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(past) != 1 {
		t.Fatalf("Past found %d sessions, want only the one a person opened", len(past))
	}
	if past[0].Name != "the real session" {
		t.Errorf("name = %q", past[0].Name)
	}

	folders, err := s.Directories(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(folders) != 1 || folders[0].Agents != 1 {
		t.Errorf("Directories = %+v, want one folder holding one agent", folders)
	}
}

func TestCodexPageReadsASessionOnlyPastKnows(t *testing.T) {
	home := t.TempDir()
	work := filepath.Join(home, "code", "api")
	id := "019cc033-f3f2-7c32-9215-a860efb9f9fd"
	writeCodexHistoryRollout(t, home, id, work, `"cli"`, codexUserTurn("what changed?"))

	s, err := NewCodexSource(home)
	if err != nil {
		t.Fatal(err)
	}
	sessionID := string(protocol.KindCodex) + ":" + id
	if _, err := s.Page(context.Background(), sessionID, "", 10); err == nil {
		t.Fatal("Page succeeded before Past ran; the session should be unknown")
	}
	if _, err := s.Past(context.Background(), work, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Page(context.Background(), sessionID, "", 10); err != nil {
		t.Fatalf("Page after Past: %v", err)
	}
}

// The registry is what the daemon asks, so the merge across agents, the
// subtree rollup and the live-wins rule are asserted together.
func TestRegistryInDirectoryMergesAgentsAndPrefersTheLiveReading(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "code", "api")
	nested := filepath.Join(root, "bin")
	elsewhere := filepath.Join(home, "code", "web")

	claudeID := "11111111-1111-4111-8111-111111111111"
	writeClaudeTranscript(t, home, claudeID, root, claudeUserLine(root, "claude work"))
	writeClaudeTranscript(t, home, "22222222-2222-4222-8222-222222222222", nested,
		claudeUserLine(nested, "nested work"))
	writeClaudeTranscript(t, home, "33333333-3333-4333-8333-333333333333", elsewhere,
		claudeUserLine(elsewhere, "other project"))
	writeCodexHistoryRollout(t, home, "019cc033-f3f2-7c32-9215-a860efb9f9fd", root, `"cli"`,
		codexUserTurn("codex work"))

	claude, err := NewClaudeSource(home)
	if err != nil {
		t.Fatal(err)
	}
	codex, err := NewCodexSource(home)
	if err != nil {
		t.Fatal(err)
	}
	r := NewRegistry()
	r.Add(claude)
	r.Add(codex)

	// Stand one of them up as live, the way a sweep would.
	liveID := string(protocol.KindClaude) + ":" + claudeID
	r.mu.Lock()
	r.last[protocol.KindClaude] = []protocol.Session{{
		ID: liveID, Kind: protocol.KindClaude, NativeID: claudeID,
		Name: "claude work", Cwd: root, State: protocol.StateBusy,
		Inject: protocol.InjectTmux, LastActivityAt: time.Now().UnixMilli(),
	}}
	r.mu.Unlock()

	sessions, err := r.InDirectory(context.Background(), root, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 3 {
		t.Fatalf("got %d sessions, want the root's two plus the one in bin/: %+v", len(sessions), sessions)
	}
	// Busy sorts ahead of ended, and the live reading is the one kept.
	if sessions[0].ID != liveID {
		t.Fatalf("first session = %q, want the live one first", sessions[0].ID)
	}
	if sessions[0].State != protocol.StateBusy {
		t.Errorf("state = %q; Past overwrote the live reading with a disk one",
			sessions[0].State)
	}
	if sessions[0].Inject != protocol.InjectTmux {
		t.Errorf("inject = %q; the live session lost its delivery channel", sessions[0].Inject)
	}
	for _, session := range sessions {
		if session.Cwd == elsewhere {
			t.Errorf("a session from %s leaked into %s", elsewhere, root)
		}
	}
}

func TestRegistryFolderIndexRollsUpSubtrees(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "code", "api")
	nested := filepath.Join(root, "bin")
	writeClaudeTranscript(t, home, "11111111-1111-4111-8111-111111111111", root,
		claudeUserLine(root, "a"))
	writeClaudeTranscript(t, home, "22222222-2222-4222-8222-222222222222", nested,
		claudeUserLine(nested, "b"))
	writeCodexHistoryRollout(t, home, "019cc033-f3f2-7c32-9215-a860efb9f9fd", nested, `"cli"`,
		codexUserTurn("c"))

	claude, err := NewClaudeSource(home)
	if err != nil {
		t.Fatal(err)
	}
	codex, err := NewCodexSource(home)
	if err != nil {
		t.Fatal(err)
	}
	r := NewRegistry()
	r.Add(claude)
	r.Add(codex)

	index, err := r.Folders(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := index.Under(root).Agents; got != 3 {
		t.Errorf("under %s = %d agents, want 3 (its own plus bin/'s two)", root, got)
	}
	if got := index.Under(nested).Agents; got != 2 {
		t.Errorf("under %s = %d agents, want 2", nested, got)
	}
	if got := index.Under(filepath.Join(home, "code", "api-old")).Agents; got != 0 {
		t.Errorf("api-old = %d agents, want 0", got)
	}
	// Recent lists real working directories, which is what makes it the only
	// way to reach one the browser skips.
	paths := map[string]bool{}
	for _, folder := range index.Recent(0) {
		paths[folder.Path] = true
	}
	if !paths[root] || !paths[nested] {
		t.Errorf("Recent = %v, want both working directories", paths)
	}
}
