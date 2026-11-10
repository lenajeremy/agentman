#!/bin/bash
# Pictures the web app in headless Chrome, signed in through `am web`, on
# the test stack. Headless Chrome draws off screen: nothing shows.
#
#   web/teststack/browser.sh <out.png> [query]     e.g. "agent=claude:6f1c…"
#
# SIZE (default 1560x980, drawn at 2x) and WAIT (seconds, default 10). Build
# the app first (cd web && npx vite build) and start the stack (start.sh).
set -euo pipefail
OUT=${1:?out.png}
QUERY=${2:-}
T=${AMX:-${TMPDIR:-/tmp}}
T=${T%/}/amx
SIZE=${SIZE:-1560x980}
WAIT=${WAIT:-10}
CHROME=${CHROME:-"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"}
[ -x "$CHROME" ] || CHROME=$(command -v google-chrome || command -v chromium || true)
[ -n "$CHROME" ] || { echo "browser: no Chrome found" >&2; exit 1; }

LINK=$("$T/am" web --print --config-home "$T/home")
if [ -n "$QUERY" ]; then
  LINK="$LINK&next=$(python3 -c 'import sys, urllib.parse; print(urllib.parse.quote("/?" + sys.argv[1], safe=""))' "$QUERY")"
fi
PROFILE=$(mktemp -d)
trap 'rm -rf "$PROFILE"' EXIT
"$CHROME" --headless=new --disable-gpu --hide-scrollbars --no-first-run --user-data-dir="$PROFILE" \
  --window-size="${SIZE/x/,}" --force-device-scale-factor=2 --virtual-time-budget=$((WAIT * 1000)) \
  --screenshot="$OUT" "$LINK" > /dev/null 2>&1
[ -f "$OUT" ] && echo "$OUT"
