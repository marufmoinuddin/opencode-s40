# OpenCode S40 — phone app

Java ME MIDlet for Nokia Series 40 (CLDC 1.1 / MIDP 2.0, class file 46.0).
English and Turkish UI (follows the phone language; Settings → Language).

Ported from the MIT-licensed Claude S40 app by Emir Karşıyakalı; the opencode
integration, protocol and gateway are specific to this project. What the port
keeps is the part that was proven on real Series 40 hardware: the keypad-first
UI, TLS 1.0 handling, text layout, paging and the setup wizard. What it changes
is everything Claude-specific — see `../../docs/ARCHITECTURE.md`.

## Build

```
cp app.local.properties.example app.local.properties   # GATEWAY_URL=https://<your server>
make                                                  # deps (SHA-256 checked), build, 47 checks twice
```

Output: `dist/OpenCodeS40.jad`, `dist/OpenCodeS40.jar` (~132 KB),
`dist/SHA256SUMS`. Version and build live only in `app.properties`.

Needs JDK 11+ (only runs the build tools), Python 3 + Pillow, curl, unzip, and
`shasum` (from `perl`) — `tools/fetch_deps.sh` calls `shasum -a 256`.

The mark is generated, not hand-drawn: `tools/make_mark.py` reads opencode's own
`logo-dark.svg` and writes `OcsMark.java` plus `tools/mark_data.py`, so the
packaged icon and the on-screen wordmark are the same letterforms. The colours
come from the theme opencode's server ships (accent `#9a5feb`).

Pipeline: ECJ compiles against the CLDC 1.1 + MIDP 2.0 API stubs plus the
optional JSR 75 FileConnection API (MicroEmulator jar), the PIM API and the
JSR 135 stubs (compile-only, in `stubs/`), ProGuard `-microedition`
preverifies (no shrink/obfuscate), `tools/package.py` writes a deterministic
JAR and the JAD, and `tools/check.py` verifies it.

## Install

Send the `.jar` to the phone (Bluetooth, PC Suite, or OTA by serving the `.jad`
as `text/vnd.sun.j2me.app-descriptor` and the `.jar` as
`application/java-archive`). Delete any older copy first — see
`../../docs/SETUP.md`.

## Emulator (optional)

```
make emu FREEJ2ME=/path/to/freej2me/classes   # headless screenshots, test mode
```

FreeJ2ME (GPL-3.0) is not included. Emulator success is not device
compatibility. The harness writes its own state under `build/emu/rms`; delete
that directory between runs or the app will restore the setup backup it wrote
the previous time (a real feature, and an annoying surprise in a test).

## Code map

| File | |
|---|---|
| `OcsS40MIDlet` | lifecycle, navigation, settings/about forms, quick prompts |
| `OcsSplash`, `OcsHomeCanvas`, `OcsChatCanvas` | custom screens |
| `OcsChatSession` | conversation state, request ids, retry rules, statuses |
| `OcsNet` | one HTTPS request with phase-aware error reporting |
| `OcsConnTest`, `OcsPairing`, `OcsSetup` | connection test, pairing, wizard |
| `OcsChatList`, `OcsSavedList` | chats on the server (pin, delete, search); replies saved on the phone |
| `OcsCalendarForm`, `OcsCal` | add to calendar / to-do |
| `OcsFiles`, `OcsPim` | the only JSR 75 users: .txt files, calendar, to-do |
| `OcsDictation`, `OcsRec` | voice message screen; `OcsRec` is the only JSR 135 recording user |
| `OcsPhoto`, `OcsCam`, `OcsPhotoPicker` | photo capture and upload |
| `OcsDataUsage` | mobile data counter (RMS) |
| `OcsBackup` | setup kept in a file outside the app, restored after a reinstall |
| `OcsSettings` | RMS record (format 6) |
| `OcsTheme`, `OcsLogo`, `OcsMark`, `OcsSound`, `OcsL`, `OcsText`, `OcsS40Message` | look, mark, tones, language, helpers, protocol |
