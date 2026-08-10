# agentman

Monitor and steer your local coding agents from your phone.

Start a Claude Code, Codex, or OpenCode session on your machine and it shows up
on your phone: what it's doing right now, what it has done, and a box to send it
your next instruction. When an agent finishes a task, your phone rings.

> **Status: Phase 1.** The read path works — discovery, live tailing, and
> scrollback for Claude Code and Codex, all driven from the `am` CLI. The relay,
> the mobile app, and message injection are not built yet. See [Roadmap](#roadmap).

## The relay stores nothing

The obvious design puts your transcripts in a database on a server. This one
doesn't, and that constraint shapes everything else.

Your agent transcripts already exist on your own disk. The daemon reads them
there and streams them to your phone on demand — live for the session you're
looking at, pulled page by page when you scroll back. The relay in the middle
matches two sockets and forwards bytes between them.

So the relay needs **no database at all**: device tokens are signed rather than
stored, pairing codes live in memory for sixty seconds, and push notifications
go from your machine straight to the notification service. Restart the relay and
nothing is lost but reconnections.

That is the point: **the relay holds nothing, so it doesn't matter whose relay
you use.** Run the public one, deploy your own, or skip it entirely and run
`am serve` on your LAN.

Two honest trade-offs. With no server-side cache, a closed laptop means an empty
app — mostly self-correcting, since there are no live agents when it's asleep.
And scrollback becomes a real algorithm rather than a SQL `LIMIT`; see
[`internal/jsonl`](internal/jsonl/jsonl.go).

## Try it now

```bash
go build -o bin/am ./cmd/am

./bin/am list                  # every running agent session
./bin/am watch                 # follow agents starting, working, going idle
./bin/am watch <session-id>    # follow one session's messages live
./bin/am history <session-id>  # recent messages, newest last
```

`am history` prints a cursor when there is more to read:

```bash
./bin/am history claude:abc123 -limit 20
./bin/am history claude:abc123 -limit 20 -before 319250377
```

## How it finds your agents

Each CLI is handled by one adapter in [`internal/source`](internal/source).
None of these formats are published APIs, so every assumption is isolated there
and covered by tests.

| | Discovery | Scrollback | Send a message |
|---|---|---|---|
| **Claude Code** | `~/.claude/sessions/<pid>.json`, a live registry with a busy/idle status. Verified against the pid, since the file outlives a crashed session. | `~/.claude/projects/<cwd-slug>/<id>.jsonl` | Planned: tmux wrapper, hook fallback |
| **Codex** | Rollout files under `~/.codex/sessions/YYYY/MM/DD/`, gated on a running `codex` process. The weakest part — hooks will replace the guess. | same rollout file | Planned: tmux, possibly `app-server` |
| **OpenCode** | Planned — `opencode serve` exposes a real HTTP API | `GET /session/:id/message` | `POST /session/:id/prompt_async` |

Two findings worth recording, since neither is documented:

- **Codex has the same hook system as Claude Code.** Its binary contains
  `hooks.json` and the events `session_start`, `stop`, `user_prompt_submit`,
  and the rest. One hook design covers both agents.
- **Codex writes two overlapping streams.** `response_item` is the raw model
  history; `event_msg` is the semantic stream its UI renders. They duplicate
  each other almost exactly, so we read only `event_msg` — it excludes injected
  `developer` instructions and reports commands already parsed.

## Reading transcripts without copying them

[`internal/jsonl`](internal/jsonl/jsonl.go) is the piece that replaces the
database. It leans on two properties of append-only files:

1. Byte offsets never change, so a scroll cursor stays valid while the agent is
   still writing.
2. `\n` never appears inside a multi-byte UTF-8 sequence, so lines are split on
   raw bytes and decoded only once complete — no split-character corruption at
   chunk boundaries.

Reading backwards from a 305MB transcript on this machine: **6.6ms** for the
first page, 8.2ms for five more, with bounded memory.

## Roadmap

- [x] **Phase 1** — discovery, live tailing, paged scrollback, the `am` CLI
- [ ] **Phase 2** — hooks, for exact turn-completion signals instead of polling
- [ ] **Phase 3** — the stateless relay
- [ ] **Phase 4** — message injection (tmux, API, hook fallback)
- [ ] **Phase 5** — the Expo app
- [ ] **Phase 6** — background push, end-to-end encryption, packaging

## Contributing

The interesting extension point is
[`source.Source`](internal/source/source.go): `Discover`, `Page`, `Follow`, and
optionally `Inject`. Adding Gemini CLI, Grok CLI, or Amp means one file there
and a line in the registry — nothing else changes.

```bash
go test ./...
```

## License

MIT
