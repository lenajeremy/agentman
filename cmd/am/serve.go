package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mdp/qrterminal/v3"
	qr "rsc.io/qr"

	"github.com/lenajeremy/agentman/internal/daemon"
	"github.com/lenajeremy/agentman/internal/hook"
	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/relay"
	"github.com/lenajeremy/agentman/internal/source"
)

// relayEnv overrides the built-in relay without passing a flag every time.
const relayEnv = "AGENTMAN_RELAY"

// DefaultRelay is the public relay, used when nothing else is specified.
//
// The service has no transcript database, but it remains a live trust boundary
// because payloads are not end-to-end encrypted. Users can point at a relay
// they operate themselves, and `-relay none` opts out entirely.
const DefaultRelay = "https://agentman-production.up.railway.app"

// relayFlagHelp describes the flag consistently across subcommands.
const relayFlagHelp = "relay URL; defaults to the public relay, " +
	"or set " + relayEnv + ". Use \"none\" to run without one."

// resolveRelay applies the precedence: an explicit flag wins, then the
// environment, then the built-in default.
//
// Returning empty means "no relay" — the daemon still watches agents and
// prints locally, which is a legitimate way to run it and the reason an opt
// out exists at all.
func resolveRelay(flagValue string) string {
	value := strings.TrimSpace(flagValue)
	if value == "" {
		value = strings.TrimSpace(os.Getenv(relayEnv))
	}
	if value == "" {
		value = DefaultRelay
	}
	switch strings.ToLower(value) {
	case "none", "off", "-":
		return ""
	}
	return value
}

// printSink reports daemon events to the terminal.
//
// It stands in for a phone: `am serve` without a relay is a complete, useful
// program on its own, and it is how the daemon is exercised before any app
// exists.
type printSink struct {
	mu sync.Mutex
	// quiet suppresses routine state churn once a relay is attached, so the
	// terminal shows connection events rather than a scrolling session list.
	quiet bool
}

func (p *printSink) Send(event protocol.Event) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	switch event.Type {
	case protocol.EvtTurnComplete:
		fmt.Printf("%s 🔔 %s %s\n", stamp(), bold("done "+event.SessionName), dim(event.Preview))
	case protocol.EvtSessionUpdate:
		if p.quiet || event.Session == nil {
			return nil
		}
		fmt.Printf("%s %s %s %s\n", stamp(), stateDot(event.Session.State),
			event.Session.Name, dim(string(event.Session.State)))
	case protocol.EvtSessionGone:
		if !p.quiet {
			fmt.Printf("%s ○ %s\n", stamp(), dim("ended "+shortSessionID(event.SessionID)))
		}
	case protocol.EvtSessions:
		if !p.quiet {
			fmt.Printf("%s %s\n", stamp(), dim(fmt.Sprintf("%d session(s)", len(event.Sessions))))
		}
	case protocol.EvtError:
		fmt.Fprintf(os.Stderr, "%s %s\n", stamp(), dim("warning: "+event.Error))
	}
	return nil
}

// multiSink fans events out to the terminal and the relay at once.
type multiSink struct {
	sinks []daemon.Transport
}

func (m multiSink) Send(event protocol.Event) error {
	for _, sink := range m.sinks {
		_ = sink.Send(event)
	}
	return nil
}

// runServe is the daemon: discovery, hooks, and — when configured — the relay
// connection that a phone reaches it through.
func runServe(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addrFlag := fs.String("addr", "", "loopback address for agent hook deliveries (persisted for installed hooks)")
	relayFlag := fs.String("relay", "", relayFlagHelp)
	if err := fs.Parse(args); err != nil {
		return err
	}
	relayURL := resolveRelay(*relayFlag)

	cfg, err := hook.LoadConfig("")
	if err != nil {
		return err
	}
	addr := cfg.ListenAddr()
	if strings.TrimSpace(*addrFlag) != "" {
		addr = strings.TrimSpace(*addrFlag)
	}
	if err := hook.ValidateAddr(addr); err != nil {
		return err
	}
	store, err := hook.NewStore("")
	if err != nil {
		return err
	}
	registry, err := buildRegistry()
	if err != nil {
		return err
	}

	// Messages for sessions with no live input channel wait here until their
	// next Stop hook, which is the only moment such a session can be reached.
	pending := source.NewPendingQueue()
	attachPending(registry, pending)

	// Hook receiver: loopback only, and the reason state changes are exact
	// rather than polled.
	hookServer := hook.NewServer(cfg.Token)
	hookServer.SetPendingSource(pending.Take)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return describeListenError(err, addr)
	}
	if cfg.HookAddr != addr {
		cfg.HookAddr = addr
		if err := hook.SaveConfig("", cfg); err != nil {
			_ = listener.Close()
			return fmt.Errorf("save hook listener address: %w", err)
		}
	}
	hookErrs := make(chan error, 1)
	go func() { hookErrs <- hookServer.Serve(ctx, listener) }()
	fmt.Printf("%s\n", dim("hooks      listening on "+addr))

	console := &printSink{quiet: relayURL != ""}
	sinks := []daemon.Transport{console}

	var client *daemon.Client
	if relayURL != "" {
		client = daemon.NewClient(relayURL, cfg.Token)
		client.OnStatus = func(connected bool, detail string) {
			if connected {
				fmt.Printf("%s %s\n", stamp(), dim("relay      connected to "+detail))
				return
			}
			fmt.Printf("%s %s\n", stamp(), dim("relay      disconnected ("+detail+") — retrying"))
		}
		client.OnPairCode = func(code, token string, expiresAt time.Time) {
			printPairCode(relayURL, code, token, expiresAt)
		}
		sinks = append(sinks, client)
		fmt.Printf("%s\n", dim("relay      "+relayURL+"  account "+string(client.Account())))
	} else {
		fmt.Printf("%s\n", dim("relay      disabled — this daemon is not reachable from your phone"))
	}

	agent := daemon.New(registry, multiSink{sinks: sinks})
	if client != nil {
		client.OnDeviceDisconnected = agent.DisconnectSubscriber
	}

	// Record hook activity for `am doctor`, then pass it to the daemon.
	hookEvents := make(chan hook.Event, 32)
	go func() {
		defer close(hookEvents)
		for {
			select {
			case <-ctx.Done():
				return
			case event := <-hookServer.Events():
				if err := store.RecordFired(event.Kind, time.UnixMilli(event.ReceivedAt)); err != nil {
					fmt.Fprintf(os.Stderr, "am: warning: %v\n", err)
				}
				select {
				case hookEvents <- event:
				default: // never block a hook delivery; the agent is waiting
				}
			}
		}
	}()

	if client != nil {
		go func() { _ = client.Run(ctx, agent.HandleFrom) }()
	}

	fmt.Printf("%s\n\n", dim("ctrl-c to stop"))

	daemonDone := make(chan error, 1)
	go func() { daemonDone <- agent.Run(ctx, hookEvents) }()

	select {
	case <-ctx.Done():
		return nil
	case err := <-hookErrs:
		return err
	case err := <-daemonDone:
		return err
	}
}

// describeListenError turns a bind failure into something actionable.
//
// "address already in use" is technically accurate and practically useless:
// the cause is almost always a second `am serve` the user forgot about, and
// the raw error says nothing about that or about how to run two on purpose.
func describeListenError(err error, addr string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, syscall.EADDRINUSE) {
		return fmt.Errorf(
			"another agentman daemon is already listening on %s.\n"+
				"       Stop it first, or run this one on a different port:\n"+
				"         am serve -addr 127.0.0.1:8788", addr)
	}
	return err
}

// runPair prints a pairing code for the phone.
//
// The code is minted by the relay and lives only in its memory for sixty
// seconds, so pairing needs no account, no email, and no stored record.
//
// This uses plain HTTP rather than the daemon's websocket: opening a second
// daemon socket would replace the live one (newest wins per account) and knock
// the running daemon offline every time the user paired a device.
func runPair(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("pair", flag.ExitOnError)
	relayFlag := fs.String("relay", "", relayFlagHelp)
	if err := fs.Parse(args); err != nil {
		return err
	}
	relayURL := resolveRelay(*relayFlag)
	if relayURL == "" {
		return fmt.Errorf("pairing needs a relay, but it was disabled with -relay none")
	}

	cfg, err := hook.LoadConfig("")
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	endpoint := strings.TrimRight(relayURL, "/") + "/pair/code"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.Token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach the relay at %s: %w", relayURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("relay refused to issue a code (HTTP %d)", resp.StatusCode)
	}

	var body struct {
		Code      string `json:"code"`
		Token     string `json:"token"`
		ExpiresAt int64  `json:"expiresAt"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return err
	}
	if body.Code == "" {
		return fmt.Errorf("relay returned an empty pairing code")
	}

	printPairCode(relayURL, body.Code, body.Token, time.UnixMilli(body.ExpiresAt))
	return nil
}

// PairingURL is the payload encoded in the QR code.
//
// A URL rather than bare JSON so the same string works as a deep link: tapping
// it on the phone opens the app straight into pairing, which is the whole
// point for anyone reading this over a remote shell where scanning is not an
// option.
//
// The relay is omitted when it is the default one, because payload length
// drives the QR version and so the printed size — dropping those sixty-odd
// characters takes the code from twenty rows to sixteen. A custom relay is
// still spelled out in full: a self-hoster whose QR quietly pointed at the
// public relay would be a far worse outcome than a slightly taller square.
func PairingURL(relayURL, token string) string {
	relay := strings.TrimRight(relayURL, "/")
	if relay == DefaultRelay {
		return "agentman://pair?token=" + url.QueryEscape(token)
	}
	return fmt.Sprintf("agentman://pair?relay=%s&token=%s",
		url.QueryEscape(relay), url.QueryEscape(token))
}

func printPairCode(relayURL, code, token string, expiresAt time.Time) {
	remaining := time.Until(expiresAt).Round(time.Second)
	if remaining < 0 {
		remaining = 0
	}

	// The QR carries the long token, so scanning needs no typing and no relay
	// address — and because that secret is 128 bits, it is not something worth
	// guessing, unlike the ten digits below it.
	if token != "" && isTerminal() {
		fmt.Println()
		// Half blocks pack two QR rows into one terminal line, which is most of
		// why this fits on screen alongside the prompt that printed it.
		//
		// This is as dense as a terminal QR can honestly get. A cell is about
		// half as wide as it is tall, so one module across by two down comes
		// out square, which is what scanners expect. Packing 2x2 modules into
		// a cell would halve the width again but leave every module squashed
		// to a 1:2 rectangle, and a code that is smaller but does not scan is
		// not smaller.
		//
		// The quiet zone stays for the same reason: it is the margin a scanner
		// uses to find the code at all, and measuring showed removing it saves
		// nothing anyway.
		qrterminal.GenerateWithConfig(PairingURL(relayURL, token), qrterminal.Config{
			// Level L, not M. Error correction exists to survive physical
			// damage — smudges, tears, dirt on a printed label. A code drawn
			// on a screen and scanned seconds later has none of that, and the
			// lower level drops this payload from a 33-module code to 29,
			// which is two fewer rows and four fewer columns.
			Level:          qr.L,
			Writer:         os.Stdout,
			HalfBlocks:     true,
			BlackChar:      qrterminal.BLACK_BLACK,
			WhiteChar:      qrterminal.WHITE_WHITE,
			BlackWhiteChar: qrterminal.BLACK_WHITE,
			WhiteBlackChar: qrterminal.WHITE_BLACK,
			QuietZone:      1,
		})
	}

	fmt.Printf("\n  %s\n\n", bold(spaced(relay.FormatPairingCode(code))))
	fmt.Printf("  %s\n", dim(fmt.Sprintf(
		"scan the code above, or type these digits — either works, for %s", remaining)))
}

// spaced widens a code so it can be read off a screen at a glance, keeping
// the grouping it arrives with.
func spaced(code string) string {
	var out []string
	for _, group := range strings.Fields(code) {
		out = append(out, strings.Join(strings.Split(group, ""), " "))
	}
	return strings.Join(out, "   ")
}
