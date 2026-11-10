#!/usr/bin/env python3
"""Writes a Claude Code transcript with every kind of row a thread draws:
sent pictures and files, pictures the agent read (one, and a run), a failed
command, an edit's diff, a long message, a compacted conversation, a
subagent's words, steps with and without the agent's own description.

Usage: thread.py <out.jsonl> <session id> <cwd> <home>
Also writes the pictures it names, under <cwd>/shots and the daemon's kept
images folder in <home>.
"""
import json
import os
import struct
import sys
import uuid
import zlib
from datetime import datetime, timedelta, timezone

out, session_id, cwd, home = sys.argv[1:5]
# "top": only the conversation up to the reply after the pictures.
part = sys.argv[5] if len(sys.argv) > 5 else "all"
t = datetime(2026, 10, 31, 9, 0, 0, tzinfo=timezone.utc)
lines = []
parent = None


def png(path, width, height, a, b):
    """A gradient from colour a to colour b, top left to bottom right."""
    os.makedirs(os.path.dirname(path), exist_ok=True)
    rows = []
    for y in range(height):
        row = bytearray([0])
        for x in range(width):
            f = (x / max(width - 1, 1) + y / max(height - 1, 1)) / 2
            row += bytes(int(a[i] + (b[i] - a[i]) * f) for i in range(3))
        rows.append(bytes(row))

    def chunk(kind, data):
        c = kind + data
        return struct.pack(">I", len(data)) + c + struct.pack(">I", zlib.crc32(c) & 0xFFFFFFFF)

    data = b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", width, height, 8, 2, 0, 0, 0))
    data += chunk(b"IDAT", zlib.compress(b"".join(rows), 9)) + chunk(b"IEND", b"")
    with open(path, "wb") as f:
        f.write(data)


def stamp():
    global t
    t += timedelta(seconds=7)
    return t.strftime("%Y-%m-%dT%H:%M:%S.000Z")


def add(kind, message, sidechain=False, **extra):
    global parent
    u = str(uuid.uuid4())
    record = {
        "type": kind, "uuid": u, "parentUuid": parent, "sessionId": session_id,
        "cwd": cwd, "isSidechain": sidechain, "timestamp": stamp(), "message": message,
    }
    record.update(extra)
    lines.append(record)
    parent = u


def user(text, **extra):
    add("user", {"role": "user", "content": text}, **extra)


def say(text, sidechain=False):
    add("assistant", {"role": "assistant", "model": "claude-opus-5-5",
                      "content": [{"type": "text", "text": text}]}, sidechain)


def tool(name, inp, result, error=False, sidechain=False, use_result=None):
    tid = "toolu_" + uuid.uuid4().hex[:20]
    add("assistant", {"role": "assistant", "model": "claude-opus-5-5",
                      "content": [{"type": "tool_use", "id": tid, "name": name, "input": inp}]}, sidechain)
    extra = {"toolUseResult": use_result} if use_result is not None else {}
    add("user", {"role": "user", "content": [{"type": "tool_result", "tool_use_id": tid,
                                              "content": result, "is_error": error}]}, sidechain, **extra)


shots = os.path.join(cwd, "shots")
png(os.path.join(shots, "sidebar.png"), 640, 400, (50, 104, 245), (240, 64, 127))
png(os.path.join(shots, "composer.png"), 300, 520, (18, 166, 106), (50, 104, 245))
png(os.path.join(shots, "header.png"), 900, 260, (255, 106, 43), (250, 250, 250))
png(os.path.join(shots, "menu.png"), 420, 420, (16, 16, 16), (153, 153, 153))
kept = os.path.join(home, ".agentman", "images", "claude-" + session_id)
png(os.path.join(kept, "1a2b3c4d.png"), 800, 500, (240, 64, 127), (255, 214, 102))
png(os.path.join(kept, "5e6f7a8b.png"), 500, 500, (50, 104, 245), (127, 204, 218))
os.makedirs(os.path.join(cwd, "src"), exist_ok=True)
with open(os.path.join(cwd, "NOTES.md"), "w") as f:
    f.write("# Notes\n\nWhat the thread should look like.\n")
with open(os.path.join(cwd, "src", "Thread.tsx"), "w") as f:
    f.write("export function Thread() {\n  return null;\n}\n")

user("This session is being continued from a previous conversation that ran out of context. "
     "The conversation is summarized below:\n\nAnalysis:\nThe user asked for the thread to match the Mac app.\n\n"
     "Summary:\n1. Primary Request: make every row of the thread look the same as on the Mac.\n"
     "2. Pending: the pictures and the question card.", isCompactSummary=True)
user(os.path.join(kept, "1a2b3c4d.png") + " " + os.path.join(kept, "5e6f7a8b.png") +
     " Here are two screenshots from the phone. Why do the \"pills\" look different -- and what's off...?")
say("I'll look at the screenshots in the repo first, then compare them with yours.")
tool("Read", {"file_path": os.path.join(shots, "sidebar.png")}, [{"type": "text", "text": "[image]"}])
say("The sidebar's rows are 4 points taller than the phone's. Let me check the rest.")
tool("Read", {"file_path": os.path.join(shots, "composer.png")}, [{"type": "text", "text": "[image]"}])
tool("Read", {"file_path": os.path.join(shots, "header.png")}, [{"type": "text", "text": "[image]"}])
tool("Read", {"file_path": os.path.join(shots, "menu.png")}, [{"type": "text", "text": "[image]"}])
if part == "top":
    with open(out, "w") as f:
        for line in lines:
            f.write(json.dumps(line) + "\n")
    sys.exit(0)
say("Here's what's off:\n\n1. The composer's corners are 18 points, not 12.\n2. The header's title is medium, not semibold.\n"
    "3. The menu's rows don't line up with the button --- they sit 4 points lower.\n\n"
    "- [x] Sidebar rows\n- [ ] Composer corners\n- [ ] Header title")
user("@" + os.path.join(cwd, "NOTES.md") + " @" + os.path.join(cwd, "src", "Thread.tsx") +
     " Fix the composer first, and follow the notes.")
tool("Bash", {"command": "npm test -- --run composer", "description": "Run the composer tests"},
     "FAIL src/Composer.test.tsx\n  ✕ keeps 18pt corners (12 ms)\n\n  Expected: 18\n  Received: 12\n\nTests: 1 failed, 11 passed",
     error=True)
tool("Edit", {"file_path": os.path.join(cwd, "src", "Thread.tsx"), "old_string": "return null;", "new_string": "return <div />;"},
     "The file has been updated successfully.",
     use_result={"filePath": os.path.join(cwd, "src", "Thread.tsx"), "structuredPatch": [
         {"oldStart": 1, "oldLines": 3, "newStart": 1, "newLines": 4,
          "lines": [" export function Thread() {", "-  return null;", "+  const radius = 18;", "+  return <div style={{ borderRadius: radius }} />;", " }"]}]})
tool("Bash", {"command": "npm test -- --run composer", "description": "Run the composer tests again"},
     "PASS src/Composer.test.tsx\n\nTests: 12 passed")
tool("Grep", {"pattern": "borderRadius", "path": cwd}, "src/Thread.tsx:3")
say("A subagent checked the other screens for the same mistake.", sidechain=True)
tool("Task", {"description": "Check the other screens", "prompt": "Look for 12pt corners"}, "None found.", sidechain=True)
user("Thanks. One more thing: the long messages. " + ("When a message is long, the Mac app clips it to eight lines and offers "
     "Show more under it, so the conversation stays easy to scan. ") * 8)
say("Done. Long messages now clip to **eight lines** with *Show more* under them, and the `--` in text reads as a dash. "
    "See [the notes](https://example.com/notes) for why.")

with open(out, "w") as f:
    for line in lines:
        f.write(json.dumps(line) + "\n")
print(f"{len(lines)} lines")
