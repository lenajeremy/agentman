#!/usr/bin/env python3
"""A transcript that isolates the two display bugs: the same reply with the
code indented by tabs, then by spaces, then a quote with no code above it."""
import json, sys, uuid
out, sid, cwd = sys.argv[1:4]
code_tabs = "for attempt := 0; attempt < maxAttempts; attempt++ {\n\terr := upload(ctx, part)\n\tif err == nil || !retryable(err) {\n\t\treturn err\n\t}\n\tselect {\n\tcase <-time.After(backoff(attempt)):\n\tcase <-ctx.Done():\n\t\treturn ctx.Err()\n\t}\n}"
code_spaces = code_tabs.replace("\t", "    ")
def reply(label, code):
    return f"**{label}**\n\n```go\n{code}\n```\n\n> The timeout test uses a fake clock, so it runs in a few milliseconds instead of 30 seconds.\n\nAll 214 tests pass."
texts = [
    reply("A. Code indented with tabs", code_tabs),
    reply("B. The same code indented with spaces", code_spaces),
    "**C. A quote with no code block above it**\n\n> The timeout test uses a fake clock, so it runs in a few milliseconds instead of 30 seconds.\n\nAll 214 tests pass.",
]
parent = None
with open(out, "w") as f:
    for i, (role, content) in enumerate([("user", "Show the three cases.")] + [("assistant", [{"type": "text", "text": t}]) for t in texts]):
        u = str(uuid.uuid4())
        msg = {"role": role, "content": content}
        if role == "assistant": msg["model"] = "claude-opus-5-5"
        f.write(json.dumps({"type": role, "uuid": u, "parentUuid": parent, "sessionId": sid, "cwd": cwd, "isSidechain": False, "timestamp": f"2026-10-31T10:00:0{i}.000Z", "message": msg}) + "\n")
        parent = u
