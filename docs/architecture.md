# Architecture

Agentman has three runtime components: a daemon on the developer machine, a
relay reachable from the internet, and a mobile app. The daemon owns all agent
integration logic. The relay only authenticates peers and forwards live frames.

## System overview

```mermaid
flowchart LR
    subgraph host["Developer machine"]
        direction TB
        Claude["Claude Code"]
        Codex["Codex"]
        OpenCode["OpenCode"]
        Files["Local transcripts"]
        Adapters["Source adapters"]
        Daemon["Agentman daemon"]

        Claude --> Adapters
        Codex --> Adapters
        OpenCode --> Adapters
        Files --> Adapters
        Adapters --> Daemon
    end

    Relay["WebSocket relay"]
    App["Expo mobile app"]

    Daemon <-->|"WSS"| Relay
    Relay <-->|"WSS"| App
```

The relay has no persistent transcript or account database. It does retain
volatile connection, pairing, last-seen, and bounded queue state in memory.
Restarting it discards that state.

## Components

| Component | Location | Responsibility |
|---|---|---|
| CLI and daemon | [`cmd/am`](../cmd/am), [`internal/daemon`](../internal/daemon) | Discovery, history, subscriptions, hooks, pairing, and command handling |
| Source adapters | [`internal/source`](../internal/source) | Convert agent files, terminal state, and APIs into the shared protocol |
| Terminal integration | [`internal/tmux`](../internal/tmux), [`internal/question`](../internal/question) | Launch managed sessions, inspect prompts, and send safe terminal input |
| Relay | [`cmd/relay`](../cmd/relay), [`internal/relay`](../internal/relay) | Authenticate peers and route frames without durable message storage |
| Protocol | [`internal/protocol`](../internal/protocol) | Session, message, question, request, event, and control contracts |
| Push | [`internal/push`](../internal/push) | Post background notifications directly from the daemon to Expo |
| Speech | [`internal/speech`](../internal/speech) | Prepare hook-reported completed turns for speech and play generated audio |
| Mobile app | [`mobile`](../mobile) | Pairing, session list, transcript UI, forms, controls, and notifications |

## Session flow

1. Each adapter discovers sessions from local files, processes, tmux, or an API.
2. The daemon publishes normalized session summaries to connected app devices.
3. An app subscribes when it opens a session. Subscriptions are reference-counted
   across devices, so only subscribed sessions are followed live.
4. Older history is requested from the daemon in pages.
5. Messages, answers, and interrupts return through the relay to the adapter
   that owns the session.

The app retains already-fetched messages in an in-memory cache, capped at 1,000
messages per session. It does not maintain a durable phone-side transcript
cache, so that cache disappears when the app process restarts.

## Question flow

```mermaid
sequenceDiagram
    participant Agent as Agent CLI
    participant Adapter as Source adapter
    participant Daemon as Daemon
    participant Relay as Relay
    participant App as Mobile app

    Agent->>Adapter: Terminal prompt or API question
    Adapter->>Daemon: Normalized question
    Daemon->>Relay: Session update
    Relay->>App: Session update
    App-->>App: Render an answer form
    App->>Relay: Answer request
    Relay->>Daemon: Answer request
    Daemon->>Adapter: Select, type, or call API
    Adapter->>Agent: Submit answer
```

Terminal parsing is intentionally conservative. Agentman re-reads a prompt
immediately before applying an answer and rejects stale or ambiguous forms
instead of risking input into the wrong terminal state.

## Optional adapter interfaces

An adapter implements `Source`, and `Injector` when it can deliver messages.
Everything else is optional. The registry finds the adapter from the kind
prefix of a session id, so an adapter adds a capability by implementing one
of these interfaces in its own files. These four are in
[`internal/source/registry.go`](../internal/source/registry.go):

| Interface | Used for | When it is not implemented |
|---|---|---|
| `ArtifactSource` | `list_artifacts`, `read_artifact` and `review_artifact` | The session has no artifacts, and reading or reviewing one fails. |
| `AttachmentPlacer` | Gives the agent a different path for each image the phone sent, such as a copy where the agent reads images. | The agent gets the saved path. A placement error, or a result that is not one absolute path on one line, also falls back to the saved path. |
| `AttachmentInjector` | Delivers the text and images as one structured message, for example ACP image blocks. The daemon prefers it to `AttachmentPlacer`. | The image paths are typed before the text. A session with no structured channel returns `source.ErrAttachmentsAsPaths` and gets the same treatment. |
| `ResumeNamer` | Names the tmux pane that a resume opens, and the session id that discovery will give it. | The daemon names the resume itself. The daemon also ignores an answer whose pane does not start with `agentman-`, contains characters other than letters, digits, `-` and `_`, or whose id belongs to another agent. |

Artifacts are documents that an agent writes for the user outside the
conversation: plans, task lists, walkthroughs and screenshots.

- **Names:** an artifact name is a plain file name. The daemon refuses a name
  that contains `/`, `\`, `..` or a control character before any adapter sees
  it. `OpenArtifact` must still confine the name to the adapter's own store.
  Use `Lstat`, open only regular files, and do not follow subdirectories.
- **Reading:** `read_artifact` uses the same reader as a workspace file. It
  returns a `workspace` event of kind `artifact`. Text has the same size limit
  as a workspace file, and an image becomes a downscaled preview. Binary files
  are refused.
- **Reviewing:** `review_artifact` delivers a message, so the daemon handles it
  like a send. It takes the session's action lock, waits its turn in the
  mutation queue, and is refused while a question is pending. The reply is a
  `send_result` event.

An adapter can also set three groups of read-only fields on `Session`:
`mode`, `contextPercent` (whole percent), and `artifacts` with
`artifactsToReview`. The daemon clamps these values to the ranges that the app
accepts. To drive a menu or picker with keys, use `tmux.SendKeys`. It accepts
only `Tab`, `BTab`, `Enter`, `Escape`, `Space` and the four arrow keys, and
only in an Agentman pane.

## Notifications

Local notifications are scheduled by the app while it is running and connected.
For a signed build with push enabled, the app registers an Expo token with the
daemon. The daemon then posts completion and blocked-question alerts directly
to Expo, without routing those notification payloads through the relay.

Push notifications contain a session name and reason by default. Transcript
preview text is opt-in. See [Mobile setup](mobile.md) and
[Configuration](configuration.md).

The app sends its Expo push token to the daemon through the relay during
registration. Notification delivery then bypasses the relay.

## Design decisions

| Decision | Consequence |
|---|---|
| The daemon is the source of truth. | Agent-specific files and APIs stay on the developer machine, while requested live content still traverses the relay. |
| The relay has no durable user database. | A relay restart loses connections and pending pairings, not local transcripts. |
| Adapters normalize agent state. | The relay and app use one protocol, but adding an agent still requires protocol registration and mobile presentation support. |
| Only subscribed sessions are followed. | Idle sessions do not continuously stream transcript data. Multiple devices may subscribe to different sessions. |
| Terminal control fails closed. | If Agentman cannot prove that a prompt is current and safe, it refuses to send input. |
| Push bypasses the relay. | A suspended phone can receive alerts, but Expo and the platform push provider become part of the notification trust boundary. |
| Transport uses TLS without end-to-end protection. | A relay operator can inspect or modify live frames. Self-host when that trust is unacceptable. |
