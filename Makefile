# OpenCode S40 — unofficial opencode client for Nokia Series 40 phones.
#
#   make test     server tests + app build/checks
#   make -C app   phone app only
#   make -C server test | build | run | mock | pki
#
# The phone app is ported from the MIT-licensed Claude S40 project
# (© 2026 Emir Karşıyakalı); the gateway and protocol are specific to this
# project. See LICENSE.

.PHONY: test app server clean help

## test: server unit tests, then the app build + package checks
test:
	$(MAKE) -C server vet test
	$(MAKE) -C app

## app: build and check the phone JAR
app:
	$(MAKE) -C app

## server: build and test the gateway
server:
	$(MAKE) -C server

clean:
	$(MAKE) -C app clean
	$(MAKE) -C server clean

help:
	@grep -E '^## ' Makefile | sed 's/## //'
