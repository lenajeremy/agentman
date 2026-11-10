#!/bin/bash
# Pictures the web app in headless Chrome, signed in through `am web`, on
# the test stack. Headless Chrome draws off screen: nothing shows.
#
#   web/teststack/browser.sh <out.png> [query] [step]...
#
# query lands on a screen ("agent=claude:6f1c…"); each step is JavaScript run
# in the page, 1.5 s apart, after WAIT seconds (default 6), e.g.
#   'document.querySelector("button[aria-label=More]").click()'
# SIZE (default 1560x980, drawn at 2x); DARK=1 for the dark appearance;
# BLUR=1 for the look of a window that isn't in front (it is by default);
# INIT=<js> runs before the page's scripts; GRANT=notifications allows them.
# Build the app first (cd web && npx vite build) and start the stack.
set -euo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
OUT=${1:?out.png}
QUERY=${2:-}
shift $(( $# > 1 ? 2 : 1 ))
T=${AMX:-${TMPDIR:-/tmp}}
T=${T%/}/amx

LINK=$("$T/am" web --print --config-home "$T/home")
if [ -n "$QUERY" ]; then
  LINK="$LINK&next=$(python3 -c 'import sys, urllib.parse; print(urllib.parse.quote("/?" + sys.argv[1], safe=""))' "$QUERY")"
fi
ARGS=(--size "${SIZE:-1560x980}" --wait "${WAIT:-6}")
[ -n "${DARK:-}" ] && ARGS+=(--dark)
[ -n "${BLUR:-}" ] && ARGS+=(--blur)
[ -n "${INIT:-}" ] && ARGS+=(--init "$INIT")
[ -n "${GRANT:-}" ] && ARGS+=(--grant "$GRANT")
for step in "$@"; do ARGS+=(--step "$step"); done
node "$HERE/shot.mjs" "$LINK" "$OUT" "${ARGS[@]}"
