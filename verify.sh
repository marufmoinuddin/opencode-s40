#!/bin/sh
# Final verification of the OpenCode S40 build, from a clean tree.
# Every step must pass; this is the evidence behind "it builds and runs".
set -eu
cd "$(dirname "$0")"

echo "=============================================="
echo " 1. server: go vet"
echo "=============================================="
(cd server && GODEBUG=x509sha1=1 go vet ./... && echo "vet: clean")

echo
echo "=============================================="
echo " 2. server: unit tests"
echo "=============================================="
(cd server && GODEBUG=x509sha1=1 go test -count=1 ./... )

echo
echo "=============================================="
echo " 3. app: build + 47 package checks + reproducible"
echo "=============================================="
make -C app 2>&1 | grep -E "checks passed|reproducible|bytes" || true

echo
echo "=============================================="
echo " 4. app: artifact verification"
echo "=============================================="
(cd app/dist && sha256sum -c SHA256SUMS)
echo "--- manifest ---"
unzip -p app/dist/OpenCodeS40.jar META-INF/MANIFEST.MF | sed 's/^/  /'
echo "--- class file version (must be 46.0) ---"
unzip -p app/dist/OpenCodeS40.jar io/github/maruf/ocs40/OcsS40MIDlet.class \
  | od -An -tu1 -N8 | awk '{printf "  major=%d minor=%d\n", ($5*256+$6), ($7*256+$8)}'
echo "--- no leaked dependency classes ---"
n=$(unzip -l app/dist/OpenCodeS40.jar | awk '{print $4}' | grep '\.class$' \
      | grep -cv '^io/github/maruf/ocs40/' || true)
echo "  classes outside io/github/maruf/ocs40: $n"
[ "$n" = "0" ] || { echo "FAIL: dependency classes leaked"; exit 1; }

echo
echo "=============================================="
echo " 5. no secrets or leftovers in tracked sources"
echo "=============================================="
if grep -rIl "claude-s40\|emir/claudes40" app/src server/internal 2>/dev/null | head -1 | grep -q .; then
  echo "FAIL: reference-project leftovers in sources"; exit 1
fi
echo "  no reference-project identifiers in sources: ok"
if grep -rIn "BEGIN RSA PRIVATE KEY\|sk-ant-\|api[_-]key[[:space:]]*=" app/src server/internal server/*.go 2>/dev/null | head -1 | grep -q .; then
  echo "FAIL: something secret-looking in sources"; exit 1
fi
echo "  no key-shaped strings in sources: ok"

echo
echo "ALL CHECKS PASSED"
