#!/usr/bin/env python3
"""
Draws opencode's app icon with Pillow.

Source: https://opencode.ai/favicon-v3.svg (the mark opencode ships for its
console/desktop app), not the browser placeholder apple-touch-icon-v3.png, which
is a generic empty-box graphic.

Their SVG is three shapes, so it is redrawn here rather than rasterised: no SVG
renderer is needed on the build host, and Pillow gives deterministic bytes, which
is what keeps the JAR reproducible.

The mark: a white rounded-square outline (even-odd fill, so the middle is a
hole) with a solid grey block in the upper half of that hole, on a near-black
rounded square.

  make_art.py icon OUT.png          46x48 MIDlet icon, packaged in the JAR
  make_art.py logo OUT.png [SIZE]   large wordmark for README / share images
  make_art.py logo-light OUT.png    the wordmark on a light ground

The MIDlet icon is opencode's own app icon (favicon-v3.svg), redrawn from its
geometry. Their apple-touch-icon-v3.png is deliberately a placeholder empty-box
graphic, so it is not used.
"""
import sys

from PIL import Image, ImageDraw

from mark_data import BASE, HEIGHT, OVERLAY, WIDTH

# The icon's own palette, from favicon-v3.svg.
ICON_BG = (0x13, 0x10, 0x10, 255)
ICON_INK = (0xFF, 0xFF, 0xFF, 255)
ICON_BLOCK = (0x5A, 0x58, 0x58, 255)

# opencode "purple" from their theme defs, for the wordmark.
ACCENT = (0x9A, 0x5F, 0xEB, 255)

# Their geometry, as fractions of the 512-unit viewBox.
_S = 512.0
# Measured from favicon-v3.svg's two paths:
#   M384 416H128V96H384V416Z  M320 160H192V352H320V160Z   (even-odd ring)
#   M320 224V352H192V224H320Z                             (solid block)
RING = (128, 96, 256, 320)      # x, y, w, h of the outer box
RING_THICKNESS = 64             # the stroke width implied by the inner cutout
BLOCK = (192, 224, 128, 128)    # x, y, w, h


def icon(draw, size, bg=ICON_BG, ink=ICON_INK, block=ICON_BLOCK):
    """Draws opencode's app icon at the given pixel size."""
    def px(v):
        return int(round(v / _S * size))

    # Background: a square, slightly inset so the rounded phone menu has a
    # little air around it.
    draw.rectangle([0, 0, size - 1, size - 1], fill=bg)

    rx, ry, rw, rh = (px(v) for v in RING)
    t = max(1, px(RING_THICKNESS))
    # Draw the ring as a filled outer box with the inner cutout erased, so the
    # stroke keeps opencode's proportions instead of Pillow centring it.
    draw.rectangle([rx, ry, rx + rw, ry + rh], fill=ink)
    draw.rectangle([rx + t, ry + t, rx + rw - t, ry + rh - t], fill=bg)
    bx, by, bw, bh = (px(v) for v in BLOCK)
    draw.rectangle([bx, by, bx + bw, by + bh], fill=block)


def render_icon(w, h, ss=8):
    """Supersampled icon; the mark is square so it is centred on the canvas."""
    big = Image.new("RGBA", (w * ss, h * ss), (0, 0, 0, 0))
    d = ImageDraw.Draw(big)
    side = min(w, h) * ss
    icon(d, side)
    return big.resize((w, h), Image.LANCZOS)


def wordmark(draw, cx, cy, height, color=ACCENT, bg=None):
    """Draws the wordmark centred on (cx, cy)."""
    u = height / float(HEIGHT)
    x0 = cx - (WIDTH * u) / 2
    y0 = cy - height / 2
    # The counters are holes, so erase them first, then paint the bodies.
    for bx, by, bw, bh in OVERLAY:
        draw.rectangle(
            [x0 + bx * u, y0 + by * u,
             x0 + (bx + bw) * u - 1, y0 + (by + bh) * u - 1], fill=(0, 0, 0, 0))
    for bx, by, bw, bh in BASE:
        draw.rectangle(
            [x0 + bx * u, y0 + by * u,
             x0 + (bx + bw) * u - 1, y0 + (by + bh) * u - 1], fill=color)


def render_wordmark(w, h, light=False, ss=8):
    big = Image.new("RGBA", (w * ss, h * ss), (0, 0, 0, 0))
    d = ImageDraw.Draw(big)
    fit_w = (w * ss * 0.94) / float(WIDTH) * HEIGHT
    wordmark(d, w * ss / 2, h * ss / 2, min(h * ss * 0.30, fit_w))
    return big.resize((w, h), Image.LANCZOS)


def main():
    kind, out = sys.argv[1], sys.argv[2]
    if kind == "icon":
        img = render_icon(46, 48)
    elif kind == "logo":
        size = int(sys.argv[3]) if len(sys.argv) > 3 else 512
        img = render_wordmark(size, size)
    elif kind == "logo-light":
        size = int(sys.argv[3]) if len(sys.argv) > 3 else 512
        img = render_wordmark(size, size, light=True)
    else:
        sys.exit("usage: make_art.py icon|logo|logo-light OUT [SIZE]")
    img.save(out, optimize=False)


if __name__ == "__main__":
    main()