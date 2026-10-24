# Agentman for Mac

A native macOS app (SwiftUI) for the agents `am serve` is watching: every
agent on this Mac in one window, grouped by folder, with each one's live
conversation, a box to message it, and its questions to answer in place.
Agentman, the manager that reads every agent for you, arrives in the next
phase.

The app talks to `am serve` over a Unix socket in its config directory,
`~/.agentman/am.sock`, which only you can open. It speaks the same protocol
as the phone, so nothing goes through the relay.

## Build and run

Needs Xcode 26 or later and [XcodeGen](https://github.com/yonaskolb/XcodeGen)
(`brew install xcodegen`). The Xcode project is generated from `project.yml`.

```sh
cd desktop
xcodegen
xcodebuild -project Agentman.xcodeproj -scheme Agentman -derivedDataPath build build
open build/Build/Products/Debug/Agentman.app
```

Run the tests with `xcodebuild … test` in place of `build`.

`am serve` must be running, version 0.18 or later (the first with the socket).

## Pointing it at another daemon

To use a test daemon instead of yours, give the app its socket:

```sh
am serve -relay none -config-home /tmp/am-test -addr 127.0.0.1:18797 &
open --env AGENTMAN_SOCKET=/tmp/am-test/.agentman/am.sock build/Build/Products/Debug/Agentman.app
```

The `socketPath` user default does the same.

## Links

`agentman://agent/<session id>` opens an agent, and `agentman://home` opens
Agentman.

## Assets

The agent logos and the app icon are generated from the phone app's, so the
two stay identical:

```sh
python3 desktop/scripts/agent-marks.py   # from mobile/components/agentMarks.ts
python3 desktop/scripts/app-icon.py      # from mobile/assets/images/icon.png
```

Geist is bundled under the SIL Open Font License (`Agentman/Resources/Fonts/OFL.txt`).
