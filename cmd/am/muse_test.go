package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/source"
)

func TestMuseResumeTargetIsTheNamedSession(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"--provider", "meta"}, ""},
		{[]string{"resume", "01a19447-2bc8-78e2-b685-8762e93e33fd"}, "01a19447-2bc8-78e2-b685-8762e93e33fd"},
		// A flag's value before the id is not the id.
		{[]string{"resume", "--model", "x", "01A19468-68BC-7991-A879-41772098D5DB"}, "01a19468-68bc-7991-a879-41772098d5db"},
		// Named by name, or not named: left to Muse.
		{[]string{"resume", "muted-pasiphae"}, ""},
		{[]string{"resume", "--last"}, ""},
		{[]string{"resume"}, ""},
		{[]string{"exec", "01a19447-2bc8-78e2-b685-8762e93e33fd"}, ""},
	}
	for _, c := range cases {
		if got := museResumeTarget(c.args); got != c.want {
			t.Errorf("%v: got %q, want %q", c.args, got, c.want)
		}
	}
}

// fakeReleaser is a daemon socket that refuses a plain release, as Muse does
// mid-turn, and lets go when asked to stop.
func fakeReleaser(t *testing.T) (*[]protocol.Request, func()) {
	t.Helper()
	dir, err := os.MkdirTemp("", "amm")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "am.sock")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTMAN_SOCKET", path)
	var mu sync.Mutex
	var got []protocol.Request
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			line, _ := bufio.NewReader(conn).ReadBytes('\n')
			var envelope protocol.Envelope
			_ = json.Unmarshal(line, &envelope)
			var req protocol.Request
			_ = json.Unmarshal(envelope.Payload, &req)
			mu.Lock()
			got = append(got, req)
			mu.Unlock()
			event := protocol.Event{Type: protocol.EvtSessionReleased, SessionID: req.SessionID}
			if !req.Stop {
				event = protocol.Event{Type: protocol.EvtError, SessionID: req.SessionID, Error: source.ErrMuseWorking.Error()}
			}
			payload, _ := json.Marshal(event)
			reply, _ := json.Marshal(protocol.Envelope{V: protocol.Version, ReplyTo: envelope.ID, Payload: payload})
			_, _ = conn.Write(append(reply, '\n'))
			_ = conn.Close()
		}
	}()
	return &got, func() { _ = listener.Close(); _ = os.RemoveAll(dir) }
}

func TestAmMuseAsksBeforeStoppingAgentmansTurn(t *testing.T) {
	asked := stdinIsTerminal
	t.Cleanup(func() { stdinIsTerminal = asked })
	stdinIsTerminal = func() bool { return true }
	const id = "01a19543-6b8b-70b3-ab13-ca2b7cb935d1"

	got, stop := fakeReleaser(t)
	var out bytes.Buffer
	if err := takeMuseBack(context.Background(), id, strings.NewReader("y\n"), &out); err != nil {
		t.Fatalf("answering yes: %v", err)
	}
	if !strings.Contains(out.String(), "Stop it and open the session here?") {
		t.Errorf("never asked: %q", out.String())
	}
	if len(*got) != 2 || (*got)[0].Stop || !(*got)[1].Stop || (*got)[1].SessionID != "muse:"+id {
		t.Errorf("requests: %+v", *got)
	}
	stop()

	got, stop = fakeReleaser(t)
	defer stop()
	if err := takeMuseBack(context.Background(), id, strings.NewReader("\n"), &out); err == nil {
		t.Error("answering no should leave the session with Agentman and not open it")
	}
	if len(*got) != 1 {
		t.Errorf("no means no stop: %+v", *got)
	}

	stdinIsTerminal = func() bool { return false }
	if err := takeMuseBack(context.Background(), id, strings.NewReader("y\n"), &out); err == nil {
		t.Error("with no one to ask, the turn is left to finish")
	}
}
