#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = ["pillow==11.3.0"]
# ///
"""Render the social preview image for things.rlew.io.

Usage: scripts/og/generate.py [output.png]

Writes docs/static/img/og.png by default. Fonts are the ones the site
uses, fetched once from a pinned google/fonts commit into a cache directory.
Set FONT_DIR to use local copies instead.
"""

import os
import sys
import urllib.request
from pathlib import Path

from PIL import Image, ImageDraw, ImageFilter, ImageFont

FONTS_COMMIT = "5e35378e6bda803962ee6fd257e444a7d459660d"
FONTS = {
    "InstrumentSans.ttf": "ofl/instrumentsans/InstrumentSans%5Bwdth%2Cwght%5D.ttf",
    "IBMPlexMono-Regular.ttf": "ofl/ibmplexmono/IBMPlexMono-Regular.ttf",
    "IBMPlexMono-Medium.ttf": "ofl/ibmplexmono/IBMPlexMono-Medium.ttf",
}

W, H = 1200, 630
S = 2  # render at 2x, downsample for crisp text

BG = (14, 14, 16)
TEXT = (236, 236, 234)
MUTED = (163, 163, 168)
DIM = (107, 107, 112)
ACCENT = (16, 185, 129)
MARK = (156, 163, 175)
PANEL = (22, 22, 24)
BORDER = (42, 42, 46)


def font_dir() -> Path:
    override = os.environ.get("FONT_DIR")
    if override:
        return Path(override)
    cache = Path.home() / "Library" / "Caches" / "things-cli" / "og-fonts" / FONTS_COMMIT[:12]
    cache.mkdir(parents=True, exist_ok=True)
    base = f"https://raw.githubusercontent.com/google/fonts/{FONTS_COMMIT}/"
    for name, path in FONTS.items():
        target = cache / name
        if not target.exists():
            print(f"fetching {name}", file=sys.stderr)
            urllib.request.urlretrieve(base + path, target)
    return cache


def load(fonts: Path, name: str, size: int, variation: str | None = None) -> ImageFont.FreeTypeFont:
    f = ImageFont.truetype(str(fonts / name), size * S)
    if variation:
        f.set_variation_by_name(variation)
    return f


def tracked(draw: ImageDraw.ImageDraw, xy, text, font, fill, tracking=0.0):
    """Draw text with letter-spacing (em fraction, negative tightens)."""
    x, y = xy
    step = tracking * font.size
    for ch in text:
        draw.text((x, y), ch, font=font, fill=fill)
        x += font.getlength(ch) + step
    return x


def glow(canvas, box, colour, alpha):
    """A soft ellipse on a transparent canvas the size of the image."""
    layer = Image.new("RGBA", canvas, (0, 0, 0, 0))
    ImageDraw.Draw(layer).ellipse(box, fill=colour + (alpha,))
    return layer.filter(ImageFilter.GaussianBlur(90 * S))


def main() -> None:
    out = Path(sys.argv[1]) if len(sys.argv) > 1 else Path(__file__).resolve().parents[2] / "docs" / "static" / "img" / "og.png"
    fonts = font_dir()
    head = load(fonts, "InstrumentSans.ttf", 84, "SemiBold")
    sub = load(fonts, "InstrumentSans.ttf", 30, "Regular")
    mono = load(fonts, "IBMPlexMono-Medium.ttf", 26)
    mono_sm = load(fonts, "IBMPlexMono-Regular.ttf", 24)

    img = Image.new("RGBA", (W * S, H * S), BG + (255,))

    # Emerald glow, top centre, like the landing page hero.
    cx = W * S / 2
    img.alpha_composite(glow(img.size, (cx - 520 * S, -340 * S, cx + 520 * S, 250 * S), ACCENT, 50))

    d = ImageDraw.Draw(img)
    m = 88 * S  # margin

    # Mark + wordmark.
    box = 44 * S
    d.rounded_rectangle((m, m, m + box, m + box), radius=10 * S, outline=MARK, width=3 * S)
    d.line([(m + 12 * S, m + 23 * S), (m + 19 * S, m + 30 * S), (m + 31 * S, m + 15 * S)],
           fill=ACCENT, width=4 * S, joint="curve")
    d.text((m + box + 16 * S, m + 8 * S), "things-cli", font=mono, fill=TEXT)

    # Headline.
    y = 198 * S
    tracked(d, (m - 3 * S, y), "Things 3 from your terminal,", head, TEXT, tracking=-0.035)
    tracked(d, (m - 3 * S, y + 96 * S), "or from your agent.", head, ACCENT, tracking=-0.035)

    # Sub line.
    d.text((m, 428 * S), "Tasks from the shell. JSON for scripts. A skill for your agent.", font=sub, fill=MUTED)

    # Install pill, bottom left.
    py = 512 * S
    label = "$ brew install ryanlewis/tap/things"
    pw = int(mono_sm.getlength(label)) + 44 * S
    d.rounded_rectangle((m, py, m + pw, py + 54 * S), radius=10 * S, fill=PANEL, outline=BORDER, width=1 * S)
    d.text((m + 22 * S, py + 13 * S), label, font=mono_sm, fill=TEXT)

    # Domain, bottom right.
    dom = "things.rlew.io"
    d.text((W * S - m - mono_sm.getlength(dom), py + 13 * S), dom, font=mono_sm, fill=DIM)

    out.parent.mkdir(parents=True, exist_ok=True)
    img.convert("RGB").resize((W, H), Image.LANCZOS).save(out, optimize=True)
    print(f"wrote {out} ({out.stat().st_size // 1024} KB)")


if __name__ == "__main__":
    main()
