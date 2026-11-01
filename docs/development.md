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

Tests never touch the tmux server you are working in. Every package whose
tests can reach tmux starts a private server for the run (see
`internal/tmux/tmuxtest`), because from inside tmux a bare `tmux` command goes
to your own server whatever `TMUX_TMPDIR` says. The live tests above drive a
pane you created, so they need your server named explicitly:
`AGENTMAN_TMUX_SOCKET=/private/tmp/tmux-$(id -u)/default` (or wherever
`tmux display -p '#{socket_path}'` says). Never use `kill-server` in a test.

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

### The CLI

Choose the next unused semantic version, then push its tag to run GoReleaser:

```bash
VERSION=v0.8.0 # example; replace with the release version
git tag "$VERSION"
git push origin "$VERSION"
```

The release workflow tests and cross-compiles `am` for macOS and Linux, creates
GitHub release archives, and updates the Homebrew tap's `agentman-cli` cask. It
requires a `HOMEBREW_TAP_TOKEN` repository secret with write access to the tap
repository.

### Agentman for Mac

The Mac app ships as a notarized DMG attached to the same GitHub release, and
updates itself with [Sparkle](https://sparkle-project.org) from the appcast at
https://agentman-nu.vercel.app/appcast.xml (`site/appcast.xml`).

1. Bump `MARKETING_VERSION` and `CURRENT_PROJECT_VERSION` in
   `desktop/project.yml` and commit. Sparkle compares the build number, so it
   must go up every release.
2. Push the CLI's tag first, so the release the DMG goes on exists.
3. Run the release script from a clean checkout, off any VPN:

   ```bash
   scripts/release-mac.sh                   # or: --notes notes.md
   ```

   It refuses to start with uncommitted changes, a version that was not
   bumped, an Agentman disk image still attached, or the network on a VPN.
   Then it archives and exports with the Developer ID certificate, checks
   Sparkle's helpers are signed with it too, builds and signs the DMG,
   notarizes and staples it, signs it for Sparkle, adds its item to
   `site/appcast.xml`, points the site's Download links at it, and writes the
   Homebrew cask for the app. The notes in `--notes` (Markdown) are what the
   update window shows; without them it links to the GitHub release.
4. Publish, in this order, as the script prints at the end:
   - upload the DMG to the release (`gh release upload`);
   - open the site PR and merge it. The site deploys from `main`, and once the
     appcast is live every copy of the app offers the update, so the DMG must
     already be up;
   - commit the cask to the tap as `Casks/agentman.rb`, once `agentman` there
     is the app's name (see Homebrew, below).

   `scripts/release-mac.sh --publish` does all three after the build. It skips
   the tap while `agentman` there is still the CLI.

What the user sees: on the second launch Sparkle asks once whether to check for
updates automatically, then checks daily. Agentman › Check for Updates… checks
now, and Settings › General › About shows the update's state beside the version,
with a switch for the daily check. Debug builds never update.

#### The signing key

Sparkle installs only updates signed with the EdDSA key whose public half is
`SUPublicEDKey` in `desktop/project.yml`. The private half is in the login
Keychain of the Mac that releases, as a "https://sparkle-project.org" item for
the account `agentman`, made once with Sparkle's `generate_keys --account
agentman` (the tool is in the release's DerivedData,
`SourcePackages/artifacts/sparkle/Sparkle/bin`). The first release asks the
Keychain to let `sign_update` use it; Always Allow stops it asking.

Keep an offline backup (`generate_keys --account agentman -x <file>`, then put
the file somewhere safe and delete it). If the key is lost, installed copies
refuse every update signed with a new one, and everyone has to download the
next version by hand.

To try the script without the real key, set `SPARKLE_KEY_FILE` to a throwaway
key (32 random bytes, base64). Its appcast item cannot install, so undo the
`site/` changes afterwards; `--publish` refuses it.

### Homebrew

The tap, [lenajeremy/homebrew-agentman](https://github.com/lenajeremy/homebrew-agentman),
has two casks:

| Name | Installs | Written by |
| --- | --- | --- |
| `agentman` | Agentman for Mac, and later `am` from inside it | `scripts/release-mac.sh` |
| `agentman-cli` | `am` alone, on macOS or Linux | GoReleaser |

`agentman-cli` is a cask rather than a formula: Homebrew installs casks on
Linux too, and a cask keeps `am` under the Caskroom, which is how `am upgrade`
recognises a Homebrew install. The app's cask sets `auto_updates`, so
`brew upgrade` leaves the app to Sparkle.

Until this split, `agentman` was the CLI. Moving those installs to
`agentman-cli` happens in two steps, so nobody's CLI turns into the app:

1. **Rename** (once, in the tap, before the release that ships
   `agentman-cli`): rename `Casks/agentman.rb` to `Casks/agentman-cli.rb` with
   the token changed, and add `cask_renames.json`:

   ```json
   {
     "agentman": "agentman-cli"
   }
   ```

   The next `brew update` on each machine (including the automatic one before
   `brew upgrade`) moves an `agentman` install to `agentman-cli`, and
   `brew install lenajeremy/agentman/agentman` keeps working, installing
   `agentman-cli` with a note that it was renamed. Merge the site's switch to
   `agentman-cli` only after this.
2. **Reuse the name** (later, once the rename has had a few weeks, and ideally
   once the app carries `am`): delete the `agentman` entry from
   `cask_renames.json` (and the file, if empty) in the same commit that adds
   the app's `Casks/agentman.rb`. From then on `release-mac.sh --publish`
   updates it. Drop `agentman` from `tap_migrations.json` too: it moved the
   old formula to the cask, and would now send any formula install still
   around to the app.

   Try it first on a Mac that went through step 1, from a scratch copy of the
   tap: Homebrew leaves `Caskroom/agentman` there as a link to
   `Caskroom/agentman-cli`, and its installer does not check for that link
   before installing a cask under the name.

   Anyone who skipped every `brew update` in between still has the CLI under
   `agentman`. A plain `brew upgrade` then skips it (the app's cask
   auto-updates), so their `am` quietly stops updating, and an old
   `am upgrade`, which names `agentman`, would replace it with the app.
   `brew migrate agentman` before step 2 is the fix for anyone known to be
   behind.
