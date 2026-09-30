#!/usr/bin/env python3
"""
Generates the opencode wordmark data (Java + Python) from opencode's own logo
SVG.

Why a generator: the mark is 23 axis-aligned shapes in three tones, and its
letterforms only appear once the darkest tone is composited ON TOP of the
lighter ones - that layering is what carves the counters out of o, e, c and the
crossbar of e. Hand-transcribing this produced a solid block, so it is derived
from the source of truth instead.

    python3 tools/make_mark.py /path/to/logo-dark.svg

Writes:
  src/io/github/maruf/ocs40/OcsMark.java   letterforms for the app
  app/tools/mark_data.py                    the same data for the packaged icon

Source: packages/web/src/assets/logo-*.svg in github.com/anomalyco/opencode.

The output keeps two layers, because the phone draws them in one colour plus a
tint:
  LAYER_BASE  drawn first, in the accent colour (the letter's body)
  LAYER_OVER  drawn on top, in a darker tone (the counter / crossbar)
"""
import pathlib
import re
import sys

HERE = pathlib.Path(__file__).resolve().parent
APP = HERE.parent

JAVA_OUT = APP / "src/io/github/maruf/ocs40/OcsMark.java"
PY_OUT = APP / "tools/mark_data.py"

GRID = 6       # the logo's grid: coordinates are multiples of 6
VIEW_W = 234
VIEW_H = 36

# Tonal order in logo-dark.svg, lightest to darkest. The darkest is the overlay.
DARK_SVG = {"#4B4646": 0, "#B7B1B1": 1, "#F1ECEC": 2}


def shapes(d):
    """One path -> list of (x0, y0, x1, y1) boxes, in SVG units."""
    out = []
    for sp in re.findall(r'M[^M]*', d):
        pts = []
        cx = cy = 0.0
        for cmd, arg in re.findall(r'([MHVLZ])([^MHVLZ]*)', sp):
            nums = [float(v) for v in arg.split()] if arg.strip() else []
            if cmd == 'M':
                cx, cy = nums[0], nums[1]
            elif cmd == 'H':
                for v in nums:
                    cx = v
            elif cmd == 'V':
                for v in nums:
                    cy = v
            elif cmd == 'L':
                for i in range(0, len(nums) - 1, 2):
                    cx, cy = nums[i], nums[i + 1]
            elif cmd == 'Z':
                break
            pts.append((cx, cy))
        if len(pts) < 3:
            continue
        xs = [p[0] for p in pts]
        ys = [p[1] for p in pts]
        out.append((min(xs), min(ys), max(xs), max(ys)))
    return out


def cells(boxes, u, maxc, maxr):
    """Boxes -> a set of (col, row) grid cells."""
    s = set()
    for x0, y0, x1, y1 in boxes:
        for c in range(max(0, int(round(x0 / u))), min(maxc, int(round(x1 / u)))):
            for r in range(max(0, int(round(y0 / u))), min(maxr, int(round(y1 / u)))):
                s.add((c, r))
    return s


def merge(cellset):
    """Greedy merge of cells into as few rectangles as possible."""
    rects = []
    remaining = set(cellset)
    while remaining:
        c, r = min(remaining, key=lambda p: (p[1], p[0]))
        w = 1
        while (c + w, r) in remaining:
            w += 1
        h = 1
        while all((c + i, r + h) in remaining for i in range(w)):
            h += 1
        for i in range(w):
            for j in range(h):
                remaining.discard((c + i, r + j))
        rects.append((c, r, w, h))
    rects.sort(key=lambda b: (b[1], b[0]))
    return rects


def parse(svg_path):
    svg = pathlib.Path(svg_path).read_text()
    u = VIEW_H / float(GRID)
    maxc, maxr = int(round(VIEW_W / u)), int(round(VIEW_H / u))

    by_tone = {}
    for d, fill in re.findall(r'<path d="([^"]+)" fill="(#[0-9a-fA-F]{6})"', svg):
        if fill not in DARK_SVG:
            continue
        by_tone.setdefault(DARK_SVG[fill], []).extend(shapes(d))

    # The overlay is the darkest tone (rank 0). Everything lighter forms the
    # base it is carved out of.
    overlay = cells(by_tone.get(0, []), u, maxc, maxr)
    base = set()
    for rank, boxes in by_tone.items():
        if rank != 0:
            base |= cells(boxes, u, maxc, maxr)
    base -= overlay

    return merge(base), merge(overlay), maxc, maxr


def _rows(blocks):
    return "\n".join("        {%d, %d, %d, %d}," % b for b in blocks)


def java_src(width_u, height_u, base, overlay):
    return "\n".join([
        "package io.github.maruf.ocs40;",
        "",
        "/**",
        " * opencode's wordmark as axis-aligned blocks, generated from their own",
        " * logo SVG by tools/make_mark.py. Do not edit by hand.",
        " *",
        " * <p>Each entry is {x, y, w, h} in grid units, where one unit is 1/"
        + str(GRID) + " of the mark's height. The letters read as \"opencode\" in",
        " * opencode's geometric pixel face.",
        " *",
        " * <p>The mark is two layers because that is how their logo is built: the",
        " * light layer is the body of each letter, and the dark overlay on top",
        " * carves out the counters of o/e/c and the crossbar of e. Dropping the",
        " * overlay leaves a solid block, so it is drawn in a darker tone.",
        " */",
        "final class OcsMark {",
        "",
        "    /** Total width of the wordmark in grid units. */",
        "    static final int WIDTH = " + str(width_u) + ";",
        "",
        "    /** Total height in grid units. */",
        "    static final int HEIGHT = " + str(height_u) + ";",
        "",
        "    /** Letter bodies: {x, y, w, h} in grid units. */",
        "    static final int[][] BASE = {",
        _rows(base),
        "    };",
        "",
        "    /** Counters and crossbars, drawn on top in a darker tone. */",
        "    static final int[][] OVERLAY = {",
        _rows(overlay),
        "    };",
        "",
        "    private OcsMark() {",
        "    }",
        "}",
        "",
    ])


def _prows(blocks):
    return "\n".join("    (%d, %d, %d, %d)," % b for b in blocks)


def py_src(width_u, height_u, base, overlay):
    return "\n".join([
        '"""opencode wordmark block data, generated by tools/make_mark.py.',
        "",
        "One unit is 1/" + str(GRID) + " of the mark height.",
        "Tuples are (x, y, w, h) in grid units.",
        "Keep in sync with src/io/github/maruf/ocs40/OcsMark.java.",
        '"""',
        "",
        "WIDTH = " + str(width_u),
        "HEIGHT = " + str(height_u),
        "",
        "# Letter bodies.",
        "BASE = [",
        _prows(base),
        "]",
        "",
        "# Counters and crossbars, drawn on top.",
        "OVERLAY = [",
        _prows(overlay),
        "]",
        "",
    ])


def main():
    if len(sys.argv) < 2:
        sys.exit("usage: make_mark.py logo-dark.svg")
    base, overlay, width_u, height_u = parse(sys.argv[1])
    JAVA_OUT.write_text(java_src(width_u, height_u, base, overlay))
    PY_OUT.write_text(py_src(width_u, height_u, base, overlay))
    print("grid %dx%d units; base %d rects, overlay %d rects"
          % (width_u, height_u, len(base), len(overlay)))
    print("wrote %s, %s" % (JAVA_OUT.name, PY_OUT.name))


if __name__ == "__main__":
    main()