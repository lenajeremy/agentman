#!/bin/bash
# The web app end to end, against the test stack:
#   web/teststack/e2e/run.sh [chromium|webkit|all] [area…]
# areas: shell board thread newagent usage connection editor settings.
# Nothing shows on screen, nothing costs money and nothing is published:
# Agentman runs on a fake Anthropic API, pull requests go to a stand-in gh,
# Muse runs on its echo provider, and every agent is a test one.
set -euo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
T=${TMPDIR%/}/amx; H=$T/home
[ -x "$T/am" ] || "$HERE/../start.sh"
[ -f "$HERE/../../../internal/webapp/dist/index.html" ] || (cd "$HERE/../.." && npx vite build >/dev/null)

# The stand-ins.
pgrep -f "fake-anthropic.py 18998" >/dev/null || (FAKE_ANTHROPIC_LOG="$T/fake-anthropic.log" nohup python3 "$HERE/fake-anthropic.py" 18998 >/dev/null 2>&1 &)
mkdir -p "$T/fakebin" /private/tmp/amxm
cp "$HERE/fake-gh" "$T/fakebin/gh"
MUSE=$(ls ~/.local/bin/muse-bin-* 2>/dev/null | tail -1 || true)
if [ -n "$MUSE" ]; then
  ln -sf "$MUSE" "$T/fakebin/muse"
  pgrep -f "$MUSE serve --listen --provider echo" >/dev/null || (nohup env -i HOME="$H" USER="$USER" LOGNAME="$USER" PATH=/usr/bin:/bin TMPDIR="$TMPDIR" LANG=en_US.UTF-8 \
    XDG_DATA_HOME=/private/tmp/amxm MUSE_NO_AUTO_UPDATE=1 "$MUSE" serve --listen --provider echo > "$T/muse-echo.log" 2>&1 &)
fi

"$HERE/prepare.sh"
node "$HERE/e2e.mjs" "$@"
