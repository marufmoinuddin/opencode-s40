package io.github.maruf.ocs40;

/**
 * opencode's wordmark as axis-aligned blocks, generated from their own
 * logo SVG by tools/make_mark.py. Do not edit by hand.
 *
 * <p>Each entry is {x, y, w, h} in grid units, where one unit is 1/6 of the mark's height. The letters read as "opencode" in
 * opencode's geometric pixel face.
 *
 * <p>The mark is two layers because that is how their logo is built: the
 * light layer is the body of each letter, and the dark overlay on top
 * carves out the counters of o/e/c and the crossbar of e. Dropping the
 * overlay leaves a solid block, so it is drawn in a darker tone.
 */
final class OcsMark {

    /** Total width of the wordmark in grid units. */
    static final int WIDTH = 39;

    /** Total height in grid units. */
    static final int HEIGHT = 6;

    /** Letter bodies: {x, y, w, h} in grid units. */
    static final int[][] BASE = {
        {30, 0, 4, 3},
        {0, 1, 4, 2},
        {5, 1, 4, 2},
        {10, 1, 4, 3},
        {15, 1, 3, 2},
        {20, 1, 4, 2},
        {25, 1, 4, 2},
        {35, 1, 4, 3},
        {18, 2, 1, 4},
        {0, 3, 1, 3},
        {3, 3, 1, 3},
        {5, 3, 1, 3},
        {8, 3, 1, 3},
        {15, 3, 1, 3},
        {20, 3, 1, 3},
        {25, 3, 1, 3},
        {28, 3, 1, 3},
        {30, 3, 1, 3},
        {33, 3, 1, 3},
        {10, 4, 1, 2},
        {35, 4, 1, 2},
        {1, 5, 2, 1},
        {6, 5, 2, 1},
        {11, 5, 3, 1},
        {21, 5, 3, 1},
        {26, 5, 2, 1},
        {31, 5, 2, 1},
        {36, 5, 3, 1},
    };

    /** Counters and crossbars, drawn on top in a darker tone. */
    static final int[][] OVERLAY = {
        {1, 3, 2, 2},
        {6, 3, 2, 2},
        {16, 3, 2, 3},
        {21, 3, 3, 2},
        {26, 3, 2, 2},
        {31, 3, 2, 2},
        {11, 4, 3, 1},
        {36, 4, 3, 1},
    };

    private OcsMark() {
    }
}
