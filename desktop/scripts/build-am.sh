#!/bin/sh
# Builds am, Agentman's command-line tool and daemon, from this repository
# into the app at Contents/Helpers/am: one file for Apple silicon and Intel,
# signed as the app is. Xcode runs it at the end of each build of the app
# (project.yml), before it signs the app around it.
#
# The app sets everything up with this am on first launch
# (App/DaemonSetup.swift): am setup copies it to ~/.agentman/bin/am, which
# the login service runs. It is never run or replaced in place, since anything changed inside
# the app breaks the app's signature.
#
# Its version is the app's, stamped as GoReleaser stamps a release's
# (.goreleaser.yaml), so the app and its am always name the same release. A
# Debug build's am says it is a development build ("0.38.0-dev"), which am
# setup never puts over a release unless forced.
set -eu

: "${TARGET_BUILD_DIR:?Xcode runs this; see project.yml}"
root="$SRCROOT/.."
helpers="$TARGET_BUILD_DIR/$CONTENTS_FOLDER_PATH/Helpers"
work="$DERIVED_FILE_DIR/am"

# Xcode's PATH has no Go. GO names one; otherwise look where it usually
# lives, and last ask the login shell, which has the user's PATH.
go="${GO:-}"
if [ -z "$go" ]; then
  for candidate in "$(command -v go 2>/dev/null || true)" /opt/homebrew/bin/go /usr/local/go/bin/go /usr/local/bin/go \
                   /opt/local/bin/go "$HOME"/sdk/go*/bin/go; do
    if [ -n "$candidate" ] && [ -x "$candidate" ]; then go="$candidate"; break; fi
  done
fi
if [ -z "$go" ]; then
  go="$("${SHELL:-/bin/zsh}" -l -c 'command -v go' 2>/dev/null </dev/null || true)"
fi
if [ -z "$go" ] || [ ! -x "$go" ]; then
  echo "error: building am into the app needs Go (https://go.dev/dl, or brew install go); set GO to its path if it is somewhere else"
  exit 1
fi

version="$MARKETING_VERSION"
[ "$CONFIGURATION" = Release ] || version="$version-dev"

mkdir -p "$work" "$helpers"
for arch in arm64 amd64; do
  CGO_ENABLED=0 GOOS=darwin GOARCH="$arch" "$go" build -C "$root" -trimpath \
    -ldflags "-s -w -X main.version=$version" -o "$work/am-$arch" ./cmd/am
done
lipo -create -output "$work/am" "$work/am-arm64" "$work/am-amd64"

# The identity Xcode signs the app with: "-" signs ad hoc, as Debug builds
# are; a real one, as an archive's, gets a secure timestamp, which
# notarization asks of every program inside the app.
identity="${EXPANDED_CODE_SIGN_IDENTITY:-}"
[ "${CODE_SIGNING_ALLOWED:-YES}" = YES ] || identity=""

# Nothing changed since the last build: the signed copy already in the app
# stays, and the app isn't sealed again for nothing.
stamp="$version $identity"
if [ -f "$helpers/am" ] && [ -f "$work/am.built" ] && cmp -s "$work/am" "$work/am.built" &&
   [ "$(cat "$work/am.stamp" 2>/dev/null)" = "$stamp" ]; then
  exit 0
fi

cp "$work/am" "$helpers/am.new"
if [ -n "$identity" ]; then
  timestamp=--timestamp
  [ "$identity" = "-" ] && timestamp=--timestamp=none
  codesign --force --sign "$identity" --identifier dev.agentman.am --options runtime "$timestamp" "$helpers/am.new"
fi
mv -f "$helpers/am.new" "$helpers/am"
cp "$work/am" "$work/am.built"
printf '%s' "$stamp" >"$work/am.stamp"
