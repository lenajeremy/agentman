package daemon

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/lenajeremy/agentman/internal/protocol"
)

type recordingDaemonSocket struct {
	mu          sync.Mutex
	frames      [][]byte
	gate        <-chan struct{}
	started     chan struct{}
	startedOnce sync.Once
	closed      chan struct{}
	closeOnce   sync.Once
}

func newRecordingDaemonSocket(gate <-chan struct{}) *recordingDaemonSocket {
	return &recordingDaemonSocket{
		gate: gate, started: make(chan struct{}), closed: make(chan struct{}),
	}
}

func (s *recordingDaemonSocket) Write(
	ctx context.Context,
	_ websocket.MessageType,
	frame []byte,
) error {
	s.startedOnce.Do(func() { close(s.started) })
	if s.gate != nil {
		select {
		case <-s.gate:
		case <-s.closed:
			return errRelayConnectionClosed
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	select {
	case <-s.closed:
		return errRelayConnectionClosed
	default:
	}
	s.mu.Lock()
	s.frames = append(s.frames, append([]byte(nil), frame...))
	s.mu.Unlock()
	return nil
}

func (s *recordingDaemonSocket) Ping(context.Context) error {
	select {
	case <-s.closed:
		return errRelayConnectionClosed
	default:
		return nil
	}
}

func (s *recordingDaemonSocket) CloseNow() error {
	s.closeOnce.Do(func() { close(s.closed) })
	return nil
}

func (s *recordingDaemonSocket) snapshot() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([][]byte, len(s.frames))
	for index := range s.frames {
		result[index] = append([]byte(nil), s.frames[index]...)
	}
	return result
}

func TestDaemonRelayConnQueuesWithoutBlockingAndPreservesOrder(t *testing.T) {
	gate := make(chan struct{})
	socket := newRecordingDaemonSocket(gate)
	conn := newDaemonRelayConnWithQueue(socket, 3)
	defer conn.Close()

	if err := conn.Send([]byte("one")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-socket.started:
	case <-time.After(time.Second):
		t.Fatal("relay writer did not start")
	}
	for _, frame := range []string{"two", "three", "four"} {
		if err := conn.Send([]byte(frame)); err != nil {
			t.Fatalf("enqueue %q: %v", frame, err)
		}
	}
	close(gate)

	deadline := time.Now().Add(time.Second)
	for len(socket.snapshot()) < 4 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	frames := socket.snapshot()
	if len(frames) != 4 {
		t.Fatalf("writer delivered %d frames, want 4", len(frames))
	}
	for index, want := range []string{"one", "two", "three", "four"} {
		if got := string(frames[index]); got != want {
			t.Fatalf("frame %d = %q, want %q", index, got, want)
		}
	}
}

func TestDaemonRelayConnClosesOnBackpressure(t *testing.T) {
	never := make(chan struct{})
	socket := newRecordingDaemonSocket(never)
	conn := newDaemonRelayConnWithQueue(socket, 1)

	if err := conn.Send([]byte("writing")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-socket.started:
	case <-time.After(time.Second):
		t.Fatal("relay writer did not block")
	}
	if err := conn.Send([]byte("queued")); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := conn.Send([]byte("overflow")); !errors.Is(err, errRelayQueueFull) {
		t.Fatalf("overflow error = %v", err)
	}
	if time.Since(start) > 100*time.Millisecond {
		t.Fatal("backpressure blocked the daemon event producer")
	}
	select {
	case <-socket.closed:
	case <-time.After(time.Second):
		t.Fatal("overflow did not close the relay socket")
	}
}

func TestDaemonRelayConnClosesAtByteBudget(t *testing.T) {
	never := make(chan struct{})
	socket := newRecordingDaemonSocket(never)
	conn := newDaemonRelayConnWithQueue(socket, 8)

	frame := make([]byte, maxFrame)
	if err := conn.Send(frame); err != nil {
		t.Fatal(err)
	}
	select {
	case <-socket.started:
	case <-time.After(time.Second):
		t.Fatal("writer did not start")
	}
	if err := conn.Send(frame); err != nil {
		t.Fatalf("second frame within byte budget: %v", err)
	}
	if err := conn.Send([]byte("overflow")); !errors.Is(err, errRelayQueueFull) {
		t.Fatalf("byte-budget overflow error = %v", err)
	}
	select {
	case <-socket.closed:
	case <-time.After(time.Second):
		t.Fatal("byte-budget overflow did not close socket")
	}
}

func TestOrderedMutationQueuePreservesArrivalOrder(t *testing.T) {
	jobs := make(chan daemonRequestJob, 2)
	firstStarted := make(chan struct{})
	secondStarted := make(chan struct{})
	allowFirst := make(chan struct{})
	done := make(chan struct{})
	var mu sync.Mutex
	var handled, replied []string
	released := 0

	go func() {
		runOrderedMutationQueue(
			context.Background(),
			jobs,
			func(_ context.Context, _ string, req protocol.Request) protocol.Event {
				mu.Lock()
				handled = append(handled, req.Text)
				mu.Unlock()
				switch req.Text {
				case "first":
					close(firstStarted)
					<-allowFirst
				case "second":
					close(secondStarted)
				}
				return protocol.Event{Type: protocol.EvtSendResult}
			},
			func(replyTo string, _ protocol.Event) {
				mu.Lock()
				replied = append(replied, replyTo)
				mu.Unlock()
			},
			func() {
				mu.Lock()
				released++
				mu.Unlock()
			},
		)
		close(done)
	}()

	jobs <- daemonRequestJob{req: protocol.Request{Text: "first"}, replyTo: "reply-1"}
	jobs <- daemonRequestJob{req: protocol.Request{Text: "second"}, replyTo: "reply-2"}
	close(jobs)
	<-firstStarted
	select {
	case <-secondStarted:
		t.Fatal("second mutation started before the first completed")
	case <-time.After(25 * time.Millisecond):
	}
	close(allowFirst)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("ordered mutation worker did not finish")
	}

	mu.Lock()
	defer mu.Unlock()
	if got, want := handled, []string{"first", "second"}; !equalStrings(got, want) {
		t.Fatalf("handled order = %v, want %v", got, want)
	}
	if got, want := replied, []string{"reply-1", "reply-2"}; !equalStrings(got, want) {
		t.Fatalf("reply order = %v, want %v", got, want)
	}
	if released != 2 {
		t.Fatalf("released slots = %d, want 2", released)
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
