#!/usr/bin/env python3
"""Make the app's agent logos from the phone's.

mobile/components/agentMarks.ts holds each agent's mark as 24x24 SVG paths,
generated from their sources and credited there. This writes one image set
per agent into the asset catalogue, so the Mac app shows exactly the marks the
phone does. A mark with no brand colour follows the text colour on the phone,
so here it is a template image, which SwiftUI tints the same way. Antigravity
ships its own artwork, which is copied as it is.

    python3 desktop/scripts/agent-marks.py
"""
import json
import re
import shutil
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
SOURCE = ROOT / "mobile/components/agentMarks.ts"
ASSETS = ROOT / "desktop/Agentman/Resources/Assets.xcassets"

text = SOURCE.read_text()
body = text[text.index("export const agentMarks"):]
blocks = re.finditer(r'\n  "?([a-z-]+)"?: \{(.*?)\n  \},', body, re.S)

made = []
for block in blocks:
    kind, inner = block.group(1), block.group(2)
    folder = ASSETS / f"Agent-{kind}.imageset"
    if folder.exists():
        shutil.rmtree(folder)
    folder.mkdir(parents=True)
    image = re.search(r'require\("([^"]+)"\)', inner)
    if image:
        png = (SOURCE.parent / image.group(1)).resolve()
        shutil.copy(png, folder / f"{kind}.png")
        contents = {"images": [{"filename": f"{kind}.png", "idiom": "universal"}],
                    "info": {"author": "xcode", "version": 1}}
    else:
        color = re.search(r'color: (null|"(#[0-9A-Fa-f]{6})")', inner)
        fill = color.group(2) if color and color.group(2) else "#000000"
        paths = re.findall(r'\{ d: "([^"]+)"(, evenOdd: true)? \}', inner)
        svg = ['<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" width="24" height="24">']
        for d, even_odd in paths:
            rule = ' fill-rule="evenodd"' if even_odd else ""
            svg.append(f'  <path d="{d}" fill="{fill}"{rule}/>')
        svg.append("</svg>\n")
        (folder / f"{kind}.svg").write_text("\n".join(svg))
        contents = {
            "images": [{"filename": f"{kind}.svg", "idiom": "universal"}],
            "info": {"author": "xcode", "version": 1},
            "properties": {"preserves-vector-representation": True,
                           "template-rendering-intent": "original" if color and color.group(2) else "template"},
        }
    (folder / "Contents.json").write_text(json.dumps(contents, indent=2) + "\n")
    made.append(kind)

print("agent marks:", ", ".join(made))
