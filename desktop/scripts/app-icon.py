#!/usr/bin/env python3
"""Make the Mac app icon from the phone's.

An iOS icon is a full-bleed square that the system rounds. A Mac icon draws
its own rounded square, inset on a transparent canvas with room for a shadow,
so this places the phone's artwork in that shape at every size macOS asks for.

    python3 desktop/scripts/app-icon.py
"""
import json
from pathlib import Path

from PIL import Image, ImageDraw, ImageFilter

ROOT = Path(__file__).resolve().parents[2]
SOURCE = ROOT / "mobile/assets/images/icon.png"
OUT = ROOT / "desktop/Agentman/Resources/Assets.xcassets/AppIcon.appiconset"

CANVAS = 1024
TILE = 824            # Apple's grid: an 824pt rounded square on a 1024 canvas
RADIUS = 185          # about 22.5% of the tile, the macOS corner
inset = (CANVAS - TILE) // 2

art = Image.open(SOURCE).convert("RGBA").resize((TILE, TILE), Image.LANCZOS)
mask = Image.new("L", (TILE, TILE), 0)
ImageDraw.Draw(mask).rounded_rectangle((0, 0, TILE - 1, TILE - 1), RADIUS, fill=255)

shadow = Image.new("RGBA", (CANVAS, CANVAS), (0, 0, 0, 0))
ImageDraw.Draw(shadow).rounded_rectangle(
    (inset, inset + 12, inset + TILE, inset + TILE + 12), RADIUS, fill=(0, 0, 0, 90))
shadow = shadow.filter(ImageFilter.GaussianBlur(18))

icon = shadow
icon.paste(art, (inset, inset), mask)

OUT.mkdir(parents=True, exist_ok=True)
images = []
for points in (16, 32, 128, 256, 512):
    for scale in (1, 2):
        pixels = points * scale
        name = f"icon_{points}x{points}{'@2x' if scale == 2 else ''}.png"
        icon.resize((pixels, pixels), Image.LANCZOS).save(OUT / name)
        images.append({"filename": name, "idiom": "mac", "scale": f"{scale}x", "size": f"{points}x{points}"})
(OUT / "Contents.json").write_text(json.dumps({"images": images, "info": {"author": "xcode", "version": 1}}, indent=2) + "\n")
print("app icon:", len(images), "sizes")
