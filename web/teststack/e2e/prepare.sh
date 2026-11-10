#!/bin/bash
# Sets the test stack up for a run: the thread agents afresh, a search index
# that knows their messages (their ids change each time), and a change on a
# branch of its own for Ship. Restarts the test daemon with the stand-ins.
set -euo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
T=${TMPDIR%/}/amx; H=$T/home
"$HERE/../thread-agents.sh" >/dev/null
pkill -f "$T/am serve" 2>/dev/null || true
rm -f "$H/.agentman/search.db"*
# am serve keeps an ended session off the list; the test agents come back.
python3 - "$H/.agentman/ended.json" <<'PY'
import json, os, sys
path = sys.argv[1]
if os.path.exists(path):
    ended = json.load(open(path))
    for name in [k for k in ended if k.startswith("claude:")]:
        del ended[name]
    json.dump(ended, open(path, "w"))
PY
D=$H/projects/demo
git -C "$D" checkout -q main
git -C "$D" checkout -q -b "e2e-ship-$(date +%s)"
echo "A line to ship, $(date)." >> "$D/TODO.txt"
"$HERE/restart.sh"
sleep 4
