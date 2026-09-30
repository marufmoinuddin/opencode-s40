#!/usr/bin/env python3
"""
End-to-end test of the OpenCode S40 gateway exactly as the phone would speak it:
OCS40/1 bodies over HTTPS, bearer-token auth, 6-digit pairing, then a real chat
through the live opencode server.

TLS 1.0 is used because that is what a Series 40 phone offers; a modern OpenSSL
refuses a SHA-1 root unless the security level is lowered, which is exactly the
weak-store situation the real phone is in.
"""
import json
import os
import socket
import ssl
import time
import urllib.error
import urllib.request

# Overridable so e2e.sh can run the whole stack (opencode + gateway + PKI) in
# one command without hand-picking ports.
BASE = os.environ.get("E2E_BASE", "https://127.0.0.1:9443")
CA = os.environ.get("E2E_CA", "/tmp/ocs40-pki/ca.pem")
ADMIN = os.environ.get("E2E_ADMIN", "http://127.0.0.1:8781")
HOST, PORT = BASE.split("://")[1].split(":")
PORT = int(PORT)
# The admin token is written next to the gateway database (see admin.go).
ADMIN_TOKEN_FILE = os.environ.get(
    "E2E_ADMIN_TOKEN_FILE",
    os.path.join(os.path.dirname(os.environ.get("E2E_DB", "/tmp/ocs40-run/ocs40.db")), "admin_token"),
)

# The phone's exact capability: TLS 1.0, RSA key exchange, AES-CBC-SHA.
# SECLEVEL=0 is needed because the private root is SHA-1 signed; a Series 40
# phone's own store accepts it without any such flag.
phone_ctx = ssl.create_default_context(cafile=CA)
phone_ctx.minimum_version = ssl.TLSVersion.TLSv1
phone_ctx.maximum_version = ssl.TLSVersion.TLSv1
phone_ctx.set_ciphers("AES128-SHA:@SECLEVEL=0")

# A modern client (curl, the admin tooling): any version.
ctx = ssl.create_default_context(cafile=CA)
ctx.set_ciphers("DEFAULT:@SECLEVEL=0")


def body(keys, text=""):
    out = "OCS40/1\n"
    for k, v in keys.items():
        out += f"{k}: {v}\n"
    return (out + "\n" + text).encode()


def call(path, keys=None, text="", token=None, raw=False, timeout=180, context=None):
    data = body(keys or {}, text)
    req = urllib.request.Request(BASE + path, data=data, method="POST")
    req.add_header("content-type", "text/plain; charset=utf-8")
    if token:
        req.add_header("Authorization", "Bearer " + token)
    try:
        with urllib.request.urlopen(req, timeout=timeout, context=context or ctx) as r:
            payload = r.read().decode()
            code = r.status
    except urllib.error.HTTPError as e:
        payload = e.read().decode()
        code = e.code
    if raw:
        return code, payload
    fields, btext = parse(payload)
    return code, (fields, btext)


def parse(payload):
    lines = payload.split("\n")
    if not lines or lines[0].strip() != "OCS40/1":
        raise AssertionError(f"not an OCS40/1 body: {payload[:120]!r}")
    fields, i = {}, 1
    while i < len(lines) and lines[i].strip() != "":
        k, _, v = lines[i].partition(":")
        fields[k.strip()] = v.strip()
        i += 1
    return fields, "\n".join(lines[i + 1:])


def admin(path):
    tok = open(ADMIN_TOKEN_FILE).read().strip()
    req = urllib.request.Request(ADMIN + path)
    req.add_header("Authorization", "Bearer " + tok)
    with urllib.request.urlopen(req, timeout=20) as r:
        return r.read().decode().strip()


ok = True


def check(name, cond, detail=""):
    global ok
    print(f"{'PASS' if cond else 'FAIL'}  {name}" + (f"  [{detail}]" if detail else ""))
    if not cond:
        ok = False


print("=== 1. health (what the phone's connection test calls) ===")
code, (f, t) = call("/health", context=phone_ctx)
check("health returns 200", code == 200, f"HTTP {code}")
check("health is OCS40/1", f.get("status") == "ok", f.get("status", "?"))
check("reports the gateway version", f.get("version", "").startswith("0."), f.get("version", "?"))
# This connection is the phone's: TLS 1.0 and the RSA/AES-CBC-SHA suite.
check("reports TLS 1.0", "TLS 1.0" in f.get("tls", ""), f.get("tls", "none"))
check("reports the phone's cipher AES128-SHA", "AES_128" in f.get("cipher", ""), f.get("cipher", "none"))
print(f"      tls={f.get('tls')} cipher={f.get('cipher')} mock={f.get('mock')}")

print("\n=== 1b. a modern client is served on the same port ===")
code, (f, t2) = call("/health")
check("modern client also gets 200", code == 200, f"HTTP {code}")
check("and a modern TLS version", f.get("tls", "").startswith("TLS 1."), f.get("tls", "none"))

print("\n=== 1c. RC4/3DES are not offered ===")
# Modern OpenSSL builds often do not even ship RC4, which is itself evidence
# the weak suites are gone. What matters is that the server never negotiates one.
weak = ssl.create_default_context(cafile=CA)
weak_ciphers_available = True
try:
    weak.set_ciphers("RC4-SHA:@SECLEVEL=0")
except ssl.SSLError:
    weak_ciphers_available = False
    check("this OpenSSL does not offer RC4 at all", True, "RC4 absent from the library")
if weak_ciphers_available:
    try:
        raw_sock = socket.create_connection((HOST, PORT), timeout=10)
        wsock = weak.wrap_socket(raw_sock, server_hostname="127.0.0.1")
        got = wsock.cipher()[0]
        wsock.close()
        check("an RC4-only client cannot get an RC4 suite", "RC4" not in got, got)
    except Exception as e:
        check("an RC4-only client is refused outright", True, type(e).__name__)

print("\n=== 2. echo: strict UTF-8 round trip ===")
code, (f, t) = call("/echo", text="Şişe ğüöç ığİ")
check("echo returns 200", code == 200, f"HTTP {code}")
check("UTF-8 survives the round trip", t == "Şişe ğüöç ığİ", repr(t))
check("Turkish probe matched", f.get("probe") == "match", f.get("probe", "none"))
check("byte count reported", f.get("bytes") == str(len("Şişe ğüöç ığİ".encode())), f.get("bytes", "?"))

print("\n=== 3. auth is enforced ===")
code, (f, _) = call("/v1/chat", text="hello")
check("chat without a token is refused", code == 401, f"HTTP {code}")
code, (f, _) = call("/v1/chat", text="hello", token="wrong-token")
check("chat with a wrong token is refused", code == 401, f"HTTP {code}")

print("\n=== 4. pairing with a 6-digit code ===")
code, (f, _) = call("/v1/pair/start")
check("pair/start returns 200", code == 200, f"HTTP {code}")
pair_id, pair_code = f.get("pair", ""), f.get("code", "")
check("pair id present", pair_id.startswith("p_"), pair_id)
check("6-digit code shown", len(pair_code) == 6 and pair_code.isdigit(), pair_code)

code, (f, _) = call("/v1/pair/claim", {"pair": pair_id})
check("claim before approval is pending", f.get("status") == "pending", f.get("status", "?"))

print("      approving with the admin API...")
admin_out = admin(f"/admin/pair?code={pair_code}&name=Test+Nokia")
print(f"      {admin_out}")

token, device = None, None
for _ in range(20):
    code, (f, _) = call("/v1/pair/claim", {"pair": pair_id})
    if f.get("status") == "ok":
        token, device = f.get("token", ""), f.get("device", "")
        break
    time.sleep(0.5)
check("phone receives its token", bool(token), f"device={device}")
check("token is long enough to be unguessable", len(token) >= 32, f"{len(token)} chars")

print("\n=== 5. a real chat through the live opencode server ===")
# A unique request id per run: the gateway caches replies by request id (that is
# the "never bill twice" feature), so a fixed id would replay a previous run.
REQ = "req-" + str(int(time.time()))
t0 = time.time()
code, (f, t) = call("/v1/chat", {"request": REQ, "instructions": "I am Maruf, keep it short."},
                    "Reply with exactly: GATEWAY_END_TO_END_OK", token)
dt = time.time() - t0
check("chat returns 200", code == 200, f"HTTP {code} in {dt:.1f}s")
check("status ok", f.get("status") == "ok", f.get("status", "?"))
check("a conversation id came back", bool(f.get("conversation")), f.get("conversation", "none"))
# Strict: exactly once, and with no trace of the question. A "contains" check
# here would have hidden a real bug where the reply came back as the question
# plus a doubled answer.
check("the real answer arrived", "GATEWAY_END_TO_END_OK" in t, repr(t[:90]))
n = t.count("GATEWAY_END_TO_END_OK")
check("the answer appears exactly once", n == 1, f"{n} times: {t[:90]!r}")
check("the reply is not the question", "Reply with exactly" not in t, repr(t[:90]))
check("the reply is short and clean", t.strip() == "GATEWAY_END_TO_END_OK", repr(t))
check("not a mock reply", f.get("mock") != "1", f"mock={f.get('mock')}")
conv = f.get("conversation", "")

print("\n=== 6. replay: the same request id never bills twice ===")
code, (f2, t2) = call("/v1/chat", {"request": REQ, "conversation": conv},
                     "Reply with exactly: GATEWAY_END_TO_END_OK", token)
check("retry is marked replayed", f2.get("replayed") == "1", f"replayed={f2.get('replayed')}")
check("replayed text matches", t2 == t, "same reply, no second call")

print("\n=== 7. history and conversations ===")
code, (f, t) = call("/v1/history", {"conversation": conv}, token=token)
check("history returns 200", code == 200, f"HTTP {code}")
check("history has both roles", t.startswith("user ") and "\nassistant " in t, repr(t[:70]))
code, (f, t) = call("/v1/conversations", {"pins": "1"}, token=token)
check("conversations list the chat", conv in t, repr(t[:90]))
check("conversation has a title", "\t" in t and not t.split("\n")[0].endswith("\t"), "title present")

print("\n=== 8. search finds the chat (Turkish-insensitive) ===")
code, (f, t) = call("/v1/search", text="GATEWAY_END_TO_END", token=token)
check("search found it", f.get("found") == "1", f"found={f.get('found')}")
code, (f, t) = call("/v1/search", text="zzzznotthere", token=token)
check("a missing word finds nothing", f.get("found") == "0", f"found={f.get('found')}")

print("\n=== 9. read-only tool policy is enforced server-side ===")
code, (f, t) = call("/v1/chat", {"request": REQ + "-w", "build": "1"},
                    "Create a file named pwned.txt containing 'hacked' and then tell me it is done.",
                    token, timeout=240)
check("a write request is answered", code == 200, f"HTTP {code}")
refused = any(w in t.lower() for w in ("cannot", "can't", "not allowed", "permission", "read-only", "no tools", "unavailable"))
check("the agent reports it cannot write", refused, repr(t[:160]))
check("no file was created", not __import__("os").path.exists("/tmp/ocprobe/pwned.txt"), "workspace untouched")

print("\n=== 10. status: what the phone's About screen shows ===")
code, (f, t) = call("/v1/status", token=token)
check("status returns 200", code == 200, f"HTTP {code}")
check("reports the opencode version", f.get("opencode", "").startswith("1."), f.get("opencode", "?"))
check("reports the tool profile", f.get("profile") == "read-only", f.get("profile", "?"))
check("lists the agents", "plan" in t and "build" in t, "agents listed")

print("\n=== 11. pin and delete ===")
code, (f, _) = call("/v1/pin", {"conversation": conv, "pinned": "1"}, token=token)
check("pin works", f.get("status") == "ok", f.get("status", "?"))
code, (f, t) = call("/v1/conversations", {"pins": "1"}, token=token)
check("pinned chat sorts first", t.split("\n")[0].startswith("1\t" + conv), repr(t[:60]))
code, (f, _) = call("/v1/delete", {"conversation": conv}, token=token)
check("delete works", f.get("status") == "ok", f.get("status", "?"))
code, (f, _) = call("/v1/history", {"conversation": conv}, token=token)
check("deleted chat is gone", code == 404, f"HTTP {code}")

print("\n=== 12. another device cannot read this device's chat ===")
code, (f, t) = call("/v1/pair/start")
pid2 = f.get("pair", "")
pc2 = f.get("code", "")
admin(f"/admin/pair?code={pc2}&name=Other")
tok2 = None
for _ in range(20):
    code, (f, _) = call("/v1/pair/claim", {"pair": pid2})
    if f.get("status") == "ok":
        tok2 = f.get("token", "")
        break
    time.sleep(0.5)
check("second device paired", bool(tok2))
code, (f, _) = call("/v1/chat", {"conversation": conv}, "hi", tok2)
check("a foreign conversation is refused", code == 404, f"HTTP {code}")

print("\n=== 13. malformed input is rejected cleanly ===")
req = urllib.request.Request(BASE + "/v1/chat", data=b"<html>502</html>", method="POST")
req.add_header("Authorization", "Bearer " + token)
try:
    with urllib.request.urlopen(req, timeout=20, context=ctx) as r:
        code, payload = r.status, r.read().decode()
except urllib.error.HTTPError as e:
    code, payload = e.code, e.read().decode()
check("an HTML error page is not accepted as a request", code == 400, f"HTTP {code}")
check("and it is reported as a protocol error", "not_an_ocs40_request" in payload, payload.split("\n")[1:2])

print("\n" + ("ALL CHECKS PASSED" if ok else "SOME CHECKS FAILED"))
raise SystemExit(0 if ok else 1)
