package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/lenajeremy/agentman/internal/daemon"
	"github.com/lenajeremy/agentman/internal/hook"
	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/source"
)

// relayEnv is where the relay URL comes from when -relay is not given.
const relayEnv = "AGENTMAN_RELAY"

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
	addr := fs.String("addr", hook.DefaultAddr, "loopback address for agent hook deliveries")
	relayURL := fs.String("relay", os.Getenv(relayEnv), "relay URL (or set "+relayEnv+")")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := hook.LoadConfig("")
	if err != nil {
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
	hookErrs := make(chan error, 1)
	go func() { hookErrs <- describeListenError(hookServer.Listen(ctx, *addr), *addr) }()
	fmt.Printf("%s\n", dim("hooks      listening on "+*addr))

	console := &printSink{quiet: *relayURL != ""}
	sinks := []daemon.Transport{console}

	var client *daemon.Client
	if *relayURL != "" {
		client = daemon.NewClient(*relayURL, cfg.Token)
		client.OnStatus = func(connected bool, detail string) {
			if connected {
				fmt.Printf("%s %s\n", stamp(), dim("relay      connected to "+detail))
				return
			}
			fmt.Printf("%s %s\n", stamp(), dim("relay      disconnected ("+detail+") — retrying"))
		}
		client.OnPairCode = printPairCode
		sinks = append(sinks, client)
		fmt.Printf("%s\n", dim("relay      "+*relayURL+"  account "+string(client.Account())))
	} else {
		fmt.Printf("%s\n", dim("relay      not configured — run with -relay to reach your phone"))
	}

	agent := daemon.New(registry, multiSink{sinks: sinks})

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
		go func() { _ = client.Run(ctx, agent.Handle) }()
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
	relayURL := fs.String("relay", os.Getenv(relayEnv), "relay URL (or set "+relayEnv+")")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *relayURL == "" {
		return fmt.Errorf("pair needs a relay: pass -relay or set %s", relayEnv)
	}

	cfg, err := hook.LoadConfig("")
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	endpoint := strings.TrimRight(*relayURL, "/") + "/pair/code"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.Token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach the relay at %s: %w", *relayURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("relay refused to issue a code (HTTP %d)", resp.StatusCode)
	}

	var body struct {
		Code      string `json:"code"`
		ExpiresAt int64  `json:"expiresAt"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return err
	}
	if body.Code == "" {
		return fmt.Errorf("relay returned an empty pairing code")
	}

	printPairCode(body.Code, time.UnixMilli(body.ExpiresAt))
	return nil
}

func printPairCode(code string, expiresAt time.Time) {
	remaining := time.Until(expiresAt).Round(time.Second)
	if remaining < 0 {
		remaining = 0
	}
	fmt.Printf("\n  %s\n\n", bold(spaced(code)))
	fmt.Printf("  %s\n", dim(fmt.Sprintf("enter this in the app within %s", remaining)))
}

// spaced widens a code so it can be read off a screen at a glance.
func spaced(code string) string {
	return strings.Join(strings.Split(code, ""), " ")
}
