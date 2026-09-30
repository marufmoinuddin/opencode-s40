# Setup guide

From zero to "opencode answers on my Nokia". Allow an hour the first time.
Commands run on your computer (Linux or macOS) unless stated otherwise.

**What you need**

- A Nokia Series 40 phone with Java (CLDC 1.1 / MIDP 2.0), 240x320.
  Verified on a Nokia 6300 RM-217 V06.60; other Series 40 phones are untested.
- A SIM with mobile data.
- **[opencode](https://opencode.ai) on your own machine**, able to serve its API
  (`opencode serve`). No API key for us to hold: the phone reaches *your*
  opencode, on your machine.
- A public IPv4 address reachable from the phone, with TCP 443 open. Your own PC
  is fine if you can port-forward; a small VPS is the usual answer. Cloud
  platforms that terminate HTTPS for you (Cloudflare, Vercel, ...) will not
  work — the phone cannot complete their handshake (see
  [ARCHITECTURE.md](ARCHITECTURE.md)).
- On your computer: Go 1.22+, JDK 11+, Python 3 with Pillow, OpenSSL, curl, and
  `shasum` (ships with `perl`).
- A way to put a JAR on the phone: Bluetooth, Nokia PC Suite, Gammu over USB, or
  the phone's own browser.

Throughout: `SERVER_IP=<your public IPv4>` and `GATEWAY=https://$SERVER_IP`.

---

## 1. Start opencode on your machine

In the project folder you want the agent to work in:

```
opencode serve --port 4096
```

It prints a health line; check it:

```
curl http://127.0.0.1:4096/global/health
```

You should see `{"healthy":true,"version":"..."}`. Leave it running. (You can
also use the opencode TUI's own server; any `opencode serve` on 4096 works.)

## 2. The private certificate authority

The phone will trust **only** this root. Keep `~/.config/ocs40/pki` private and
backed up. The root key never goes anywhere.

```
cd server
make pki HOST=$SERVER_IP        # creates the CA and the server certificate
make pki                        # or, to see the fingerprints to compare later:
scripts/pki.sh show ~/.config/ocs40/pki
```

Note the root's **SHA-1** fingerprint. You will compare it on the phone.

Why a private CA and SHA-1: a 2007 phone's certificate store is from around
2008, so no public CA issues a chain it accepts, and the phone only verifies
SHA-1-signed certificates. SHA-1 is acceptable here because only you issue
certificates under this root. (Modern Go and OpenSSL refuse SHA-1 by default;
the tests set `GODEBUG=x509sha1=1`. The phone does not need it.)

## 3. Build and run the gateway

Without a public address yet, start it in **mock** mode: the phone works fully
(fake replies, nothing costs anything, opencode is never called).

```
cd server
make mock
```

For real answers, point it at your opencode and set the listen address:

```
cd server
OCS40_LISTEN=:443 \
OPENCODE_URL=http://127.0.0.1:4096 \
OPENCODE_PROJECT=$HOME/code/myproject \
make run
```

Settings worth knowing (all optional except the first):

| Variable | Default | What it does |
|---|---|---|
| `OCS40_LISTEN` | `:443` | phone-facing TLS address |
| `OCS40_CERT` / `OCS40_KEY` | `certs/server-chain.pem` / `.key` | the private-CA certificate and key |
| `OPENCODE_URL` | `http://127.0.0.1:4096` | your opencode server (loopback) |
| `OPENCODE_PROJECT` | (opencode's cwd) | the folder the agent works in |
| `OCS40_AGENT` | `plan` | `plan` = read-only (safe), `build` = can edit and run commands |
| `OCS40_ALLOW_BUILD` | `0` | set to `1` to let the phone request the `build` agent |
| `OCS40_MODEL` | (opencode's choice) | force a `provider/model` |
| `OCS40_REQ_LIMIT` | `100` | requests per phone per UTC day |
| `OCS40_MOCK` | `0` | `1` = fake replies, opencode never called |
| `OCS40_DB` | `data/ocs40.db` | SQLite file (chats, devices) |

The admin API is on `127.0.0.1:8781` only. Its token is written next to the
database on first start (`data/admin_token`); read it when you need to approve
a phone:

```
cat data/admin_token
```

## 4. Build the phone app

```
cd ..
echo "GATEWAY_URL=$GATEWAY" > app/app.local.properties
make -C app
ls app/dist        # OpenCodeS40.jad, OpenCodeS40.jar, SHA256SUMS
```

(Without `app.local.properties` the build still works; you type the address into
the app instead.)

## 5. Install the app on the phone

Pick one:

- **Bluetooth**: send `OpenCodeS40.jar` to the phone, open it from the inbox.
- **Nokia PC Suite** (Windows): Install applications.
- **Gammu over USB** (see `app/tools/install-gammu.sh` from the reference
  project): dry-run first; it never overwrites — delete an older copy by hand.
- **Browser (OTA)**: serve `OpenCodeS40.jad` as
  `text/vnd.sun.j2me.app-descriptor` and the `.jar` as
  `application/java-archive`, then open the JAD URL on the phone.

On a Nokia 6300 the app appears under Menu → Applications → Collection. The
first network access asks permission — allow it.

## 6. Put the root CA on the phone

The phone must trust your root before the connection test can pass.

1. Open `http://<server-ip>/ca.cer` in the phone's browser (temporarily allow
   port 80, or copy `~/.config/ocs40/pki/ocs40-ca.cer` to the phone any way you
   like — Bluetooth, USB, an email to yourself).
2. Before saving, **compare the fingerprint the phone shows** with the one
   `scripts/pki.sh show` printed. Do not save it if they differ.
3. Save it as an authority certificate and allow it for applications /
   connections if asked.

Also make sure the phone's mobile data works (open any plain `http://` page in
its browser). On old phones select the operator's *internet* access point, not a
stale WAP profile.

## 7. Connection test and pairing

In the app. A fresh install opens a setup wizard that walks through this:

1. **Connection test → Start**: it checks `/health`, does a strict UTF-8 round
   trip (`/echo`, including Turkish characters), and then reports your opencode
   version, the agent and the tool profile. Chat stays locked until it passes.
   If the phone offers to accept an untrusted certificate, say **No** — that
   means step 6 is missing.
2. **Settings → Options → Pair this phone**: a 6-digit code appears.
3. On your computer, approve it:

   ```
   curl -H "Authorization: Bearer $(cat data/admin_token)" \
     "http://127.0.0.1:8781/admin/pair?code=<code>&name=My+Nokia"
   ```

   The phone fetches its access token by itself within a few seconds.
4. **Chat.** In mock mode you get "[Test mode]" replies; live, you get real
   opencode answers.

## 8. Day to day

```
curl -H "Authorization: Bearer $(cat data/admin_token)" http://127.0.0.1:8781/admin/devices
curl -H "Authorization: Bearer $(cat data/admin_token)" "http://127.0.0.1:8781/admin/revoke?id=dev_xxx"
```

Request logs are the gateway's stdout, one JSON line per request: method, path,
status, TLS version and cipher. Never message text, tokens, keys or client
addresses.

On the phone: **Chats** lists earlier conversations; `0` loads the rest of a
long reply (free, opencode is not asked again), `7` is reading mode, `9` the
text size, `1/3` jump between messages. Options → Shortcuts lists every key.

## Troubleshooting

| Symptom on the phone | Likely cause |
|---|---|
| "signature not verified" | root CA not saved on the phone (step 6) |
| "host name mismatch" | certificate issued for a different name/IP than the app is told to use |
| "Could not connect ... TLS" and nothing in the logs | mobile data / access point; or a CDN/proxy in front of the gateway (it must terminate TLS itself) |
| "opencode unreachable" in the connection test | the gateway cannot reach your opencode (`OPENCODE_URL`), or `opencode serve` stopped |
| "Daily limit reached" | raise `OCS40_REQ_LIMIT` |
| "Access code invalid or revoked" | pair again; the old device is gone |
| The agent says it cannot read/write files | it is in `plan` (read-only) mode, which is the default and the safe one |
