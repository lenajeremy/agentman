package daemon

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/knowledge"
	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/source"
)

type notesLab struct {
	notes *sharedNotes
	store *knowledge.Store
	home  string
	clock time.Time
}

func newNotesLab(t *testing.T) *notesLab {
	t.Helper()
	home := t.TempDir()
	for _, dir := range []string{".claude", ".config/muse"} {
		if err := os.MkdirAll(filepath.Join(home, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	store := knowledge.Open(t.TempDir())
	notes := newSharedNotes(store, knowledge.Homes{Home: home}, filepath.Join(t.TempDir(), "shared-notes.json"))
	lab := &notesLab{notes: notes, store: store, home: home, clock: time.UnixMilli(1_000_000)}
	notes.now = func() time.Time { return lab.clock }
	return lab
}

func (l *notesLab) tick(t *testing.T) {
	t.Helper()
	l.clock = l.clock.Add(time.Minute)
	l.notes.sync()
}

func (l *notesLab) write(t *testing.T, title, body string) knowledge.Note {
	t.Helper()
	note, err := l.store.Write(knowledge.Note{Tier: knowledge.Shared, Title: title, Body: body, Always: true})
	if err != nil {
		t.Fatal(err)
	}
	return note
}

func TestChangingANoteRewritesTheFilesByItself(t *testing.T) {
	lab := newNotesLab(t)
	lab.store.OnChange(lab.notes.kick)
	lab.write(t, "Use pnpm", "Always pnpm.")
	lab.notes.wg.Wait()
	raw, err := os.ReadFile(filepath.Join(lab.home, ".claude", "CLAUDE.md"))
	if err != nil || !strings.Contains(string(raw), "Always pnpm.") {
		t.Fatalf("got %q, %v", raw, err)
	}
}

func TestACursorFolderGetsItsCopyWhenSeen(t *testing.T) {
	lab := newNotesLab(t)
	lab.write(t, "Use pnpm", "Always pnpm.")
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	lab.notes.observe([]protocol.Session{{ID: "cursor-cli:a", Kind: protocol.KindCursorCLI, Cwd: dir}})
	lab.notes.wg.Wait()
	raw, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if err != nil || !strings.Contains(string(raw), "Always pnpm.") {
		t.Fatalf("got %q, %v", raw, err)
	}
}

func TestNotHomeForCursor(t *testing.T) {
	lab := newNotesLab(t)
	lab.notes.observe([]protocol.Session{{ID: "cursor-cli:a", Kind: protocol.KindCursorCLI, Cwd: lab.home}})
	lab.notes.wg.Wait()
	if len(lab.notes.state.Targets) != 0 {
		t.Fatalf("targets %v", lab.notes.state.Targets)
	}
}

func TestAFolderThatsGoneIsForgotten(t *testing.T) {
	lab := newNotesLab(t)
	lab.notes.state.Targets[cursorNotesPrefix+filepath.Join(lab.home, "deleted")] = map[string]noteOnDisk{}
	lab.tick(t)
	if len(lab.notes.state.Targets) != 1 { // muse's only
		t.Fatalf("targets %v", lab.notes.state.Targets)
	}
}

func TestWhatWentOnDiskIsRememberedAcrossARestart(t *testing.T) {
	lab := newNotesLab(t)
	note := lab.write(t, "Use pnpm", "Always pnpm.")
	lab.tick(t)
	again := newSharedNotes(lab.store, lab.notes.homes, lab.notes.path)
	got := again.state.Targets[museNotesTarget][note.Name]
	if got.Hash == "" || got.At == notOnDisk {
		t.Fatalf("got %+v", got)
	}
}

func TestANoteOnlyGetsANewDateWhenItChanges(t *testing.T) {
	lab := newNotesLab(t)
	note := lab.write(t, "Use pnpm", "Always pnpm.")
	lab.tick(t)
	first := lab.notes.state.Targets[museNotesTarget][note.Name].At
	lab.tick(t)
	if got := lab.notes.state.Targets[museNotesTarget][note.Name].At; got != first {
		t.Fatalf("moved from %d to %d", first, got)
	}
	lab.write(t, "Use pnpm", "Always pnpm, even here.")
	lab.tick(t)
	if got := lab.notes.state.Targets[museNotesTarget][note.Name].At; got == first {
		t.Fatal("didn't move")
	}
}

func openedAt(lab *notesLab, kind protocol.Kind, offset time.Duration) protocol.Session {
	return protocol.Session{ID: string(kind) + ":a", Kind: kind, Cwd: "/w", NativeID: "a", StartedAt: lab.clock.Add(offset).UnixMilli()}
}

func deliver(lab *notesLab, session protocol.Session, compactions int, text string) string {
	out, done := lab.notes.attach(session, func() int { return compactions }, text)
	done()
	return out
}

func TestANoteAddedSinceASessionOpenedGoesWithItsNextMessageOnce(t *testing.T) {
	lab := newNotesLab(t)
	session := openedAt(lab, protocol.KindMuse, 0)
	lab.write(t, "Use pnpm", "Always pnpm.")
	lab.tick(t)
	got := deliver(lab, session, 0, "fix the login bug")
	if !strings.HasPrefix(got, "fix the login bug\n\n<agentman-shared-notes>") || !strings.Contains(got, "Always pnpm.") {
		t.Fatalf("got %q", got)
	}
	if again := deliver(lab, session, 0, "and the tests"); again != "and the tests" {
		t.Fatalf("sent twice: %q", again)
	}
}

func TestASessionOpenedSinceHasItFromTheFile(t *testing.T) {
	lab := newNotesLab(t)
	lab.write(t, "Use pnpm", "Always pnpm.")
	lab.tick(t)
	session := openedAt(lab, protocol.KindMuse, time.Second)
	if got := deliver(lab, session, 0, "hello"); got != "hello" {
		t.Fatalf("got %q", got)
	}
}

func TestAnEditedNoteIsSentAgain(t *testing.T) {
	lab := newNotesLab(t)
	session := openedAt(lab, protocol.KindMuse, 0)
	lab.write(t, "Use pnpm", "Always pnpm.")
	lab.tick(t)
	deliver(lab, session, 0, "one")
	lab.write(t, "Use pnpm", "Always pnpm, even here.")
	lab.tick(t)
	if got := deliver(lab, session, 0, "two"); !strings.Contains(got, "even here") {
		t.Fatalf("got %q", got)
	}
}

func TestAfterCompactingNotesThatCameByMessageAreSentAgain(t *testing.T) {
	lab := newNotesLab(t)
	session := openedAt(lab, protocol.KindMuse, 0)
	lab.write(t, "Use pnpm", "Always pnpm.")
	lab.tick(t)
	deliver(lab, session, 1, "one")
	if got := deliver(lab, session, 1, "two"); got != "two" {
		t.Fatalf("sent with no compaction: %q", got)
	}
	if got := deliver(lab, session, 2, "three"); !strings.Contains(got, "Always pnpm.") {
		t.Fatalf("got %q", got)
	}
}

func TestCompactingDoesntResendWhatCameFromTheFile(t *testing.T) {
	lab := newNotesLab(t)
	lab.write(t, "Use pnpm", "Always pnpm.")
	lab.tick(t)
	session := openedAt(lab, protocol.KindMuse, time.Second)
	deliver(lab, session, 0, "one")
	if got := deliver(lab, session, 5, "two"); got != "two" {
		t.Fatalf("got %q", got)
	}
}

func TestARemovedNoteIsTakenBackFromThoseWhoHadIt(t *testing.T) {
	lab := newNotesLab(t)
	sent := openedAt(lab, protocol.KindMuse, 0)
	note := lab.write(t, "Use pnpm", "Always pnpm.")
	lab.tick(t)
	deliver(lab, sent, 0, "one")
	fromFile := openedAt(lab, protocol.KindMuse, time.Second)
	fromFile.ID = "muse:b"
	if err := lab.store.Delete(knowledge.Shared, note.Name); err != nil {
		t.Fatal(err)
	}
	lab.tick(t)
	never := openedAt(lab, protocol.KindMuse, time.Second)
	never.ID = "muse:c"
	for _, session := range []protocol.Session{sent, fromFile} {
		got := deliver(lab, session, 0, "two")
		if !strings.Contains(got, `Stop following the shared note "Use pnpm"`) {
			t.Fatalf("%s: got %q", session.ID, got)
		}
		if again := deliver(lab, session, 0, "three"); again != "three" {
			t.Fatalf("%s told twice: %q", session.ID, again)
		}
	}
	if got := deliver(lab, never, 0, "two"); got != "two" {
		t.Fatalf("told one that never had it: %q", got)
	}
}

func TestACommandIsLeftAlone(t *testing.T) {
	lab := newNotesLab(t)
	session := openedAt(lab, protocol.KindMuse, 0)
	lab.write(t, "Use pnpm", "Always pnpm.")
	lab.tick(t)
	for _, command := range []string{"/compact", "  /model x", "!ls"} {
		if got := deliver(lab, session, 0, command); got != command {
			t.Fatalf("got %q", got)
		}
	}
	if got := deliver(lab, session, 0, "hello"); !strings.Contains(got, "Always pnpm.") {
		t.Fatalf("waited and then lost it: %q", got)
	}
}

func TestOtherAgentsGetNoMessage(t *testing.T) {
	lab := newNotesLab(t)
	lab.write(t, "Use pnpm", "Always pnpm.")
	lab.tick(t)
	for _, kind := range []protocol.Kind{protocol.KindClaude, protocol.KindCodex, protocol.KindOpenCode, protocol.KindAntigravity, protocol.KindKiro, protocol.KindCursor} {
		if got := deliver(lab, openedAt(lab, kind, -time.Hour), 0, "hello"); got != "hello" {
			t.Fatalf("%s: got %q", kind, got)
		}
	}
}

func TestACursorFolderWithoutItsFileGetsTheMessageEvenInNewSessions(t *testing.T) {
	lab := newNotesLab(t)
	folder := t.TempDir() // not a repository, so no file
	lab.notes.state.Targets[cursorNotesPrefix+folder] = map[string]noteOnDisk{}
	lab.write(t, "Use pnpm", "Always pnpm.")
	lab.tick(t)
	session := openedAt(lab, protocol.KindCursorCLI, time.Hour)
	session.Cwd = folder
	if got := deliver(lab, session, 0, "hello"); !strings.Contains(got, "Always pnpm.") {
		t.Fatalf("got %q", got)
	}
}

func TestANoteOnlyCountsAsSentOnceItWas(t *testing.T) {
	lab := newNotesLab(t)
	session := openedAt(lab, protocol.KindMuse, 0)
	lab.write(t, "Use pnpm", "Always pnpm.")
	lab.tick(t)
	lab.notes.attach(session, func() int { return 0 }, "failed to send") // never told it went
	if got := deliver(lab, session, 0, "hello"); !strings.Contains(got, "Always pnpm.") {
		t.Fatalf("got %q", got)
	}
}

func TestMessagesCarryTheNoteAllTheWayToTheAgent(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".config", "muse"), 0o755); err != nil {
		t.Fatal(err)
	}
	store := knowledge.Open(t.TempDir())
	session := protocol.Session{ID: "muse:a", Kind: protocol.KindMuse, NativeID: "a", Cwd: "/w", State: protocol.StateIdle, Inject: protocol.InjectAPI, StartedAt: 1}
	sent := &notesInjectSource{liveSource: liveSource{session: session}}
	registry := source.NewRegistry()
	registry.Add(sent)
	agent := New(registry, &recordingSink{})
	agent.SetSharedNotes(store, knowledge.Homes{Home: home}, filepath.Join(t.TempDir(), "s.json"))
	if _, err := store.Write(knowledge.Note{Tier: knowledge.Shared, Title: "Use pnpm", Body: "Always pnpm.", Always: true}); err != nil {
		t.Fatal(err)
	}
	agent.sharedNotes.wg.Wait()
	agent.refresh(context.Background(), true)
	reply := agent.HandleFrom(context.Background(), "phone", protocol.Request{Type: protocol.ReqSendMessage, SessionID: session.ID, Text: "hello", ClientID: "c"})
	if reply.Status != protocol.StatusDelivered {
		t.Fatalf("reply %+v", reply)
	}
	if !strings.Contains(sent.text, "hello") || !strings.Contains(sent.text, "Always pnpm.") {
		t.Fatalf("sent %q", sent.text)
	}
}

type notesInjectSource struct {
	liveSource
	text string
}

func (s *notesInjectSource) Inject(_ context.Context, _, text string) (protocol.InjectMode, error) {
	s.text = text
	return protocol.InjectAPI, nil
}

func TestWithNothingToSayTheTranscriptIsntRead(t *testing.T) {
	lab := newNotesLab(t)
	asked := 0
	out, done := lab.notes.attach(openedAt(lab, protocol.KindMuse, 0), func() int { asked++; return 0 }, "hello")
	done()
	if out != "hello" || asked != 0 {
		t.Fatalf("out %q, asked %d times", out, asked)
	}
}
