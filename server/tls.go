package main

import (
	"crypto/tls"
	"log"
	"strings"
)

// phoneTLS is the TLS policy for the phone listener.
//
// A Series 40 phone of this era (measured on a Nokia 6300, firmware V06.60)
// sends: TLS 1.0 only, no SNI, no extensions, and offers
// RC4-MD5, RC4-SHA, 3DES-EDE-CBC-SHA, AES128-CBC-SHA, AES256-CBC-SHA. Its
// certificate store only holds roots from around 2008, so no public CA issues
// a chain it would accept, and every CDN/PaaS refuses that handshake.
//
// So the gateway terminates TLS itself with a certificate from a private root
// CA the user installs on the phone once, and:
//   - MinVersion TLS 1.0 (the phone offers nothing newer),
//   - TLS_RSA_WITH_AES_128/256_CBC_SHA present, because that is what it picks
//     (it has no ECDHE and cannot do GCM),
//   - no RC4 and no 3DES offered, and no plain-HTTP listener anywhere.
func phoneTLS(certFile, keyFile string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, err
	}
	cfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS10,
		CipherSuites: []uint16{
			// Modern clients (curl, the admin tooling) land here.
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
			// The 2007 phone: RSA key exchange, AES-CBC, SHA-1 MAC.
			tls.TLS_ECDHE_RSA_WITH_AES_128_CBC_SHA,
			tls.TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA,
			tls.TLS_RSA_WITH_AES_128_CBC_SHA,
			tls.TLS_RSA_WITH_AES_256_CBC_SHA,
		},
		// The phone sends no SNI; do not require it.
		ClientAuth: tls.NoClientCert,
	}
	cfg.GetConfigForClient = func(h *tls.ClientHelloInfo) (*tls.Config, error) {
		logJSON(map[string]any{
			"evt":    "hello",
			"sni":    h.ServerName != "",
			"suites": len(h.CipherSuites),
			"ver":    tls.VersionName(h.SupportedVersions[0]),
		})
		return nil, nil
	}
	return cfg, nil
}

// handshakeLog keeps net/http's TLS error text (useful when a phone cannot
// connect) but strips anything that looks like an address, so no client IP ever
// reaches the log.
type handshakeLog struct{}

func (handshakeLog) Write(p []byte) (int, error) {
	s := strings.TrimSpace(string(p))
	// net/http prefixes "from ADDR: " and adds "read tcp LOCAL->REMOTE".
	if i := strings.Index(s, " from "); i >= 0 {
		if j := strings.Index(s[i+6:], ": "); j >= 0 {
			s = s[:i] + s[i+6+j:]
		}
	}
	s = ipRE.ReplaceAllString(s, "[addr]")
	logJSON(map[string]any{"evt": "http_error", "detail": truncate(s, 200)})
	return len(p), nil
}

// describeTLS reports the TLS version and cipher of the connection a request
// arrived on, for /health: the phone's connection test shows this to prove it
// really spoke to the gateway over its own limited stack.
func describeTLS(r *httpRequest) (string, string) {
	if r == nil || r.tls == nil {
		return "", ""
	}
	return tlsVersionName(r.tls.Version), tls.CipherSuiteName(r.tls.CipherSuite)
}

func tlsVersionName(v uint16) string {
	switch v {
	case tls.VersionTLS10:
		return "TLS 1.0"
	case tls.VersionTLS11:
		return "TLS 1.1"
	case tls.VersionTLS12:
		return "TLS 1.2"
	case tls.VersionTLS13:
		return "TLS 1.3"
	}
	return "unknown"
}

func logJSON(v map[string]any) {
	if _, ok := v["t"]; !ok {
		v["t"] = nowUTC()
	}
	b, err := jsonMarshal(v)
	if err != nil {
		return
	}
	log.Print(string(b))
}
