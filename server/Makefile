# OpenCode S40 — server
BIN   := ocs40-server
PKI   ?= certs
ADMIN ?= 127.0.0.1:8781

# The gateway's private CA and server certificate are SHA-1 signed by default:
# that is the combination verified on real Series 40 hardware. Go refuses SHA-1
# verification out of the box, so the tests opt back in. The phone does not need
# this; it is only for Go/OpenSSL/curl talking to the gateway locally.
export GODEBUG ?= x509sha1=1

.PHONY: all build test vet run mock clean pki help

all: vet test build

build:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o $(BIN) .

vet:
	go vet ./...

## test: unit tests, plus the live integration tests when a server is reachable.
## The live ones skip automatically if OCS40_LIVE_OPENCODE is not answering.
test:
	go test -timeout 180s ./...
	@echo "--- live integration tests (skipped if no opencode server) ---"
	@OCS40_LIVE_OPENCODE=$${OCS40_LIVE_OPENCODE:-http://127.0.0.1:4096} \
		go test -timeout 300s -run 'TestLive' -v ./internal/oc/ | tail -20

## run: start the gateway against an opencode server already on :4096
run: build
	OCS40_LISTEN=$${OCS40_LISTEN:-:443} \
	OCS40_ADMIN=$(ADMIN) \
	OCS40_CERT=$(PKI)/server-chain.pem \
	OCS40_KEY=$(PKI)/server.key \
	OCS40_DB=data/ocs40.db \
	OPENCODE_URL=$${OPENCODE_URL:-http://127.0.0.1:4096} \
	./$(BIN)

## mock: start in test mode; opencode is never called and nothing costs anything
mock: build
	OCS40_MOCK=1 \
	OCS40_LISTEN=$${OCS40_LISTEN:-:443} \
	OCS40_ADMIN=$(ADMIN) \
	OCS40_CERT=$(PKI)/server-chain.pem \
	OCS40_KEY=$(PKI)/server.key \
	OCS40_DB=data/ocs40-mock.db \
	./$(BIN)

## pki: create a private CA and a server certificate for HOST (default: your IP)
pki:
	@scripts/pki.sh ca $(PKI)
	@scripts/pki.sh server $(PKI) $${HOST:-$$(hostname -I | awk '{print $$1}')}
	@scripts/pki.sh show $(PKI)

clean:
	rm -f $(BIN)
	rm -rf data

help:
	@grep -E '^## ' Makefile | sed 's/## //'
