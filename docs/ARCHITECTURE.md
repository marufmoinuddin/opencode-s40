# Architecture

## Why a private CA and our own TLS endpoint

Measured on a Nokia 6300 (RM-217, V06.60) against the reference gateway:

```
ClientHello: TLS 1.0 only, no SNI, no extensions
cipher suites: RC4-MD5, RC4-SHA, 3DES-EDE-CBC-SHA, AES128-CBC-SHA, AES256-CBC-SHA
```

and the phone's certificate store contains only roots from the late 1990s and
2000s (expired, or distrusted Symantec roots under which no public CA issues
TLS certificates today).

Consequences:

- Cloudflare and similar CDNs serve ECDSA certificates to such a client and
  require SNI → the handshake fails before a certificate is even sent.
- No publicly obtainable chain leads to a root the phone trusts.

So the gateway terminates TLS itself with an RSA-2048 certificate from a
private root CA the user saves on the phone once. Server TLS policy:

- TLS 1.0-1.3 accepted.
- `TLS_RSA_WITH_AES_128_CBC_SHA` / `_256_` **offered**, because that is what the
  phone negotiates (no ECDHE, no GCM).
- ECDHE-RSA suites for modern clients on the same port.
- **No RC4, no 3DES, no plain-HTTP listener.** (Asserted by
  `TestNoPlainHTTPPath` and `TestTLS10WithPhoneCipher`.)
- SHA-1 signatures by default, because that is the combination verified on real
  Series 40 hardware. SHA-256 is untested there.

### The plain-HTTP trap

An earlier version called `http.Server.ServeTLS` on a listener that had already
been wrapped in `tls.NewListener`, with empty certificate paths. That serves
**plain HTTP**: `ServeTLS` sees no cert files and hands the connection to the
plain `Serve` path. A phone could then talk to the gateway unencrypted. The
code now calls `Serve` on the wrapped listener, and
`TestNoPlainHTTPPath` writes a raw HTTP request to the real socket and requires
that the application handler never runs.

## Components

```
app/     Java ME MIDlet (CLDC 1.1, MIDP 2.0, class file 46.0), package io.github.maruf.ocs40
server/  Go: phone TLS listener, opencode client, SQLite store, admin API on loopback
```

### Phone app

Ported from the MIT-licensed Claude S40 app; the gateway and protocol are
specific to this project. What was kept because it is device-verified: the
keypad-first navigation, the custom `Canvas` screens, the text layout
(paragraphs, "1." and "- " items with a hanging indent), reading mode, long
replies in parts, chats list with pin/search, the setup wizard, the connection
test, the offline copy, the theme and text sizes, and the strict HTTPS-only
`Net` layer with phase-aware error reporting.

What changed for opencode:

- The `KIND_CLAUDE` message kind is now `KIND_ASSISTANT`.
- Anthropic's server-side web search is gone (opencode has no equivalent server
  tool). The settings slot is now the **`build` agent** request, and the
  "web search can take a while" note became "opencode is working, this can take
  a while", which is true of a coding agent.
- The connection test gained a third step, `GET /v1/status`, which reports the
  opencode version, the agent and the tool profile the gateway is enforcing.
  That is the opencode-specific thing worth showing on a 240x320 screen.
- RMS records and the setup-backup file are named for this app, so an installed
  Claude S40 and this app never share state.

### Server

- `main.go` wiring, routes, pairing, TLS policy, JSON-line logging.
- `chat.go` — the `OCS40/1` chat semantics: paging, replay, tool policy,
  mock mode, Markdown cleanup.
- `internal/oc` — a small hand-written client for opencode's HTTP API.
- `internal/protocol` — the wire format.
- `internal/store` — SQLite: devices, conversations, messages, search.
- `admin.go` — loopback-only admin API (pair, devices, revoke, status).

## The opencode client

Written by hand rather than using the published Go SDK
(`github.com/sst/opencode-sdk-go`, last tagged v0.19.2 in December 2025 while
the CLI is at 1.18.33). The SDK is stale relative to the server, and it does not
expose the event stream the phone needs. The endpoints were read from the
running server's own OpenAPI document (`GET /doc`) and verified with live calls
against opencode 1.18.33:

| Call | Behaviour |
|---|---|
| `GET /global/health` | `{"healthy":true,"version":"..."}` |
| `GET /agent` | the agent list (`build`, `plan`, `explore`, ...) |
| `POST /session` | create; returns `{"id":"ses_..."}` |
| `POST /session/{id}/prompt_async` | send; **HTTP 204, no body** |
| `GET /event` | SSE: `message.part.delta`, `message.part.updated`, `session.idle`, ... |
| `GET /session/{id}/message` | history, used when the stream drops |
| `POST /session/{id}/abort` | stop a run |

The phone gets a typing indicator for free because the gateway subscribes to
`/event` first, sends the prompt, and assembles `message.part.delta` increments
until `session.idle` marks the answer complete. If the stream dies, the answer
is recovered from history rather than lost.

Two things about that stream are not obvious and both cost real debugging time
on 1.18.33:

- **The prompt is echoed back on the same stream**, as a text part shaped
  exactly like an assistant part. Nothing in the part itself distinguishes the
  question from the answer.
- **Event order is not guaranteed.** `session.idle` has been observed arriving
  before the first delta and between deltas.

So the answer is identified by text, not by position: the gateway sent the
question, so it knows the question's exact text, and a part equal to it (or
labelled `user`) is the echo and is dropped. On an idle that carried no text, a
short grace period reads on before falling back to history. Inferring the answer
from stream order, or from a history lookup, does not work: history cannot tell
the two messages apart before the answer exists.

### Errors opencode reports on the message, not the HTTP status

A provider refusal (rate limit, quota, refused model) arrives as an assistant
message carrying an `error` object, with no text. Without care this looks
exactly like an empty answer — it cost an hour of debugging a "gateway bug" that
was really the provider. The client therefore records the error from
`message.updated` and `WaitIdle` returns it, so the phone shows the real reason.

### The `tools` override is deliberately not used

Passing `tools: {bash: false, write: false, ...}` to opencode makes the provider
reject the request: HTTP 403, *"OpenCode's free tier can only be used from
within OpenCode"*. Every phone message would have failed. The read-only
guarantee comes from the **agent** instead, which needs no `tools` field.

## Safety model

opencode is a coding agent, so "what can a phone make it do" is the question
that matters most in this design.

1. **The gateway picks the agent, not the phone.** `plan` is opencode's
   read-only primary agent; it refuses edit tools at the prompt level. Verified
   live: asked to create a file, it declined, used no tools, and nothing
   appeared in the workspace. The `build` agent is used only when the operator
   set `OCS40_ALLOW_BUILD=1`.
2. **The phone cannot widen it.** A `build: 1` field is honoured only when the
   server already allows it. Turning the switch on in Settings changes what the
   phone *asks* for, never what the gateway permits.
3. **No keys on the gateway.** opencode runs on your machine and the gateway
   talks to it over loopback, so there is no API key to leak from the server.
4. **Pairing, not open access.** A phone needs a revocable per-device token
   obtained through a 6-digit code you approve; the admin API that issues tokens
   is bound to loopback.
5. **No automatic retries of a possibly-billed call.** A phone retry replays
   the stored reply (same `request` id) instead of asking again. If a call times
   out the phone is told `uncertain`, not "failed" — the protocol makes no
   exactly-once claim.

## `OCS40/1` protocol

A clean fork of the reference project's `S40/1`, keeping the shape the 2007
phone's HTTP stack is known to handle: a magic line, `key: value` headers, a
blank line, then a UTF-8 body. Headers are ASCII and newlines in values are
collapsed, so a header can never break the framing.

    OCS40/1
    status: ok
    conversation: c_9f2a1b
    more: 1
    next: 2000

    <free text, UTF-8>

| Route | Auth | Purpose |
|---|---|---|
| `POST /health` | – | server, version, mock flag, the TLS version/cipher of *this* connection |
| `POST /echo` | – | ≤512 bytes strict UTF-8, echoed; `probe: match` for the Turkish test string |
| `GET /v1/status` | – | opencode version, agent, tool profile, agent list (the connection test's step 3) |
| `POST /v1/pair/start` | – | → `pair` (id), `code` (6 digits), `expires` |
| `POST /v1/pair/claim` | – | body `pair: <id>` → `pending` / `ok` + `device` + `token` (once) / `expired` |
| `POST /v1/chat` | Bearer | the message → opencode → reply text |
| `POST /v1/more` | Bearer | the next part of a stored reply; never calls opencode again |
| `POST /v1/conversations` | Bearer | pinned then newest, one line each: `id TAB updated-ms TAB messages TAB title` |
| `POST /v1/history` | Bearer | newest messages ≤6000 bytes, oldest first, `u N` / `a N` + length + text |
| `POST /v1/pin` | Bearer | `conversation: <id>`, `pinned: 1` or `0` |
| `POST /v1/delete` | Bearer | `conversation: <id>` |
| `POST /v1/search` | Bearer | every-word AND match, case- and Turkish-insensitive ("sise" finds "Şişe") |

Chat statuses: `ok` (with `conversation`, `more`, `next`, `truncated`, `mock`,
`replayed`), `pending`, `limit`, `conversation_not_found`, `upstream_error`,
`uncertain`, plus input errors. A body that is not `OCS40/1` (an operator proxy
or an HTML error page) is rejected with `not_an_ocs40_request` rather than
misread.

### Long replies in parts

The gateway stores up to 8000 characters of an answer and sends it in parts of
≤2000 (the phone reads at most 8 KiB). `more: 1` + `next: <offset>` mean another
part can be fetched with `/v1/more`, which never calls opencode. The cut prefers
a line boundary and never splits a UTF-8 sequence, so a phone never shows a
broken character or half a sentence at a page edge.

## Rules for changing this

- Keep the phone on CLDC 1.1 / MIDP 2.0 APIs only: no `StringBuilder`, no
  generics, no `String.format`. Every user-visible string is bilingual
  `OcsL.s("Türkçe", "English")`. Sizes come from `getWidth()`/`getHeight()`.
  Networking only on worker threads. HTTPS only, no fallback.
- Bump `VERSION`/`BUILD` in `app/app.properties` for every build given to a
  phone; never reuse a version. JAD/manifest values stay ASCII.
- The protocol is shared: keep changes backwards compatible, or bump both sides
  together.
- Never add a plain-HTTP listener, never disable certificate checks, never offer
  RC4/3DES.
- Logs never contain message text, replies, tokens, keys or client addresses.
- Mock replies say "[Test mode]" and are never presented as real opencode output.
- If you change the opencode integration, re-run the live tests against a real
  `opencode serve` (`server/e2e_test.py`, 48 checks). The API is versioned
  independently of this project and drifts; `/v1/status` is the tripwire.
