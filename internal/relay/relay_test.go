package relay

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/lenajeremy/agentman/internal/protocol"
)

/* --------------------------------- tokens -------------------------------- */

func TestDeviceTokenRoundTrip(t *testing.T) {
	const secret = "relay-secret"
	account := DeriveAccount("daemon-token")

	token, err := MintDeviceToken(secret, account, "device-1")
	if err != nil {
		t.Fatal(err)
	}
	got, err := VerifyDeviceToken(secret, token)
	if err != nil {
		t.Fatal(err)
	}
	if got != account {
		t.Errorf("account = %q, want %q", got, account)
	}
}

func TestDeviceTokenRejectsTampering(t *testing.T) {
	const secret = "relay-secret"
	token, err := MintDeviceToken(secret, DeriveAccount("daemon-token"), "d1")
	if err != nil {
		t.Fatal(err)
	}

	// Re-signing with a different secret must not verify: this is what lets
	// the relay trust a token it never stored.
	if _, err := VerifyDeviceToken("other-secret", token); !errors.Is(err, ErrTokenSignature) {
		t.Errorf("a token signed by another relay verified: %v", err)
	}

	// Swapping the claims for another account without re-signing must fail.
	forged, err := MintDeviceToken("attacker", DeriveAccount("victim"), "d1")
	if err != nil {
		t.Fatal(err)
	}
	body, _, _ := strings.Cut(forged, ".")
	_, signature, _ := strings.Cut(token, ".")
	if _, err := VerifyDeviceToken(secret, body+"."+signature); err == nil {
		t.Error("a token with swapped claims verified")
	}

	for _, bad := range []string{"", ".", "nodot", "a.b", token + "x"} {
		if _, err := VerifyDeviceToken(secret, bad); err == nil {
			t.Errorf("malformed token %q verified", bad)
		}
	}
	if _, err := VerifyDeviceToken("", token); !errors.Is(err, ErrTokenSignature) {
		t.Errorf("token verified with an empty relay secret: %v", err)
	}
}

func TestDeriveAccountIsStableAndDistinct(t *testing.T) {
	// Stable across restarts: the derivation must be a pure function of the
	// token, with no randomness or per-process salt, or a returning daemon
	// would land in a different account and lose its paired devices.
	const knownAccount = "6f4a441b727ba6f50133c69e8e0520b5"
	if got := DeriveAccount("token-a"); string(got) != knownAccount {
		t.Errorf("DeriveAccount changed: got %q, want the pinned %q — "+
			"every paired device would be orphaned by this", got, knownAccount)
	}
	if DeriveAccount("token-a") == DeriveAccount("token-b") {
		t.Error("different daemon tokens must not share an account")
	}
	// The account must not leak the token that produced it.
	if strings.Contains(string(DeriveAccount("token-a")), "token-a") {
		t.Error("account id leaks the daemon token")
	}
}

/* ---------------------------------- hub ---------------------------------- */

// fakeConn records what was sent to it.
type fakeConn struct {
	mu     sync.Mutex
	frames [][]byte
	closed bool
	err    error
}

type recordingSocket struct {
	mu          sync.Mutex
	frames      [][]byte
	gate        <-chan struct{}
	writeErr    error
	started     chan struct{}
	startedOnce sync.Once
	closed      chan struct{}
	closeOnce   sync.Once
}

func newRecordingSocket(gate <-chan struct{}, writeErr error) *recordingSocket {
	return &recordingSocket{
		gate:     gate,
		writeErr: writeErr,
		started:  make(chan struct{}),
		closed:   make(chan struct{}),
	}
}

func (s *recordingSocket) Write(ctx context.Context, _ websocket.MessageType, frame []byte) error {
	s.startedOnce.Do(func() { close(s.started) })
	if s.gate != nil {
		select {
		case <-s.gate:
		case <-s.closed:
			return errOutboundClosed
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if s.writeErr != nil {
		return s.writeErr
	}
	select {
	case <-s.closed:
		return errOutboundClosed
	default:
	}
	s.mu.Lock()
	s.frames = append(s.frames, append([]byte(nil), frame...))
	s.mu.Unlock()
	return nil
}

func (s *recordingSocket) Ping(context.Context) error {
	select {
	case <-s.closed:
		return errOutboundClosed
	default:
		return nil
	}
}

func (s *recordingSocket) CloseNow() error {
	s.closeOnce.Do(func() { close(s.closed) })
	return nil
}

func (s *recordingSocket) snapshot() [][]byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	frames := make([][]byte, len(s.frames))
	for i := range s.frames {
		frames[i] = append([]byte(nil), s.frames[i]...)
	}
	return frames
}

func waitForFrames(t *testing.T, socket *recordingSocket, count int) [][]byte {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if frames := socket.snapshot(); len(frames) >= count {
			return frames
		}
		time.Sleep(time.Millisecond)
	}
	frames := socket.snapshot()
	t.Fatalf("writer recorded %d frames, want %d", len(frames), count)
	return nil
}

func (c *fakeConn) Send(frame []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return c.err
	}
	c.frames = append(c.frames, frame)
	return nil
}

func (c *fakeConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

func (c *fakeConn) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.frames)
}

func TestProtocolMismatchReplyRemainsReadableToOldClient(t *testing.T) {
	conn := &fakeConn{}
	if err := sendControlReplyVersion(conn, "old-request", 1, protocol.Control{
		Type: protocol.CtlError, Message: "unsupported protocol version",
	}); err != nil {
		t.Fatal(err)
	}
	conn.mu.Lock()
	defer conn.mu.Unlock()
	if len(conn.frames) != 1 {
		t.Fatalf("control frames = %d, want 1", len(conn.frames))
	}
	var envelope protocol.Envelope
	if err := json.Unmarshal(conn.frames[0], &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.V != 1 || envelope.ReplyTo != "old-request" {
		t.Fatalf("old client cannot correlate version error: %+v", envelope)
	}
	var control protocol.Control
	if err := json.Unmarshal(envelope.Payload, &control); err != nil {
		t.Fatal(err)
	}
	if control.Type != protocol.CtlError || control.Message != "unsupported protocol version" {
		t.Fatalf("unexpected version response: %+v", control)
	}
}

func TestHubRoutesBetweenDaemonAndApps(t *testing.T) {
	hub := NewHub()
	account := DeriveAccount("t")

	daemon := &fakeConn{}
	phone := &fakeConn{}
	tablet := &fakeConn{}
	hub.AddDaemon(account, daemon)
	hub.AddApp(account, "phone", phone)
	hub.AddApp(account, "tablet", tablet)

	if err := hub.ToDaemon(account, []byte("send this")); err != nil {
		t.Fatal(err)
	}
	if daemon.count() != 1 {
		t.Errorf("daemon received %d frames, want 1", daemon.count())
	}

	// Daemon output fans out to every device the user has open.
	if n := hub.ToApps(account, []byte("new message")); n != 2 {
		t.Errorf("delivered to %d devices, want 2", n)
	}
	if phone.count() != 1 || tablet.count() != 1 {
		t.Error("both devices should receive daemon output")
	}
}

func TestHubSlowAppDoesNotDelayOtherApps(t *testing.T) {
	hub := NewHub()
	account := DeriveAccount("fanout")
	slowGate := make(chan struct{})
	slowSocket := newRecordingSocket(slowGate, nil)
	fastSocket := newRecordingSocket(nil, nil)
	slow := newWSConnWithQueue(slowSocket, 1)
	fast := newWSConnWithQueue(fastSocket, 8)
	defer fast.Close()
	hub.AddApp(account, "slow", slow)
	hub.AddApp(account, "fast", fast)

	if delivered := hub.ToApps(account, []byte("one")); delivered != 2 {
		t.Fatalf("first fanout accepted by %d devices, want 2", delivered)
	}
	select {
	case <-slowSocket.started:
		// The writer now owns frame one and is blocked in network I/O, leaving
		// exactly one queue slot available.
	case <-time.After(time.Second):
		t.Fatal("slow writer did not start")
	}
	if delivered := hub.ToApps(account, []byte("two")); delivered != 2 {
		t.Fatalf("second fanout accepted by %d devices, want 2", delivered)
	}

	// Frame three overflows only the slow queue. Fanout must return without
	// waiting for its blocked network write, while the healthy peer receives
	// every frame in order.
	done := make(chan int, 1)
	go func() { done <- hub.ToApps(account, []byte("three")) }()
	select {
	case delivered := <-done:
		if delivered != 1 {
			t.Fatalf("overflow fanout accepted by %d devices, want only the healthy one", delivered)
		}
	case <-time.After(time.Second):
		t.Fatal("a full slow-app queue stalled hub fanout")
	}
	select {
	case <-slowSocket.closed:
	case <-time.After(time.Second):
		t.Fatal("overflowing slow app was not closed")
	}
	_, apps, _ := hub.Stats()
	if apps != 1 {
		t.Fatalf("hub retained %d apps after removing slow consumer, want 1", apps)
	}

	frames := waitForFrames(t, fastSocket, 3)
	for i, want := range []string{"one", "two", "three"} {
		if got := string(frames[i]); got != want {
			t.Fatalf("healthy frame %d = %q, want %q", i, got, want)
		}
	}
}

func TestWSConnPreservesQueuedFrameOrder(t *testing.T) {
	gate := make(chan struct{})
	socket := newRecordingSocket(gate, nil)
	conn := newWSConnWithQueue(socket, 3)
	defer conn.Close()

	if err := conn.Send([]byte("one")); err != nil {
		t.Fatal(err)
	}
	select {
	case <-socket.started:
	case <-time.After(time.Second):
		t.Fatal("writer did not start")
	}
	for _, frame := range []string{"two", "three", "four"} {
		if err := conn.Send([]byte(frame)); err != nil {
			t.Fatalf("enqueue %q: %v", frame, err)
		}
	}
	close(gate)

	frames := waitForFrames(t, socket, 4)
	for i, want := range []string{"one", "two", "three", "four"} {
		if got := string(frames[i]); got != want {
			t.Fatalf("frame %d = %q, want %q", i, got, want)
		}
	}
}

func TestWSConnClosesAtOutboundByteBudget(t *testing.T) {
	gate := make(chan struct{})
	socket := newRecordingSocket(gate, nil)
	conn := newWSConnWithQueue(socket, 16)

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
	if err := conn.Send([]byte("overflow")); !errors.Is(err, errOutboundQueueFull) {
		t.Fatalf("byte-budget overflow error = %v", err)
	}
	select {
	case <-socket.closed:
	case <-time.After(time.Second):
		t.Fatal("byte-budget overflow did not close socket")
	}
}

func TestWSConnWriteFailureClosesAndHubRemovesConsumer(t *testing.T) {
	hub := NewHub()
	account := DeriveAccount("write-failure")
	gate := make(chan struct{})
	socket := newRecordingSocket(gate, errors.New("write failed"))
	conn := newWSConnWithQueue(socket, 1)
	hub.AddApp(account, "broken", conn)

	if delivered := hub.ToApps(account, []byte("one")); delivered != 1 {
		t.Fatalf("initial enqueue accepted by %d devices, want 1", delivered)
	}
	select {
	case <-socket.started:
	case <-time.After(time.Second):
		t.Fatal("writer did not start")
	}
	close(gate)
	select {
	case <-socket.closed:
	case <-time.After(time.Second):
		t.Fatal("write failure did not close the websocket")
	}

	// The next fanout observes the closed queue and removes it synchronously.
	if delivered := hub.ToApps(account, []byte("two")); delivered != 0 {
		t.Fatalf("closed consumer accepted a later frame (delivered=%d)", delivered)
	}
	_, apps, _ := hub.Stats()
	if apps != 0 {
		t.Fatalf("hub retained %d apps after writer failure, want 0", apps)
	}
}

func TestHubFanoutDropsManySlowConsumersWithoutStalling(t *testing.T) {
	hub := NewHub()
	account := DeriveAccount("slow-consumer-load")
	const slowCount = 64 // deliberately exceeds the old 32-worker fanout cap

	sockets := make([]*recordingSocket, 0, slowCount)
	connections := make([]*wsConn, 0, slowCount)
	neverWrite := make(chan struct{})
	for i := range slowCount {
		socket := newRecordingSocket(neverWrite, nil)
		conn := newWSConnWithQueue(socket, 1)
		sockets = append(sockets, socket)
		connections = append(connections, conn)
		hub.AddApp(account, "slow-"+strconv.Itoa(i), conn)
	}
	t.Cleanup(func() {
		for _, conn := range connections {
			_ = conn.Close()
		}
	})
	healthy := &fakeConn{}
	hub.AddApp(account, "healthy", healthy)

	if delivered := hub.ToApps(account, []byte("one")); delivered != slowCount+1 {
		t.Fatalf("first fanout accepted by %d devices, want %d", delivered, slowCount+1)
	}
	for i, socket := range sockets {
		select {
		case <-socket.started:
		case <-time.After(time.Second):
			t.Fatalf("slow writer %d did not start", i)
		}
	}
	if delivered := hub.ToApps(account, []byte("two")); delivered != slowCount+1 {
		t.Fatalf("second fanout accepted by %d devices, want %d", delivered, slowCount+1)
	}

	done := make(chan int, 1)
	go func() { done <- hub.ToApps(account, []byte("three")) }()
	select {
	case delivered := <-done:
		if delivered != 1 {
			t.Fatalf("overflow fanout accepted by %d devices, want only the healthy one", delivered)
		}
	case <-time.After(time.Second):
		t.Fatal("64 saturated consumers stalled fanout")
	}
	if healthy.count() != 3 {
		t.Fatalf("healthy consumer received %d frames, want 3", healthy.count())
	}
	_, apps, _ := hub.Stats()
	if apps != 1 {
		t.Fatalf("hub retained %d apps after overflow load, want 1", apps)
	}
}

func TestHubIsolatesAccounts(t *testing.T) {
	hub := NewHub()
	mine := DeriveAccount("my-daemon")
	theirs := DeriveAccount("their-daemon")

	myDaemon := &fakeConn{}
	theirPhone := &fakeConn{}
	hub.AddDaemon(mine, myDaemon)
	hub.AddApp(theirs, "phone", theirPhone)

	// Another user's device must not reach my daemon.
	if err := hub.ToDaemon(theirs, []byte("hello")); !errors.Is(err, ErrNoPeer) {
		t.Errorf("cross-account routing succeeded: %v", err)
	}
	if myDaemon.count() != 0 {
		t.Fatal("a frame crossed accounts")
	}
	if n := hub.ToApps(mine, []byte("out")); n != 0 {
		t.Errorf("my daemon output reached %d foreign devices", n)
	}
	if theirPhone.count() != 0 {
		t.Fatal("output crossed accounts")
	}
}

func TestHubReportsOfflineImmediately(t *testing.T) {
	hub := NewHub()
	account := DeriveAccount("t")

	// Nothing is buffered while the Mac is away: storing the frame would mean
	// storing user data, and a prompt failure beats silent delayed delivery.
	if err := hub.ToDaemon(account, []byte("x")); !errors.Is(err, ErrNoPeer) {
		t.Errorf("expected ErrNoPeer, got %v", err)
	}

	daemon := &fakeConn{}
	hub.AddDaemon(account, daemon)
	if online, _ := hub.DaemonOnline(account); !online {
		t.Error("daemon should report online once connected")
	}

	if !hub.RemoveDaemon(account, daemon) {
		t.Fatal("current daemon was not removed")
	}
	online, lastSeen := hub.DaemonOnline(account)
	if online {
		t.Error("daemon should report offline after disconnect")
	}
	if lastSeen.IsZero() {
		t.Error("last-seen should be recorded so the app can say how long")
	}
}

func TestHubReplacesStaleDaemonConnection(t *testing.T) {
	hub := NewHub()
	account := DeriveAccount("t")

	first := &fakeConn{}
	hub.AddDaemon(account, first)
	second := &fakeConn{}

	replaced := hub.AddDaemon(account, second)
	if replaced != Conn(first) {
		t.Fatal("reconnecting should surface the stale connection for closing")
	}

	// A late cleanup from the old connection must not deregister the new one.
	if hub.RemoveDaemon(account, first) {
		t.Fatal("stale daemon cleanup reported that it removed the replacement")
	}
	if online, _ := hub.DaemonOnline(account); !online {
		t.Error("stale disconnect wrongly removed the current daemon")
	}
	if err := hub.ToDaemon(account, []byte("x")); err != nil {
		t.Fatal(err)
	}
	if second.count() != 1 {
		t.Error("frames should route to the newest daemon connection")
	}
}

func TestHubCapsPendingPairings(t *testing.T) {
	hub := NewHub()
	hub.maxPairings = 1
	if _, err := hub.NewPairing(DeriveAccount("first")); err != nil {
		t.Fatal(err)
	}
	if _, err := hub.NewPairing(DeriveAccount("second")); !errors.Is(err, ErrPairingCapacity) {
		t.Fatalf("second pairing error = %v, want ErrPairingCapacity", err)
	}
}

func TestPairingCodeIsSingleUseAndExpires(t *testing.T) {
	hub := NewHub()
	account := DeriveAccount("t")

	code, err := hub.NewPairingCode(account)
	if err != nil {
		t.Fatal(err)
	}
	if len(code) != pairingCodeDigits {
		t.Fatalf("code = %q, want %d random digits", code, pairingCodeDigits)
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			t.Fatalf("code %q is not all digits", code)
		}
	}

	got, ok := hub.RedeemPairingCode(code)
	if !ok || got != account {
		t.Fatalf("redeem failed: %q %v", got, ok)
	}
	if _, ok := hub.RedeemPairingCode(code); ok {
		t.Error("a pairing code must not be reusable")
	}
}

func TestLimiterCannotBeBypassedByConcurrentRequests(t *testing.T) {
	limit := newLimiter(1, time.Minute)
	start := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	allowed := 0
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if limit.allow("caller", time.Now()) {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()
	if allowed != 1 {
		t.Fatalf("concurrent limiter allowed %d requests, want 1", allowed)
	}
}

func TestLimiterReservationsChargeOnlyFailures(t *testing.T) {
	limit := newLimiter(1, time.Minute)
	now := time.Now()
	if !limit.reserve("caller", now) {
		t.Fatal("first reservation was refused")
	}
	if limit.reserve("caller", now) {
		t.Fatal("concurrent reservation bypassed the limit")
	}
	limit.release("caller", now, false)
	if !limit.reserve("caller", now) {
		t.Fatal("successful operation was charged as a failure")
	}
	limit.release("caller", now, true)
	if limit.reserve("caller", now) {
		t.Fatal("failed operation did not consume the budget")
	}
}

func TestWrongGuessesDoNotAffectOtherUsersCodes(t *testing.T) {
	// Regression: the relay is multi-tenant and its pairing table is shared
	// across accounts, so charging a failed guess to every outstanding entry
	// let any anonymous caller delete every other user's in-flight code with a
	// handful of wrong guesses. Failures are charged to the caller who made
	// them instead — see limiter.
	hub := NewHub()
	victim := DeriveAccount("victim-daemon")

	code, err := hub.NewPairingCode(victim)
	if err != nil {
		t.Fatal(err)
	}

	for range 50 {
		if _, ok := hub.RedeemPairingCode("00000000"); ok {
			t.Fatal("a guessed code was accepted")
		}
	}

	account, ok := hub.RedeemPairingCode(code)
	if !ok {
		t.Fatal("a stranger's wrong guesses invalidated an unrelated user's code")
	}
	if account != victim {
		t.Errorf("account = %q, want the victim's", account)
	}
}

func TestExpiredPairingCodeIsRejected(t *testing.T) {
	hub := NewHub()
	account := DeriveAccount("t")
	code, err := hub.NewPairingCode(account)
	if err != nil {
		t.Fatal(err)
	}

	hub.mu.Lock()
	entry := hub.pairings[code]
	entry.expires = time.Now().Add(-time.Second)
	hub.pairings[code] = entry
	hub.mu.Unlock()

	if _, ok := hub.RedeemPairingCode(code); ok {
		t.Error("an expired pairing code was accepted")
	}
}

func TestStatsExposeOnlyCounts(t *testing.T) {
	hub := NewHub()
	account := DeriveAccount("t")
	hub.AddDaemon(account, &fakeConn{})
	hub.AddApp(account, "phone", &fakeConn{})
	if _, err := hub.NewPairingCode(account); err != nil {
		t.Fatal(err)
	}

	daemons, apps, pending := hub.Stats()
	if daemons != 1 || apps != 1 || pending != 1 {
		t.Errorf("stats = (%d, %d, %d), want (1, 1, 1)", daemons, apps, pending)
	}
}

func TestHubIsConcurrencySafe(t *testing.T) {
	hub := NewHub()
	account := DeriveAccount("t")
	hub.AddDaemon(account, &fakeConn{})

	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(3)
		go func() { defer wg.Done(); _ = hub.ToDaemon(account, []byte("x")) }()
		go func() { defer wg.Done(); hub.ToApps(account, []byte("y")) }()
		go func() {
			defer wg.Done()
			conn := &fakeConn{}
			hub.AddApp(account, string(rune('a'+i%26)), conn)
			hub.RemoveApp(account, string(rune('a'+i%26)))
		}()
	}
	wg.Wait()
}
