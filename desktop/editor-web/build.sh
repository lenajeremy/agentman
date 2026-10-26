#!/bin/sh
# Builds the editor page when its sources are newer than the last build.
# Xcode runs this before each build of the app (project.yml), so the page in
# Resources/EditorWeb, which git ignores, is always there and current.
set -e
cd "$(dirname "$0")"
out=../Agentman/Resources/EditorWeb/main.js

# Xcode's PATH has no Node: look where it usually lives.
for dir in /usr/local/bin /opt/homebrew/bin "$HOME"/.nvm/versions/node/*/bin "$HOME/.volta/bin" "$HOME/.local/share/fnm/aliases/default/bin"; do
  [ -x "$dir/node" ] && PATH="$dir:$PATH"
done
export PATH

if [ -f "$out" ] && [ -z "$(find src build.mjs package.json package-lock.json -newer "$out" 2>/dev/null | head -1)" ]; then
  exit 0
fi
if ! command -v node >/dev/null 2>&1; then
  if [ -f "$out" ]; then
    echo "warning: Node.js not found; using the editor page built before"
    exit 0
  fi
  echo "error: building the editor page needs Node.js (https://nodejs.org)"
  exit 1
fi
[ -d node_modules/monaco-editor ] || npm ci --no-audit --no-fund
node build.mjs
