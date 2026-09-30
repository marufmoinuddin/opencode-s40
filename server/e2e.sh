#!/usr/bin/env bash
#
# One command to run the full live end-to-end suite: start a private opencode
# server, build and start the gateway against it, run e2e_test.py, then tear
# everything down. Exits with the test's own result.
#
#   ./e2e.sh              # ephemeral PKI in a temp dir, free ports
#   KEEP=1 ./e2e.sh       # keep the PKI, database and logs for inspection
#   E2E_PORT=9443 ./e2e.sh
#
# Nothing is installed, no privileged port is used, and no firewall is touched.
set -uo pipefail

cd "$(dirname "$0")"
ROOT=$(pwd)

WORK=${WORK:-/tmp/ocs40-e2e}
OC_PORT=${OC_PORT:-7788}
E2E_PORT=${E2E_PORT:-9443}
ADMIN_PORT=${ADMIN_PORT:-8782}
export PATH="$HOME/.local/bin:$PATH"

say() { printf '\n\033[1m%s\033[0m\n' "$*"; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

command -v opencode >/dev/null || die "opencode not on PATH (npm i -g opencode-ai)"
command -v go       >/dev/null || die "go not on PATH"

# The gateway's private CA is SHA-1 signed (real Series 40 hardware verifies
# only SHA-1). Go refuses SHA-1 unless told otherwise.
export GODEBUG=x509sha1=1

OC_PID=""; GW_PID=""
cleanup() {
  [ -n "$GW_PID" ] && kill "$GW_PID" 2>/dev/null
  [ -n "$OC_PID" ] && kill "$OC_PID" 2>/dev/null
  wait 2>/dev/null
  if [ "${KEEP:-0}" = "1" ]; then
    printf '\nkept: %s (opencode log: opencode.log, gateway log: gateway.log)\n' "$WORK"
  else
    rm -rf "$WORK"
  fi
}
trap cleanup EXIT

# ---------------------------------------------------------------- 1. opencode
mkdir -p "$WORK/proj"
cd "$WORK/proj" && git init -q 2>/dev/null

say "1/5  starting opencode on 127.0.0.1:$OC_PORT"
opencode serve --port "$OC_PORT" --hostname 127.0.0.1 >"$WORK/opencode.log" 2>&1 &
OC_PID=$!
for _ in $(seq 1 30); do
  if curl -fsS -o /dev/null --max-time 2 "http://127.0.0.1:$OC_PORT/global/health" 2>/dev/null; then
    break
  fi
  sleep 1
done
curl -fsS --max-time 5 "http://127.0.0.1:$OC_PORT/global/health" \
  | sed 's/^/      /' || die "opencode did not become healthy (see $WORK/opencode.log)"
echo

# ---------------------------------------------------------------- 2. PKI
say "2/5  private CA + server certificate (SHA-1, for Series 40 hardware)"
cd "$ROOT"
./scripts/pki.sh ca "$WORK/pki" >/dev/null || die "pki.sh ca"
./scripts/pki.sh server "$WORK/pki" 127.0.0.1 >/dev/null || die "pki.sh server"
echo "      ok"

# ---------------------------------------------------------------- 3. gateway
say "3/5  building and starting the gateway on :$E2E_PORT"
go build -o "$WORK/ocs40-server" . || die "go build"
OCS40_LISTEN=":$E2E_PORT" \
OCS40_ADMIN="127.0.0.1:$ADMIN_PORT" \
OCS40_CERT="$WORK/pki/server-chain.pem" \
OCS40_KEY="$WORK/pki/server.key" \
OCS40_DB="$WORK/ocs40.db" \
OPENCODE_URL="http://127.0.0.1:$OC_PORT" \
OPENCODE_PROJECT="$WORK/proj" \
  "$WORK/ocs40-server" >"$WORK/gateway.log" 2>&1 &
GW_PID=$!
for _ in $(seq 1 20); do
  if curl -fsS -o /dev/null --max-time 2 \
      --ciphers 'DEFAULT:@SECLEVEL=0' --cacert "$WORK/pki/ca.pem" \
      "https://127.0.0.1:$E2E_PORT/health" 2>/dev/null; then
    break
  fi
  sleep 1
done
curl -fsS --max-time 5 --ciphers 'DEFAULT:@SECLEVEL=0' --cacert "$WORK/pki/ca.pem" \
  "https://127.0.0.1:$E2E_PORT/health" | sed 's/^/      /' \
  || die "gateway did not answer /health (see $WORK/gateway.log)"
echo

# ---------------------------------------------------------------- 4. test
say "4/5  end-to-end suite (TLS 1.0 / AES128-SHA, pairing, real chat, read-only)"
E2E_BASE="https://127.0.0.1:$E2E_PORT" \
E2E_CA="$WORK/pki/ca.pem" \
E2E_ADMIN="http://127.0.0.1:$ADMIN_PORT" \
E2E_ADMIN_TOKEN_FILE="$WORK/admin_token" \
  python3 e2e_test.py
RC=$?

# ---------------------------------------------------------------- 5. logs
say "5/5  gateway log (no message text, no tokens, no client addresses)"
sed 's/^/      /' "$WORK/gateway.log" | head -20

if [ "$RC" -eq 0 ]; then
  printf '\n\033[1mE2E PASSED\033[0m\n'
else
  printf '\n\033[1mE2E FAILED (exit %d)\033[0m\n' "$RC"
fi
exit "$RC"
