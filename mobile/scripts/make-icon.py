#!/usr/bin/env python3
"""Generate the app icon set.

The mark is an AM monogram whose two letters are kerned into one another until
they overlap, and the overlap is painted in a deeper tone. That intersection is
the whole idea: two things joined, which is what the product does — an agent on
one machine and a person on another, merged into one view.

Futura Bold because its A is a clean triangle and its M is splayed, so the two
interlock along a real diagonal rather than colliding.

Colours are taken from lib/theme.ts so the icon and the app agree.

    python3 scripts/make-icon.py
"""

from PIL import Image, ImageChops, ImageDraw, ImageFont

FONT = "/System/Library/Fonts/Supplemental/Futura.ttc"
FONT_INDEX = 2  # Futura Bold

INK = (9, 12, 18)
INK_TOP = (18, 25, 38)
LETTER = (77, 163, 255)
# The overlap stays a separate hue rather than a darker blue, so the join reads
# as its own thing instead of a shadow falling across the letters.
MERGE = (58, 146, 148)

SIZE = 1024
# Fraction of the canvas the monogram spans. iOS masks the corners, so the mark
# has to sit well inside the square or it reads as cramped.
MARK_WIDTH = 0.62
# How far M is pulled into A, as a fraction of A's width. Enough to force a real
# intersection; more than this and the A stops being readable.
OVERLAP = 0.253


def glyph_mask(character: str, point_size: int) -> Image.Image:
    """Render one character to a tightly cropped alpha mask."""
    font = ImageFont.truetype(FONT, point_size, index=FONT_INDEX)
    canvas = Image.new("L", (point_size * 3, point_size * 3), 0)
    ImageDraw.Draw(canvas).text(
        (point_size, point_size), character, font=font, fill=255
    )
    return canvas.crop(canvas.getbbox())


def monogram(size: int, width_fraction: float) -> Image.Image:
    """Build the interlocking AM as an RGBA layer on a transparent ground."""
    a = glyph_mask("A", 512)
    m = glyph_mask("M", 512)

    overlap = int(a.width * OVERLAP)
    raw_width = a.width + m.width - overlap
    raw_height = max(a.height, m.height)

    a_layer = Image.new("L", (raw_width, raw_height), 0)
    m_layer = Image.new("L", (raw_width, raw_height), 0)
    # Baselines align: both are cap-height letters, so bottom-align the crops.
    a_layer.paste(a, (0, raw_height - a.height))
    m_layer.paste(m, (a.width - overlap, raw_height - m.height))

    union = ImageChops.lighter(a_layer, m_layer)
    intersection = ImageChops.darker(a_layer, m_layer)

    mark = Image.new("RGBA", (raw_width, raw_height), (0, 0, 0, 0))
    mark.paste(Image.new("RGBA", mark.size, LETTER + (255,)), (0, 0), union)
    mark.paste(Image.new("RGBA", mark.size, MERGE + (255,)), (0, 0), intersection)

    target_width = int(size * width_fraction)
    scale = target_width / raw_width
    return mark.resize(
        (target_width, max(1, int(raw_height * scale))), Image.LANCZOS
    )


def background(size: int) -> Image.Image:
    """A near-black ground with a barely-there vertical lift.

    Flat #090C12 reads as dead on a phone screen; the gradient gives the mark
    something to sit on without becoming a visible effect in its own right.
    """
    base = Image.new("RGB", (size, size), INK)
    draw = ImageDraw.Draw(base)
    for y in range(size):
        t = y / (size - 1)
        draw.line(
            [(0, y), (size, y)],
            fill=tuple(
                round(INK_TOP[i] + (INK[i] - INK_TOP[i]) * t) for i in range(3)
            ),
        )
    return base


def centred(layer: Image.Image, size: int, ground: Image.Image | None) -> Image.Image:
    canvas = (
        ground.convert("RGBA")
        if ground is not None
        else Image.new("RGBA", (size, size), (0, 0, 0, 0))
    )
    canvas.alpha_composite(
        layer, ((size - layer.width) // 2, (size - layer.height) // 2)
    )
    return canvas


def main() -> None:
    import pathlib

    out = pathlib.Path(__file__).resolve().parent.parent / "assets" / "images"
    out.mkdir(parents=True, exist_ok=True)

    # iOS rejects an icon with an alpha channel outright.
    icon = centred(monogram(SIZE, MARK_WIDTH), SIZE, background(SIZE))
    icon.convert("RGB").save(out / "icon.png")

    # Android masks its adaptive foreground hard, so the mark has to be smaller.
    centred(monogram(SIZE, MARK_WIDTH * 0.66), SIZE, None).save(
        out / "adaptive-icon.png"
    )

    centred(monogram(SIZE, MARK_WIDTH * 0.72), SIZE, None).save(
        out / "splash-icon.png"
    )

    icon.convert("RGB").resize((48, 48), Image.LANCZOS).save(out / "favicon.png")

    for name in ("icon.png", "adaptive-icon.png", "splash-icon.png", "favicon.png"):
        written = out / name
        with Image.open(written) as opened:
            print(f"{name:20} {opened.size[0]}x{opened.size[1]} {opened.mode}")


if __name__ == "__main__":
    main()
