package io.github.maruf.ocs40;

import java.io.ByteArrayInputStream;
import java.io.ByteArrayOutputStream;
import java.io.DataInputStream;
import java.io.DataOutputStream;
import java.io.IOException;

import javax.microedition.rms.RecordStore;
import javax.microedition.rms.RecordStoreException;

/**
 * OcsSettings kept on the phone (RMS record store "cs40cfg", one record).
 *
 * Stored: gateway URL, access code, test mode flag, the URL for which the
 * connection test last passed, look & feel (theme, text size, sound,
 * vibration, language), web search on/off and whether the last chat is kept
 * on the phone (OcsChatStore; off by default), and whether the setup wizard was
 * finished or skipped, the two backlight options and the user's notes for
 * OpenCode (sent with every message). No chat content is stored here. The
 * setup part is also kept outside the app (OcsBackup), so reinstalling does
 * not need the setup wizard again.
 * Format 1-5 records (0.1.x-0.6.x) are still read.
 *
 * The access code is typed on the phone by the user; it is never part of
 * the JAR/JAD. Removing the application deletes this record store.
 */
final class OcsSettings {

    private static final String STORE = "cs40cfg";
    private static final int FORMAT = 6;
    /** Longest note for OpenCode (the server keeps at most 300 characters). */
    static final int MAX_INSTRUCTIONS = 300;

    String url = "";
    String token = "";
    boolean testMode;
    /** URL on which the connection test (health + echo) last succeeded. */
    String verifiedUrl = "";
    /** 0 = light (Gündüz), 1 = dark (Gece). */
    int theme;
    /** 0 small, 1 medium, 2 large. */
    int fontSize = 1;
    boolean sound = true;
    boolean vibrate = true;
    /** OcsL.AUTO (follow the phone), OcsL.TURKISH or OcsL.ENGLISH. */
    int lang = OcsL.AUTO;
    /**
     * Let opencode use its full-power "build" agent, which can edit files and
     * run shell commands. Default off: a coding agent driven from a keypad
     * should be read-only unless the operator deliberately allows otherwise.
     * The gateway enforces this independently, so this flag is a request, not a
     * grant.
     */
    boolean buildAgent;
    /** Keep the last chat on the phone for offline reading (OcsChatStore). */
    boolean saveChat;
    /** The setup wizard was finished or skipped (OcsSetup); true for settings from before 0.5.0. */
    boolean setupDone;
    /** Keep the backlight on while a reply is read in reading mode (Display.flashBacklight). */
    boolean lightReading = true;
    /** Light the screen up when a reply arrives. */
    boolean lightReply = true;
    /** The user's notes for OpenCode ("my name is..., answer briefly"); "" if none. */
    String instructions = "";
    /** load() found saved settings; false right after installing (OcsBackup may restore them). */
    boolean stored;

    boolean connectionVerified() {
        return url.length() > 0 && url.equals(verifiedUrl);
    }

    boolean ready() {
        return OcsNet.isHttps(url) && token.length() >= 16 && connectionVerified();
    }

    void load(String defaultUrl) {
        url = defaultUrl == null ? "" : defaultUrl.trim();
        RecordStore rs = null;
        try {
            rs = RecordStore.openRecordStore(STORE, false);
            byte[] b = rs.getRecord(1);
            DataInputStream in = new DataInputStream(new ByteArrayInputStream(b));
            int format = in.readInt();
            stored = format >= 1 && format <= FORMAT;
            if (format >= 1 && format <= FORMAT) {
                url = in.readUTF();
                token = in.readUTF();
                testMode = in.readBoolean();
                verifiedUrl = in.readUTF();
            }
            if (format >= 2 && format <= FORMAT) {
                theme = in.readByte();
                fontSize = in.readByte();
                sound = in.readBoolean();
                vibrate = in.readBoolean();
            }
            if (format >= 3 && format <= FORMAT) {
                lang = in.readByte();
            }
            if (format >= 4 && format <= FORMAT) {
                buildAgent = in.readBoolean();
                saveChat = in.readBoolean();
            }
            // settings from before 0.5.0 belong to a phone that is already set up
            setupDone = format >= 5 && format <= FORMAT ? in.readBoolean() : format >= 1 && format < 5;
            if (format >= 5 && format <= FORMAT) {
                lightReading = in.readBoolean();
                lightReply = in.readBoolean();
            }
            if (format >= 6 && format <= FORMAT) {
                instructions = in.readUTF();
            }
        } catch (RecordStoreException e) {
            // first start: nothing stored yet
        } catch (IOException e) {
            // unreadable: keep defaults
        } finally {
            closeQuietly(rs);
        }
        if (url.length() == 0 && defaultUrl != null) {
            url = defaultUrl.trim(); // nothing saved yet: use OpenCodeS40-Gateway from the JAD
        }
        if (stored) {
            OcsBackup.loaded(this);
        }
    }

    /** Returns null on success, or a message for the user. */
    String save() {
        RecordStore rs = null;
        try {
            ByteArrayOutputStream bo = new ByteArrayOutputStream();
            DataOutputStream out = new DataOutputStream(bo);
            out.writeInt(FORMAT);
            out.writeUTF(url);
            out.writeUTF(token);
            out.writeBoolean(testMode);
            out.writeUTF(verifiedUrl);
            out.writeByte(theme);
            out.writeByte(fontSize);
            out.writeBoolean(sound);
            out.writeBoolean(vibrate);
            out.writeByte(lang);
            out.writeBoolean(buildAgent);
            out.writeBoolean(saveChat);
            out.writeBoolean(setupDone);
            out.writeBoolean(lightReading);
            out.writeBoolean(lightReply);
            out.writeUTF(instructions);
            out.close();
            byte[] b = bo.toByteArray();
            rs = RecordStore.openRecordStore(STORE, true);
            if (rs.getNumRecords() == 0) {
                rs.addRecord(b, 0, b.length);
            } else {
                rs.setRecord(1, b, 0, b.length);
            }
            stored = true;
            OcsBackup.changed(this);
            return null;
        } catch (RecordStoreException e) {
            return OcsL.s("Ayarlar kaydedilemedi: ", "Could not save settings: ") + e.getMessage();
        } catch (IOException e) {
            return OcsL.s("Ayarlar kaydedilemedi: ", "Could not save settings: ") + e.getMessage();
        } finally {
            closeQuietly(rs);
        }
    }

    private static void closeQuietly(RecordStore rs) {
        if (rs != null) {
            try {
                rs.closeRecordStore();
            } catch (RecordStoreException e) {
                // ignore
            }
        }
    }
}
