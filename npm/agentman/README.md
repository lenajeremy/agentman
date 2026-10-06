# agentman

Watch your coding agents from your phone, and answer them when they get stuck.
Claude Code, Codex, Cursor, Kiro, Antigravity and OpenCode.

```sh
npm install -g agentman
```

This puts `am` on your path (`agentman` works too). Then:

```sh
am install-hooks   # let your agents tell agentman when they finish or need you (once)
am serve           # start agentman, and leave it running
am pair            # in a second terminal: shows a code to scan with the app
am claude          # start an agent you can message from your phone
```

`npx agentman <command>` runs a command without installing.

The setup guide: https://agentman-nu.vercel.app/start
Source, docs and the iPhone app: https://github.com/lenajeremy/agentman

agentman uses `tmux` to type into your agents. Install it with your package
manager (`brew install tmux`, `sudo apt-get install tmux`, …).
