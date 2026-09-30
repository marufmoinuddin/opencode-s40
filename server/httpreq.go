package main

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"math/big"
	"net"
	"net/http"
	"unicode/utf8"
)

// httpRequest is the small wrapper the handlers get: the request plus its TLS
// state, so /health can report what the phone actually negotiated.
type httpRequest struct {
	r   *http.Request
	tls *tls.ConnectionState
}

func (h *httpRequest) ctx() context.Context {
	if h == nil || h.r == nil {
		return context.Background()
	}
	return h.r.Context()
}

func (h *httpRequest) tlsVersion() string {
	if h == nil || h.tls == nil {
		return ""
	}
	return tlsVersionName(h.tls.Version)
}

func tlsCipherName(h *httpRequest) string {
	if h == nil || h.tls == nil {
		return ""
	}
	return tls.CipherSuiteName(h.tls.CipherSuite)
}

func tlsNewListener(ln net.Listener, cfg *tls.Config) net.Listener {
	return tls.NewListener(ln, cfg)
}

func utf8Valid(s string) bool { return utf8.ValidString(s) }

func cryptoRandomInt(max int64) (int64, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(max))
	if err != nil {
		return 0, err
	}
	return n.Int64(), nil
}

// randomToken returns a 256-bit hex token for a device.
func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
