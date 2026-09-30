package io.github.maruf.ocs40;

import javax.microedition.lcdui.Command;
import javax.microedition.lcdui.CommandListener;
import javax.microedition.lcdui.Displayable;
import javax.microedition.lcdui.Form;
import javax.microedition.lcdui.List;
import javax.microedition.lcdui.StringItem;

/**
 * Adds a photo to the next message: take it with the camera (OcsCam) or pick
 * a file (OcsPhotoPicker), upload it to /v1/image, then open the editor with
 * the photo attached (OcsChatSession.setImage) for the user to write the
 * question and send it. The upload never calls OpenCode and costs nothing but
 * mobile data; the server keeps the same photo under the same id, so
 * "Retry" is harmless. The photo is held in RAM only until the upload ends.
 */
final class OcsPhoto implements CommandListener, Runnable, OcsNet.Listener {

    /** The server takes at most 1 MB. */
    static final int MAX_BYTES = 1024 * 1024;

    private final OcsS40MIDlet midlet;
    private final boolean fromComposer;
    private List source;
    private final Form form;
    private final StringItem status;
    private final Command retryCmd = new Command(OcsL.s("Tekrar dene", "Retry"), Command.OK, 1);
    private final Command otherCmd = new Command(OcsL.s("Başka fotoğraf", "Another photo"), Command.SCREEN, 2);
    private final Command cancelCmd = new Command(OcsL.s("Vazgeç", "Cancel"), Command.BACK, 1);
    private Command[] shownCmds = new Command[0];

    private byte[] data;
    /** Bumped when the screen moves on; an older upload's answer is ignored. */
    private int generation;
    private boolean closed;

    OcsPhoto(OcsS40MIDlet midlet, boolean fromComposer) {
        this.midlet = midlet;
        this.fromComposer = fromComposer;
        form = new Form(OcsL.s("Fotoğraf ekle", "Add a photo"));
        status = new StringItem(null, "");
        form.append(status);
        form.append(new StringItem(null, OcsL.s(
                "Fotoğraf sunucuya gider ve bu sohbette OpenCode'a gösterilir. Sohbetle birlikte sunucuda kalır "
                        + "(30 gün ya da sohbeti silene kadar).",
                "The photo goes to the server and is shown to OpenCode in this chat. It stays on the server with "
                        + "the chat (30 days, or until you delete the chat).")));
        form.setCommandListener(this);
    }

    /** Camera and files: asks which; otherwise opens the one there is. */
    void start() {
        boolean cam = OcsS40MIDlet.hasCamera();
        boolean files = OcsS40MIDlet.hasFiles();
        if (cam && files) {
            source = new List(OcsL.s("Fotoğraf ekle", "Add a photo"), List.IMPLICIT);
            source.append(OcsL.s("Kamerayla çek", "Take a photo"), null);
            source.append(OcsL.s("Telefondan seç", "Choose from the phone"), null);
            source.addCommand(cancelCmd);
            source.setCommandListener(this);
            midlet.display().setCurrent(source);
        } else if (cam) {
            new OcsCam(midlet, this).start();
        } else {
            new OcsPhotoPicker(midlet, this).start();
        }
    }

    // ------------------------------------------------------------ from OcsCam / OcsPhotoPicker (any thread)

    void chosen(byte[] b) {
        synchronized (this) {
            if (closed) {
                return;
            }
            data = b;
        }
        upload();
    }

    /** Camera or picker closed without a photo: back to the choice, or out. */
    void cancelled() {
        if (source != null && !closed) {
            midlet.display().setCurrent(source);
        } else {
            close();
        }
    }

    void failed(String reason) {
        synchronized (this) {
            if (closed) {
                return;
            }
            show(reason, source != null ? new Command[] { otherCmd, cancelCmd } : new Command[] { cancelCmd });
        }
        midlet.display().setCurrent(form);
        OcsSound.play(midlet.settings, OcsSound.ERROR);
    }

    // ------------------------------------------------------------ upload

    private synchronized void upload() {
        generation++;
        show(OcsL.s("Gönderiliyor (", "Sending (") + (data.length + 1023) / 1024 + " KB)...", new Command[] { cancelCmd });
        midlet.display().setCurrent(form);
        new Thread(this).start();
    }

    public void run() {
        byte[] b;
        int gen;
        synchronized (this) {
            b = data;
            gen = generation;
        }
        OcsSettings s = midlet.settings;
        if (s.testMode) {
            try {
                Thread.sleep(800);
            } catch (InterruptedException e) {
                // ignore
            }
            done(gen, "test");
            return;
        }
        OcsNet.Result r = OcsNet.upload(s.url + "/v1/image", s.token, b, contentType(b), midlet.userAgent(), this);
        OcsS40Message m = r.msg;
        String st = m == null ? "" : m.field("status");
        if (r.ok() && "ok".equals(st) && m.field("image").length() == 32) {
            done(gen, m.field("image"));
            return;
        }
        String msg;
        boolean retry = false;
        if (!r.ok()) {
            msg = OcsNet.explain(r);
            retry = true;
        } else if (m == null) {
            msg = OcsL.s("Sunucu yanıtı tanınmadı (HTTP " + r.httpCode + ").", "Unrecognised server reply (HTTP "
                    + r.httpCode + ").");
            retry = true;
        } else if ("bad_image".equals(st)) {
            msg = OcsL.s("Sunucu bu fotoğrafı okuyamadı (JPEG veya PNG olmalı).",
                    "The server could not read this photo (it must be JPEG or PNG).");
        } else if ("too_large".equals(st)) {
            msg = OcsL.s("Fotoğraf çok büyük.", "The photo is too large.");
        } else if ("limit".equals(st)) {
            msg = OcsL.s("Bugünkü fotoğraf hakkınız bitti. Yarın (UTC) yenilenir.",
                    "No photos left today. More tomorrow (UTC).");
        } else if ("unauthorized".equals(st)) {
            msg = OcsL.s("Eşleştirme geçersiz. Ayarlar > Seçenekler > 'Cihazı eşleştir'.",
                    "The pairing is not valid. OcsSettings > Options > 'Pair this phone'.");
        } else if ("not_found".equals(st)) {
            msg = OcsL.s("Sunucu fotoğrafları desteklemiyor (eski sürüm).",
                    "The server does not take photos (older version).");
        } else {
            msg = OcsL.s("Sunucu: ", "Server: ") + st;
            retry = true;
        }
        synchronized (this) {
            if (closed || gen != generation) {
                return;
            }
            Command[] c = retry ? new Command[] { retryCmd, otherCmd, cancelCmd } : new Command[] { otherCmd, cancelCmd };
            show(msg, c);
        }
        OcsSound.play(midlet.settings, OcsSound.ERROR);
    }

    public void phase(int phase) {
        if (phase >= OcsNet.PHASE_RESPONSE) {
            synchronized (this) {
                if (!closed) {
                    status.setText(OcsL.s("Sunucu fotoğrafı hazırlıyor...", "The server is preparing the photo..."));
                }
            }
        }
    }

    private void done(int gen, String id) {
        synchronized (this) {
            if (closed || gen != generation) {
                return;
            }
            closed = true;
            data = null;
        }
        midlet.photoAttached(id, fromComposer);
    }

    static String contentType(byte[] b) {
        if (b.length >= 2 && (b[0] & 0xFF) == 0xFF && (b[1] & 0xFF) == 0xD8) {
            return "image/jpeg";
        }
        if (b.length >= 4 && (b[0] & 0xFF) == 0x89 && b[1] == 'P' && b[2] == 'N' && b[3] == 'G') {
            return "image/png";
        }
        return "application/octet-stream";
    }

    // ------------------------------------------------------------ screen

    /** Called with the lock held. */
    private void show(String text, Command[] cmds) {
        status.setText(text);
        for (int i = 0; i < shownCmds.length; i++) {
            form.removeCommand(shownCmds[i]);
        }
        for (int i = 0; i < cmds.length; i++) {
            form.addCommand(cmds[i]);
        }
        shownCmds = cmds;
    }

    private void close() {
        synchronized (this) {
            closed = true;
            generation++;
            data = null;
        }
        midlet.photoClosed(fromComposer);
    }

    public void commandAction(Command c, Displayable d) {
        midlet.userActive();
        if (d == source) {
            if (c == List.SELECT_COMMAND) {
                if (source.getSelectedIndex() == 0) {
                    new OcsCam(midlet, this).start();
                } else {
                    new OcsPhotoPicker(midlet, this).start();
                }
            } else {
                close();
            }
            return;
        }
        if (c == retryCmd) {
            upload();
        } else if (c == otherCmd) {
            synchronized (this) {
                generation++;
                data = null;
            }
            start();
        } else if (c == cancelCmd) {
            close();
        }
    }
}
