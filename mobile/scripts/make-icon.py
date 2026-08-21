#!/usr/bin/env python3
"""Generate the app icon set.

The mark is an AM monogram. A and M are drawn as complete, independent letters
— the A keeps its own right leg, the M keeps its own left stem — and the two
are placed so those strokes coincide exactly, left edge on left edge and right
edge on right edge. Nothing is shared and nothing is trimmed to fit; they are
separate lines that land in precisely the same place.

The green is the intersection of the two letters, computed rather than drawn.
Because the strokes coincide exactly, that intersection is the whole stroke and
nothing else, which `verify` asserts on every run: if the two ever drift, the
green region stops matching the stroke and the build fails loudly instead of
quietly producing slivers.

Drawn from polygons rather than set in a typeface because no real face allows
this. Measured against Futura: descending the cap height, an A's right leg
travels right while an M's left stem travels left, so they cross rather than
coincide and one letter always protrudes past the other near the baseline.

Colours come from lib/theme.ts so the icon and the app agree.

    python3 scripts/make-icon.py
"""

import pathlib

from PIL import Image, ImageChops, ImageDraw

INK = (9, 12, 18)
INK_TOP = (18, 25, 38)
# Letters run blue to cyan down their height; where they coincide is the
# theme's green, so the join reads as its own element rather than a shadow.
LETTER_TOP = (77, 163, 255)
LETTER_BOTTOM = (69, 224, 232)
JOIN = (82, 223, 163)

SIZE = 1024
# Fraction of the canvas the mark spans. iOS masks the corners, so it has to sit
# well inside the square or it reads as cramped.
MARK_WIDTH = 0.72

CAP = 1000  # cap height in mark units; everything else is relative to it
STROKE = 190
# How far a stroke travels sideways over the cap height. Tuned by eye against
# 170 and 250: tighter crowds the A's counter shut, wider slackens the M until
# its stems stop reading as a pair.
SLANT = 210
# Where the A's crossbar sits, as a fraction of cap height.
CROSSBAR = 0.70
# Rendered at this multiple and downsampled — polygon fills are not antialiased.
SUPERSAMPLE = 4

# The one position both letters must agree on: the A's right leg and the M's
# left stem are each specified from these, so they cannot drift apart.
APEX = 320.0
JOIN_FOOT = APEX + SLANT


def _lerp(start: float, end: float, fraction: float) -> float:
    return start + (end - start) * fraction


def _stroke(top_x: float, bottom_x: float) -> list[tuple[float, float]]:
    """One slanted stroke of full cap height, centred on the given x positions."""
    half = STROKE / 2
    return [
        (top_x - half, 0),
        (top_x + half, 0),
        (bottom_x + half, CAP),
        (bottom_x - half, CAP),
    ]


def join_stroke() -> list[tuple[float, float]]:
    """The stroke the A's right leg and the M's left stem both occupy."""
    return _stroke(APEX, JOIN_FOOT)


def letter_a() -> list[list[tuple[float, float]]]:
    """A complete A: left leg, right leg, crossbar."""
    half = STROKE / 2
    left_leg = [(0, CAP), (STROKE, CAP), (APEX + half, 0), (APEX - half, 0)]

    # The crossbar spans the gap between the legs, so its ends follow them
    # rather than sitting at fixed x.
    def left_inner(y: float) -> float:
        return _lerp(APEX + half, STROKE, y / CAP)

    def right_inner(y: float) -> float:
        return _lerp(APEX - half, JOIN_FOOT - half, y / CAP)

    top, bottom = CROSSBAR * CAP, CROSSBAR * CAP + STROKE * 0.78
    crossbar = [
        (left_inner(top), top),
        (right_inner(top), top),
        (right_inner(bottom), bottom),
        (left_inner(bottom), bottom),
    ]
    return [left_leg, join_stroke(), crossbar]


def letter_m() -> list[list[tuple[float, float]]]:
    """A complete M: left stem, inner descent, inner ascent, right stem."""
    half = STROKE / 2
    # The inner vertex clears the left stem's foot before descending, or the
    # M's counter closes up.
    vertex = JOIN_FOOT + SLANT + 60
    peak = vertex + SLANT
    right_foot = peak + SLANT
    return [
        join_stroke(),
        _stroke(APEX, vertex),
        [(vertex - half, CAP), (vertex + half, CAP), (peak + half, 0), (peak - half, 0)],
        _stroke(peak, right_foot),
    ]


def mark_width() -> float:
    return max(x for polygon in letter_m() for x, _ in polygon)


def _fill(polygons: list[list[tuple[float, float]]], size: tuple[int, int],
          scale: int) -> Image.Image:
    layer = Image.new("L", size, 0)
    draw = ImageDraw.Draw(layer)
    for polygon in polygons:
        draw.polygon([(x * scale, y * scale) for x, y in polygon], fill=255)
    return layer


def masks(scale: int) -> tuple[Image.Image, Image.Image, tuple[int, int]]:
    """Return (silhouette, join, canvas size).

    The join is the intersection of the two letters, not a drawn shape.
    """
    size = (int(mark_width() * scale), int(CAP * scale))
    a = _fill(letter_a(), size, scale)
    m = _fill(letter_m(), size, scale)
    return ImageChops.lighter(a, m), ImageChops.darker(a, m), size


def verify() -> None:
    """Assert the two letters coincide on exactly one stroke and nothing else.

    This is the property the design rests on. Checked here rather than by eye
    because a few stray pixels along a diagonal are invisible at review size and
    obvious once the icon is on a phone.
    """
    scale = SUPERSAMPLE
    _, join, size = masks(scale)
    expected = _fill([join_stroke()], size, scale)
    difference = ImageChops.difference(join, expected)
    stray = sum(count for value, count in enumerate(difference.histogram()) if value > 8)
    if stray:
        raise SystemExit(
            f"make-icon: the A and M do not coincide exactly — {stray} stray pixels. "
            "Their shared stroke must be specified from APEX and JOIN_FOOT alone."
        )


def gradient(size: tuple[int, int], top: tuple[int, int, int],
             bottom: tuple[int, int, int]) -> Image.Image:
    """A vertical two-stop ramp."""
    ramp = Image.new("RGBA", size)
    draw = ImageDraw.Draw(ramp)
    for y in range(size[1]):
        fraction = y / max(1, size[1] - 1)
        draw.line(
            [(0, y), (size[0], y)],
            fill=tuple(round(_lerp(top[i], bottom[i], fraction)) for i in range(3)) + (255,),
        )
    return ramp


def mark(target_width: int) -> Image.Image:
    """The monogram as an RGBA layer on a transparent ground."""
    silhouette, join, size = masks(SUPERSAMPLE)
    layer = Image.new("RGBA", size, (0, 0, 0, 0))
    layer.paste(gradient(size, LETTER_TOP, LETTER_BOTTOM), (0, 0), silhouette)
    layer.paste(Image.new("RGBA", size, JOIN + (255,)), (0, 0), join)
    height = max(1, round(target_width * size[1] / size[0]))
    return layer.resize((target_width, height), Image.LANCZOS)


def background(size: int) -> Image.Image:
    """A near-black ground with a barely-there vertical lift.

    Flat ink reads as dead on a phone screen; the gradient gives the mark
    something to sit on without becoming a visible effect in its own right.
    """
    return gradient((size, size), INK_TOP, INK).convert("RGB")


def compose(size: int, width_fraction: float, ground: Image.Image | None) -> Image.Image:
    layer = mark(int(size * width_fraction))
    canvas = (
        ground.convert("RGBA")
        if ground is not None
        else Image.new("RGBA", (size, size), (0, 0, 0, 0))
    )
    canvas.alpha_composite(layer, ((size - layer.width) // 2, (size - layer.height) // 2))
    return canvas


def main() -> None:
    verify()

    out = pathlib.Path(__file__).resolve().parent.parent / "assets" / "images"
    out.mkdir(parents=True, exist_ok=True)

    # iOS rejects an icon carrying an alpha channel outright.
    icon = compose(SIZE, MARK_WIDTH, background(SIZE))
    icon.convert("RGB").save(out / "icon.png")

    # Android masks its adaptive foreground hard, so the mark has to be smaller.
    compose(SIZE, MARK_WIDTH * 0.66, None).save(out / "adaptive-icon.png")
    compose(SIZE, MARK_WIDTH * 0.72, None).save(out / "splash-icon.png")
    icon.convert("RGB").resize((48, 48), Image.LANCZOS).save(out / "favicon.png")

    print("join verified: the A and M coincide on exactly one stroke")
    for name in ("icon.png", "adaptive-icon.png", "splash-icon.png", "favicon.png"):
        with Image.open(out / name) as written:
            print(f"{name:20} {written.size[0]}x{written.size[1]} {written.mode}")


if __name__ == "__main__":
    main()
