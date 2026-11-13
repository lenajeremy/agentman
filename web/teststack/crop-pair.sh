#!/bin/bash
# The same region of a comparison's two pictures, Mac on the left, web on the
# right, at full size (2x), for looking closely:
#   web/teststack/crop-pair.sh <compare out dir> <x> <y> <w> <h> [name] [web dy]
# x, y, w, h in the window's points (half the picture's pixels). web dy moves
# the web's region down by that many points, where its layout has drifted.
set -euo pipefail
DIR=$1; X=$2; Y=$3; W=$4; H=$5; NAME=${6:-crop}; DY=${7:-0}
px() { echo $(( $1 * 2 )); }
ffmpeg -loglevel error -y -i "$DIR/mac.png" -i "$DIR/web.png" -filter_complex \
  "[0]crop=$(px $W):$(px $H):$(px $X):$(px $Y),pad=iw+8:ih:0:0:color=red[a];[1]crop=$(px $W):$(px $H):$(px $X):$(px $((Y + DY)))[b];[a][b]hstack=inputs=2" \
  "$DIR/$NAME.png"
echo "$DIR/$NAME.png"
