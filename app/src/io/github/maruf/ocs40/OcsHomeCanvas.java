package io.github.maruf.ocs40;

import javax.microedition.lcdui.Canvas;
import javax.microedition.lcdui.Command;
import javax.microedition.lcdui.CommandListener;
import javax.microedition.lcdui.Displayable;
import javax.microedition.lcdui.Font;
import javax.microedition.lcdui.Graphics;

/**
 * Main menu with drawn icons. UP/DOWN (game actions) move, FIRE or "Seç"
 * opens, number keys 1-9 (Canvas.KEY_NUMx constants) jump directly.
 * Softkeys are standard Commands.
 */
final class OcsHomeCanvas extends Canvas implements CommandListener {

    private final String[] titles = {
        OcsL.s("Sohbet", "Chat"), OcsL.s("Sohbetler", "Chats"), OcsL.s("Hızlı sorular", "Quick prompts"),
        OcsL.s("Yeni sohbet", "New chat"), OcsL.s("Kaydedilenler", "Saved"),
        OcsL.s("Bağlantı testi", "Connection test"), OcsL.s("Ayarlar", "OcsSettings"), OcsL.s("Hakkında", "About"),
        OcsL.s("Çıkış", "Exit") };
    private final String[] hints = {
        OcsL.s("Kaldığın yerden devam et", "Pick up where you left off"),
        OcsL.s("Önceki sohbetleri aç", "Open earlier chats"),
        OcsL.s("Web'de ara, çevir, özetle...", "Search the web, translate..."),
        OcsL.s("Temiz bir sayfa aç", "Start fresh"),
        OcsL.s("Telefondaki yanıtlar, internetsiz", "Replies on the phone, offline"),
        OcsL.s("Sunucuya ulaşıyor muyuz?", "Can we reach the server?"),
        OcsL.s("Görünüm, dil, ses, eşleştirme", "Look, language, sound, pairing"),
        OcsL.s("OpenCode S40 nedir?", "What is OpenCode S40?"),
        OcsL.s("Görüşmek üzere", "See you soon") };

    private static final int MARGIN = 6;

    private final OcsS40MIDlet midlet;
    private final Command selectCmd = new Command(OcsL.s("Seç", "Select"), Command.OK, 1);
    private final Command exitCmd = new Command(OcsL.s("Çıkış", "Exit"), Command.EXIT, 2);
    private int selected;
    private int top;

    OcsHomeCanvas(OcsS40MIDlet midlet) {
        this.midlet = midlet;
        addCommand(selectCmd);
        addCommand(exitCmd);
        setCommandListener(this);
    }

    public void commandAction(Command c, Displayable d) {
        if (c == selectCmd) {
            midlet.menuSelected(selected);
        } else if (c == exitCmd) {
            midlet.exit();
        }
    }

    protected void keyPressed(int keyCode) {
        if (keyCode >= KEY_NUM1 && keyCode <= KEY_NUM9) {
            selected = keyCode - KEY_NUM1;
            repaint();
            midlet.menuSelected(selected);
            return;
        }
        int action;
        try {
            action = getGameAction(keyCode);
        } catch (IllegalArgumentException e) {
            return;
        }
        if (action == UP) {
            selected = (selected + titles.length - 1) % titles.length;
        } else if (action == DOWN) {
            selected = (selected + 1) % titles.length;
        } else if (action == FIRE) {
            midlet.menuSelected(selected);
            return;
        } else {
            return;
        }
        repaint();
    }

    protected void keyRepeated(int keyCode) {
        keyPressed(keyCode);
    }

    protected void paint(Graphics g) {
        int w = getWidth();
        int h = getHeight();
        Font f = OcsTheme.bold;
        Font sm = OcsTheme.small;

        g.setColor(OcsTheme.bg);
        g.fillRect(0, 0, w, h);

        // header: wordmark + title + status line
        int headH = Math.max(f.getHeight() + sm.getHeight() + 10, 40);
        g.setColor(OcsTheme.bar);
        g.fillRect(0, 0, w, headH);
        // The wordmark needs width, not height: 39:6, so a 96 px mark is about
        // 15 px tall. Sized to fit beside the title on a 128 px screen.
        int tw = f.stringWidth("OpenCode S40") + 10;
        int lw = w - 2 * MARGIN - tw - 6;
        if (lw >= 48) {
            OcsLogo.draw(g, MARGIN + lw / 2, headH / 2, lw, 100, 0);
        } else {
            // Too narrow for a wordmark: the small sparkle stands in.
            OcsLogo.sparkle(g, MARGIN + 8, headH / 2, headH / 3, OcsTheme.spark);
            lw = 18;
        }
        int tx = MARGIN + lw + 8;
        g.setColor(OcsTheme.barInk);
        g.setFont(f);
        g.drawString("OpenCode S40", tx, 5, Graphics.TOP | Graphics.LEFT);
        g.setFont(sm);
        g.setColor(OcsTheme.mix(OcsTheme.barInk, OcsTheme.bar, 90));
        g.drawString(OcsText.fit(midlet.homeStatus(), sm, w - tx - MARGIN), tx, 5 + f.getHeight(), Graphics.TOP | Graphics.LEFT);

        // rows
        int rowH = Math.max(f.getHeight() + sm.getHeight() + 6, 30);
        int footH = sm.getHeight() + 4;
        int area = h - headH - footH;
        int visible = Math.max(1, area / rowH);
        if (selected < top) {
            top = selected;
        } else if (selected >= top + visible) {
            top = selected - visible + 1;
        }
        int y = headH + Math.max(2, (area - visible * rowH) / 2);
        for (int i = top; i < titles.length && i < top + visible; i++) {
            boolean sel = i == selected;
            if (sel) {
                g.setColor(OcsTheme.selection);
                g.fillRoundRect(MARGIN / 2, y + 1, w - MARGIN, rowH - 2, 12, 12);
                g.setColor(OcsTheme.accent);
                g.fillRoundRect(MARGIN / 2, y + 1, 4, rowH - 2, 4, 4);
            }
            int ic = rowH - 12;
            icon(g, i, MARGIN + 4 + ic / 2, y + rowH / 2, ic, sel);
            int x = MARGIN + ic + 14;
            String num = String.valueOf(i + 1);
            int textW = w - x - MARGIN - 6 - sm.stringWidth(num);
            g.setFont(f);
            g.setColor(OcsTheme.ink);
            g.drawString(OcsText.fit(titles[i], f, textW), x, y + 3, Graphics.TOP | Graphics.LEFT);
            g.setFont(sm);
            g.setColor(OcsTheme.muted);
            g.drawString(OcsText.fit(midlet.homeHint(i, hints[i]), sm, textW), x, y + 3 + f.getHeight(), Graphics.TOP | Graphics.LEFT);
            g.drawString(num, w - MARGIN - 2, y + rowH / 2 - sm.getHeight() / 2, Graphics.TOP | Graphics.RIGHT);
            y += rowH;
        }

        // footer
        g.setFont(sm);
        g.setColor(OcsTheme.muted);
        g.drawString(OcsText.fit(OcsL.s("Resmî olmayan istemci · ", "Unofficial client · ") + midlet.attr("MIDlet-Version"), sm,
                w - 2 * MARGIN), w / 2, h - footH + 2, Graphics.TOP | Graphics.HCENTER);
    }

    /** Small line icons drawn with primitives. */
    private static void icon(Graphics g, int item, int cx, int cy, int s, boolean sel) {
        int c = sel ? OcsTheme.accent : OcsTheme.muted;
        g.setColor(c);
        int r = s / 2;
        switch (item) {
        case 0: // chat bubble
            g.fillRoundRect(cx - r, cy - r + 2, s, s * 3 / 4, 8, 8);
            g.fillTriangle(cx - r + 3, cy + r / 2, cx - r + 9, cy + r / 2, cx - r + 1, cy + r);
            g.setColor(OcsTheme.bg);
            for (int i = -1; i <= 1; i++) {
                g.fillArc(cx + i * (s / 4) - 2, cy - 1, 4, 4, 0, 360);
            }
            break;
        case 1: // list of chats: three lines with dots
            for (int i = -1; i <= 1; i++) {
                int ly = cy + i * (s / 3) - 1;
                g.fillArc(cx - r, ly, 4, 4, 0, 360);
                g.fillRect(cx - r + 6, ly + 1, s - 6, 2);
            }
            break;
        case 2: // lightning
            g.fillTriangle(cx + 2, cy - r, cx - r / 2, cy + 2, cx + 1, cy + 1);
            g.fillTriangle(cx - 2, cy + r, cx + r / 2, cy - 2, cx - 1, cy - 1);
            break;
        case 3: // plus in a circle
            g.drawArc(cx - r, cy - r, s, s, 0, 360);
            g.fillRect(cx - r / 2, cy - 1, r, 3);
            g.fillRect(cx - 1, cy - r / 2, 3, r);
            break;
        case 4: // a page with a folded corner and lines
            g.fillRect(cx - r + 2, cy - r, s - 4 - r / 2, s);
            g.fillTriangle(cx + r - 2 - r / 2, cy - r, cx + r - 2, cy - r + r / 2, cx + r - 2 - r / 2, cy - r + r / 2);
            g.fillRect(cx + r - 2 - r / 2, cy - r + r / 2, r / 2, s - r / 2);
            g.setColor(OcsTheme.bg);
            for (int i = 0; i < 3; i++) {
                g.fillRect(cx - r + 5, cy - r / 3 + i * (s / 4), s - 10, 2);
            }
            break;
        case 5: // signal bars
            for (int i = 0; i < 4; i++) {
                int bh = (i + 1) * s / 4;
                g.fillRect(cx - r + i * (s / 4), cy + r - bh, Math.max(2, s / 6), bh);
            }
            break;
        case 6: // gear-ish: ring with teeth
            g.fillArc(cx - r, cy - r, s, s, 0, 360);
            g.setColor(OcsTheme.bg);
            g.fillArc(cx - r / 2, cy - r / 2, r, r, 0, 360);
            g.setColor(c);
            g.fillRect(cx - 1, cy - r - 2, 3, 4);
            g.fillRect(cx - 1, cy + r - 2, 3, 4);
            g.fillRect(cx - r - 2, cy - 1, 4, 3);
            g.fillRect(cx + r - 2, cy - 1, 4, 3);
            break;
        case 7: // info "i"
            g.drawArc(cx - r, cy - r, s, s, 0, 360);
            g.fillRect(cx - 1, cy - r / 2, 3, 3);
            g.fillRect(cx - 1, cy - r / 6, 3, r * 2 / 3 + 2);
            break;
        default: // power
            g.drawArc(cx - r, cy - r, s, s, 120, 300);
            g.fillRect(cx - 1, cy - r - 1, 3, r);
            break;
        }
    }
}
