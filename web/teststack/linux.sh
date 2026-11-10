#!/bin/bash
# Runs the cross-platform app on Ubuntu, in the OrbStack machine
# agentman-linux (Ubuntu 24.04 with Go, Node, GTK 4, WebKitGTK 6, Xvfb),
# against the test stack started there, and captures the screen:
#
#   scripts/teststack/linux.sh <out.png> [app query]
#
# The machine sees this Mac's files at the same paths. Build the frontend on
# the Mac first (npm run build in frontend/); this builds the Go side there.
set -euo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
REPO=$(cd "$HERE/../.." && pwd)
OUT=${1:?out.png}
QUERY=${2:-}
# One run at a time: the machine has one screen.
LOCK=${TMPDIR%/}/amx-linux.lock
for _ in $(seq 1 600); do
  mkdir "$LOCK" 2>/dev/null && break
  [ -n "$(find "$LOCK" -maxdepth 0 -mmin +5 2>/dev/null)" ] && rm -rf "$LOCK"
  sleep 1
done
trap 'rm -rf "$LOCK"' EXIT
orb -m agentman-linux bash -s -- "$REPO" "$OUT" "$QUERY" "${WAIT:-9}" <<'EOF'
set -uo pipefail
REPO=$1 OUT=$2 QUERY=$3 WAIT=$4
export PATH=/usr/local/go/bin:$PATH AMX=/tmp
cd "$REPO/crossplatform" && go build -tags production -trimpath -buildvcs=false -ldflags="-w -s" -o ~/amx-agentman . || exit 1
if ! pgrep -f "/tmp/amx/am serve" >/dev/null; then "$REPO/crossplatform/scripts/teststack/start.sh" >/dev/null; fi
pgrep -x Xvfb >/dev/null || { nohup Xvfb :99 -screen 0 1600x1000x24 >/tmp/amx/xvfb.log 2>&1 & sleep 1; }
export DISPLAY=:99
pkill -f "$HOME/amx-agentman" 2>/dev/null; sleep 0.5
AGENTMAN_HOME=/tmp/amx/home AGENTMAN_OPEN="$QUERY" AGENTMAN_SIZE=1560x980 GDK_BACKEND=x11 \
  nohup dbus-run-session -- "$HOME/amx-agentman" > /tmp/amx/app.log 2>&1 &
sleep "$WAIT"
import -window root "$OUT" && echo "$OUT"
EOF
