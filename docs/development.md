# Development

## Repository layout

| Path | Purpose |
|---|---|
| `cmd/am` | CLI, wrappers, daemon startup, pairing, hooks, and diagnostics |
| `cmd/relay` | Relay executable |
| `internal/daemon` | Session polling, subscriptions, requests, and event delivery |
| `internal/source` | Claude Code, Codex, and OpenCode adapters |
| `internal/protocol` | Go wire and domain types |
| `internal/relay` | Pairing, authentication, WebSockets, and routing |
| `internal/tmux` | Managed terminal launch, capture, input, and interruption |
| `internal/question` | Conservative terminal question parsing |
| `internal/hook` | Local hook server, installation, config, and state |
| `internal/push` | Expo push-token storage and delivery |
| `internal/speech` | Speech preparation, generation, and playback |
| `mobile` | Expo app and the TypeScript copy of the wire protocol |

## Prerequisites

- Go 1.26.6
- Node.js 24 and npm for the mobile app
- `tmux` for terminal integration and related tests
- Agent CLIs for manual integration testing
- Xcode and CocoaPods for local iOS builds

## Verification

Run the same checks used by CI:

```bash
test -z "$(gofmt -l ./cmd ./internal)"
go vet ./...
go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./...
go mod tidy
git diff --exit-code -- go.mod go.sum
go test -race ./...
go build ./cmd/am ./cmd/relay

cd mobile
npm ci --legacy-peer-deps
node scripts/check-npm-audit.mjs
npx expo install --check
npx tsc --noEmit
npm test
```

Terminal parser fixtures live in `internal/question/testdata`. Optional live
tests are guarded by these environment variables and documented beside the
tests that consume them:

- `AGENTMAN_LIVE_CLAUDE_FORM`
- `AGENTMAN_LIVE_CLAUDE_SINGLE_FORM`
- `AGENTMAN_CLAUDE_PANE_CORPUS`
- `AGENTMAN_CLAUDE_MULTI_PANE_CORPUS`
- `AGENTMAN_CLAUDE_PREVIEW_PANE_CORPUS`
- `AGENTMAN_LIVE_OPENCODE_QUESTIONS`

## Adding an agent adapter

The primary Go extension point is `source.Source`, but a complete adapter is a
cross-layer change:

1. Add the protocol kind in `internal/protocol`.
2. Implement discovery, paging, and live following in `internal/source`.
3. Implement optional message, answer, question-inspection, and interrupt
   interfaces only where the agent can support them safely.
4. Register the adapter in `cmd/am`.
5. Add the kind and relevant decoding/presentation behavior to the mobile app.
6. Add fixtures and tests for discovery, history, state, and control behavior.
7. Update the capability table and agent-specific documentation.

Keep agent-specific formats and heuristics inside the adapter. The daemon,
relay, and app should continue to communicate through normalized protocol
types.

## Protocol changes

The Go and TypeScript protocol definitions are maintained separately. A type
check does not prove wire compatibility. When changing the protocol:

1. Update Go types and validation in `internal/protocol`.
2. Update TypeScript types and runtime decoders in `mobile/lib/protocol.ts`.
3. Add representative encode/decode tests on both sides.
4. Increase the wire protocol version for an incompatible change.
5. Test version rejection and upgrade the daemon, relay, and app together.

## Release

Choose the next unused semantic version, then push its tag to run GoReleaser:

```bash
VERSION=v0.8.0 # example; replace with the release version
git tag "$VERSION"
git push origin "$VERSION"
```

The release workflow tests and cross-compiles `am` for macOS and Linux, creates
GitHub release archives, and updates the Homebrew tap. It requires a
`HOMEBREW_TAP_TOKEN` repository secret with write access to the tap repository.
