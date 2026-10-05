package source

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/tmux"
)

// Lines Kiro CLI 2.24.1 wrote for one real turn; see parser/kiro_test.go.
const (
	kiroLinePrompt = `{"version":"v1","kind":"Prompt","data":{"message_id":"p1","content":[{"kind":"text","data":"list the files"}],"meta":{"timestamp":1790528359}}}`
	kiroLineCall   = `{"version":"v1","kind":"AssistantMessage","data":{"message_id":"a1","content":[{"kind":"text","data":""},{"kind":"toolUse","data":{"toolUseId":"t1","name":"shell","input":{"command":"ls -la"}}}]}}`
	kiroLineResult = `{"version":"v1","kind":"ToolResults","data":{"message_id":"r1","content":[{"kind":"toolResult","data":{"toolUseId":"t1","content":[{"kind":"json","data":{"exit_status":"exit status: 0","stdout":"notes.txt\n","stderr":""}}],"status":"success"}}]}}`
	kiroLineReply  = `{"version":"v1","kind":"AssistantMessage","data":{"message_id":"a2","content":[{"kind":"text","data":"There is 1 file."}]}}`
)

// writeKiroSession lays out one session the way Kiro does. pid 0 leaves the
// lock out, as Kiro does once a session is closed.
func writeKiroSession(t *testing.T, home, id, cwd string, pid int, lines ...string) string {
	t.Helper()
	dir := filepath.Join(home, ".kiro", "sessions", "cli")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(dir, id)
	meta := fmt.Sprintf(`{"session_id":%q,"cwd":%q,"created_at":"2026-09-27T16:59:07.644671Z","title":"list the files",`+
		`"session_state":{"rts_model_state":{"model_info":{"model_id":"auto"}}}}`, id, cwd)
	if err := os.WriteFile(base+".json", []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
	body := strings.Join(lines, "\n")
	if body != "" {
		body += "\n"
	}
	if err := os.WriteFile(base+".jsonl", []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if pid > 0 {
		lock := fmt.Sprintf(`{"pid":%d,"started_at":"2026-09-27T16:59:07.643584Z"}`, pid)
		if err := os.WriteFile(base+".lock", []byte(lock), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return base + ".jsonl"
}

// deadPID returns the pid of a process that has already exited.
func deadPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	return cmd.Process.Pid
}

// newTestKiro builds a source with no tmux panes and a process table in which
// the given pid runs Kiro.
func newTestKiro(t *testing.T, home string, table string, panes ...tmux.Session) *KiroSource {
	t.Helper()
	s, err := NewKiroSource(home)
	if err != nil {
		t.Fatal(err)
	}
	s.listPanes = func(context.Context) ([]tmux.Session, error) { return panes, nil }
	s.snapshotProcesses = func(context.Context) (*tmux.ProcessTree, error) {
		return tmux.ProcessTreeFromTable(table), nil
	}
	s.capturePane = func(context.Context, string) (string, error) { return "", nil }
	return s
}

func discoverKiro(t *testing.T, s *KiroSource) map[string]protocol.Session {
	t.Helper()
	found, err := s.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]protocol.Session{}
	for _, session := range found {
		byID[session.ID] = session
	}
	return byID
}

func TestKiroFindsASessionWhoseProcessIsAlive(t *testing.T) {
	home := t.TempDir()
	pid := os.Getpid()
	writeKiroSession(t, home, "9afec73d-0bce-4d68-91ff-95cfe36d542f", "/work/api", pid,
		kiroLinePrompt, kiroLineCall, kiroLineResult, kiroLineReply)
	s := newTestKiro(t, home, fmt.Sprintf("%d 1 /Users/me/.local/bin/kiro-cli-chat\n", pid))

	sessions := discoverKiro(t, s)
	session, ok := sessions["kiro:9afec73d-0bce-4d68-91ff-95cfe36d542f"]
	if !ok {
		t.Fatalf("live session not found: %v", sessions)
	}
	if session.Cwd != "/work/api" || session.Model != "auto" || session.Name != "list the files" {
		t.Errorf("session = %+v", session)
	}
	if session.State != protocol.StateIdle {
		t.Errorf("a finished turn reported %s", session.State)
	}
	// Started outside Agentman: visible, but nothing can type into it.
	if session.Inject != protocol.InjectNone {
		t.Errorf("inject = %s, want none", session.Inject)
	}
}

// A crash leaves the lock behind. Its pid is the only thing that says whether
// the session is still running, and a stale lock must not surface a ghost.
func TestKiroIgnoresALockLeftByACrash(t *testing.T) {
	home := t.TempDir()
	writeKiroSession(t, home, "crashed", "/work/api", deadPID(t), kiroLinePrompt)
	if sessions := discoverKiro(t, newTestKiro(t, home, "")); len(sessions) != 0 {
		t.Errorf("a stale lock surfaced a session: %v", sessions)
	}
}

// In time the operating system reuses a crashed session's pid. A live pid
// running something else is no more a Kiro session than a dead one.
func TestKiroIgnoresALockWhosePidWasReused(t *testing.T) {
	home := t.TempDir()
	pid := os.Getpid()
	writeKiroSession(t, home, "reused", "/work/api", pid, kiroLinePrompt)
	s := newTestKiro(t, home, fmt.Sprintf("%d 1 /usr/bin/vim\n", pid))
	if sessions := discoverKiro(t, s); len(sessions) != 0 {
		t.Errorf("a pid now running vim surfaced as Kiro: %v", sessions)
	}
}

func TestKiroClosedSessionIsNotListed(t *testing.T) {
	home := t.TempDir()
	writeKiroSession(t, home, "closed", "/work/api", 0, kiroLinePrompt, kiroLineReply)
	if sessions := discoverKiro(t, newTestKiro(t, home, "")); len(sessions) != 0 {
		t.Errorf("a closed session was listed: %v", sessions)
	}
}

// The session's process descends from the pane's, which is what binds them.
// Bound, it is keyed on the pane, so a phone launch keeps the id it was given
// before Kiro chose a session id of its own.
func TestKiroBindsASessionToItsPaneByProcessAncestry(t *testing.T) {
	home := t.TempDir()
	lockPID := os.Getpid()
	panePID := 70_000
	writeKiroSession(t, home, "bound", "/work/api", lockPID, kiroLinePrompt, kiroLineReply)
	table := fmt.Sprintf("%d %d /Users/me/.local/bin/kiro-cli-chat\n%d 1 kiro-cli\n", lockPID, panePID, panePID)
	pane := tmux.Session{Name: "agentman-kiro-1790528359000-ab12", PanePID: panePID, Cwd: "/work/api"}
	s := newTestKiro(t, home, table, pane)
	s.capturePane = func(context.Context, string) (string, error) {
		return "›  ask a question or describe a task ↵\n", nil
	}

	sessions := discoverKiro(t, s)
	session, ok := sessions["kiro:tmux-agentman-kiro-1790528359000-ab12"]
	if !ok {
		t.Fatalf("the session was not keyed on its pane: %v", sessions)
	}
	if session.Inject != protocol.InjectTmux || session.NativeID != "bound" {
		t.Errorf("session = %+v", session)
	}
	if len(sessions) != 1 {
		t.Errorf("the bound pane was listed a second time as a bare pane: %v", sessions)
	}
}

func TestKiroListsAPaneThatHasNotWrittenASessionYet(t *testing.T) {
	pane := tmux.Session{
		Name: "agentman-kiro-1790528359000-ab12", PanePID: 70_000, Cwd: "/work/api",
		Created: time.Unix(1790528359, 0),
	}
	s := newTestKiro(t, t.TempDir(), "", pane)
	sessions := discoverKiro(t, s)
	session, ok := sessions["kiro:tmux-agentman-kiro-1790528359000-ab12"]
	if !ok || session.Inject != protocol.InjectTmux || session.Cwd != "/work/api" {
		t.Fatalf("bare pane = %+v (found %v)", session, sessions)
	}
}

// Kiro writes an event only once it is complete, so the transcript alone
// cannot tell a turn being streamed from one not yet started. It can tell a
// finished turn from a running one, which is what these cases pin down.
func TestKiroStateFromTheLastEvent(t *testing.T) {
	cases := []struct {
		name  string
		lines []string
		want  protocol.State
	}{
		{"prompt awaiting a reply", []string{kiroLinePrompt}, protocol.StateBusy},
		{"tool call awaiting its result", []string{kiroLinePrompt, kiroLineCall}, protocol.StateBusy},
		{"result awaiting the next step", []string{kiroLinePrompt, kiroLineCall, kiroLineResult}, protocol.StateBusy},
		{"reply", []string{kiroLinePrompt, kiroLineCall, kiroLineResult, kiroLineReply}, protocol.StateIdle},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			pid := os.Getpid()
			writeKiroSession(t, home, "s", "/work", pid, tc.lines...)
			s := newTestKiro(t, home, fmt.Sprintf("%d 1 kiro-cli-chat\n", pid))
			if got := discoverKiro(t, s)["kiro:s"].State; got != tc.want {
				t.Errorf("state = %s, want %s", got, tc.want)
			}
		})
	}
}

// The pane outranks the transcript: an approval menu is only visible there.
func TestKiroApprovalMenuMeansWaitingForYou(t *testing.T) {
	home := t.TempDir()
	pid := os.Getpid()
	writeKiroSession(t, home, "s", "/work", pid, kiroLinePrompt, kiroLineCall)
	pane := tmux.Session{Name: "agentman-kiro-1-a", PanePID: pid, Cwd: "/work"}
	s := newTestKiro(t, home, fmt.Sprintf("%d 1 kiro-cli-chat\n", pid), pane)
	s.capturePane = func(context.Context, string) (string, error) {
		return "↓ Shell ls -la\n──────\n shell requires approval\n ❯ Yes, single permission\n" +
			"   No (Tab to edit)\n──────\n esc to close · ↑↓ to navigate · ↵ to select · Tab to edit\n", nil
	}
	session := discoverKiro(t, s)["kiro:tmux-agentman-kiro-1-a"]
	if session.State != protocol.StateWaitingInput || session.Question == nil {
		t.Fatalf("session = %+v", session)
	}
	if session.Question.Detail != "Shell ls -la" {
		t.Errorf("question detail = %q", session.Question.Detail)
	}
}

func TestKiroPagesNewestFirstWithAStableCursor(t *testing.T) {
	home := t.TempDir()
	pid := os.Getpid()
	writeKiroSession(t, home, "s", "/work", pid, kiroLinePrompt, kiroLineCall, kiroLineResult, kiroLineReply)
	s := newTestKiro(t, home, fmt.Sprintf("%d 1 kiro-cli-chat\n", pid))
	discoverKiro(t, s)

	// Three messages: the prompt, the tool call settled in place, the reply.
	latest, err := s.Page(context.Background(), "kiro:s", "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(latest.Messages) != 2 || !latest.HasMore || latest.NextCursor == "" {
		t.Fatalf("latest page = %+v", latest)
	}
	if latest.Messages[0].Tool == nil || latest.Messages[0].Tool.Status != protocol.ToolOK {
		t.Errorf("the tool call did not settle in place: %+v", latest.Messages[0])
	}
	older, err := s.Page(context.Background(), "kiro:s", latest.NextCursor, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(older.Messages) != 1 || older.Messages[0].Role != protocol.RoleUser || older.HasMore {
		t.Fatalf("older page = %+v", older)
	}
}

// Follow reads the existing transcript silently to rebuild the turn's clock
// and the calls in flight, then streams only what is appended — stamped so it
// sorts after what the page already showed.
func TestKiroFollowStreamsAppendedEventsInOrder(t *testing.T) {
	home := t.TempDir()
	pid := os.Getpid()
	path := writeKiroSession(t, home, "s", "/work", pid, kiroLinePrompt, kiroLineCall)
	s := newTestKiro(t, home, fmt.Sprintf("%d 1 kiro-cli-chat\n", pid))
	discoverKiro(t, s)
	page, err := s.Page(context.Background(), "kiro:s", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	shownTs := page.Messages[len(page.Messages)-1].Ts

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out := make(chan []protocol.Message, 8)
	done := make(chan error, 1)
	go func() { done <- s.Follow(ctx, "kiro:s", out) }()
	time.Sleep(3 * followInterval)

	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(kiroLineResult + "\n" + kiroLineReply + "\n"); err != nil {
		t.Fatal(err)
	}
	file.Close()

	var got []protocol.Message
	for len(got) < 2 {
		select {
		case batch := <-out:
			got = append(got, batch...)
		case <-ctx.Done():
			t.Fatalf("follow delivered %d messages, want 2: %+v", len(got), got)
		}
	}
	cancel()
	<-done
	// The result settles the call it already knew about, under the same id.
	if got[0].ID != "t1" || got[0].Tool == nil || got[0].Tool.Status != protocol.ToolOK {
		t.Errorf("result = %+v", got[0])
	}
	if got[1].Text != "There is 1 file." || got[1].Ts <= shownTs {
		t.Errorf("reply = %+v, want it after ts %d", got[1], shownTs)
	}
}

// The Kiro /model picker, open over the prompt (Kiro CLI 2.27.1, 80 columns,
// rules shortened).
const kiroModelPickerPane = `› /model
Select model:   type to search

❯ auto                 1.00x credits    Models chosen by task for optimal u...
  claude-sonnet-4.5    1.30x credits    Claude Sonnet 4.5 model
  claude-haiku-4.5     0.40x credits    The latest Claude Haiku model
────────────────────────────────────────
 Settings for selected model: auto                         tab to switch panels

  thinking  n/a   Not available for this model.
────────────────────────────────────────
 esc to close · ↑↓ to navigate · ↵ to select · tab to switch panels
`

// A message typed into an open picker becomes its search, and the Enter
// meant to submit it picks a model, an agent, or a point to fork from. The
// send is refused instead, with the reason.
func TestKiroRefusesToTypeIntoAPicker(t *testing.T) {
	home := t.TempDir()
	pid := os.Getpid()
	writeKiroSession(t, home, "s", "/work", pid, kiroLinePrompt, kiroLineReply)
	pane := tmux.Session{Name: "agentman-kiro-1-a", PanePID: pid, Cwd: "/work"}
	s := newTestKiro(t, home, fmt.Sprintf("%d 1 kiro-cli-chat\n", pid), pane)
	s.capturePane = func(context.Context, string) (string, error) { return kiroModelPickerPane, nil }

	session := discoverKiro(t, s)["kiro:tmux-agentman-kiro-1-a"]
	if session.Question != nil {
		t.Fatalf("the picker read as a question: %+v", session.Question)
	}
	if _, err := s.Inject(context.Background(), session.ID, "run the tests"); !errors.Is(err, errKiroOverlay) {
		t.Fatalf("send into an open picker: err = %v", err)
	}
}

// A long diff or a tall task list can push the "↓" row that names the call
// off the screen. The call is already on disk, so the question names it from
// there — and an answer checks against the same name.
func TestKiroQuestionNamesTheCallFromTheTranscriptWhenTheScreenCannot(t *testing.T) {
	home := t.TempDir()
	pid := os.Getpid()
	writeKiroSession(t, home, "s", "/work", pid, kiroLinePrompt, kiroLineCall)
	pane := tmux.Session{Name: "agentman-kiro-1-a", PanePID: pid, Cwd: "/work"}
	s := newTestKiro(t, home, fmt.Sprintf("%d 1 kiro-cli-chat\n", pid), pane)
	menu := "     40+  the last line of a long diff\n──────\n shell requires approval\n ❯ Yes, single permission\n" +
		"   Trust, always allow in this session\n   No (Tab to edit)\n──────\n esc to close · ↑↓ to navigate · ↵ to select · Tab to edit\n"
	s.capturePane = func(context.Context, string) (string, error) { return menu, nil }

	session := discoverKiro(t, s)["kiro:tmux-agentman-kiro-1-a"]
	if session.Question == nil || session.Question.Detail != "Shell ls -la" {
		t.Fatalf("question = %+v", session.Question)
	}
	current, err := s.CurrentQuestion(context.Background(), session.ID)
	if err != nil || current == nil || current.ID != session.Question.ID {
		t.Errorf("read back as %+v (%v), shown as %+v", current, err, session.Question)
	}
}

// Kiro's editor for the reason a call is refused is answered with text, or
// with its one choice, back to the menu. Anything else, or an answer to a
// question no longer on screen, is refused before a key is pressed.
func TestKiroDenialReasonAnswersAreChecked(t *testing.T) {
	home := t.TempDir()
	pid := os.Getpid()
	writeKiroSession(t, home, "s", "/work", pid, kiroLinePrompt, kiroLineCall)
	pane := tmux.Session{Name: "agentman-kiro-1-a", PanePID: pid, Cwd: "/work"}
	s := newTestKiro(t, home, fmt.Sprintf("%d 1 kiro-cli-chat\n", pid), pane)
	editor := "↓ Shell ls -la\n──────\n shell requires approval · Modify request\n ›  add your feedback...\n──────\n esc to close\n"
	s.capturePane = func(context.Context, string) (string, error) { return editor, nil }

	session := discoverKiro(t, s)["kiro:tmux-agentman-kiro-1-a"]
	shown := session.Question
	if shown == nil || !shown.Custom || session.State != protocol.StateWaitingInput {
		t.Fatalf("session = %+v", session)
	}
	ctx := context.Background()
	for name, tc := range map[string]struct {
		answer protocol.QuestionAnswer
		want   string
	}{
		"stale":          {protocol.QuestionAnswer{QuestionID: "old", Text: "use ls -l"}, "no longer current"},
		"no reason":      {protocol.QuestionAnswer{QuestionID: shown.ID}, "write the reason"},
		"unknown choice": {protocol.QuestionAnswer{QuestionID: shown.ID, OptionKey: "2"}, "write the reason"},
	} {
		if err := s.Answer(ctx, session.ID, tc.answer); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}
	// The editor closed between the phone reading it and answering.
	s.capturePane = func(context.Context, string) (string, error) {
		return "›  ask a question or describe a task ↵\n", nil
	}
	if err := s.Answer(ctx, session.ID, protocol.QuestionAnswer{QuestionID: shown.ID, Text: "use ls -l"}); err == nil ||
		!strings.Contains(err.Error(), "no longer on screen") {
		t.Errorf("answer to a closed editor: %v", err)
	}
}
