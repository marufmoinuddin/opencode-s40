package io.github.maruf.ocs40;

import javax.microedition.lcdui.Graphics;

/**
 * opencode's wordmark, drawn with MIDP primitives.
 *
 * <p>The geometry lives in {@link OcsMark}, generated from opencode's own logo
 * SVG by tools/make_mark.py, so the packaged icon and this on-screen mark are
 * the same shape. Drawing it with fillRect costs a few dozen bytes instead of a
 * bitmap, which matters in a ~130 KB MIDlet.
 *
 * <p>The mark is two layers, because that is how opencode builds theirs: the
 * base is the body of each letter and the overlay is the counters of o/e/c plus
 * the crossbar of the e, drawn on top in a darker tone. On the phone the base
 * is the purple accent and the overlay is a deep purple that reads as a hole at
 * 240x320.
 *
 * <p>Unofficial: opencode and its name are their owners' trademarks. This is a
 * client written by a user, not an official release.
 */
final class OcsLogo {

    private OcsLogo() {
    }

    /**
     * Draws the wordmark centred on (cx, cy).
     *
     * @param width   the mark's width in pixels. Its height follows the 39:6
     *                aspect, so callers size this horizontally: a wordmark sized
     *                by height would come out as a sliver.
     * @param twinkle scale in percent for the splash animation (100 = rest)
     * @param spin    unused. The mark is a wordmark and does not rotate; the
     *                parameter is kept so the splash can keep its signature
     */
    static void draw(Graphics g, int cx, int cy, int width, int twinkle, int spin) {
        int u = (width * twinkle / 100) / OcsMark.WIDTH;
        if (u < 1) {
            return;
        }
        // Centre on the true drawn size, since u is floored and w may be odd.
        int w = OcsMark.WIDTH * u;
        int x0 = cx - w / 2;
        int y0 = cy - (OcsMark.HEIGHT * u) / 2;

        g.setColor(OcsTheme.spark);
        fillBlocks(g, OcsMark.BASE, x0, y0, u);
        g.setColor(OcsTheme.accentDeep);
        fillBlocks(g, OcsMark.OVERLAY, x0, y0, u);
    }

    private static void fillBlocks(Graphics g, int[][] blocks, int x0, int y0, int u) {
        for (int i = 0; i < blocks.length; i++) {
            int[] b = blocks[i];
            g.fillRect(x0 + b[0] * u, y0 + b[1] * u, b[2] * u, b[3] * u);
        }
    }

    /**
     * A small four-point sparkle in opencode's purple, kept for the splash
     * decoration and the chat avatar.
     */
    static void sparkle(Graphics g, int cx, int cy, int r, int color) {
        if (r < 1) {
            return;
        }
        int t = Math.max(1, r / 4);
        g.setColor(color);
        g.fillTriangle(cx, cy - r, cx - t, cy, cx + t, cy);
        g.fillTriangle(cx, cy + r, cx - t, cy, cx + t, cy);
        g.fillTriangle(cx - r, cy, cx, cy - t, cx, cy + t);
        g.fillTriangle(cx + r, cy, cx, cy - t, cx, cy + t);
    }
}