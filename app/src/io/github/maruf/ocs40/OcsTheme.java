package io.github.maruf.ocs40;

import javax.microedition.lcdui.Font;

/**
 * Colours and fonts for the custom screens. Two palettes (light "Gündüz",
 * dark "Gece") and three text sizes, chosen in Ayarlar.
 *
 * <p>The dark palette is opencode's own theme, taken from the schema the
 * opencode server ships (its theme.json "defs"): background #181818, panel
 * #15141b, border #2d2d2d, text #e4e4e4, muted #565B66, with the accent set to
 * opencode's "purple" (#9a5feb), which is what their theme maps
 * primary/accent/info to. Secondary pink #ff628c, success cyan #93e0e3,
 * error red #cc9393 and warning orange #dfaf8f are their defs too.
 *
 * <p>opencode's own interface is dark-only, so there is no light palette to
 * copy. The light one here is built from the same hue family (purple accent,
 * pink and cyan supports) on a warm-neutral base chosen for a small reflective
 * screen in daylight.
 */
final class OcsTheme {

    // current palette (set by apply())
    static int bg;
    static int surface;
    static int border;
    static int ink;
    static int muted;
    static int accent;
    /** Deeper accent for the wordmark's counters, so they read as holes. */
    static int accentDeep;
    static int accentInk;
    static int bar;
    static int barInk;
    static int error;
    static int errorBg;
    static int testBar;
    static int selection;
    static int spark;

    static Font font = Font.getDefaultFont();
    static Font bold = font;
    static Font small = font;

    private OcsTheme() {
    }

    static void apply(OcsSettings s) {
        if (s.theme == 1) {
            // opencode's own dark theme.
            bg = 0x181818;
            surface = 0x15141B;
            border = 0x2D2D2D;
            ink = 0xE4E4E4;
            muted = 0x565B66;
            accent = 0x9A5FEB;      // opencode "purple": primary + accent
            accentDeep = 0x0B0B0F; // counters/crossbar: near-black on the dark bg
            accentInk = 0x14101C;   // dark ink on the purple accent
            bar = 0x0F0F0F;
            barInk = 0xE4E4E4;
            error = 0xCC9393;       // opencode "red"
            errorBg = 0x37222C;     // their diffRemovedBg
            testBar = 0x6B3FA0;     // purple, darkened for the TEST banner
            selection = 0x2A2140;   // purple tint over the panel
            spark = 0x9A5FEB;
        } else {
            // Light: the same purple family on a daylight-readable base.
            bg = 0xFAF9FC;
            surface = 0xFFFFFF;
            border = 0xE2DFEA;
            ink = 0x1F1D24;
            muted = 0x6E6A7C;
            accent = 0x7C3AED;      // opencode purple, darkened for contrast
            accentDeep = 0xF3EEFF; // counters/crossbar: near-white on the light bg
            accentInk = 0xFFFFFF;
            bar = 0x1F1D24;
            barInk = 0xFFFFFF;
            error = 0xB3261E;
            errorBg = 0xFBE4E9;
            testBar = 0x5B2DA8;
            selection = 0xEDE7FB;
            spark = 0x9A5FEB;
        }
        int size = s.fontSize == 0 ? Font.SIZE_SMALL : s.fontSize == 2 ? Font.SIZE_LARGE : Font.SIZE_MEDIUM;
        font = Font.getFont(Font.FACE_PROPORTIONAL, Font.STYLE_PLAIN, size);
        bold = Font.getFont(Font.FACE_PROPORTIONAL, Font.STYLE_BOLD, size);
        small = Font.getFont(Font.FACE_PROPORTIONAL, Font.STYLE_PLAIN, Font.SIZE_SMALL);
    }

    /** Linear blend of two RGB colours, t in 0..256. */
    static int mix(int a, int b, int t) {
        int r = ((a >> 16) & 0xFF) + ((((b >> 16) & 0xFF) - ((a >> 16) & 0xFF)) * t >> 8);
        int g = ((a >> 8) & 0xFF) + ((((b >> 8) & 0xFF) - ((a >> 8) & 0xFF)) * t >> 8);
        int bl = (a & 0xFF) + (((b & 0xFF) - (a & 0xFF)) * t >> 8);
        return (r << 16) | (g << 8) | bl;
    }
}