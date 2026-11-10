#!/bin/bash
# Restarts the test stack's am serve with the stand-ins: the stand-in gh
# first on its PATH, Agentman on the fake Anthropic API (a fake key from the
# environment, never the Keychain), Muse's data in a short folder (its
# socket's path must fit). Only the test daemon under $TMPDIR/amx.
set -euo pipefail
REPO=$(cd "$(dirname "$0")/../../.." && pwd)
T=${TMPDIR%/}/amx; H=$T/home
pkill -f "$T/am serve" 2>/dev/null || true
sleep 1
PATHS="$T/fakebin:/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
nohup env -i HOME="$H" USER="$USER" LOGNAME="$USER" SHELL=/bin/bash TERM=xterm-256color LANG=en_US.UTF-8 \
  TMPDIR="${TMPDIR:-/tmp}" TMUX_TMPDIR="$T/tmux" PATH="$PATHS" AGENTMAN_WEB_DIR="$REPO/internal/webapp/dist" \
  XDG_DATA_HOME=/private/tmp/amxm MUSE_NO_AUTO_UPDATE=1 \
  ANTHROPIC_API_KEY=sk-ant-fake-test-key ANTHROPIC_BASE_URL=http://127.0.0.1:18998 \
  "$T/am" serve -relay none -config-home "$H" -addr 127.0.0.1:18797 > "$T/serve.log" 2>&1 &
for _ in $(seq 1 40); do
  curl -s -o /dev/null -w "%{http_code}" http://127.0.0.1:18797/ 2>/dev/null | grep -q 401 && exit 0
  sleep 0.5
done
echo "am serve didn't come up; see $T/serve.log" >&2
exit 1
