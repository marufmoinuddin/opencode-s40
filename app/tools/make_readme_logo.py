#!/usr/bin/env python3
"""
Renders the OpenCode S40 wordmark as a tight, transparent PNG for the README.

`make_art.py logo` draws the mark centred on a square canvas, which is right for
a share image but leaves a 640x640 file that is 98 px of actual logo. This crops
to the content and adds an even margin, so the README gets a banner rather than a
mostly-empty square.

The counters of o/e/c are transparent holes, which is what lets one file read
correctly on both GitHub's light and dark themes.

  make_readme_logo.py OUT.png [WIDTH]
"""
import sys
import pathlib

from PIL import Image, ImageDraw

HERE = pathlib.Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))

from mark_data import HEIGHT, WIDTH  # noqa: E402
from make_art import ACCENT, wordmark  # noqa: E402

# Margin around the mark, as a fraction of its height. Enough that the mark does
# not touch the edge on a white page.
MARGIN = 0.35


def render(width_px, scale=8):
    """Renders the wordmark tightly cropped, with margin, transparent."""
    # One grid unit in final pixels.
    u = width_px / float(WIDTH)
    mark_h = HEIGHT * u
    pad = mark_h * MARGIN
    total_w = int(round(width_px + 2 * pad))
    total_h = int(round(mark_h + 2 * pad))

    big = Image.new("RGBA", (total_w * scale, total_h * scale), (0, 0, 0, 0))
    wordmark(ImageDraw.Draw(big), total_w * scale / 2, total_h * scale / 2,
             mark_h * scale)
    return big.resize((total_w, total_h), Image.LANCZOS)


def main():
    out = sys.argv[1]
    width = int(sys.argv[2]) if len(sys.argv) > 2 else 560
    img = render(width)
    img.save(out, optimize=True)
    bbox = img.getchannel("A").getbbox()
    print("%s: %dx%d, mark %dx%d, accent #%02X%02X%02X"
          % (out, img.width, img.height, bbox[2] - bbox[0], bbox[3] - bbox[1],
             ACCENT[0], ACCENT[1], ACCENT[2]))


if __name__ == "__main__":
    main()