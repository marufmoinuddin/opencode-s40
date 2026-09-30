#!/usr/bin/env python3
"""
Renders the OpenCode S40 app icon for the README.

`make_art.py icon` writes the 46x48 icon that goes in the JAR, which is tiny on a
README page. This renders the same mark 4x with nearest-neighbour scaling, so its
straight edges stay exact, and writes a transparent PNG that works on either
GitHub theme.

  make_readme_icon.py OUT.png [SCALE]
"""
import sys
import pathlib

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parent))

from make_art import render_icon  # noqa: E402


def main():
    out = sys.argv[1]
    scale = int(sys.argv[2]) if len(sys.argv) > 2 else 4
    img = render_icon(46 * scale, 48 * scale)
    img.save(out, optimize=True)
    print("%s: %dx%d" % (out, img.width, img.height))


if __name__ == "__main__":
    main()
