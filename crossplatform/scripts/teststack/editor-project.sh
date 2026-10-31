#!/bin/bash
# Fills the test stack's "demo" folder with what the editor window shows:
# folders inside folders, a git history with files modified, added, deleted,
# renamed and untracked, Markdown with pictures, an image, a large file, a
# binary, dotfiles and a heavy folder. Run after start.sh; safe to run again
# (it starts the folder's history over).
#
#   AMX=<dir> scripts/teststack/editor-project.sh
set -euo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
REPO=$(cd "$HERE/../../.." && pwd)
T=${AMX:-${TMPDIR:-/tmp}}
T=${T%/}/amx
P=$T/home/projects/demo
git() { command git -C "$P" -c user.name=Test -c user.email=test@example.com -c commit.gpgsign=false "$@"; }

rm -rf "$P/.git" "$P/docs" "$P/src" "$P/assets" "$P/node_modules" "$P/scripts"
mkdir -p "$P/src/components" "$P/src/lib" "$P/docs/images" "$P/assets" "$P/node_modules/left-pad" "$P/scripts"
cp "$REPO/internal/term/term.go" "$P/term.go"
cp "$REPO/crossplatform/frontend/src/daemon.ts" "$P/daemon.ts"
cp "$REPO/crossplatform/frontend/src/App.tsx" "$P/src/App.tsx"
cp "$REPO/crossplatform/frontend/src/labels.ts" "$P/src/lib/labels.ts"
cp "$REPO/crossplatform/frontend/src/nav.ts" "$P/src/lib/nav.ts"
cp "$REPO/crossplatform/frontend/src/ui.tsx" "$P/src/components/ui.tsx"
cp "$REPO/crossplatform/frontend/src/Sidebar.tsx" "$P/src/components/Sidebar.tsx"
cp "$REPO/README.md" "$P/README.md"
cp "$REPO/crossplatform/build/appicon.png" "$P/assets/logo.png"
cp "$REPO/crossplatform/build/appicon.png" "$P/docs/images/diagram.png"
cp "$REPO/crossplatform/scripts/teststack/start.sh" "$P/scripts/start.sh"
printf 'node_modules\n.env\n' > "$P/.gitignore"
printf 'API_URL=http://localhost:3000\n' > "$P/.env"
printf '{\n  "name": "left-pad",\n  "version": "1.3.0"\n}\n' > "$P/node_modules/left-pad/package.json"
printf '{\n  "name": "demo",\n  "private": true,\n  "scripts": {\n    "dev": "vite"\n  }\n}\n' > "$P/package.json"
cat > "$P/docs/guide.md" <<'EOF'
# Guide

How the **demo** fits together, with a picture:

![The diagram](images/diagram.png)

## Files

- [The app](../src/App.tsx) starts everything
- [Labels](../src/lib/labels.ts) say what things are called
- [The README](../README.md)

```ts
export function hello(name: string): string {
  return `Hello, ${name}`
}
```

| Part | Where |
| --- | --- |
| App | `src/App.tsx` |
| Labels | `src/lib/labels.ts` |

> Agents change these files while you read them.
EOF
cat > "$P/docs/notes.md" <<'EOF'
# Notes

Old notes, to be renamed.
EOF
cat > "$P/old.ts" <<'EOF'
// To be deleted.
export const old = true
EOF
git init -q
git add -A
git commit -qm "First version"

# Changes since: a file modified, one staged new, one deleted, one renamed,
# one untracked, and a folder git hasn't seen.
printf '\n// Changed after the first commit.\nexport const changed = true\n' >> "$P/daemon.ts"
sed -i '' 's/function/function /' "$P/src/App.tsx" 2>/dev/null || true
printf 'export function format(n: number): string {\n  return n.toFixed(2)\n}\n' > "$P/src/lib/format.ts"
git add src/lib/format.ts
rm "$P/old.ts"
git mv docs/notes.md docs/notes-old.md
printf 'A file git has not seen.\n' > "$P/TODO.txt"
mkdir -p "$P/drafts"
printf '# Draft\n' > "$P/drafts/idea.md"
# Too large to edit (over 8 MB), and not text.
head -c 9000000 /dev/zero | tr '\0' 'a' > "$P/assets/big.log"
printf '\x00\x01\x02\x03binary' > "$P/assets/data.bin"
echo "$P"
