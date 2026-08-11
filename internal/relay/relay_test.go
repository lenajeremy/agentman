package relay

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
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
}

func TestDeriveAccountIsStableAndDistinct(t *testing.T) {
	// Stable across restarts: the derivation must be a pure function of the
	// token, with no randomness or per-process salt, or a returning daemon
	// would land in a different account and lose its paired devices.
	const knownAccount = "6f4a441b727ba6f5"
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

	hub.RemoveDaemon(account, daemon)
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
	hub.RemoveDaemon(account, first)
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

func TestPairingCodeIsSingleUseAndExpires(t *testing.T) {
	hub := NewHub()
	account := DeriveAccount("t")

	code, err := hub.NewPairingCode(account)
	if err != nil {
		t.Fatal(err)
	}
	if len(code) != 6 {
		t.Fatalf("code = %q, want 6 digits", code)
	}

	got, ok := hub.RedeemPairingCode(code)
	if !ok || got != account {
		t.Fatalf("redeem failed: %q %v", got, ok)
	}
	if _, ok := hub.RedeemPairingCode(code); ok {
		t.Error("a pairing code must not be reusable")
	}
}

func TestWrongGuessesDoNotAffectOtherUsersCodes(t *testing.T) {
	// Regression, and the reason pairing is no longer rate limited by
	// penalising codes: the relay is multi-tenant, its pairing table is shared
	// across accounts, and charging a failed guess to every outstanding entry
	// let any anonymous caller delete every other user's in-flight code with a
	// handful of wrong guesses. A wrong guess belongs to nobody, so it is
	// charged to the caller instead — see limiter.
	hub := NewHub()
	victim := DeriveAccount("victim-daemon")

	code, err := hub.NewPairingCode(victim)
	if err != nil {
		t.Fatal(err)
	}

	for range 50 {
		if _, ok := hub.RedeemPairingCode("000000"); ok {
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
