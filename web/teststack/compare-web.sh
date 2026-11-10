#!/bin/bash
# The Mac app beside the web app, at the same size and scale, WITHOUT SHOWING
# ANYTHING: the Mac app as a test run of its Debug build (its own app id,
# never the user's Agentman) that stays behind everything, draws a picture of
# its window and quits; the web app in headless Chrome. Both on the test
# stack, both from their default settings, both in the same look.
#
#   web/teststack/compare-web.sh <out dir> [mac link] [web query] [web step]...
#
#   out/mac.png, out/web.png   the window's content at 2x
#   out/pair.png               the two, Mac on top, 1600 wide
#   out/diff.png               where they differ, brightened (same size only)
#
# mac link is what the Mac app opens (AGENTMAN_OPEN), default the Board
# (agentman://agentman); web query lands the web app there, default the
# Board too. Web steps are browser.sh's (JS, click:, type:, key:…).
#   LOOK=light|dark   both apps' appearance (default light; the Mac's own
#                     doesn't matter)
#   THEME=classic|site  the Mac's theme (default classic)
#   WAIT=<s>          seconds before the pictures (default 10)
#   SIZE=WxH          the window (default 1560x980)
#   MAC_WINDOW, MAC_ENV, MAC_DEFAULTS, KEEP_STATE: as compare.sh
#   WEB_PREFS=<json>  more settings for the web run only, merged in
# Build the web app first (cd web && npm run build); the test stack's am
# serve must be running (e2e/restart.sh).
set -uo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
REPO=$(cd "$HERE/../.." && pwd)
OUT=${1:?out dir}
MAC_LINK=${2:-agentman://agentman}
WEB_QUERY=${3:-}
shift $(( $# < 3 ? $# : 3 ))
T=${TMPDIR%/}/amx
H=$T/home
SIZE=${SIZE:-1560x980}
WAIT=${WAIT:-10}
LOOK=${LOOK:-light}
THEME=${THEME:-classic}
mkdir -p "$OUT"
rm -f "$OUT/mac.png" "$OUT/web.png" "$OUT/pair.png" "$OUT/diff.png"

MAC_APP=${MAC_APP:-$T/macbuild/Build/Products/Debug/Agentman.app}
[ -d "$MAC_APP" ] || { echo "no Mac test build at $MAC_APP (see compare.sh)" >&2; exit 1; }
MAC_APP=$(cd -P "$MAC_APP" && pwd)

# One Mac run at a time: they share the test copy's saved state.
LOCK=${TMPDIR%/}/amx-compare.lock
for _ in $(seq 1 600); do
  mkdir "$LOCK" 2>/dev/null && break
  [ -n "$(find "$LOCK" -maxdepth 0 -mmin +5 2>/dev/null)" ] && rm -rf "$LOCK"
  sleep 1
done
RUN=amx-run-$$-$RANDOM
cleanup() {
  pkill -f -- "-AgentmanTestRun $RUN" 2>/dev/null
  rm -rf "$LOCK"
}
trap cleanup EXIT

# The test copy's settings (dev.agentman.mac.debug, never the user's) from
# the defaults, in the look asked for.
[ -n "${KEEP_STATE:-}" ] || defaults delete dev.agentman.mac.debug 2>/dev/null || true
W=${SIZE%x*}; HGT=${SIZE#*x}
defaults write dev.agentman.mac.debug "NSWindow Frame main" "0 0 $W $HGT 0 0 1920 1080" 2>/dev/null || true
defaults write dev.agentman.mac.debug appearance -string "$LOOK"
for d in ${MAC_DEFAULTS:-}; do
  value=${d#*=}
  case $value in
    "{"*|"("*) defaults write dev.agentman.mac.debug "${d%%=*}" "$value"; continue ;;
  esac
  defaults write dev.agentman.mac.debug "${d%%=*}" "${value%%:*}" "${value#*:}"
done
MAC_ENV_ARGS=(--env "AGENTMAN_THEME=$THEME")
for kv in ${MAC_ENV:-}; do MAC_ENV_ARGS+=(--env "$kv"); done
[ -n "${MAC_WINDOW:-}" ] && MAC_ENV_ARGS+=(--env "AGENTMAN_TEST_WINDOW=$MAC_WINDOW")

open -g -n --env AGENTMAN_SOCKET="$H/.agentman/am.sock" --env "AGENTMAN_OPEN=$MAC_LINK" \
  --env "AGENTMAN_TEST_SNAPSHOT=$OUT/mac.png" --env "AGENTMAN_TEST_DELAY=$WAIT" \
  "${MAC_ENV_ARGS[@]}" "$MAC_APP" \
  --args -ApplePersistenceIgnoreState YES -AgentmanTestRun "$RUN"

# The web app: its defaults (fresh), the same look, this run only.
PREFS=$(python3 -c 'import json,sys; p={"appearance": sys.argv[1]}; p.update(json.loads(sys.argv[2] or "{}")); print(json.dumps(p))' "$LOOK" "${WEB_PREFS:-}")
Q="fresh&prefs=$(python3 -c 'import urllib.parse,sys;print(urllib.parse.quote(sys.argv[1]))' "$PREFS")${WEB_QUERY:+&$WEB_QUERY}"
DARK_ARG=; [ "$LOOK" = dark ] && DARK_ARG=1
DARK=$DARK_ARG SIZE=$SIZE WAIT=$WAIT "$HERE/browser.sh" "$OUT/web.png" "$Q" "$@" > "$OUT/web.log" 2>&1 &
WEB_PID=$!

for _ in $(seq 1 $((WAIT + 40))); do
  [ -f "$OUT/mac.png" ] && ! kill -0 $WEB_PID 2>/dev/null && break
  sleep 1
done
wait $WEB_PID 2>/dev/null
[ -f "$OUT/mac.png" ] || echo "compare: the Mac app made no picture" >&2
[ -f "$OUT/web.png" ] || { echo "compare: the web app made no picture:" >&2; tail -5 "$OUT/web.log" >&2; }
[ -f "$OUT/mac.png" ] && [ -f "$OUT/web.png" ] || exit 1
ffmpeg -loglevel error -y -i "$OUT/mac.png" -i "$OUT/web.png" \
  -filter_complex "[0]scale=1600:-1[a];[1]scale=1600:-1[b];[a][b]vstack=inputs=2" "$OUT/pair.png"
MS=$(sips -g pixelWidth -g pixelHeight "$OUT/mac.png" | awk '/pixel/{printf "%s ", $2}')
WS=$(sips -g pixelWidth -g pixelHeight "$OUT/web.png" | awk '/pixel/{printf "%s ", $2}')
if [ "$MS" = "$WS" ]; then
  ffmpeg -loglevel error -y -i "$OUT/mac.png" -i "$OUT/web.png" \
    -filter_complex "[0][1]blend=all_mode=difference,curves=all='0/0 0.06/1 1/1',scale=1600:-1" "$OUT/diff.png"
else
  echo "compare: sizes differ, mac $MS, web $WS: no diff" >&2
fi
echo "$OUT/pair.png"
