# Build checklist — OpenCode S40

1. [x] Scaffold repo + MIT licence/attribution for ported code
2. [x] Server core: OCS40/1 protocol, phone TLS 1.0 config, /health, /echo
3. [x] opencode client: session, prompt_async, SSE /event pump, idle, abort, history
4. [x] Store + pairing + admin API + per-device daily limits
5. [x] Chat semantics: store-then-call, pending/uncertain, part paging, mock mode
6. [x] go vet + go test pass; real TLS 1.0 handshake; live end-to-end chat through gateway
7. [x] Phone app: port reference MIDlet, strip Claude, add opencode specifics
8. [x] Build + package checks + reproducible rebuild of the JAR
9. [x] FreeJ2ME headless run of the real JAR renders screens, zero exceptions
10. [x] Docs (SETUP, ARCHITECTURE, README) + save skill

## Verified with real output (not claims)

Run `./verify.sh` for steps 1-5; all pass from a clean tree.

- `go vet` clean; `go test ./...` green (protocol 9, store 11, server 10, oc 5 live).
- **TLS 1.0 + `TLS_RSA_WITH_AES_128_CBC_SHA`** accepted and verified — the exact
  offer a Series 40 phone makes. Confirmed from both Go and OpenSSL clients.
- **No plain-HTTP path**: a raw HTTP request never reaches the handler
  (`TestNoPlainHTTPPath` speaks to the real socket).
- No RC4/3DES offered; modern clients get TLS 1.3 on the same port.
- Live end-to-end chat returned `GATEWAY_END_TO_END_OK` from opencode 1.18.33.
- Read-only profile: a "create a file" request was refused by the `plan` agent,
  no tool ran, no file appeared in the workspace.
- Replay: the same `request` id replays instead of billing twice.
- Pairing: 6-digit code, admin approval, token handed over exactly once.
- Foreign device cannot read another device's conversation (404).
- HTML error page from a proxy rejected as `not_an_ocs40_request`.
- **48/48** checks in `server/e2e_test.py` against the live opencode server.
- App: 47/47 package checks, byte-identical reproducible rebuild, class file
  version 46.0, manifest first + ASCII, no leaked dependency classes.
- Emulator: the real JAR launched, rendered **29 screens, 0 exceptions**, and
  exited cleanly.

## Bugs found and fixed while building (do not repeat these)

1. `ServeTLS` on an already-wrapped TLS listener served **plain HTTP**. Fixed to
   `Serve`; `TestNoPlainHTTPPath` guards it.
2. `pairMu` was a channel used as a mutex, sent to twice in one path —
   deadlocking the whole gateway on `/v1/pair/start`. Replaced with `sync.Mutex`.
3. `CountUsage` double-counted on a fresh day and silently let an unknown device
   past the daily limit. One statement now, unknown device = error.
4. Replay cached the message **before** `Text` was set, so a retry returned an
   empty body.
5. `mockReply` skipped replay and request counting, so test mode did not
   exercise the live code path.
6. `sanitize` stripped `**bold**` then ran a single-asterisk italic regex that
   ate the rest of the line.
7. modernc SQLite **ignores** `_pragma=foreign_keys(1)` in the DSN, so
   `ON DELETE CASCADE` never fired and deleted conversations left orphan
   messages. Set as a statement, asserted by a test.
8. Passing `tools:{...}` to opencode makes the provider return HTTP 403 ("free
   tier can only be used from within OpenCode"). Dropped: the `plan` agent gives
   the same read-only guarantee without breaking the call.
9. An empty upstream answer was reported as "opencode returned no text", hiding
   the real provider error for an hour of debugging. Errors from
   `message.updated` are now surfaced.
10. Porting: renaming the class to match the file (`L` → `OcsL`) needs the
    ~830 call sites rewritten too, and the `package` statement itself; the
    `class version 46.0` and `check.py` name expectations follow from it.
11. The app's own setup-backup file (RMS/`ocs40-setup.dat`) is restored on the
    next emulator run, which makes the harness click through a wizard that is
    no longer there. Delete `app/build/emu/rms` between runs.
12. **opencode echoes the prompt onto the same SSE stream as the answer**, with
    the same shape as an assistant part, and event order is not guaranteed:
    `session.idle` can arrive before the first delta, and the echo can arrive
    before or after it. The first implementation appended the echo and the
    answer's own full-text snapshot, so a reply came back as
    `"<question><answer><answer>"`.
    The fix is `isQuestion`: the caller sent the question, so it knows the exact
    text, and a part whose text equals it (or whose role is `user`) is the echo.
    Order-independent, no guessing.
    Do NOT try to infer this from stream order or from a history lookup — both
    were tried and both fail: history cannot tell the two messages apart before
    the answer exists.
