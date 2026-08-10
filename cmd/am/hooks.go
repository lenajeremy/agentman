package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/lenajeremy/agentman/internal/hook"
	"github.com/lenajeremy/agentman/internal/protocol"
)

// runHook is the handler the agent CLIs invoke: `am hook <kind> <event>`.
//
// It runs inside the agent's critical path — the session is blocked until this
// returns — so it is deliberately spartan: read stdin, POST it, exit. It also
// exits 0 unconditionally. A daemon that is not running, a wedged socket, or a
// malformed payload must never surface as a hook failure in someone's coding
// session; missing a status update is always the cheaper outcome.
func runHook(ctx context.Context, args []string) error {
	if len(args) < 2 {
		return nil
	}
	kind, event := args[0], args[1]

	payload, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
	if err != nil {
		return nil
	}

	cfg, err := hook.LoadConfig("")
	if err != nil {
		return nil
	}

	// Short and hard: the user is waiting on this.
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	url := fmt.Sprintf("http://%s/hook/%s/%s", hook.DefaultAddr, kind, event)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return nil
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Agentman-Token", cfg.Token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil // daemon not running; that is fine and expected
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	_ = resp.Body.Close()
	return nil
}

// runInstallHooks registers agentman's hooks with every supported agent.
func runInstallHooks(ctx context.Context, args []string, remove bool) error {
	fs := flag.NewFlagSet("install-hooks", flag.ExitOnError)
	dryRun := fs.Bool("dry-run", false, "show what would change without writing")
	if err := fs.Parse(args); err != nil {
		return err
	}

	binary, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot determine own path: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(binary); err == nil {
		binary = resolved
	}

	cfg, err := hook.LoadConfig("")
	if err != nil {
		return err
	}

	plans, err := hook.Installer{Binary: binary}.Plans(cfg.Token, remove)
	if err != nil {
		return err
	}

	verb, verbPast := "install", "installed"
	if remove {
		verb, verbPast = "remove", "removed"
	}

	var failures int
	for _, plan := range plans {
		switch {
		case plan.Err != nil:
			failures++
			fmt.Printf("  ✗ %-8s %s\n", plan.Kind, plan.Err)
			continue
		case !plan.Changed:
			fmt.Printf("  ✓ %-8s already up to date %s\n", plan.Kind, dim(collapseHome(plan.Path)))
		case *dryRun:
			fmt.Printf("  · %-8s would %s hooks in %s\n", plan.Kind, verb, collapseHome(plan.Path))
		default:
			if err := plan.Apply(); err != nil {
				failures++
				fmt.Printf("  ✗ %-8s %v\n", plan.Kind, err)
				continue
			}
			fmt.Printf("  ✓ %-8s %s hooks in %s\n", plan.Kind, verbPast, collapseHome(plan.Path))
			if plan.Before != "" {
				fmt.Printf("    %s\n", dim("backup: "+collapseHome(plan.Path)+".agentman.bak"))
			}
		}
		if plan.Note != "" && !remove {
			fmt.Printf("    %s\n", dim("note: "+plan.Note))
		}
	}

	if failures > 0 {
		return fmt.Errorf("%d agent(s) could not be configured", failures)
	}
	if *dryRun {
		fmt.Printf("\n%s\n", dim("dry run — nothing was written"))
		return nil
	}
	if !remove {
		fmt.Printf("\n%s\n", dim("run `am serve` to start receiving events, then `am doctor` to verify"))
	}
	return nil
}

// shortSessionID trims a composite id to something readable in a log line.
func shortSessionID(id string) string {
	if len(id) > 20 {
		return id[:20] + "…"
	}
	return id
}

/* --------------------------------- doctor -------------------------------- */

// runDoctor checks that the pieces agentman depends on are actually present
// and working — including the assumptions about undocumented file formats,
// which is where this project is most exposed to a CLI upgrade.
func runDoctor(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ExitOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	var problems int
	check := func(ok bool, label, detail string) {
		mark := "✓"
		if !ok {
			mark = "✗"
			problems++
		}
		fmt.Printf("  %s %-22s %s\n", mark, label, dim(detail))
	}
	warn := func(label, detail string) {
		fmt.Printf("  %s %-22s %s\n", "⚠", label, dim(detail))
	}

	fmt.Println("Agents")
	registry, err := buildRegistry()
	if err != nil {
		return err
	}
	sessions, discoverErr := registry.Discover(ctx)
	if discoverErr != nil {
		warn("discovery", discoverErr.Error())
	}
	check(true, "sessions found", fmt.Sprintf("%d running", len(sessions)))

	fmt.Println("\nHooks")
	cfg, err := hook.LoadConfig("")
	if err != nil {
		return err
	}
	check(cfg.Token != "", "local config", collapseHome(filepath.Join(home, ".agentman", "config.json")))

	binary, _ := os.Executable()
	if resolved, err := filepath.EvalSymlinks(binary); err == nil {
		binary = resolved
	}
	plans, err := hook.Installer{Binary: binary}.Plans(cfg.Token, false)
	if err != nil {
		return err
	}
	for _, plan := range plans {
		switch {
		case plan.Err != nil:
			check(false, string(plan.Kind)+" hooks", plan.Err.Error())
		case plan.Changed:
			check(false, string(plan.Kind)+" hooks", "not installed — run `am install-hooks`")
		default:
			check(true, string(plan.Kind)+" hooks", "registered in "+collapseHome(plan.Path))
		}
	}

	// Registered is not the same as working. Codex's schema is unverified, so
	// this distinction is reported rather than assumed.
	store, err := hook.NewStore("")
	if err != nil {
		return err
	}
	state := store.Load()
	for _, kind := range registry.Kinds() {
		if at, ok := state.LastFired[kind]; ok && at > 0 {
			check(true, string(kind)+" hooks firing", "last fired "+humanAge(at))
		} else {
			warn(string(kind)+" hooks firing", "never observed — run `am serve`, then use "+string(kind))
		}
	}

	fmt.Println("\nDaemon")
	if err := pingDaemon(ctx); err != nil {
		warn("hook listener", "not running — start it with `am serve`")
	} else {
		check(true, "hook listener", "responding on "+hook.DefaultAddr)
	}

	fmt.Println("\nTranscript formats")
	reportFormatDrift(ctx, registry, sessions, check, warn)

	if problems > 0 {
		fmt.Printf("\n%d problem(s) found.\n", problems)
		return fmt.Errorf("doctor found %d problem(s)", problems)
	}
	fmt.Printf("\n%s\n", "All good.")
	return nil
}

func pingDaemon(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"http://"+hook.DefaultAddr+"/health", nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	return nil
}

// reportFormatDrift reads a live session through the real parser and reports
// whether anything came back.
//
// The transcript layouts are undocumented, so a CLI upgrade could quietly
// start producing records we skip. Without this check that shows up as an
// inexplicably empty feed on the phone; with it, it is a line in `am doctor`.
func reportFormatDrift(
	ctx context.Context,
	registry interface {
		Page(context.Context, string, string, int) (protocol.Page, error)
	},
	sessions []protocol.Session,
	check func(bool, string, string),
	warn func(string, string),
) {
	if len(sessions) == 0 {
		warn("parser check", "no running sessions to sample")
		return
	}
	for _, session := range sessions {
		page, err := registry.Page(ctx, session.ID, "", 25)
		if err != nil {
			check(false, string(session.Kind)+" transcript", err.Error())
			continue
		}
		if len(page.Messages) == 0 {
			warn(string(session.Kind)+" transcript",
				"parsed 0 messages from "+session.Name+" — possible format change")
			continue
		}
		check(true, string(session.Kind)+" transcript",
			fmt.Sprintf("parsed %d messages from %s", len(page.Messages), session.Name))
		// One healthy sample per agent kind is enough.
		break
	}
}
