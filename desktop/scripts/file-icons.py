#!/usr/bin/env python3
"""Make the file tree's icons from material-icon-theme.

The Material Icon Theme (github.com/material-extensions/vscode-material-icon-theme,
MIT) is what VS Code users know a TypeScript file or a package.json by. This
copies a chosen set of its SVGs into the asset catalogue, one image set each,
with the theme's light variant where it has one, and writes the theme's own
rules for those icons (file names, extensions, folder names) to
FileIconMap.swift. Anything the set doesn't cover gets the theme's plain file
or folder, so the tree is one family.

    npm pack material-icon-theme@5.39.0 && tar -xzf material-icon-theme-5.39.0.tgz
    python3 desktop/scripts/file-icons.py package
"""
import json
import shutil
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
ASSETS = ROOT / "desktop/Agentman/Resources/Assets.xcassets"
SWIFT = ROOT / "desktop/Agentman/Design/FileIconMap.swift"

# What agents' projects are made of: languages, their configs and tools,
# documents and media. Each is an icon in the theme.
FILES = """
file typescript typescript-def react react_ts javascript python go go-mod swift rust json markdown
readme nodejs npm next postcss tailwindcss css sass less html yaml toml docker git settings tune lock
console image svg xml database java kotlin ruby php c cpp h hpp csharp vue svelte astro graphql prisma
eslint prettier vite babel webpack tsconfig makefile license document pdf zip log lua dart zig gradle
jest vitest bun deno pnpm yarn powershell nix terraform proto http jupyter font video audio certificate
key vercel netlify firebase supabase github-actions-workflow scala perl elixir erlang haskell ocaml
clojure fsharp groovy objective-c objective-cpp assembly webassembly solidity matlab julia mdx tex
storybook cypress playwright turborepo editorconfig browserlist robots favicon authors changelog
contributing conduct todo diff r stylelint husky nginx shader email exe android dependabot codeowners
renovate swc rollup esbuild biome expo tauri wrangler
""".split()

# Folders worth telling apart at a glance, each with its open variant.
FOLDERS = """
folder folder-src folder-node folder-git folder-github folder-test folder-docs folder-public
folder-images folder-scripts folder-config folder-dist folder-components folder-lib folder-app
folder-api folder-utils folder-hook folder-tools folder-mobile folder-ios folder-android
folder-desktop folder-resource folder-vscode folder-temp folder-routes folder-server folder-client
folder-claude
""".split()

# What the theme leaves to VS Code's language detection.
EXTRA_NAMES = {
    ".env": "tune", ".env.local": "tune", ".env.example": "tune", ".env.development": "tune",
    ".env.production": "tune", ".env.test": "tune", "package.swift": "swift",
}


def main(package: Path) -> None:
    icons = package / "icons"
    manifest = json.loads((package / "dist/material-icons.json").read_text())
    light = manifest["light"]
    chosen = set(FILES) | set(FOLDERS) | {f"{name}-open" for name in FOLDERS if name != "folder"}
    chosen.add("folder-open")
    missing = sorted(name for name in chosen if not (icons / f"{name}.svg").exists())
    if missing:
        sys.exit(f"not in the theme: {', '.join(missing)}")

    for old in ASSETS.glob("FileIcon-*.imageset"):
        shutil.rmtree(old)
    for name in sorted(chosen):
        folder = ASSETS / f"FileIcon-{name}.imageset"
        folder.mkdir(parents=True)
        shutil.copy(icons / f"{name}.svg", folder / f"{name}.svg")
        images = [{"filename": f"{name}.svg", "idiom": "universal"}]
        # The theme draws for a dark editor; where that vanishes on a light
        # one it has a light variant, which is the default here.
        if (icons / f"{name}_light.svg").exists():
            shutil.copy(icons / f"{name}_light.svg", folder / f"{name}_light.svg")
            images = [
                {"filename": f"{name}_light.svg", "idiom": "universal"},
                {"filename": f"{name}.svg", "idiom": "universal",
                 "appearances": [{"appearance": "luminosity", "value": "dark"}]},
            ]
        (folder / "Contents.json").write_text(json.dumps({
            "images": images,
            "info": {"author": "xcode", "version": 1},
            "properties": {"preserves-vector-representation": True, "template-rendering-intent": "original"},
        }, indent=2) + "\n")

    def rules(section: str) -> dict:
        found = {}
        for key, icon in manifest[section].items():
            icon = icon.removesuffix("_light")
            if icon in chosen:
                found.setdefault(key.lower(), icon)
        return found

    names = rules("fileNames")
    names.update(EXTRA_NAMES)
    tables = {
        "fileNames": names,
        "extensions": rules("fileExtensions"),
        "folderNames": rules("folderNames"),
        "openFolderNames": rules("folderNamesExpanded"),
    }
    version = json.loads((package / "package.json").read_text())["version"]
    lines = [
        f"// Generated by desktop/scripts/file-icons.py from material-icon-theme {version}",
        "// (MIT, github.com/material-extensions/vscode-material-icon-theme). Don't edit.",
        "",
        "/// The theme's own rules for the icons in the asset catalogue, by lowercased",
        "/// file name, extension (\"d.ts\" as well as \"ts\") and folder name.",
        "enum FileIconMap {",
    ]
    for table, entries in tables.items():
        lines.append(f"    static let {table}: [String: String] = [")
        for key in sorted(entries):
            lines.append(f"        {json.dumps(key)}: {json.dumps(entries[key])},")
        lines.append("    ]")
    lines.append("}")
    SWIFT.write_text("\n".join(lines) + "\n")
    print(f"{len(chosen)} icons, " + ", ".join(f"{len(v)} {k}" for k, v in tables.items()))


if __name__ == "__main__":
    main(Path(sys.argv[1]))
