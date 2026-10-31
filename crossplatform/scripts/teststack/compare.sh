#!/bin/bash
# Opens the Mac app (a Debug build of desktop/, its own app id, never the
# user's Agentman) and the cross-platform app on the test stack, at the same
# size, on the same screen, and captures both windows:
#
#   scripts/teststack/compare.sh <out dir> [agent id] [mac link] [app query]
#
#   out/mac.png, out/new.png   each window at 2x
#   out/pair.png               the two, Mac on top, scaled to 1600 wide
#
# mac link is what the Mac app opens (AGENTMAN_OPEN), default
# agentman://agent/<agent id>; app query is the cross-platform app's
# AGENTMAN_OPEN, default agent=<agent id>. Run start.sh first. Captures need
# screen recording access (outside a sandbox). Crop with
#   sips -c <h> <w> --cropOffset <y> <x> in.png --out out.png   (offsets >= 1)
set -euo pipefail
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
mkdir -p "$OUT"

[ -x "$T/winid" ] || swiftc -O -o "$T/winid" "$HERE/winid.swift"
MAC_APP=${MAC_APP:-$T/macbuild/Build/Products/Debug/Agentman.app}
if [ ! -d "$MAC_APP" ]; then
  (cd "$REPO/desktop" && xcodegen -q && xcodebuild -quiet -project Agentman.xcodeproj -scheme Agentman \
    -configuration Debug -derivedDataPath "$T/macbuild" build)
fi
MAC_APP=$(cd -P "$MAC_APP" && pwd)
NEW_APP=${NEW_APP:-$REPO/crossplatform/bin/agentman}

# One comparison at a time: they share the screen and the Mac app's saved
# state. The lock is a directory; a run that died leaves it for 5 minutes.
LOCK=${TMPDIR%/}/amx-compare.lock
for _ in $(seq 1 600); do
  mkdir "$LOCK" 2>/dev/null && break
  [ -n "$(find "$LOCK" -maxdepth 0 -mmin +5 2>/dev/null)" ] && rm -rf "$LOCK"
  sleep 1
done
trap 'rm -rf "$LOCK"' EXIT

# The test copy's saved state (its own app id, never the user's) is cleared
# so both apps start the same way: no panel left open, no tab remembered.
# KEEP_STATE=1 keeps it, to compare a state set up by hand.
[ -n "${KEEP_STATE:-}" ] || defaults delete dev.agentman.mac.debug 2>/dev/null || true
# The Mac window opens at the size it last had; set it, as the new app's is.
W=${SIZE%x*}; HGT=${SIZE#*x}
defaults write dev.agentman.mac.debug "NSWindow Frame main" "0 0 $W $HGT 0 0 1920 1080" 2>/dev/null || true
open -g -n --env AGENTMAN_SOCKET="$H/.agentman/am.sock" --env "AGENTMAN_OPEN=$MAC_LINK" "$MAC_APP" \
  --args -ApplePersistenceIgnoreState YES
AGENTMAN_HOME=$H AGENTMAN_SIZE=$SIZE AGENTMAN_OPEN="$APP_QUERY" nohup "$NEW_APP" > "$T/app.log" 2>&1 &
NEW_PID=$!
sleep "${WAIT:-12}"
MAC_PID=$(pgrep -n -f "$MAC_APP/Contents/MacOS/Agentman")

screencapture -x -o -l "$("$T/winid" "$MAC_PID")" "$OUT/mac.png"
screencapture -x -o -l "$("$T/winid" "$NEW_PID")" "$OUT/new.png"
ffmpeg -loglevel error -y -i "$OUT/mac.png" -i "$OUT/new.png" \
  -filter_complex "[0]scale=1600:-1[a];[1]scale=1600:-1[b];[a][b]vstack=inputs=2" "$OUT/pair.png"
echo "$OUT/pair.png"
[ -n "${KEEP:-}" ] || { kill "$NEW_PID" 2>/dev/null; kill "$MAC_PID" 2>/dev/null; } || true
