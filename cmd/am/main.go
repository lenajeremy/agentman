// Command am is the agentman daemon and CLI.
//
// Phase 1 ships the read path only: discover running agent sessions, list
// them, page through their history, and follow one live. Everything is proven
// from the terminal before any relay or app exists.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/lenajeremy/agentman/internal/protocol"
	"github.com/lenajeremy/agentman/internal/source"
)

// version is stamped at build time with -ldflags "-X main.version=...".
// Without it a released binary cannot say what it is, which matters most for
// the one path where the user did not build it themselves: `brew install`.
var version = "dev"

const usage = `am — monitor your local coding agents

Usage:
  am list                     List running agent sessions
  am history <session-id>     Print a session's recent messages
  am watch [session-id]       Follow sessions live (all, or one in detail)
  am serve                    Run the daemon (hooks + relay connection)
  am pair                     Print a pairing code for your phone
  am claude [args...]         Start Claude Code so you can message it later
  am codex [args...]          Start Codex so you can message it later
  am opencode [args...]       Start OpenCode so you can message it later
  am send <session-id> <text> Send a message to a running session
  am install-hooks            Register agentman's hooks with your agents
  am uninstall-hooks          Remove them again
  am doctor                   Check that everything is wired up correctly
  am version                  Print the version

Flags:
  -json                       Machine-readable output
  -limit <n>                  Messages per history page (default 30)
  -before <cursor>            Page further back, using a cursor from history
  -dry-run                    With install-hooks: show changes without writing
  -relay <url>                Relay to use. Defaults to the public relay;
                              set AGENTMAN_RELAY to change it, or pass
                              "none" to run without one.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	// Ctrl-C should stop the watchers cleanly rather than leave goroutines
	// mid-read on a transcript.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	command, args := os.Args[1], os.Args[2:]

	var err error
	switch command {
	case "list":
		err = runList(ctx, args)
	case "history":
		err = runHistory(ctx, args)
	case "watch":
		err = runWatch(ctx, args)
	case "serve":
		err = runServe(ctx, args)
	case "hook":
		// Invoked by the agent CLIs themselves, never by a human.
		err = runHook(ctx, args)
	case "install-hooks":
		err = runInstallHooks(ctx, args, false)
	case "uninstall-hooks":
		err = runInstallHooks(ctx, args, true)
	case "pair":
		err = runPair(ctx, args)
	case "claude", "codex":
		// Launch an agent inside tmux so it can receive messages later.
		err = runWrap(ctx, command, args)
	case "opencode":
		// No tmux: OpenCode takes prompts over its API. What it needs instead
		// is its API on the port the daemon watches.
		err = runOpenCode(ctx, args)
	case "send":
		err = runSend(ctx, args)
	case "doctor":
		err = runDoctor(ctx, args)
	case "version", "--version", "-v":
		fmt.Printf("agentman %s\n", version)
		return
	case "-h", "--help", "help":
		fmt.Print(usage)
		return
	default:
		fmt.Fprintf(os.Stderr, "am: unknown command %q\n\n%s", command, usage)
		os.Exit(2)
	}

	if err != nil && ctx.Err() == nil {
		fmt.Fprintf(os.Stderr, "am: %v\n", err)
		os.Exit(1)
	}
}

// buildRegistry wires up every adapter. An adapter whose CLI is not installed
// stays silent rather than failing, so this works on any machine.
func buildRegistry() (*source.Registry, error) {
	registry := source.NewRegistry()

	claude, err := source.NewClaudeSource("")
	if err != nil {
		return nil, err
	}
	registry.Add(claude)

	codex, err := source.NewCodexSource("")
	if err != nil {
		return nil, err
	}
	registry.Add(codex)

	// OpenCode is reached over HTTP rather than the filesystem, so there is
	// nothing to configure: the adapter stays silent when no server is up.
	openCode := source.NewOpenCodeSource(os.Getenv("AGENTMAN_OPENCODE_URL"))
	if err := openCode.Validate(); err != nil {
		return nil, err
	}
	registry.Add(openCode)

	return registry, nil
}

// parseWithPositional parses flags that may appear on either side of a single
// positional argument.
//
// Go's flag package stops at the first non-flag argument, so `am history <id>
// -limit 4` would otherwise silently ignore -limit — and that is the order
// people naturally type. Parsing twice around the positional accepts both.
func parseWithPositional(fs *flag.FlagSet, args []string) (string, error) {
	if err := fs.Parse(args); err != nil {
		return "", err
	}
	rest := fs.Args()
	if len(rest) == 0 {
		return "", nil
	}
	positional := rest[0]
	if err := fs.Parse(rest[1:]); err != nil {
		return "", err
	}
	if extra := fs.Args(); len(extra) > 0 {
		return "", fmt.Errorf("unexpected argument %q", extra[0])
	}
	return positional, nil
}

func runList(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return err
	}

	registry, err := buildRegistry()
	if err != nil {
		return err
	}

	sessions, discoverErr := registry.Discover(ctx)
	if *asJSON {
		if err := json.NewEncoder(os.Stdout).Encode(sessions); err != nil {
			return err
		}
		return discoverErr
	}

	if len(sessions) == 0 {
		fmt.Println("No running agent sessions.")
		fmt.Println("Start one with `am claude`, `am codex`, or `am opencode` and run `am list` again.")
		return discoverErr
	}

	fmt.Printf("%d running session(s)\n\n", len(sessions))
	for _, s := range sessions {
		fmt.Printf("  %s %-28s %s\n", stateDot(s.State), truncate(s.Name, 28), dim(s.ID))
		fmt.Printf("     %-9s %-6s %s\n", s.Kind, s.State, dim(collapseHome(s.Cwd)))
		fmt.Printf("     %s\n\n", dim("last activity "+humanAge(s.LastActivityAt)))
	}

	// Report a partial failure rather than pretending the list is complete.
	return discoverErr
}

func runHistory(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("history", flag.ExitOnError)
	limit := fs.Int("limit", 30, "messages per page")
	before := fs.String("before", "", "cursor from a previous page")
	asJSON := fs.Bool("json", false, "machine-readable output")
	sessionID, err := parseWithPositional(fs, args)
	if err != nil {
		return err
	}
	if sessionID == "" {
		return fmt.Errorf("history needs a session id (see `am list`)")
	}
	if err := source.ValidatePageLimit(*limit); err != nil {
		return fmt.Errorf("history: %w", err)
	}

	registry, buildErr := buildRegistry()
	if buildErr != nil {
		return buildErr
	}
	// Discovery populates each adapter's session table, which Page reads.
	if _, err := registry.Discover(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "am: warning: %v\n", err)
	}

	page, err := registry.Page(ctx, sessionID, *before, *limit)
	if err != nil {
		return err
	}

	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(page)
	}

	for _, msg := range page.Messages {
		printMessage(msg)
	}
	if page.HasMore {
		fmt.Printf("\n%s\n", dim(fmt.Sprintf(
			"more history above — am history %s -before %s", sessionID, page.NextCursor)))
	} else {
		fmt.Printf("\n%s\n", dim("beginning of session"))
	}
	return nil
}

func runWatch(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("watch", flag.ExitOnError)
	sessionID, err := parseWithPositional(fs, args)
	if err != nil {
		return err
	}

	registry, err := buildRegistry()
	if err != nil {
		return err
	}

	if sessionID != "" {
		return watchSession(ctx, registry, sessionID)
	}
	return watchAll(ctx, registry)
}

// watchSession follows one session's messages as they are written.
func watchSession(ctx context.Context, registry *source.Registry, sessionID string) error {
	if _, err := registry.Discover(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "am: warning: %v\n", err)
	}

	fmt.Printf("%s\n\n", dim("following "+sessionID+" — ctrl-c to stop"))

	messages := make(chan []protocol.Message, 8)
	errs := make(chan error, 1)
	go func() { errs <- registry.Follow(ctx, sessionID, messages) }()

	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-errs:
			if ctx.Err() != nil {
				return nil
			}
			return err
		case batch := <-messages:
			for _, msg := range batch {
				printMessage(msg)
			}
		}
	}
}

// watchAll polls discovery and reports sessions appearing, changing state, and
// disappearing — the terminal equivalent of the app's agent list.
func watchAll(ctx context.Context, registry *source.Registry) error {
	fmt.Printf("%s\n\n", dim("watching all agents — ctrl-c to stop"))

	known := map[string]protocol.Session{}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for first := true; ; first = false {
		sessions, err := registry.Discover(ctx)
		if err != nil {
			fmt.Fprintf(os.Stderr, "am: warning: %v\n", err)
		}

		seen := make(map[string]bool, len(sessions))
		for _, s := range sessions {
			seen[s.ID] = true
			previous, existed := known[s.ID]
			switch {
			case !existed:
				verb := "found"
				if !first {
					verb = "started"
				}
				fmt.Printf("%s %s %s %s\n", stamp(), stateDot(s.State), verb,
					fmt.Sprintf("%s (%s) %s", s.Name, s.Kind, dim(collapseHome(s.Cwd))))
			case previous.State != s.State:
				fmt.Printf("%s %s %s is now %s\n", stamp(), stateDot(s.State), s.Name, s.State)
			}
			known[s.ID] = s
		}

		for id, s := range known {
			if !seen[id] {
				fmt.Printf("%s ○ ended  %s\n", stamp(), s.Name)
				delete(known, id)
			}
		}

		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

/* ------------------------------ presentation ----------------------------- */

func printMessage(msg protocol.Message) {
	// Use the message's own timestamp: in history these are minutes or days
	// old, and stamping them with the current time would be a lie.
	prefix := dim(formatStamp(msg.Ts))
	if msg.IsSidechain {
		prefix += " " + dim("↳")
	}

	switch msg.Role {
	case protocol.RoleUser:
		fmt.Printf("%s %s %s\n", prefix, bold("you"), firstLine(msg.Text, 100))
	case protocol.RoleAssistant:
		fmt.Printf("%s %s %s\n", prefix, bold("agent"), firstLine(msg.Text, 100))
	case protocol.RoleTool:
		if msg.Tool == nil {
			return
		}
		fmt.Printf("%s %s %s %s\n", prefix, toolMark(msg.Tool.Status),
			msg.Tool.Name, dim(truncate(msg.Tool.Summary, 80)))
	case protocol.RoleSystem:
		fmt.Printf("%s %s\n", prefix, dim(firstLine(msg.Text, 100)))
	}
}

func toolMark(status protocol.ToolStatus) string {
	switch status {
	case protocol.ToolRunning:
		return "◐"
	case protocol.ToolError:
		return "✗"
	default:
		return "⏺"
	}
}

func stateDot(state protocol.State) string {
	switch state {
	case protocol.StateBusy:
		return "●"
	case protocol.StateWaitingInput:
		return "◆"
	case protocol.StateEnded:
		return "○"
	default:
		return "◦"
	}
}

func stamp() string { return dim(time.Now().Format("15:04:05")) }

// formatStamp renders a message timestamp, widening to include the date once
// it is old enough that a bare clock time would be ambiguous.
func formatStamp(epochMillis int64) string {
	if epochMillis == 0 {
		return "--:--:--"
	}
	t := time.UnixMilli(epochMillis)
	if time.Since(t) > 12*time.Hour {
		return t.Format("Jan 02 15:04")
	}
	return t.Format("15:04:05")
}

// Colour is applied only for a terminal; piping to a file or another program
// yields clean text.
var useColour = isTerminal()

func isTerminal() bool {
	info, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func dim(text string) string {
	if !useColour || text == "" {
		return text
	}
	return "\x1b[2m" + text + "\x1b[0m"
}

func bold(text string) string {
	if !useColour {
		return text
	}
	return "\x1b[1m" + text + "\x1b[0m"
}

func firstLine(text string, maxLen int) string {
	if line, _, found := strings.Cut(text, "\n"); found {
		return truncate(line, maxLen) + dim(" …")
	}
	return truncate(text, maxLen)
}

func truncate(text string, maxLen int) string {
	runes := []rune(text)
	if len(runes) <= maxLen {
		return text
	}
	return string(runes[:maxLen-1]) + "…"
}

func collapseHome(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" || !strings.HasPrefix(path, home) {
		return path
	}
	return "~" + path[len(home):]
}

func humanAge(epochMillis int64) string {
	if epochMillis == 0 {
		return "unknown"
	}
	d := time.Since(time.UnixMilli(epochMillis))
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	}
}
