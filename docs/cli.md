# CLI reference

The `am` binary is both the local command-line interface and the long-running
daemon used by the mobile app.

## Commands

| Command | Description |
|---|---|
| `am list` | List currently discovered sessions. |
| `am history <session-id>` | Print a page of recent messages. |
| `am watch [session-id]` | Follow all sessions, or one session in detail. |
| `am send <session-id> <text>` | Attempt to send text to a session. |
| `am serve` | Run discovery, the hook receiver, and the optional relay client. |
| `am pair` | Request and display a mobile pairing code. |
| `am claude [args...]` | Start Claude Code in managed tmux. |
| `am codex [args...]` | Start Codex in managed tmux. |
| `am opencode [args...]` | Start OpenCode with a discoverable local HTTP API. |
| `am install-hooks` | Install Agentman's Claude Code and Codex completion hooks. |
| `am uninstall-hooks` | Remove only Agentman's hook entries. |
| `am doctor` | Check hooks, daemon health, agent discovery, and transcript parsing. |
| `am version` | Print the installed version. |

`am help`, `am --help`, and `am -h` print command help. `am --version` and
`am -v` are aliases for `am version`.

## Session inspection

### `am list`

```bash
am list
am list -json
```

Discovery can partially succeed. When one adapter fails, Agentman prints or
returns the sessions found by other adapters and reports the discovery error.

### `am history`

```bash
am history claude:abc123
am history claude:abc123 -limit 20
am history claude:abc123 -limit 20 -before 319250377
am history claude:abc123 -json
```

| Flag | Default | Description |
|---|---:|---|
| `-limit <n>` | `30` | Messages per page. Accepted range: 1 to 100. |
| `-before <cursor>` | none | Opaque cursor returned by an earlier page. |
| `-json` | off | Emit the protocol page as JSON. |

Flags may appear before or after the session ID.

### `am watch`

```bash
am watch
am watch opencode:abc123
```

Without an ID, the command reports changes across discovered sessions. With an
ID, it follows that session's messages until interrupted.

## Sending messages

```bash
am send claude:abc123 "Run the tests"
```

Delivery depends on the adapter:

| Session | Delivery behavior |
|---|---|
| Claude Code started with `am claude` | Sent immediately through tmux. |
| Claude Code started directly | The standalone `am send` command cannot preserve its process-local queue. With Agentman's `Stop` hook installed and firing, `am serve` can hold a best-effort queue for messages sent from the app. Otherwise, restart with `am claude`. |
| Codex started with `am codex` | Sent immediately through tmux. |
| Codex started directly | Not deliverable. Restart with `am codex`. |
| OpenCode | Sent through `prompt_async` on the native API. |

For terminal sessions, Agentman refuses normal messages while a detected
question is on screen. Answer the question first.

## Daemon and pairing

### `am serve`

```bash
am serve
am serve -relay https://relay.example.com
am serve -relay none
am serve -addr 127.0.0.1:8788
```

| Flag | Description |
|---|---|
| `-relay <url>` | Select a relay. HTTPS/WSS is required unless the relay is on loopback. Use `none` to disable phone connectivity. |
| `-addr <host:port>` | Select the local hook receiver and persist it for installed hooks. Only numeric loopback addresses are accepted. |

Relay selection uses this precedence:

1. `-relay <url>`
2. `AGENTMAN_RELAY`
3. `https://agentman-production.up.railway.app`

The values `none`, `off`, and `-` disable the relay.

### `am pair`

```bash
am pair
am pair -relay https://relay.example.com
```

`am pair` uses the same relay selection rules as `am serve`. The daemon does
not have to be connected to mint a code, but the app will show the computer as
offline until `am serve` connects with the same local identity.

## Agent wrappers

`am claude` and `am codex` launch the underlying CLI inside a managed tmux
session, then attach your terminal. The working directory and all trailing
arguments are preserved.

`am opencode` does not use tmux. It starts OpenCode on loopback and chooses a
free port from 4096 through 4111. A user-supplied `--port` is preserved; ports
outside that range require `AGENTMAN_OPENCODE_URL` for discovery.

## Hooks

```bash
am install-hooks -dry-run
am install-hooks
am uninstall-hooks -dry-run
am uninstall-hooks
```

Hook installation updates `~/.claude/settings.json` and
`~/.codex/config.toml`. Existing files are backed up as `.agentman.bak` before
they are changed. Writes are private and atomic.

Agentman preserves unrelated Claude hooks. If Codex already has a top-level
`notify` command, installation refuses to replace it; Codex completion
integration remains uninstalled until that conflict is resolved.

## Diagnostics

Run `am doctor` after installing hooks or when sessions do not appear. It checks
the local hook configuration, daemon health, OpenCode reachability, and whether
currently discovered transcripts can be parsed. It does not perform a formal
Go-to-TypeScript protocol compatibility check.
