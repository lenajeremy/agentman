# Mobile setup

The mobile app is an Expo SDK 57 project in [`mobile`](../mobile). CI runs it
with Node.js 24.

## Development with Expo Go

```bash
cd mobile
npm ci --legacy-peer-deps
npm start
```

Open the project in Expo Go, then pair it with `am pair`. Metro delivers
JavaScript changes without rebuilding the native app.

Expo Go is suitable for session monitoring and local notifications while the
app is running. It is not a substitute for a signed Agentman build when you
need remote push after the operating system suspends the app.

## Local iOS device build

Connect an iPhone, then run:

```bash
APPLE_TEAM_ID=YOUR_TEAM_ID npm run device
```

By default, the build plugin removes the Apple push entitlement so a free Apple
development team can sign the app. That build can still schedule local alerts
while its process and WebSocket are alive.

To preserve the push entitlement, use a paid team and set `APPLE_PUSH=1`:

```bash
APPLE_TEAM_ID=YOUR_TEAM_ID APPLE_PUSH=1 npm run device
```

## EAS builds

The repository defines `development`, `preview`, and `production` profiles in
[`mobile/eas.json`](../mobile/eas.json). All three preserve the push
entitlement.

```bash
cd mobile
npx eas-cli@latest build --platform ios --profile production
npx eas-cli@latest submit --platform ios --profile production
```

The production profile creates a store build. The preview profile creates an
internal-distribution build that can be installed from its EAS link.

The application identifiers and EAS project ownership in `app.json` are
project-specific. Forks should replace them before creating builds or store
records.

## Notification behavior

Agentman has two notification paths:

| Path | When it works | Data path |
|---|---|---|
| Local notification | App process is running with a live connection and remote push is not considered active | Generated on the phone from live state |
| Remote push | A signed build obtains an Expo token and registers it with the daemon | Daemon to Expo to the platform push service |

The app's push-token registration passes through the Agentman relay, but
notification delivery and its payload do not. By default, the payload contains
a session name and a reason, not transcript content. Set `push.includePreview`
in `~/.agentman/config.json` to opt into a short excerpt.

Push-token registration currently has no separate daemon acknowledgement. If
registration is interrupted, reconnect or reopen the app while `am serve` is
running. The daemon keeps up to 32 tokens. At startup it prunes registrations
that have not checked in for 60 days, and it also removes tokens that Expo
reports as unregistered.

## Android

Android remote push is not configured in this repository. It requires a
Firebase project, a matching `google-services.json` referenced by
`expo.android.googleServicesFile`, and the corresponding credential in Expo.
Without that setup, the app still runs and can show local notifications while
its process is active.

## Useful commands

```bash
npm start
npm run ios
npm run android
npm run typecheck
npm test
npm run device
```

## Troubleshooting

- Run `am doctor` on the computer to inspect hooks, daemon health, and agent discovery.
- Keep `am serve` running and verify that it reports a relay connection.
- Re-run `am pair` after deleting or reinstalling the app.
- Delete `~/.agentman/push.json`, then reconnect the app, after changing build or push environments.
- Confirm notification permissions in the phone's system settings.
