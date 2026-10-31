package source

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
)

// What Codex 0.166 answered, verbatim in their openings, for a method it
// does not know, for parameters of the wrong shape, and for a thread it
// cannot act on.
var (
	codexUnknownMethod = map[string]any{"code": -32600, "message": "Invalid request: unknown variant `thread/settings/update`, expected one of `initialize`, `thread/start`"}
	codexWrongShape    = map[string]any{"code": -32600, "message": "Invalid request: invalid type: integer `5`, expected a string"}
	codexBadThread     = map[string]any{"code": -32600, "message": "invalid thread id: invalid character: expected an optional prefix of `urn:uuid:`"}
	codexNoRollout     = map[string]any{"code": -32600, "message": "no rollout found for thread id t1"}
)

func rpcError(fields map[string]any) error {
	return &codexRPCError{Code: fields["code"].(int), Message: fields["message"].(string)}
}

func TestCodexTellsAProtocolMismatchFromARealRefusal(t *testing.T) {
	for name, c := range map[string]struct {
		err  error
		want bool
	}{
		"unknown method":   {rpcError(codexUnknownMethod), true},
		"wrong shape":      {rpcError(codexWrongShape), true},
		"json-rpc unknown": {&codexRPCError{Code: -32601, Message: "Method not found"}, true},
		"bad thread":       {rpcError(codexBadThread), false},
		"no rollout":       {rpcError(codexNoRollout), false},
		"daemon gone":      {errCodexAppServerDown, true},
		"anything else":    {errors.New("boom"), false},
	} {
		if got := codexFallBack(c.err); got != c.want {
			t.Errorf("%s: codexFallBack = %v, want %v", name, got, c.want)
		}
	}
}

func replyError(t *testing.T, conn *fakeCodexConn, method string, fields map[string]any) {
	t.Helper()
	request := conn.expect(t, method)
	conn.send(t, map[string]any{"id": request.ID, "error": fields})
}

// A daemon without thread/settings/update sends a switch back to the
// terminal's pickers, which for a session with no pane say so plainly.
func TestCodexSwitchGoesTheOldWayWhenTheDaemonCannotTakeIt(t *testing.T) {
	src, conn := switchSource(t)
	result := make(chan error, 1)
	go func() { result <- src.SetModel(context.Background(), "codex:t1", "gpt-6-astra") }()
	replyError(t, conn, "thread/settings/update", codexUnknownMethod)
	err := waitErr(t, result)
	if err == nil || !strings.Contains(err.Error(), "am codex") {
		t.Fatalf("err = %v, want the terminal path's answer", err)
	}
	// Nor is the switch shown as made.
	if model := discovered(t, src)["codex:t1"].Model; model != "gpt-6.1-sol" {
		t.Fatalf("model = %q", model)
	}

	// A refusal about the thread itself is Codex's answer, not a mismatch.
	go func() { result <- src.SetModel(context.Background(), "codex:t1", "gpt-6-astra") }()
	replyError(t, conn, "thread/settings/update", codexBadThread)
	if err := waitErr(t, result); err == nil || !strings.Contains(err.Error(), "Codex did not take the change") {
		t.Fatalf("err = %v, want Codex's refusal", err)
	}
}

// A session in a pane is typed into, as before, when the daemon cannot take
// the message.
func TestCodexSendGoesToThePaneWhenTheDaemonCannotTakeIt(t *testing.T) {
	home := t.TempDir()
	now := time.Now()
	path := writeDaemonRollout(t, home, "t1", "/work/api", now.Add(-time.Hour), now.Add(-time.Minute), "task_complete")
	src, conn := connectedDaemonSource(t, home, nil, openThread("t1", path, "idle"))
	session := codexSession{meta: protocol.Session{ID: "codex:tmux-p", NativeID: "t1"}, transcript: path, tmuxName: "agentman-codex-p"}

	for _, text := range []string{"run the tests", "/compact"} {
		result := make(chan error, 1)
		var handled bool
		go func() {
			var err error
			handled, err = src.sendThroughDaemon(context.Background(), "codex:tmux-p", session, text, nil)
			result <- err
		}()
		method := "turn/start"
		if text == "/compact" {
			method = "thread/compact/start"
		}
		replyError(t, conn, method, codexUnknownMethod)
		if err := waitErr(t, result); err != nil || handled {
			t.Fatalf("%q: handled=%v err=%v, want it left for the pane", text, handled, err)
		}
	}
}

func TestCodexLaunchGoesTheOldWayWhenTheDaemonCannotStartIt(t *testing.T) {
	home := t.TempDir()
	src, conn := connectedDaemonSource(t, home, nil)
	result := startThreadAsync(src, filepath.Join(home, "work"), "add a health check")
	replyError(t, conn, "thread/start", codexWrongShape)
	if launched := <-result; !errors.Is(launched.err, ErrNoCodexDaemon) {
		t.Fatalf("err = %v, want the old launch", launched.err)
	}
}
