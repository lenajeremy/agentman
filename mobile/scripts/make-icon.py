#!/usr/bin/env python3
"""Generate the app icon set.

The mark is an AM ligature: one stroke serves as both the A's right leg and the
M's left stem, and that shared stroke is painted green. Two letters built from
one piece of shared geometry is the product — an agent on one machine and a
person on another, looking at the same thing.

Drawn from polygons rather than set in a typeface. Overlaying a real A and M
cannot produce a shared edge: in every geometric face their adjacent legs slope
in opposite directions, so going down the page they diverge and leave slivers
of one letter protruding past the other. Constructing the mark makes the shared
stroke exact by definition.

Colours come from lib/theme.ts so the icon and the app agree.

    python3 scripts/make-icon.py
"""

import pathlib

from PIL import Image, ImageDraw

INK = (9, 12, 18)
INK_TOP = (18, 25, 38)
# Letters run blue to cyan down their height; the shared stroke is the theme's
# green so the join reads as its own element rather than a shadow.
LETTER_TOP = (77, 163, 255)
LETTER_BOTTOM = (69, 224, 232)
SHARED = (82, 223, 163)

SIZE = 1024
# Fraction of the canvas the mark spans. iOS masks the corners, so it has to sit
# well inside the square or it reads as cramped.
MARK_WIDTH = 0.72

CAP = 1000  # cap height in mark units; everything else is relative to it
STROKE = 190
# How far each stroke travels sideways over the cap height. Tuned by eye against
# 170 and 250: tighter crowds the A's counter shut, wider slackens the M until
# its stems stop reading as a pair.
SLANT = 210
# Where the A's crossbar sits, as a fraction of cap height. Low enough to leave
# the counter open once the legs have converged.
CROSSBAR = 0.70
# Rendered at this multiple and downsampled, which is what keeps the diagonals
# clean — polygon fills are not antialiased.
SUPERSAMPLE = 4


def _lerp(start: float, end: float, fraction: float) -> float:
    return start + (end - start) * fraction


def _stroke(top_x: float, bottom_x: float) -> list[tuple[float, float]]:
    """One slanted stroke, full cap height, centred on the given x positions."""
    half = STROKE / 2
    return [
        (top_x - half, 0),
        (top_x + half, 0),
        (bottom_x + half, CAP),
        (bottom_x - half, CAP),
    ]


def ligature() -> tuple[list[list[tuple[float, float]]], list[tuple[float, float]], float]:
    """Return (letter polygons, the shared stroke, total width).

    The shared stroke is returned separately because it is drawn twice: once as
    part of the silhouette, once in its own colour on top.
    """
    apex = 320.0
    shared_foot = apex + SLANT
    # The M's inner vertex clears the shared stroke's foot before descending,
    # or its counter closes up.
    vertex = shared_foot + SLANT + 60
    peak = vertex + SLANT
    right_foot = peak + SLANT

    shared = _stroke(apex, shared_foot)

    half = STROKE / 2
    a_left_leg = [(0, CAP), (STROKE, CAP), (apex + half, 0), (apex - half, 0)]

    # The crossbar spans the gap between the two legs, so its ends have to
    # follow them rather than sit at fixed x.
    def left_leg_inner(y: float) -> float:
        return _lerp(apex + half, STROKE, y / CAP)

    def shared_inner(y: float) -> float:
        return _lerp(apex - half, shared_foot - half, y / CAP)

    top, bottom = CROSSBAR * CAP, CROSSBAR * CAP + STROKE * 0.78
    crossbar = [
        (left_leg_inner(top), top),
        (shared_inner(top), top),
        (shared_inner(bottom), bottom),
        (left_leg_inner(bottom), bottom),
    ]

    letters = [
        a_left_leg,
        crossbar,
        _stroke(apex, vertex),  # M's inner descent, from the shared stem's top
        [  # M's inner ascent, rising from the vertex at the baseline
            (vertex - half, CAP),
            (vertex + half, CAP),
            (peak + half, 0),
            (peak - half, 0),
        ],
        _stroke(peak, right_foot),
    ]
    return letters, shared, right_foot + half


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
    """The ligature as an RGBA layer on a transparent ground."""
    letters, shared, width = ligature()
    scale = SUPERSAMPLE
    canvas_size = (int(width * scale), int(CAP * scale))

    def fill(polygons: list[list[tuple[float, float]]]) -> Image.Image:
        layer = Image.new("L", canvas_size, 0)
        draw = ImageDraw.Draw(layer)
        for polygon in polygons:
            draw.polygon([(x * scale, y * scale) for x, y in polygon], fill=255)
        return layer

    silhouette = fill(letters + [shared])
    join = fill([shared])

    layer = Image.new("RGBA", canvas_size, (0, 0, 0, 0))
    layer.paste(gradient(canvas_size, LETTER_TOP, LETTER_BOTTOM), (0, 0), silhouette)
    layer.paste(Image.new("RGBA", canvas_size, SHARED + (255,)), (0, 0), join)

    height = max(1, round(target_width * canvas_size[1] / canvas_size[0]))
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
    out = pathlib.Path(__file__).resolve().parent.parent / "assets" / "images"
    out.mkdir(parents=True, exist_ok=True)

    # iOS rejects an icon carrying an alpha channel outright.
    icon = compose(SIZE, MARK_WIDTH, background(SIZE))
    icon.convert("RGB").save(out / "icon.png")

    # Android masks its adaptive foreground hard, so the mark has to be smaller.
    compose(SIZE, MARK_WIDTH * 0.66, None).save(out / "adaptive-icon.png")
    compose(SIZE, MARK_WIDTH * 0.72, None).save(out / "splash-icon.png")
    icon.convert("RGB").resize((48, 48), Image.LANCZOS).save(out / "favicon.png")

    for name in ("icon.png", "adaptive-icon.png", "splash-icon.png", "favicon.png"):
        with Image.open(out / name) as written:
            print(f"{name:20} {written.size[0]}x{written.size[1]} {written.mode}")


if __name__ == "__main__":
    main()
