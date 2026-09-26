#!/usr/bin/env python3
"""Render Play Store graphics using the captured Device Hub phone frame."""

from __future__ import annotations

import argparse
from pathlib import Path

from PIL import Image, ImageChops, ImageDraw, ImageFilter, ImageFont, ImageOps


ROOT = Path(__file__).resolve().parent.parent
OUT = Path(__file__).resolve().parent / "assets"
FRAME_CAPTURE = Path(__file__).resolve().parent / "sources/devicehub-pairing.png"
# Display boundaries measured from the captured iPhone frame, in source pixels.
DISPLAY = (38, 32, 810, 1707)
DISPLAY_RADIUS = 113
STATUS_BOTTOM = 145
CONTENT_BOTTOM = 1620
FONT = ROOT / "mobile/node_modules/@expo-google-fonts/geist"
MONO = ROOT / "mobile/node_modules/@expo-google-fonts/geist-mono"
INK = "#0F1012"
PAPER = "#F5F5F2"
ORANGE = "#FF6A2B"


def font(size: int, weight: str = "600SemiBold", mono: bool = False) -> ImageFont.FreeTypeFont:
    family = MONO if mono else FONT
    stem = "GeistMono" if mono else "Geist"
    return ImageFont.truetype(family / weight / f"{stem}_{weight}.ttf", size)


def display_mask(size: tuple[int, int]) -> Image.Image:
    scale = 4
    mask = Image.new("L", (size[0] * scale, size[1] * scale), 0)
    ImageDraw.Draw(mask).rounded_rectangle(
        tuple(value * scale for value in DISPLAY), radius=DISPLAY_RADIUS * scale, fill=255
    )
    return mask.resize(size, Image.Resampling.LANCZOS)


def phone(screenshot: Path, content_only: bool) -> Image.Image:
    """Put a screenshot inside the real bezel, with safe space for system UI."""
    frame = devicehub_cutout(FRAME_CAPTURE)
    with Image.open(screenshot) as opened:
        source = ImageOps.exif_transpose(opened).convert("RGB")
    x0, y0, x1, y1 = DISPLAY
    # Match the screenshot's own page background in the status and safe areas.
    page_background = source.getpixel((0, 0))
    display = Image.new("RGB", frame.size, page_background)
    if content_only:
        width, height = x1 - x0 - 8, CONTENT_BOTTOM - STATUS_BOTTOM
        content = ImageOps.contain(source, (width, height), Image.Resampling.LANCZOS)
        display.paste(content, ((frame.width - content.width) // 2, STATUS_BOTTOM))
        # Keep the real, correctly positioned island and status icons.
        status = frame.crop((0, y0, frame.width, STATUS_BOTTOM)).convert("RGB")
        pixels = status.load()
        assert pixels is not None
        for y in range(status.height):
            for x in range(x0, x1):
                r, g, b = pixels[x, y]
                if 239 <= r <= 249 and abs(r - g) <= 2 and 0 <= r - b <= 7:
                    pixels[x, y] = page_background
        display.paste(status, (0, y0))
        ImageDraw.Draw(display).rounded_rectangle((355, 1662, 493, 1668), radius=3, fill=INK)
    else:
        content = ImageOps.fit(source, (x1 - x0, y1 - y0), Image.Resampling.LANCZOS)
        display.paste(content, (x0, y0))
    mask = display_mask(frame.size)
    display.putalpha(mask)
    # Keep the bezel, buttons, and their antialiasing from the real capture.
    frame.putalpha(ImageChops.subtract(frame.getchannel("A"), mask))
    return Image.alpha_composite(display, frame)


def fit_headline(draw: ImageDraw.ImageDraw, words: str, max_width: int, max_size: int) -> tuple[str, ImageFont.FreeTypeFont]:
    for size in range(max_size, 40, -2):
        face = font(size)
        if draw.textbbox((0, 0), words, font=face)[2] <= max_width:
            return words, face
    raise ValueError(f"Headline is too wide for the canvas: {words}")


def phone_asset(screenshot: Path, output: Path, headline: str, content_only: bool) -> None:
    compose_phone_asset(phone(screenshot, content_only), output, headline)


def compose_phone_asset(device: Image.Image, output: Path, headline: str) -> None:
    canvas = Image.new("RGB", (1080, 1920), PAPER)
    draw = ImageDraw.Draw(canvas)
    draw.ellipse((690, 260, 1340, 910), fill="#E5EAFF")
    draw.ellipse((-250, 1330, 270, 1850), fill="#FFE9DD")
    draw.text((80, 66), "AGENTMAN", font=font(24, "500Medium", mono=True), fill="#59616F")
    title, face = fit_headline(draw, headline, 930, 70)
    draw.text((76, 122), title, font=face, fill=INK)
    draw.rounded_rectangle((80, 229, 168, 237), radius=4, fill=ORANGE)

    device.thumbnail((766, 1608), Image.Resampling.LANCZOS)
    x = (canvas.width - device.width) // 2
    y = 272
    shadow = Image.new("RGBA", canvas.size, (0, 0, 0, 0))
    shape = ImageDraw.Draw(shadow)
    shape.rounded_rectangle((x + 22, y + 45, x + device.width + 15, y + device.height + 65), radius=115, fill=(17, 19, 34, 85))
    shadow = shadow.filter(ImageFilter.GaussianBlur(48))
    canvas = Image.alpha_composite(canvas.convert("RGBA"), shadow)
    canvas.alpha_composite(device, (x, y))
    output.parent.mkdir(parents=True, exist_ok=True)
    canvas.convert("RGB").save(output, "PNG", optimize=True)


def devicehub_cutout(capture: Path) -> Image.Image:
    """Remove the entire canvas outside a Device Hub phone, preserving its pixels."""
    with Image.open(capture) as opened:
        source = ImageOps.exif_transpose(opened).convert("RGB")
    result = source.convert("RGBA")
    original = source.load()
    output = result.load()
    assert original is not None and output is not None
    for y in range(source.height):
        # The dark bezel surrounds the display. Find its outermost point on
        # this scanline; side-button rows naturally expand to include buttons.
        bezel = [x for x in range(source.width) if max(original[x, y]) < 190]
        if not bezel:
            for x in range(source.width):
                output[x, y] = (0, 0, 0, 0)
            continue
        left, right = bezel[0], bezel[-1]
        for x in (*range(left), *range(right + 1, source.width)):
            # The source canvas is white. Recover a soft dark edge from the
            # antialiased pixels rather than leaving a white fringe.
            alpha = 255 - min(original[x, y])
            output[x, y] = (0, 0, 0, alpha)
    return result


def feature_asset(screenshot: Path, output: Path) -> None:
    canvas = Image.new("RGB", (1024, 500), INK)
    draw = ImageDraw.Draw(canvas)
    draw.ellipse((-190, 250, 260, 700), fill="#1D2340")
    draw.ellipse((830, -250, 1320, 240), fill="#302220")
    draw.text((56, 49), "AGENTMAN", font=font(18, "500Medium", mono=True), fill="#CDD1DD")
    draw.text((53, 118), "Your agents,", font=font(60), fill="white")
    draw.text((53, 184), "within reach.", font=font(60), fill="white")
    draw.rounded_rectangle((57, 299, 117, 306), radius=3, fill=ORANGE)
    draw.text((55, 336), "Follow. Answer. Review.", font=font(24, "400Regular"), fill="#B6BBC9")
    device = phone(screenshot, content_only=True)
    device.thumbnail((355, 750), Image.Resampling.LANCZOS)
    shadow = Image.new("RGBA", canvas.size, (0, 0, 0, 0))
    ImageDraw.Draw(shadow).rounded_rectangle((648, 63, 1025, 783), radius=55, fill=(0, 0, 0, 160))
    canvas = Image.alpha_composite(canvas.convert("RGBA"), shadow.filter(ImageFilter.GaussianBlur(24)))
    canvas.alpha_composite(device, (675, 36))
    output.parent.mkdir(parents=True, exist_ok=True)
    canvas.convert("RGB").save(output, "PNG", optimize=True)


def build() -> None:
    sources = ROOT / "site/images"
    items = (
        ("app-agents.png", "01-agents.png", "Know what needs you"),
        ("app-session.png", "02-session.png", "Follow every turn"),
        ("app-question.png", "03-questions.png", "Answer in one tap"),
    )
    for source, name, headline in items:
        phone_asset(sources / source, OUT / name, headline, content_only=True)
    capture = Path(__file__).resolve().parent / "sources/devicehub-pairing.png"
    if capture.exists():
        cutout = devicehub_cutout(capture)
        cutout.save(OUT / "devicehub-pairing-cutout.png", "PNG", optimize=True)
        compose_phone_asset(cutout, OUT / "04-pairing-devicehub.png", "Connect to your Mac")
    feature_asset(sources / "app-agents.png", OUT / "feature-graphic.png")
    with Image.open(ROOT / "mobile/assets/images/icon.png") as source:
        icon = source.convert("RGBA").resize((512, 512), Image.Resampling.LANCZOS)
        icon.save(OUT / "play-icon.png", "PNG", optimize=True)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    commands.add_parser("build", help="Regenerate the complete Play asset pack")
    custom = commands.add_parser("phone", help="Put any screenshot in the reusable phone frame")
    custom.add_argument("input", type=Path)
    custom.add_argument("output", type=Path)
    custom.add_argument("--headline", required=True)
    custom.add_argument("--content-only", action="store_true", help="Input excludes the status bar")
    framed = commands.add_parser("devicehub", help="Remove the full Device Hub canvas and use the real phone frame")
    framed.add_argument("input", type=Path)
    framed.add_argument("output", type=Path)
    framed.add_argument("--headline", required=True)
    cutout = commands.add_parser("cutout", help="Save the real Device Hub phone with a transparent exterior")
    cutout.add_argument("input", type=Path)
    cutout.add_argument("output", type=Path)
    args = parser.parse_args()
    if args.command == "build":
        build()
    elif args.command == "phone":
        phone_asset(args.input, args.output, args.headline, args.content_only)
    elif args.command == "devicehub":
        compose_phone_asset(devicehub_cutout(args.input), args.output, args.headline)
    else:
        args.output.parent.mkdir(parents=True, exist_ok=True)
        devicehub_cutout(args.input).save(args.output, "PNG", optimize=True)


if __name__ == "__main__":
    main()
