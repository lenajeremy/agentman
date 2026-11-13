#!/bin/bash
# Adds agents for comparing the thread and the composer to a test stack that
# start.sh is running (same AMX):
#
#   claude:7a1e5c0b-2d4f-4e6a-9b8c-1f2e3d4c5b6a  "pics"   every kind of row (thread.py), in a pane
#   claude:8b2f6d1c-3e5a-4f7b-8c9d-2a3b4c5d6e7f  "ask"    a pane showing Claude's permission question
#   claude:2f6a0b5c-7d9e-4f1a-8b3c-6e7f8a9b0c1d  "pick"   a pane showing Claude's question that takes a typed answer
#   claude:9c3a7e2d-4f6b-4a8c-9d0e-3b4c5d6e7f8a  "modes"  a pane showing Claude's mode line
#   claude:0d4b8f3e-5a7c-4b9d-8e1f-4c5d6e7f8a9b  "empty"  no messages, not in a pane
#   claude:1e5f9a4b-6c8d-4e0f-9a2b-5d6e7f8a9b0c  "shots"  the start of pics alone: sent and read pictures
#
#   AMX=$TMPDIR/amx-thread PORT=18810 scripts/teststack/start.sh
#   AMX=$TMPDIR/amx-thread scripts/teststack/thread-agents.sh
set -euo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
REPO=$(cd "$HERE/../.." && pwd)
T=${AMX:-${TMPDIR:-/tmp}}
T=${T%/}/amx
H=$T/home
export TMUX_TMPDIR=$T/tmux
unset TMUX TMUX_PANE
TMUX_BIN=$(command -v tmux)
DATA=$REPO/internal/question/testdata

# agent <id> <name> <pid> [transcript generator args…]: a registry entry, and
# a transcript (empty without a generator).
agent() {
  local sid=${1#claude:} name=$2 pid=$3
  shift 3
  local proj=$H/projects/$name
  mkdir -p "$proj"
  [ -d "$proj/.git" ] || git -C "$proj" init -q
  local munged
  munged=$(printf '%s' "$proj" | sed 's/[^A-Za-z0-9]/-/g')
  mkdir -p "$H/.claude/projects/$munged"
  if [ $# -gt 0 ]; then
    python3 "$HERE/$1" "$H/.claude/projects/$munged/$sid.jsonl" "$sid" "$proj" "$H" ${2:-}
  else
    : > "$H/.claude/projects/$munged/$sid.jsonl"
  fi
  local now=$(($(date +%s) * 1000))
  printf '{"pid":%s,"sessionId":"%s","cwd":"%s","startedAt":%s,"kind":"interactive","status":"idle","updatedAt":%s}\n' \
    "$pid" "$sid" "$proj" "$now" "$now" > "$H/.claude/sessions/$pid.json"
}

# pane <session> <folder> <command>: a pane on the agents' tmux, its pid.
pane() {
  mkdir -p "$2"
  "$TMUX_BIN" -L agentman has-session -t "=$1" 2>/dev/null ||
    "$TMUX_BIN" -L agentman new-session -d -s "$1" -c "$2" -x 160 -y 45 "$3"
  "$TMUX_BIN" -L agentman list-panes -t "=$1" -F '#{pane_pid}' | head -1
}

PID=$(pane agentman-pics "$H/projects/pics" "bash --norc --noprofile -i")
agent claude:7a1e5c0b-2d4f-4e6a-9b8c-1f2e3d4c5b6a pics "$PID" thread.py

# shots: the first part of pics alone, short enough to see whole.
PID=$(pane agentman-shots "$H/projects/shots" "bash --norc --noprofile -i")
agent claude:1e5f9a4b-6c8d-4e0f-9a2b-5d6e7f8a9b0c shots "$PID" thread.py top

PID=$(pane agentman-ask "$H/projects/ask" "cat '$DATA/claude_permission_real_pane.txt'; exec sleep 86400")
agent claude:8b2f6d1c-3e5a-4f7b-8c9d-2a3b4c5d6e7f ask "$PID"

PID=$(pane agentman-pick "$H/projects/pick" "cat '$DATA/claude_single_real_pane.txt'; exec sleep 86400")
agent claude:2f6a0b5c-7d9e-4f1a-8b3c-6e7f8a9b0c1d pick "$PID"

PID=$(pane agentman-modes "$H/projects/modes" "cat '$DATA/claude_mode_accept_edits_real_pane.txt'; exec sleep 86400")
agent claude:9c3a7e2d-4f6b-4a8c-9d0e-3b4c5d6e7f8a modes "$PID"

pkill -f "amx-empty $T 86400" 2>/dev/null || true
(exec -a "sleep 86400 amx-empty $T" sleep 86400) >/dev/null 2>&1 &
agent claude:0d4b8f3e-5a7c-4b9d-8e1f-4c5d6e7f8a9b empty "$!"
echo "added pics, shots, ask, pick, modes, empty to $H"
