# OpenCode S40 — implementation plan

Unofficial OpenCode client for Nokia Series 40 (Java ME MIDlet) + a Go gateway
that fronts the **official `opencode serve`** HTTP API on the developer's PC.

Modelled on the verified architecture of `emir/claude-s40` (MIT, © 2026 Emir
Karşıyakalı) — same shape: a phone-facing TLS 1.0 listener, a private CA, an
S40/1 text protocol, SQLite history, per-device tokens, an admin API bound to
localhost. Reused under MIT with attribution.

---

## 1. Verified facts (all checked against a live server, not assumed)

| Fact | Value | How verified |
|---|---|---|
| opencode CLI | 1.18.33 | `opencode --version` |
| Server mode exists | `opencode serve --port N --hostname H` | `--help` |
| OpenAPI spec served at | `GET /doc` (3.1.0, 478 KB, **162 paths**) | curl |
| Health | `GET /global/health` → `{"healthy":true,"version":"1.18.33"}` | curl |
| Create session | `POST /session {}` → `{id:"ses_..."}` | live |
| Send prompt (blocking) | `POST /session/{id}/message` body `{parts:[{type:"text",text:"..."}]}` → `{info,parts}` | live, got real reply |
| Send prompt (async) | `POST /session/{id}/prompt_async` → **HTTP 204, no body** | live |
| Events | `GET /event` = SSE. Types: `message.part.updated` (`properties.part.text`), `message.part.delta` (`properties.delta`), `session.idle`, `message.updated` | live |
| Completion signal | `session.idle` (2.0 s after a short reply) | live |
| History | `GET /session/{id}/message` → `[{info:{role,error,tokens},parts:[...]}]` | live |
| Abort | `POST /session/{id}/abort` (no body) | spec |
| Agent + tools | `POST /session/{id}/message` accepts `agent`, `tools:{name:bool}`, `system`, `model:{providerID,modelID}` | spec |
| Session-level perms | `POST /session` accepts `permission:[{permission,pattern,action}]` | spec |
| Tool ids in binary | `read, write, edit, bash, glob, list, patch, grep, todowrite, task` | `strings` |
| Agents available | `build, plan, general, explore, summary, title, compaction` | live `/agent` |
| Go SDK | `sst/opencode-sdk-go` last tagged **v0.19.2 (Dec 2025)** — stale vs 1.18.33 | proxy.golang.org |
| Go toolchain here | go1.22.2 | `go version` |
| Reference app scale | 8,862 lines Java / 31 classes; server 4,875 lines Go | wc |

**Decision: do not use the stale Go SDK.** Hand-roll a ~300-line client against
the documented endpoints. Less version risk, and it is the only way to consume
the SSE event stream the phone needs.

## 2. Hard constraint that drives the whole design

A Nokia S40 phone (6300-class, CLDC 1.1/MIDP 2.0) offers **only TLS 1.0, no
SNI, RSA key exchange, AES-CBC-SHA**, and its root store is from ~2008. Measured
ClientHello facts are in the reference project's ARCHITECTURE.md and I reuse
them:

- Server terminates TLS itself: `MinVersion: VersionTLS10`, explicit cipher
  list including `TLS_RSA_WITH_AES_128/256_CBC_SHA`, **no RC4, no 3DES**.
- Certificate from a **private root CA** the user installs on the phone once.
  RSA-2048, **SHA-1 signatures** (verified on real hardware; SHA-256 untested
  there), root 10 y / server 2 y.
- Never a plain-HTTP listener. Never disable cert checks. Never SNI-dependent.

## 3. What is dangerous here, and the guardrails

opencode is a **coding agent**: its `build` agent can run `bash`, `write`,
`edit`, `patch`. Driving that from a 2007 phone keypad is a real hazard, so the
gateway enforces these server-side and the phone cannot override them:

1. **Default agent is `plan`** (read-only: "Disallows all edit tools"), and
   `build` must be selected explicitly by the user.
2. **Per-session tool policy.** A `PERMISSION_PROFILE=read-only` (default)
   passes `tools` disabling `write/edit/patch/bash`; `read-only` is the only
   profile allowed unless the operator sets `ALLOW_BUILD=1` explicitly in the
   server env.
3. **Workspace containment.** The gateway refuses to run unless the opencode
   project directory is inside `PROJECT_DIR` (default: the repo you point it at).
4. **Deny list is server-side, not phone-side.** Even `ALLOW_BUILD=1` keeps a
   deny list for the paths that matter (`PERMISSION_DENY`, default excludes
   `**/.env*`, `**/*.pem`, `**/*.key`, `**/id_*`, `**/.git/**`).
5. **Nothing auto-runs.** Every message is an explicit Send from the phone. No
   retries of a possibly-billed call, same rule as the reference project.

Mock mode returns `[Test mode]` replies and never touches opencode.

## 4. Protocol — `OCS40/1`, a clean fork of the reference `S40/1`

Same shape (a status line, `key: value` headers, blank line, UTF-8 text body)
because the reference proves the 2007 phone's HTTP stack handles it:

    OCS40/1
    status: ok
    conversation: c_9f2a1b
    truncated: 1
    more: 1
    next: 2000
    mock: 0

    <free text, UTF-8>

Routes (bearer-token auth on all but the first two):

| Route | Purpose |
|---|---|
| `GET /health` | server, version, mock flag, TLS version/cipher of *this* connection |
| `POST /echo` | ≤512 B strict UTF-8 round trip; Turkish probe string check |
| `POST /v1/pair/start` | → `pair` (128-bit id), `code` (6 digits), `expires` |
| `POST /v1/pair/claim` | body `pair: <id>` → `pending` / `ok` + `device` + `token` (once) / `expired` |
| `POST /v1/chat` | the message → opencode → reply text |
| `POST /v1/more` | next part of a long stored reply; **never** re-asks opencode |
| `POST /v1/conversations` | pinned then newest, one line each `id TAB updated-ms TAB messages TAB title` |
| `POST /v1/history` | newest messages ≤6000 B, oldest first, `u N`/`a N` + length + text |
| `POST /v1/pin`, `POST /v1/delete` | pin/unpin, delete a conversation |
| `POST /v1/search` | every-word AND match, case- and Turkish-insensitive, never calls opencode |
| `GET /v1/status` | opencode server reachability + current model (for the connection test) |

Long replies: server stores ≤8000 chars, sends ≤2000-char parts, `more: 1` +
`next: <offset>`. Matches the reference so the proven phone-side paging code is
reusable.

## 5. Layout

    ocs40/
      app/       Java ME MIDlet, package io.github.maruf.ocs40, CLDC 1.1/MIDP 2.0, class 46.0
      server/    Go: phone TLS listener, opencode client, SQLite store, admin API
      docs/      SETUP.md, ARCHITECTURE.md
      Makefile

Phone app: port the reference app (MIT, attributed) and change what is
Claude-specific. New classes for opencode-specific concerns. Keep the proven
UI/navigation/TLS/paging code — that is the expensive part and it is
device-verified on a 6300.

Server: new code. opencode client, event-stream pump, tool policy.

## 6. Build plan — ordered, each step verified

1. **Scaffold + licence/attribution.** Clone-free: new repo dir, MIT licence
   naming Emir Karşıyakalı as the original author of the ported code.
2. **Server core**: `OCS40/1` parse/format, TLS config, `main.go` wiring, `/health`,
   `/echo`. Test with a real TLS 1.0 client.
3. **opencode client**: `internal/oc` — session create, prompt_async, SSE
   `/event` pump, idle detection, text extraction, abort, history. **Unit-test
   against the live server I already have running on :7777.**
4. **Store + pairing + admin.** SQLite (modernc, pure Go), per-device tokens,
   6-digit pairing, daily limits, `request_id` replay, `uncertain` semantics.
5. **Chat semantics**: store-then-call so a dropped connection returns
   `pending`, never an exactly-once claim; part paging; mock mode.
6. **Phone app port**: strip Claude, keep UI, add `/v1/status`, tool-profile
   display, agent switcher (plan/build), notes field.
7. **Build + package checks** (reusing the reference's ECJ/ProGuard pipeline and
   its `check.py`, extended for the new fields).
8. **Verify**: 47-style checks, reproducible rebuild, and a **FreeJ2ME headless
   run** of the real JAR (I already have a working emulator build).
9. **Docs**: SETUP.md, ARCHITECTURE.md, README.md.
10. **Skill** so this is reproducible.

## 7. Definition of done

- `make -C app` produces a reproducible ~100 KB JAR passing all package checks,
  class version 46.0, no leaked deps.
- Server `go vet` + `go test` pass; TLS 1.0 handshake verified with a real
  TLS 1.0 client; `/health` reports the right cipher.
- A real end-to-end chat through the gateway to the **live** opencode server
  returns a real answer (not `[Test mode]`).
- Mock mode returns `[Test mode]` and never calls opencode.
- Read-only profile proven: a "write a file" request cannot write.
- FreeJ2ME headless run of the real JAR renders screens, zero exceptions.
- No secrets or personal data in tracked files.

## 8. Risks and honest limits

- **Untested on real hardware.** I can prove the JAR builds, the MIDlet runs in
  an emulator, and the TLS 1.0 handshake works. I cannot prove a Nokia 6300
  accepts it without a Nokia. SHA-1 certs and the exact cipher are the
  measured, known-good combination from the reference project's real device.
- **opencode API drift.** Pinned against 1.18.33's live `/doc`. If opencode
  changes, the gateway's `/v1/status` health check is the tripwire.
- **A 240x320 screen is a poor terminal** for a coding agent. The phone app is
  genuinely useful for *asking questions and reading answers*; editing code on
  a keypad is not the goal, and the read-only default reflects that.
- **Every opencode call bills your provider.** Daily limits are per device and
  server-enforced, but the real spend is whatever your opencode provider costs.
