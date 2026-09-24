# Configuration

Agentman creates its local configuration on first use at
`~/.agentman/config.json`. The file is written with mode `0600`; its parent
directory uses mode `0700`. Edit the generated file in place and preserve its
`token`; do not replace it with a copied example.

## Local configuration

| Field | Default | Description |
|---|---|---|
| `token` | generated | Daemon identity and local hook secret. Do not share it. |
| `hookAddr` | `127.0.0.1:8787` | Numeric loopback address used by agent hooks. |
| `speech.enabled` | `false` | Read completed turns aloud on the daemon machine. |
| `speech.token` | none | Bearer token accepted by the configured speech endpoint. |
| `speech.endpoint` | Agentman's Celery endpoint | HTTP endpoint that accepts speech requests. |
| `speech.voice` | `nova` | Voice sent to the speech service. |
| `speech.instructions` | calm status-update delivery | Delivery guidance sent to the speech service. |
| `speech.speed` | `1.0` | Speech-rate multiplier. Non-positive values use `1.0`. |
| `push.includePreview` | `false` | Include up to 140 runes of agent output in remote push payloads. |

Do not delete `config.json` as a routine reset. Recreating it generates a new
daemon identity, so existing paired device credentials will no longer address
that daemon and the devices must be paired again.

## Speech

Speech is enabled only when both `speech.enabled` is `true` and `speech.token`
is non-empty. Add or update this section inside the existing generated file:

```json
{
  "speech": {
    "enabled": true,
    "token": "<token>",
    "voice": "nova",
    "speed": 1.15
  }
}
```

Agentman removes code, URLs, file paths, tables, and other screen-only syntax,
then sends at most 600 runes. Generated MP3 files are temporary. Playback
requires one of `afplay`, `mpv`, `ffplay`, `paplay`, or `aplay` on `PATH`.

Speech runs only for completion events received through installed agent hooks;
poll-detected and OpenCode completions are not spoken. The prepared text and
bearer token are sent to `speech.endpoint`, which is a separate trust boundary.
Use HTTPS for any remote endpoint.

Restart `am serve` after changing `config.json`; the daemon reads configuration
only during startup.

## Client environment variables

| Variable | Description |
|---|---|
| `AGENTMAN_RELAY` | Default relay URL for `am serve` and `am pair`. An explicit `-relay` flag wins. |
| `AGENTMAN_OPENCODE_URL` | Pin OpenCode discovery to one HTTP(S) server. Remote URLs must use HTTPS. |
| `OPENCODE_SERVER_USERNAME` | Basic-auth username for the pinned OpenCode server. Defaults to `opencode`. |
| `OPENCODE_SERVER_PASSWORD` | Basic-auth password. Requires `AGENTMAN_OPENCODE_URL`. |

When OpenCode authentication is enabled, Agentman requires an explicit URL so
it never sends the credential while scanning local ports.

## Relay environment variables

These variables configure the separately deployed relay binary:

| Variable | Required | Description |
|---|---|---|
| `AGENTMAN_RELAY_SECRET` | Yes | Stable signing secret of at least 16 characters. Rotation invalidates all device credentials. |
| `PORT` | No | HTTP listen port. Defaults to `8080`. |
| `AGENTMAN_TRUST_PROXY` | No | Trust `X-Forwarded-For`. Accepted true values are `1`, `true`, `yes`, and `on`. Enable only behind a proxy that overwrites the header. |

## Mobile build environment variables

| Variable | Description |
|---|---|
| `APPLE_TEAM_ID` | Apple development team used by the local iOS build plugin. |
| `APPLE_PUSH=1` | Preserve the iOS push entitlement. Other values remove it for free-team signing. |

## Generated state

| Path | Purpose |
|---|---|
| `~/.agentman/config.json` | Daemon identity, hook address, speech, and push preferences. |
| `~/.agentman/state.json` | Last-seen hook timestamps used by diagnostics. |
| `~/.agentman/push.json` | Registered Expo push tokens. At most 32 are retained; tokens older than 60 days are pruned when the daemon starts. |
| `~/.claude/settings.json.agentman.bak` | Backup made before Agentman changes Claude settings. |
| `~/.codex/config.toml.agentman.bak` | Backup made before Agentman changes Codex settings. |

Deleting `push.json` removes all stored push registrations. Reopen and reconnect
the mobile app to register its current token again.
