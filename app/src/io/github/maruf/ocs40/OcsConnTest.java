package io.github.maruf.ocs40;

import javax.microedition.lcdui.Command;
import javax.microedition.lcdui.CommandListener;
import javax.microedition.lcdui.Display;
import javax.microedition.lcdui.Displayable;
import javax.microedition.lcdui.Form;
import javax.microedition.lcdui.StringItem;

/**
 * Connection test: GET /health, POST /echo with a fixed Turkish probe, then
 * GET /v1/status (the opencode version, the agent and the tool profile).
 * No access code and no chat text is sent. Reports what the phone's TLS
 * stack says about the connection; a successful connection alone is not
 * proof that its security level is current.
 *
 * Only when both steps pass is the URL marked as verified; chat is locked
 * until then. As step 3 of the setup wizard (OcsSetup) it gets "İleri" once
 * the address is verified, and "Geri" goes to the previous step.
 */
final class OcsConnTest implements CommandListener, Runnable {

    /** The Turkish probe string; the gateway answers probe: match for it. */
    static final String PROBE = "OpenCode S40 UTF-8: ç ğ ı İ ö ş ü Ç Ğ Ö Ş Ü";

    private final OcsS40MIDlet midlet;
    /** The wizard this test is a step of, or null. */
    private final OcsSetup setup;
    private final Form form;
    private final Command startCmd = new Command(OcsL.s("Başlat", "Start"), Command.SCREEN, 1);
    private final Command backCmd = new Command(OcsL.s("Geri", "Back"), Command.BACK, 1);
    private final Command nextCmd = new Command(OcsL.s("İleri", "Next"), Command.OK, 1);
    private boolean running;
    private boolean nextShown;

    OcsConnTest(OcsS40MIDlet midlet, OcsSetup setup) {
        this.midlet = midlet;
        this.setup = setup;
        form = new Form(setup != null ? OcsSetup.title(2) : OcsL.s("Bağlantı testi", "Connection test"));
        form.addCommand(startCmd);
        form.addCommand(backCmd);
        form.setCommandListener(this);
        intro();
        showNext(midlet.settings.connectionVerified());
    }

    /** Wizard only: "İleri" while the address is verified. */
    private synchronized void showNext(boolean want) {
        if (setup == null || want == nextShown) {
            return;
        }
        if (want) {
            form.addCommand(nextCmd);
        } else {
            form.removeCommand(nextCmd);
        }
        nextShown = want;
    }

    private void intro() {
        form.deleteAll();
        line(null, OcsL.s("Sunucu: ", "Server: ") + (midlet.settings.url.length() > 0 ? midlet.settings.url : OcsL.s("(ayarlanmadı)", "(not set)")));
        line(null, OcsL.s("İki adım: /health ve /echo (Türkçe UTF-8). Erişim kodu ve sohbet metni gönderilmez. "
                + "Telefon ağ izni isteyebilir.",
                "Two steps: /health and /echo (Turkish UTF-8 round trip). No access code and no chat text are sent. "
                + "The phone may ask for network access."));
        line(OcsL.s("Telefonda görünüm", "Rendering on this phone"), "ç ğ ı İ ö ş ü  Ç Ğ Ö Ş Ü");
    }

    void show(Display d) {
        if (!running) {
            intro();
        }
        d.setCurrent(form);
    }

    public void commandAction(Command c, Displayable d) {
        if (c == backCmd) {
            if (setup != null) {
                setup.back();
            } else {
                midlet.showMenu();
            }
        } else if (c == nextCmd) {
            setup.next();
        } else if (c == startCmd && !running) {
            running = true;
            intro();
            line(null, OcsL.s("Çalışıyor...", "Running..."));
            new Thread(this).start();
        }
    }

    private void line(String label, String text) {
        form.append(new StringItem(label, text + "\n"));
    }

    public void run() {
        try {
            test();
        } finally {
            running = false;
        }
    }

    private void test() {
        OcsSettings s = midlet.settings;
        String base = s.url;
        if (!OcsNet.isHttps(base)) {
            line(OcsL.s("SONUÇ", "RESULT"), OcsL.s("Sunucu adresi https:// ile başlamalı. Ayarlar'dan girin.", "The server address must start with https://. Set it in OcsSettings."));
            return;
        }

        // 1. health
        OcsNet.Result h = OcsNet.request(base + "/health", "GET", null, null, midlet.userAgent(), null);
        if (!report("1. /health", h)) {
            fail();
            return;
        }
        OcsS40Message hm = h.msg;
        line(OcsL.s("Sunucu", "Server"), hm.field("server") + " " + hm.field("version")
                + (hm.flag("mock") ? OcsL.s(", sunucu TEST MODU (sahte OpenCode)", ", server in TEST MODE (fake OpenCode)") : ""));
        line(OcsL.s("Telefonun TLS bağlantısı", "This phone's TLS connection"), h.tls.length() > 0 ? h.tls : OcsL.s("(bilgi yok)", "(no info)"));
        line(OcsL.s("Sunucunun gördüğü TLS", "TLS seen by the server"),
                hm.field("tls") + ", " + hm.field("cipher"));

        // 2. echo
        OcsNet.Result e = OcsNet.request(base + "/echo", "POST", null, PROBE, midlet.userAgent(), null);
        if (!report("2. /echo", e)) {
            fail();
            return;
        }
        boolean same = PROBE.equals(e.msg.text);
        boolean serverMatch = "match".equals(e.msg.field("probe"));
        line(OcsL.s("UTF-8 gönderim", "UTF-8 upload"), serverMatch ? OcsL.s("Sunucu Türkçe metni doğru aldı.", "The server received the Turkish text correctly.")
                : OcsL.s("Sunucu farklı bayt aldı: ", "The server received different bytes: ") + e.msg.field("hex"));
        line(OcsL.s("UTF-8 alım", "UTF-8 download"), (same ? OcsL.s("Telefon Türkçe metni doğru aldı: ", "The phone received the Turkish text correctly: ")
                : OcsL.s("Farklı geldi: ", "Received something else: ")) + e.msg.text);
        if (!same || !serverMatch) {
            line(OcsL.s("SONUÇ", "RESULT"), OcsL.s("Karakter kodlaması hatası. Sohbet kilitli kaldı.", "Character encoding error. Chat stays locked."));
            showNext(false);
            return;
        }

        s.verifiedUrl = base;
        String err = s.save();
        showNext(true);
        line(OcsL.s("SONUÇ", "RESULT"), OcsL.s("Bağlantı doğrulandı; sohbet kullanılabilir.", "Connection verified; chat is ready.")
                + (err != null ? " (" + err + ")" : "")
                + OcsL.s(" Not: bağlantının kurulması tek başına güncel güvenlik düzeyinin kanıtı değildir; "
                + "yukarıdaki TLS sürümü ve şifreyi kontrol edin.",
                " Note: a working connection alone does not prove a current security level; "
                + "check the TLS version and cipher above."));

        // 3. opencode status: is the coding agent on the other side actually up,
        // and which agent (read-only or full power) is it allowed to use? This is
        // the step that is specific to opencode, and it is best effort: the
        // connection is already verified by now.
        statusStep(base);
    }

    /**
     * GET /v1/status: the opencode version, the agent in use and the tool
     * profile the gateway is enforcing. Failure here is reported but does not
     * undo a successful connection test.
     */
    private void statusStep(String base) {
        OcsNet.Result r = OcsNet.request(base + "/v1/status", "GET", null, null, midlet.userAgent(), null);
        if (!r.ok() || r.msg == null) {
            line("3. " + OcsL.s("OpenCode durumu", "opencode status"),
                    OcsL.s("bilinemedi: ", "unknown: ") + OcsNet.explain(r));
            return;
        }
        OcsS40Message m = r.msg;
        line("3. " + OcsL.s("OpenCode durumu", "opencode status"),
                m.field("opencode") + (m.field("version").length() > 0 ? " (gateway " + m.field("version") + ")" : ""));
        line(OcsL.s("Ajan", "Agent"), m.field("agent"));
        String profile = m.field("profile");
        if (profile.length() > 0) {
            String meaning = "read-only".equals(profile)
                    ? OcsL.s("yalnızca okuma (dosya değiştiremez)", "read-only (cannot change files)")
                    : profile;
            line(OcsL.s("Araçlar", "Tools"), meaning);
        }
        if (m.text != null && m.text.trim().length() > 0) {
            String first = m.text.trim();
            int nl = first.indexOf('\n');
            if (nl >= 0) {
                first = first.substring(0, nl);
            }
            line(OcsL.s("Sunucu", "Server"), first);
        }
    }

    private boolean report(String step, OcsNet.Result r) {
        if (!r.ok()) {
            line(step, OcsL.s("HATA: ", "ERROR: ") + OcsNet.explain(r));
            return false;
        }
        if (r.msg == null) {
            line(step, "HTTP " + r.httpCode + OcsL.s(", yanıt OpenCode S40 sunucusundan değil (operatör ağı, yanlış adres?).", ", not a OpenCode S40 server reply (carrier network, wrong address?)."));
            return false;
        }
        String st = r.msg.field("status");
        if (r.httpCode != 200 || !"ok".equals(st)) {
            line(step, "HTTP " + r.httpCode + OcsL.s(", durum: ", ", status: ") + st);
            return false;
        }
        line(step, OcsL.s("Tamam (HTTP 200)", "OK (HTTP 200)"));
        return true;
    }

    private void fail() {
        OcsSettings s = midlet.settings;
        if (s.connectionVerified()) {
            s.verifiedUrl = "";
            s.save();
        }
        showNext(false);
        line(OcsL.s("SONUÇ", "RESULT"), OcsL.s("Bağlantı doğrulanamadı. Sohbet kilitli.", "Connection not verified. Chat is locked."));
    }
}
