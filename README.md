# agentman

Monitor and steer your local coding agents from your phone.

Start a Claude Code, Codex, or OpenCode session on your machine and it shows up
on your phone: what it's doing right now, what it has done, and a box to send it
your next instruction. When an agent finishes a task, your phone rings.

> **Status: Phase 5.** The daemon, hooks, relay, and message injection all
> work, and so does the app: a paired phone lists live sessions, reads their
> history, sends instructions, and answers the approval prompts an agent is
> blocked on. All three agents are supported, though Codex is the least
> verified. See [Roadmap](#roadmap).

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

To get exact "it's done" signals instead of polling, register the hooks:

```bash
./bin/am install-hooks -dry-run   # see the change first
./bin/am install-hooks
./bin/am serve                    # the daemon
./bin/am doctor                   # confirm it is all wired up
```

`install-hooks` edits `~/.claude/settings.json`, so it backs the file up, writes
atomically, preserves every key it does not own, and refuses outright if the
file will not parse. `uninstall-hooks` removes only its own entries.

To reach it from your phone, run a relay and pair:

```bash
AGENTMAN_RELAY_SECRET=<long-random-string> ./bin/relay   # anywhere reachable
./bin/am serve -relay https://your-relay.example.com
./bin/am pair                                            # prints a 6-digit code
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
| **Claude Code** | `~/.claude/sessions/<pid>.json`, a live registry with a busy/idle status. Verified against the pid, since the file outlives a crashed session. | `~/.claude/projects/<cwd-slug>/<id>.jsonl` | tmux (mid-turn) or the hook queue |
| **Codex** | Rollout files under `~/.codex/sessions/YYYY/MM/DD/`, gated on a running `codex` process. The weakest part — hooks will replace the guess. | same rollout file | tmux (mid-turn) or the hook queue |
| **OpenCode** | `opencode serve`'s HTTP API, which reports exactly which sessions are running | `GET /api/session/:id/message`, cursor-paged | `POST .../prompt` with `delivery: steer` — native, mid-turn |

OpenCode is the one agent that needs no tricks — a real API covers sessions,
messages, mid-turn delivery, and questions as structured data. It is the shape
`source.Source` was designed around, and the best reference for adding a fourth
agent. Worth knowing if you build against it: its OpenAPI spec and its actual
responses disagree in three places (the session list is wrapped in
`{data, cursor}`, the working directory lives under `location`, and messages
are flat rather than `{info, parts}`), so check the wire, not the schema.

Two more findings worth recording, since neither is documented:

- **Codex has the same hook system as Claude Code.** Its binary contains
  `hooks.json` and the events `session_start`, `stop`, `user_prompt_submit`,
  and the rest. One hook design covers both agents.
- **Codex writes two overlapping streams.** `response_item` is the raw model
  history; `event_msg` is the semantic stream its UI renders. They duplicate
  each other almost exactly, so we read only `event_msg` — it excludes injected
  `developer` instructions and reports commands already parsed.

## Sending a message to a running agent

Agent CLIs are interactive terminal programs with no input API, so the last hop
is the hard part. Every session shows which of three paths it has, because they
are not equally good:

| Mode | How | Quality |
|---|---|---|
| `tmux` | Started with `am claude` / `am codex`, so the daemon can type into it | Works **mid-turn**, exactly like typing |
| `hook` | Queued, handed over at the session's next `Stop` hook | Between turns only, and the CLI can discard it |
| `none` | No route — the composer is disabled | — |

```bash
am claude                        # start a session you can message later
am send claude:abc123 "run the tests and tell me what fails"
```

Two details that matter more than they look:

- **The prompt box is cleared first.** Typing on top of a half-written draft
  fuses them into one garbled prompt — a real bug this hit in testing, where a
  draft `this is false` turned an injected message into `this is falserun the
  tests`. `Ctrl-U` discards the draft into the kill ring, so it is still
  recoverable with `Ctrl-Y`, unlike a nonsense prompt already sent to the agent.
- **Multi-line messages use bracketed paste.** Raw newlines would submit at the
  first line break and scatter the rest across follow-up turns.

## Answering the prompts that block an agent

When an agent asks "can I run this command?", it stops until someone answers.
That is the state worth a notification, and the one the app makes actionable:
the question, the exact command under review, and the choices all appear on the
phone, and tapping one answers the real agent.

Finding these is harder than it sounds, and the reason is worth recording:

- **No hook fires for a permission prompt.** Claude Code's `Notification` hook
  does not run for one, verified by waiting on a live prompt with the daemon
  attached.
- **The session registry still says `idle`.** From the outside, an agent blocked
  on a decision is indistinguishable from one that has finished.

So detection reads the terminal itself — `tmux capture-pane`, parsed by
[`internal/question`](internal/question/question.go). That carries a real cost:
only sessions started with `am claude` / `am codex` can be seen or answered.
It also means the parser must be conservative, since a false positive would
show a phantom question: a lone numbered line is treated as prose, and a menu
with anything after it is treated as already answered.

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
- [x] **Phase 2** — hooks, for exact turn-completion signals instead of polling
- [x] **Phase 3** — the stateless relay
- [x] **Phase 4** — message injection (tmux, hook fallback)
- [x] **Phase 5** — the Expo app, including approval prompts
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
