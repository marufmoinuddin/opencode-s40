package main

import (
	"bufio"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"unicode/utf8"
)

// Small helpers shared by the server tests, kept in one place so each test file
// stays readable.

// TestMain allows SHA-1 signed certificates.
//
// The gateway's private root and server certificate are SHA-1 signed by
// default: that is the combination verified on real Series 40 hardware, and
// SHA-256 is untested there. Go refuses to verify SHA-1 out of the box
// (x509sha1), and modern OpenSSL does too, so every modern tool that talks to
// this gateway needs the same override. The phone does not.
func TestMain(m *testing.M) {
	// Set before any TLS verification happens.
	if v := os.Getenv("GODEBUG"); v == "" {
		_ = os.Setenv("GODEBUG", "x509sha1=1")
	} else if !strings.Contains(v, "x509sha1") {
		_ = os.Setenv("GODEBUG", v+",x509sha1=1")
	}
	// crypto/x509 reads the GODEBUG setting once, at package init time, so it
	// has to be set before this package is loaded. Setting it here is a
	// best-effort fallback; the test command also passes it on the command line
	// (see the Makefile's `test` target) so CI and local runs behave the same.
	os.Exit(m.Run())
}

func execCommand(name string, args ...string) *exec.Cmd { return exec.Command(name, args...) }

func newDiscardLogger() *log.Logger { return log.New(io.Discard, "", 0) }

func newBufReader(c net.Conn) *bufio.Reader { return bufio.NewReader(c) }

func readAllString(t *testing.T, r io.Reader) string {
	t.Helper()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func utf8ValidString(s string) bool { return utf8.ValidString(s) }

// asStatus unwraps a *statusError from an error returned by the chat service.
func asStatus(err error, target **statusError) bool {
	se, ok := err.(*statusError)
	if ok {
		*target = se
	}
	return ok
}
