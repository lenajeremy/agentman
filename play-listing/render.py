#!/usr/bin/env python3
"""Render Play Store graphics using the landing page's titanium phone geometry."""

from __future__ import annotations

import argparse
from pathlib import Path

from PIL import Image, ImageDraw, ImageFilter, ImageFont, ImageOps


ROOT = Path(__file__).resolve().parent.parent
OUT = Path(__file__).resolve().parent / "assets"
S = 3  # Draw the 417 x 876 point landing-page device at 3x, then downsample.
PHONE = (417, 876)
SCREEN = (12, 12, 393, 852)
FONT = ROOT / "mobile/node_modules/@expo-google-fonts/geist"
MONO = ROOT / "mobile/node_modules/@expo-google-fonts/geist-mono"
INK = "#0F1012"
PAPER = "#F5F5F2"
ORANGE = "#FF6A2B"


def font(size: int, weight: str = "600SemiBold", mono: bool = False) -> ImageFont.FreeTypeFont:
    family = MONO if mono else FONT
    stem = "GeistMono" if mono else "Geist"
    return ImageFont.truetype(family / weight / f"{stem}_{weight}.ttf", size)


def rounded_mask(size: tuple[int, int], radius: int) -> Image.Image:
    mask = Image.new("L", size, 0)
    ImageDraw.Draw(mask).rounded_rectangle((0, 0, size[0] - 1, size[1] - 1), radius, fill=255)
    return mask


def rect(box: tuple[float, float, float, float]) -> tuple[int, int, int, int]:
    return tuple(round(value * S) for value in box)  # type: ignore[return-value]


def status_bar(screen: Image.Image) -> None:
    draw = ImageDraw.Draw(screen)
    draw.text((53 * S, 17 * S), "9:41", font=font(17 * S), fill=INK)
    draw.rounded_rectangle(rect((134, 11, 259, 48)), radius=19 * S, fill="#050506")
    draw.ellipse(rect((235, 23, 246, 34)), fill="#141A31")
    for x, height in ((304, 6), (311, 8), (318, 10), (325, 12)):
        draw.rounded_rectangle(rect((x, 30 - height, x + 4, 30)), radius=S, fill=INK)
    draw.arc(rect((338, 19, 355, 34)), 205, 335, fill=INK, width=2 * S)
    draw.arc(rect((342, 24, 351, 33)), 205, 335, fill=INK, width=2 * S)
    draw.ellipse(rect((346, 31, 348, 33)), fill=INK)
    draw.rounded_rectangle(rect((364, 20, 387, 33)), radius=4 * S, outline=INK, width=S)
    draw.rounded_rectangle(rect((366, 22, 383, 31)), radius=2 * S, fill=INK)
    draw.rounded_rectangle(rect((387, 24, 390, 29)), radius=S, fill=INK)
    draw.rounded_rectangle(rect((129, 839, 264, 844)), radius=3 * S, fill=INK)


def phone(screenshot: Path, content_only: bool) -> Image.Image:
    """Build the site's 417 x 876 phone with any screenshot under the glass."""
    width, height = (dimension * S for dimension in PHONE)
    result = Image.new("RGBA", (width, height), (0, 0, 0, 0))
    draw = ImageDraw.Draw(result)

    # Hardware buttons and layered titanium edge follow site/styles.css.
    for box in ((0, 132, 5, 166), (0, 196, 5, 260), (0, 274, 5, 338), (412, 220, 417, 322)):
        draw.rounded_rectangle(rect(box), radius=2 * S, fill="#67676B")
    draw.rounded_rectangle(rect((3, 0, 414, 876)), radius=67 * S, fill="#444448")
    draw.rounded_rectangle(rect((5, 2, 412, 874)), radius=65 * S, fill="#222225", outline="#77777A", width=S)
    draw.rounded_rectangle(rect((7, 4, 410, 872)), radius=64 * S, fill="#050506")

    sx, sy, sw, sh = SCREEN
    sw *= S
    sh *= S
    screen = Image.new("RGB", (sw, sh), "white")
    with Image.open(screenshot) as opened:
        source = ImageOps.exif_transpose(opened).convert("RGB")
    if content_only:
        content = ImageOps.fit(source, (sw, (852 - 59) * S), Image.Resampling.LANCZOS)
        screen.paste(content, (0, 59 * S))
        status_bar(screen)
    else:
        screen.paste(ImageOps.fit(source, (sw, sh), Image.Resampling.LANCZOS))
    result.paste(screen, (sx * S, sy * S), rounded_mask((sw, sh), 55 * S))

    # A soft glass highlight, like the landing page's .screen::after.
    glare = Image.new("RGBA", (sw, sh), (0, 0, 0, 0))
    overlay = ImageDraw.Draw(glare)
    for x in range(0, 85 * S, 3 * S):
        opacity = round(15 * (1 - x / (85 * S)))
        overlay.line((x, 0, max(0, x - 42 * S), sh), fill=(255, 255, 255, opacity), width=3 * S)
    glare.putalpha(Image.composite(glare.getchannel("A"), Image.new("L", (sw, sh), 0), rounded_mask((sw, sh), 55 * S)))
    result.alpha_composite(glare, (sx * S, sy * S))
    return result


def fit_headline(draw: ImageDraw.ImageDraw, words: str, max_width: int, max_size: int) -> tuple[str, ImageFont.FreeTypeFont]:
    for size in range(max_size, 40, -2):
        face = font(size)
        if draw.textbbox((0, 0), words, font=face)[2] <= max_width:
            return words, face
    raise ValueError(f"Headline is too wide for the canvas: {words}")


def phone_asset(screenshot: Path, output: Path, headline: str, content_only: bool) -> None:
    canvas = Image.new("RGB", (1080, 1920), PAPER)
    draw = ImageDraw.Draw(canvas)
    draw.ellipse((690, 260, 1340, 910), fill="#E5EAFF")
    draw.ellipse((-250, 1330, 270, 1850), fill="#FFE9DD")
    draw.text((80, 66), "AGENTMAN", font=font(24, "500Medium", mono=True), fill="#59616F")
    title, face = fit_headline(draw, headline, 930, 70)
    draw.text((76, 122), title, font=face, fill=INK)
    draw.rounded_rectangle((80, 229, 168, 237), radius=4, fill=ORANGE)

    device = phone(screenshot, content_only)
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
    args = parser.parse_args()
    if args.command == "build":
        build()
    else:
        phone_asset(args.input, args.output, args.headline, args.content_only)


if __name__ == "__main__":
    main()
