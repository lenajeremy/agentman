#!/bin/bash
# Compares the Mac app (a Debug build of desktop/, its own app id, never the
# user's Agentman) with the cross-platform app on the test stack, at the same
# size, WITHOUT SHOWING ANYTHING: both run as test runs (AGENTMAN_TEST_SNAPSHOT)
# that never become the active app, keep their windows transparent and behind
# everything, draw a picture of themselves and quit. Someone using the Mac
# sees nothing. Never capture the screen instead.
#
#   scripts/teststack/compare.sh <out dir> [agent id] [mac link] [app query]
#
#   out/mac.png, out/new.png   each window's content at 2x (no traffic lights)
#   out/pair.png               the two, Mac on top, scaled to 1600 wide
#
# mac link is what the Mac app opens (AGENTMAN_OPEN), default
# agentman://agent/<agent id>; app query is the cross-platform app's
# AGENTMAN_OPEN, default agent=<agent id>.
#   WAIT=<s>          seconds before the pictures (default 12)
#   MAC_WINDOW=<t>    the Mac window to draw, by title (default the largest;
#                     an editor window's title is its folder's name)
#   MAC_ENV="K=V …"   more environment for the Mac app (AGENTMAN_FILES=changes)
#   MAC_DEFAULTS="key=-type:value …"  its defaults, set first (rightPanelOpen=-bool:true)
#   KEEP_STATE=1      keep the Mac test copy's saved state instead of clearing it
# Run start.sh first. Crop with
#   sips -c <h> <w> --cropOffset <y> <x> in.png --out out.png   (offsets >= 1)
set -uo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
REPO=$(cd "$HERE/../../.." && pwd)
OUT=${1:?out dir}
AGENT=${2:-claude:6f1c2a9e-3b7d-4c55-9a10-2d8e4f6b7c01}
MAC_LINK=${3:-agentman://agent/$AGENT}
APP_QUERY=${4:-agent=$AGENT}
T=${AMX:-${TMPDIR:-/tmp}}
T=${T%/}/amx
H=$T/home
SIZE=${SIZE:-1560x980}
WAIT=${WAIT:-12}
mkdir -p "$OUT"
rm -f "$OUT/mac.png" "$OUT/new.png" "$OUT/pair.png"

MAC_APP=${MAC_APP:-$T/macbuild/Build/Products/Debug/Agentman.app}
if [ ! -d "$MAC_APP" ]; then
  (cd "$REPO/desktop" && xcodegen -q && xcodebuild -quiet -project Agentman.xcodeproj -scheme Agentman \
    -configuration Debug -derivedDataPath "$T/macbuild" build) || exit 1
fi
MAC_APP=$(cd -P "$MAC_APP" && pwd)
NEW_APP=${NEW_APP:-$REPO/crossplatform/bin/agentman}

# One comparison at a time: they share the Mac app's saved state. The lock is
# a directory; a run that died leaves it for 5 minutes.
LOCK=${TMPDIR%/}/amx-compare.lock
for _ in $(seq 1 600); do
  mkdir "$LOCK" 2>/dev/null && break
  [ -n "$(find "$LOCK" -maxdepth 0 -mmin +5 2>/dev/null)" ] && rm -rf "$LOCK"
  sleep 1
done
RUN=amx-run-$$-$RANDOM
NEW_PID=
# Whatever happens, this run's apps go and the lock is freed. Only this run's:
# the Mac app is found by the run's own tag in its arguments.
cleanup() {
  [ -n "$NEW_PID" ] && kill "$NEW_PID" 2>/dev/null
  pkill -f -- "-AgentmanTestRun $RUN" 2>/dev/null
  rm -rf "$LOCK"
}
trap cleanup EXIT

# The test copy's saved state (its own app id, never the user's) is cleared
# so both apps start the same way: no panel left open, no tab remembered.
[ -n "${KEEP_STATE:-}" ] || defaults delete dev.agentman.mac.debug 2>/dev/null || true
W=${SIZE%x*}; HGT=${SIZE#*x}
defaults write dev.agentman.mac.debug "NSWindow Frame main" "0 0 $W $HGT 0 0 1920 1080" 2>/dev/null || true
for d in ${MAC_DEFAULTS:-}; do
  value=${d#*=}
  # A plist without spaces, such as a dictionary: key={pinned=("id");}
  case $value in
    "{"*|"("*) defaults write dev.agentman.mac.debug "${d%%=*}" "$value"; continue ;;
  esac
  defaults write dev.agentman.mac.debug "${d%%=*}" "${value%%:*}" "${value#*:}"
done
MAC_ENV_ARGS=()
for kv in ${MAC_ENV:-}; do MAC_ENV_ARGS+=(--env "$kv"); done
[ -n "${MAC_WINDOW:-}" ] && MAC_ENV_ARGS+=(--env "AGENTMAN_TEST_WINDOW=$MAC_WINDOW")

open -g -n --env AGENTMAN_SOCKET="$H/.agentman/am.sock" --env "AGENTMAN_OPEN=$MAC_LINK" \
  --env "AGENTMAN_TEST_SNAPSHOT=$OUT/mac.png" --env "AGENTMAN_TEST_DELAY=$WAIT" \
  ${MAC_ENV_ARGS[@]+"${MAC_ENV_ARGS[@]}"} "$MAC_APP" \
  --args -ApplePersistenceIgnoreState YES -AgentmanTestRun "$RUN"
AGENTMAN_HOME=$H AGENTMAN_SIZE=$SIZE AGENTMAN_OPEN="$APP_QUERY" \
  AGENTMAN_TEST_SNAPSHOT="$OUT/new.png" AGENTMAN_TEST_DELAY="$WAIT" \
  "$NEW_APP" > "$T/app.log" 2>&1 &
NEW_PID=$!

# Each app writes its picture and quits; give them the wait and a margin.
for _ in $(seq 1 $((WAIT + 30))); do
  [ -f "$OUT/mac.png" ] && [ -f "$OUT/new.png" ] && break
  sleep 1
done
[ -f "$OUT/mac.png" ] || echo "compare: the Mac app made no picture" >&2
[ -f "$OUT/new.png" ] || { echo "compare: the new app made no picture; its log:" >&2; tail -5 "$T/app.log" >&2; }
[ -f "$OUT/mac.png" ] && [ -f "$OUT/new.png" ] || exit 1
ffmpeg -loglevel error -y -i "$OUT/mac.png" -i "$OUT/new.png" \
  -filter_complex "[0]scale=1600:-1[a];[1]scale=1600:-1[b];[a][b]vstack=inputs=2" "$OUT/pair.png"
echo "$OUT/pair.png"
