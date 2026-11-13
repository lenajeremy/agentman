# Agentman, cross-platform

**Parked.** Agentman is a web app first (`web/`, served by `am serve` and
opened with `am web`). This Wails shell will wrap that same app later; to
build it now, copy `internal/webapp/dist` (what `npm run build` in `web/`
makes) into `frontend/dist` first.

Every agent `am serve` watches, in one window, on macOS, Linux and Windows.
A [Wails v3](https://v3.wails.io) shell around React screens (`frontend/`).
The Mac app in `desktop/` (SwiftUI) keeps shipping while this one catches up.

The screens talk to `am serve` themselves, over the loopback WebSocket it
offers apps (`/app`, internal/daemon/appws.go); terminals are ptys `am serve`
owns (`/app/terminal`, internal/term), drawn with xterm.js. The shell finds
the daemon by reading `~/.agentman/app.json`, which `am serve` writes 0600 at
every start with the URL and a fresh token.

Shared, not copied:

- `mobile/lib/protocol.ts` and `mobile/lib/markdown.ts`, the phone app's
  protocol and markdown parser (imported as `@shared/…`);
- the Mac app's editor page, `desktop/editor-web` (Monaco, the
  language-server client, Vim, git bars), copied into `frontend/public/editor`
  by `frontend/scripts/editor.mjs` with `host-shim.js` standing in for Swift.

## Build and run

Needs Go 1.26, Node 24 and `wails3` (`go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0`);
on Linux also GTK 4 and WebKitGTK 6 (`libgtk-4-dev libwebkitgtk-6.0-dev`,
Ubuntu 24.04 or later). `wails3 doctor` checks.

```sh
cd crossplatform
wails3 build          # bin/agentman
./bin/agentman
```

`am serve` must be running, from this branch or later (the first with `/app`).

### Against a test daemon

```sh
am serve -relay none -config-home /tmp/am-test -addr 127.0.0.1:18797 &
AGENTMAN_HOME=/tmp/am-test ./bin/agentman
```

`AGENTMAN_OPEN` opens the window on a screen without clicking, as a query:
`tab=terminal`, `tab=editor&file=README.md`, `renderer=dom` (xterm without WebGL).

### The screens in a browser

```sh
cd frontend
AGENTMAN_HOME=/tmp/am-test npm run dev    # http://127.0.0.1:9245
```

The Vite server hands the page the daemon's endpoint in place of the shell.
