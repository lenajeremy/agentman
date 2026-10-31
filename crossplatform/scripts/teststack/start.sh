#!/bin/bash
# A test stack for comparing the cross-platform app with the Mac app, on
# this Mac or on Linux: a fake home, a private tmux (never the user's), fake
# agents with long transcripts, and am serve built from this checkout on
# 127.0.0.1:18797. Safe to run again: it rebuilds am and restarts the daemon.
#
#   scripts/teststack/start.sh          # prints the home and the agents' ids
#
# Agents:
#   claude:6f1c2a9e-3b7d-4c55-9a10-2d8e4f6b7c01  "demo"  a long thread, in a tmux pane
#   claude:0b9d4e21-7c3a-4f6e-8d12-5a6b7c8d9e10  "bugs"  a code block before a quote
set -euo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
REPO=$(cd "$HERE/../../.." && pwd)
T=${AMX:-${TMPDIR:-/tmp}}
T=${T%/}/amx
H=$T/home
export TMUX_TMPDIR=$T/tmux
unset TMUX TMUX_PANE
TMUX_BIN=$(command -v tmux)
GO=$(command -v go || echo /usr/local/go/bin/go)

mkdir -p "$H/.claude/sessions" "$TMUX_TMPDIR"
(cd "$REPO" && "$GO" build -o "$T/am" ./cmd/am)

# session <id> <folder name> <pid>: a Claude registry entry and its transcript.
session() {
  local sid=$1 name=$2 pid=$3 gen=$4
  local proj=$H/projects/$name
  local munged
  munged=$(printf '%s' "$proj" | sed 's/[^A-Za-z0-9]/-/g')
  mkdir -p "$H/.claude/projects/$munged"
  python3 "$HERE/$gen" "$H/.claude/projects/$munged/$sid.jsonl" "$sid" "$proj" ${5:-}
  local now=$(($(date +%s) * 1000))
  printf '{"pid":%s,"sessionId":"%s","cwd":"%s","startedAt":%s,"kind":"interactive","status":"idle","updatedAt":%s}\n' \
    "$pid" "$sid" "$proj" "$now" "$now" > "$H/.claude/sessions/$pid.json"
}

rm -f "$H/.claude/sessions/"*.json

# demo: a git repo with a few files, its agent an interactive shell in a pane
# named agentman-…, which is how am serve recognises its own panes.
PROJ=$H/projects/demo
mkdir -p "$PROJ/src"
cp "$REPO/internal/term/term.go" "$PROJ/term.go"
cp "$REPO/crossplatform/frontend/src/daemon.ts" "$PROJ/daemon.ts"
cp "$REPO/crossplatform/frontend/src/App.tsx" "$PROJ/src/App.tsx"
cp "$REPO/README.md" "$PROJ/README.md"
[ -d "$PROJ/.git" ] || git -C "$PROJ" init -q
"$TMUX_BIN" -L agentman has-session -t "=agentman-demo" 2>/dev/null ||
  "$TMUX_BIN" -L agentman new-session -d -s agentman-demo -c "$PROJ" -x 160 -y 45 "bash --norc --noprofile -i"
PID=$("$TMUX_BIN" -L agentman list-panes -t agentman-demo -F '#{pane_pid}' | head -1)
session 6f1c2a9e-3b7d-4c55-9a10-2d8e4f6b7c01 demo "$PID" transcript.py "${TURNS:-120}"

# bugs: any live process will do for an agent that isn't in a pane.
mkdir -p "$H/projects/bugs"
pkill -f "sleep 86400 amx-bugs" 2>/dev/null || true
(exec -a "sleep 86400 amx-bugs" sleep 86400) >/dev/null 2>&1 &
session 0b9d4e21-7c3a-4f6e-8d12-5a6b7c8d9e10 bugs "$!" quote-after-code.py

pkill -f "$T/am serve" 2>/dev/null || true
sleep 0.5
PATHS="/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
nohup env -i HOME="$H" USER="$USER" LOGNAME="$USER" SHELL=/bin/bash TERM=xterm-256color LANG=en_US.UTF-8 \
  TMPDIR="${TMPDIR:-/tmp}" TMUX_TMPDIR="$TMUX_TMPDIR" PATH="$PATHS" \
  "$T/am" serve -relay none -config-home "$H" -addr 127.0.0.1:18797 > "$T/serve.log" 2>&1 &
for _ in $(seq 1 50); do
  [ -f "$H/.agentman/app.json" ] && break
  sleep 0.2
done
echo "AGENTMAN_HOME=$H"
echo "socket       $H/.agentman/am.sock"
tail -3 "$T/serve.log"
