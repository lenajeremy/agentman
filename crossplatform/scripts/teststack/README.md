# Test stack

Compares the cross-platform app with the Mac app on the same fake agents,
without touching the user's daemon, tmux or Agentman.

```sh
scripts/teststack/start.sh                 # am serve from this checkout, fake home, fake agents
scripts/teststack/compare.sh /tmp/cmp      # both apps, same agent, same size: mac.png, new.png, pair.png
scripts/teststack/linux.sh /tmp/linux.png  # the app on Ubuntu (OrbStack machine agentman-linux)
```

- The home is `$TMPDIR/amx/home`; tmux runs on a private socket under
  `$TMPDIR/amx/tmux` (`env -u TMUX TMUX_TMPDIR=$TMPDIR/amx/tmux tmux -L agentman …`).
  Never use the user's tmux server.
- A fake Claude agent is a registry file in `~/.claude/sessions/<pid>.json`
  with a live pid, plus its transcript in `~/.claude/projects/<folder with
  non-alphanumerics as ->/<id>.jsonl`. To have a terminal, its pid must be a
  pane's on the agents tmux server, in a session named `agentman-…`.
- The Mac app is a Debug build of `desktop/` (app id `dev.agentman.mac.debug`),
  built into `$TMPDIR/amx/macbuild` on first use. It opens a screen with
  `AGENTMAN_OPEN=agentman://…`; the new app with `AGENTMAN_OPEN=<query>`
  (`agent=<id>`, `tab=terminal`, `view=editor&session=<id>&file=<path>`).
- Screen captures need screen-recording access, so run these outside a sandbox.
