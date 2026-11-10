#!/usr/bin/env python3
"""Writes a long, synthetic Claude Code transcript for the test daemon.

Usage: transcript.py <out.jsonl> <session id> <cwd> <turns>
"""
import json
import sys
import uuid
from datetime import datetime, timedelta, timezone

out, session_id, cwd, turns = sys.argv[1], sys.argv[2], sys.argv[3], int(sys.argv[4])
t = datetime(2026, 10, 31, 9, 0, 0, tzinfo=timezone.utc)
lines = []
parent = None


def stamp():
    global t
    t += timedelta(seconds=7)
    return t.strftime("%Y-%m-%dT%H:%M:%S.000Z")


def add(kind, message):
    global parent
    u = str(uuid.uuid4())
    lines.append({
        "type": kind, "uuid": u, "parentUuid": parent, "sessionId": session_id,
        "cwd": cwd, "isSidechain": False, "timestamp": stamp(), "message": message,
    })
    parent = u


prompts = [
    "Add a retry with backoff to the uploader, and make sure the tests cover the timeout path.",
    "Why does the sidebar jump when an agent finishes? Find the cause before changing anything.",
    "Write a migration that moves the settings file to the new location, keeping the old one as a fallback.",
    "Summarise what changed in the last three commits in plain words.",
    "The terminal flickers on resize. Can you reproduce it and fix it?",
]

bodies = [
    """I found the cause. The list re-sorts on every `session_update`, and a finished agent moves to the top of its folder.

### What happens

1. The agent's state flips from **busy** to **idle**.
2. `lastActivityAt` changes, so the sort key changes.
3. The row moves, and everything under it shifts by one row.

### The fix

Sort by the time each agent *started*, and only re-sort when an agent arrives or leaves:

```ts
const order = useMemo(
  () => [...sessions].sort((a, b) => a.startedAt - b.startedAt),
  [sessions.length],
);
```

| Before | After |
|---|---|
| Rows move when a turn ends | Rows stay put |
| 3 re-sorts per second while busy | 0 |

I also added a test that sends two `session_update` events and checks the order is unchanged.""",
    """Done. The uploader now retries up to **5 times** with exponential backoff, capped at 8 seconds.

- First retry after 250 ms, then 500 ms, 1 s, 2 s, 4 s
- A `408` or a `5xx` retries; a `4xx` does not
- Each attempt is logged with its number, so a slow network shows up in the log

```go
for attempt := 0; attempt < maxAttempts; attempt++ {
	err := upload(ctx, part)
	if err == nil || !retryable(err) {
		return err
	}
	select {
	case <-time.After(backoff(attempt)):
	case <-ctx.Done():
		return ctx.Err()
	}
}
```

> The timeout test uses a fake clock, so it runs in a few milliseconds instead of 30 seconds.

All 214 tests pass.""",
    """Here's the summary, newest first:

- **Mac: a file named with @ shows as its icon and name.** Paths you tag in a message now render as a chip with the file's icon.
- **Mac: right-click a picture to copy it.** Works in the viewer, the editor and conversations.
- **Mac: less room around the text in the message box.** The padding went from 18 to 13 points.

Nothing in these commits touches the daemon, so `am` doesn't need a new release.""",
]

tools = [
    ("Bash", {"command": "go test ./internal/... -count=1", "description": "Run the Go tests"}, "ok  github.com/example/uploader\t2.114s\nok  github.com/example/settings\t0.481s"),
    ("Read", {"file_path": cwd + "/term.go"}, "package term\n\n// Package term runs a program on a pseudo-terminal..."),
    ("Grep", {"pattern": "lastActivityAt", "path": cwd}, "src/Sidebar.tsx:41\nsrc/store.ts:88"),
    ("Edit", {"file_path": cwd + "/daemon.ts", "old_string": "a", "new_string": "b"}, "The file has been updated."),
]

for turn in range(turns):
    add("user", {"role": "user", "content": prompts[turn % len(prompts)]})
    for k in range(2 + turn % 3):
        name, inp, result = tools[(turn + k) % len(tools)]
        tid = "toolu_" + uuid.uuid4().hex[:20]
        add("assistant", {"role": "assistant", "model": "claude-opus-5-5",
                          "content": [{"type": "tool_use", "id": tid, "name": name, "input": inp}]})
        add("user", {"role": "user", "content": [{"type": "tool_result", "tool_use_id": tid, "content": result}]})
    add("assistant", {"role": "assistant", "model": "claude-opus-5-5",
                      "content": [{"type": "text", "text": bodies[turn % len(bodies)]}]})

with open(out, "w") as f:
    for line in lines:
        f.write(json.dumps(line) + "\n")
print(f"{len(lines)} lines")
