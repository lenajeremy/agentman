package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"

	"github.com/lenajeremy/agentman/internal/hook"
	"github.com/lenajeremy/agentman/internal/tunnel"
)

// runExpose shares one local port as a public link until interrupted.
//
// It is a foreground command of its own, like `am pair`, rather than something
// `am serve` does: the relay keeps one daemon socket per account, so a second
// daemon connection would knock the running daemon offline. A tunnel uses its
// own endpoint and socket, and exiting is how sharing stops.
func runExpose(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("expose", flag.ExitOnError)
	relayFlag := fs.String("relay", "", relayFlagHelp)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("expose needs exactly one port, e.g. `am expose 3000`")
	}
	port, err := strconv.Atoi(fs.Arg(0))
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("%q is not a valid port", fs.Arg(0))
	}

	relayURL := resolveRelay(*relayFlag)
	if relayURL == "" {
		return fmt.Errorf("sharing a port needs a relay, but it was disabled with -relay none")
	}
	relayURL, err = normalizeRelayURL(relayURL)
	if err != nil {
		return err
	}
	cfg, err := hook.LoadConfig("")
	if err != nil {
		return err
	}

	// Not an error: agents often expose a port and then start the server.
	// Visitors see an explanatory page until something is listening.
	if !portBusy(port) {
		fmt.Fprintf(os.Stderr, "%s\n", dim(fmt.Sprintf(
			"note: nothing is listening on localhost:%d yet — the link will work once something is", port)))
	}

	announced := false
	client := &tunnel.Client{
		RelayURL: relayURL,
		Token:    cfg.Token,
		Port:     port,
		// A link is derived from the account and this nonce, so supplying one
		// keeps the same address across restarts. Reconnecting already keeps
		// it; this covers stopping and starting again, which is what a
		// development loop does every time the binary is rebuilt — and a new
		// address there silently unpairs the phone that was using it.
		Nonce: os.Getenv("AGENTMAN_TUNNEL_NONCE"),
		OnReady: func(hello tunnel.Hello) {
			if announced {
				fmt.Printf("%s %s\n", stamp(), dim("relay      reconnected — same link"))
				return
			}
			announced = true
			// The link gets a line of its own so an agent reading this output,
			// or a person double-clicking it, picks up exactly the URL.
			fmt.Printf("\n  %s\n\n", bold(hello.URL))
			fmt.Printf("  %s\n", dim(fmt.Sprintf("→ localhost:%d on this machine", port)))
			fmt.Printf("  %s\n", dim(fmt.Sprintf(
				"anyone with this link can reach localhost:%d until you stop sharing", port)))
			fmt.Printf("  %s\n\n", dim("ctrl-c to stop"))
		},
		OnDisconnect: func(err error) {
			detail := "connection lost"
			if err != nil {
				detail = err.Error()
			}
			fmt.Printf("%s %s\n", stamp(), dim("relay      disconnected ("+detail+") — retrying"))
		},
	}
	return client.Run(ctx)
}
