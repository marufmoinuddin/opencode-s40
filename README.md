# OpenCode S40

<p align="center">
  <img src="docs/images/logo.png" alt="OpenCode S40 — talk to your own opencode coding agent from a 2007 Nokia" width="560">
</p>

OpenCode S40 is an unofficial [opencode](https://opencode.ai) client for Nokia
Series 40 phones (Java ME, CLDC 1.1 / MIDP 2.0), plus the small Go gateway it
talks to. The phone never runs opencode: you run `opencode serve` on your own
machine, the gateway translates between the phone's 240x320 world and
opencode's HTTP API, and your code and keys never leave your computer.

<p align="center">
  <img src="docs/images/icon.png" alt="The app icon: opencode's own mark" width="72">
</p>

> Unofficial side project. Not made, endorsed or supported by opencode, its
> authors, or Nokia. The phone app is ported from the MIT-licensed
> [Claude S40](https://github.com/emir/claude-s40) by Emir Karşıyakalı.

## What is in here

| Path | What |
|---|---|
| [`app/`](app/) | The phone app: a CLDC 1.1 / MIDP 2.0 MIDlet, ~132 KB JAR, English + Turkish UI. Reproducible build with 47 package checks. |
| [`server/`](server/) | The gateway: one Go binary. Phone-facing TLS, the opencode client, SQLite, pairing, admin API on loopback. |
| [`docs/`](docs/) | [SETUP.md](docs/SETUP.md) step by step, [ARCHITECTURE.md](docs/ARCHITECTURE.md) protocol, TLS, safety. |

The phone app is ported from the MIT-licensed
[Claude S40](https://github.com/emir/claude-s40) project by Emir Karşıyakalı
(© 2026), which proved the hard parts on real Series 40 hardware: TLS 1.0, the
keypad UI, text layout, paging. The gateway, the protocol and the opencode
integration are specific to this project. See [LICENSE](LICENSE).

## How it works

```
Nokia (Java ME) --HTTPS: TLS 1.0, RSA, no SNI, cert from YOUR private CA--> ocs40-server --HTTP on loopback--> opencode serve
```

A 2007 phone cannot speak modern HTTPS: a Nokia 6300 offers only TLS 1.0 with
RSA key exchange, sends no SNI, and its certificate store stops around 2008, so
no public CA issues a chain it will accept. The gateway therefore terminates TLS
itself with a certificate from a private root CA that you install on the phone
once. Details and measurements: [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

## Look

The palette and the mark are opencode's: the dark theme is the one their server
ships (`#181818` background, `#15141b` panel, `#2d2d2d` border, `#e4e4e4` text),
with their accent `#9a5feb` as the highlight colour, and the wordmark is
generated from their own logo SVG rather than drawn by eye. The light theme is
ours, since opencode's own UI is dark-only. Light and dark, three text sizes.

## Safety: the phone is read-only by default

opencode is a coding agent. Its default agent can run shell commands and write
files, and a 2007 keypad is the worst possible place to grant that by accident.
So:

- The gateway uses opencode's read-only **`plan`** agent by default. Asked to
  create a file, it refuses and uses no tools at all. (Verified: see
  [CHECKLIST.md](CHECKLIST.md).)
- The phone can *ask* for the full-power `build` agent, but the gateway ignores
  that unless you started it with `OCS40_ALLOW_BUILD=1`. The phone cannot widen
  what the server permits.
- The gateway refuses to start unless it can reach your opencode server, so a
  phone can never pair with a dead gateway.

Verified live against opencode 1.18.33: a "create a file" request returned
*"I must not create the file"*, no tool ran, and nothing appeared in the
workspace.

## Quick start

Full guide: **[docs/SETUP.md](docs/SETUP.md)**. In short:

1. `opencode serve --port 4096` on your PC (or `opencode` in that folder).
2. `cd server && make pki` to create a private CA and a server certificate.
3. `make mock` to try the phone with fake replies (no cost), or
   `OCS40_ALLOW_BUILD=... make run` for the real thing.
4. `echo GATEWAY_URL=https://<server-ip> > app/app.local.properties && make -C app`,
   then install `app/dist/OpenCodeS40.jad/.jar` on the phone.
5. Put the root CA on the phone, then pair it from the setup wizard.

## Development

```
make -C server test     # go vet + unit tests, plus live tests when a server is up
make -C app             # build + 47 checks + reproducible rebuild
make -C app emu FREEJ2ME=...   # optional: headless screenshots in an emulator
```

Requirements: Go 1.22+, JDK 11+, Python 3 with Pillow, OpenSSL, `shasum` (from
`perl`) for the pinned build tools.

## Privacy and cost

- opencode runs on **your** machine; the gateway forwards to it over loopback.
  The phone only ever sees the gateway.
- The Claude API key has no equivalent here because there is no API key: your
  opencode provider credentials stay where they are, on your machine.
- The phone gets a per-device, revocable access token through pairing.
- The gateway stores chats in SQLite (30 days after the last message; pinned
  ones until unpinned). Logs contain no message text, no replies, no tokens, no
  keys and no client addresses.
- Every message costs whatever your opencode provider charges. The gateway
  enforces a per-device daily request limit and never retries a possibly-billed
  call on its own.
