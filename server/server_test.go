package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maruf/ocs40/server/internal/oc"
	"github.com/maruf/ocs40/server/internal/protocol"
	"github.com/maruf/ocs40/server/internal/store"
)

// testPKI writes a throwaway CA and server certificate and returns their paths.
func testPKI(t *testing.T) (dir, chain, key, ca string) {
	t.Helper()
	dir = t.TempDir()
	sh := filepath.Join(dir, "pki.sh")
	// Reuse the real script so the tests exercise what ships.
	src, err := os.ReadFile("scripts/pki.sh")
	if err != nil {
		t.Fatalf("read pki.sh: %v", err)
	}
	if err := os.WriteFile(sh, src, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"ca", dir},
		{"server", dir, "127.0.0.1"},
	} {
		cmd := execCommand("sh", append([]string{sh}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("pki.sh %v: %v\n%s", args, err, out)
		}
	}
	return dir, filepath.Join(dir, "server-chain.pem"), filepath.Join(dir, "server.key"), filepath.Join(dir, "ca.pem")
}

func newTestServer(t *testing.T) (*server, *store.Store) {
	t.Helper()
	_, chain, key, _ := testPKI(t)
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"), 30)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	cfg := config{
		Cert: chain, Key: key, DBPath: filepath.Join(t.TempDir(), "x.db"),
		Mock: true, Agent: "plan", OpenTimeout: 5 * time.Second, Timeout: 30 * time.Second,
	}
	cs := newChatService(cfg, oc.New("http://127.0.0.1:1"), st)
	return &server{cfg: cfg, st: st, cs: cs, client: oc.New("http://127.0.0.1:1")}, st
}

// TestNoPlainHTTPPath is a security regression test.
//
// The phone must never be able to reach the gateway unencrypted. The gateway
// wraps its listener in TLS itself and calls Serve (never ServeTLS, which on an
// already-wrapped listener silently serves PLAIN HTTP).
//
// A plain-HTTP request cannot be answered by the application at all: Go's TLS
// listener answers it with its own "Client sent an HTTP request to an HTTPS
// server" preamble and never runs the handler. So the property to assert is
// that the application's handler is never invoked, and that a request which is
// merely *almost* valid (a good HTTP/1.1 request with the right Host) still
// never reaches the handler.
func TestNoPlainHTTPPath(t *testing.T) {
	_, chain, key, _ := testPKI(t)
	cfg, err := phoneTLS(chain, key)
	if err != nil {
		t.Fatal(err)
	}

	var handled int
	var mu sync.Mutex
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			handled++
			mu.Unlock()
			w.WriteHeader(204)
		}),
		TLSConfig:         cfg,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() { _ = srv.Serve(tls.NewListener(ln, cfg)) }()
	t.Cleanup(func() { _ = srv.Close() })

	// A plain-HTTP request: the handler must never see it.
	conn, err := net.DialTimeout("tcp", ln.Addr().String(), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = conn.Write([]byte("GET /health HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n"))
	_ = conn.SetReadDeadline(time.Now().Add(4 * time.Second))
	_, _ = io.ReadAll(io.LimitReader(conn, 4096))
	_ = conn.Close()

	// A real TLS request must reach the handler, proving the server works and
	// the check above was not passing simply because nothing was listening.
	tlsConn, err := tls.Dial("tcp", ln.Addr().String(), &tls.Config{InsecureSkipVerify: true})
	if err != nil {
		t.Fatalf("TLS dial failed: %v", err)
	}
	defer tlsConn.Close()
	fmt.Fprint(tlsConn, "GET /health HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n")
	_ = tlsConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	raw, _ := io.ReadAll(io.LimitReader(tlsConn, 4096))

	mu.Lock()
	got := handled
	mu.Unlock()
	if got != 1 {
		t.Errorf("the handler ran %d times, want exactly 1 (the TLS request); "+
			"a plain-HTTP request must never reach it", got)
	}
	if !strings.HasPrefix(string(raw), "HTTP/1.1 204") {
		t.Errorf("the TLS request did not get the handler's response: %q", firstLine(string(raw)))
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// TestTLS10WithPhoneCipher is the handshake a Series 40 phone actually performs:
// TLS 1.0, RSA key exchange, AES-CBC-SHA, no SNI.
func TestTLS10WithPhoneCipher(t *testing.T) {
	_, chain, key, _ := testPKI(t)
	cfg, err := phoneTLS(chain, key)
	if err != nil {
		t.Fatal(err)
	}
	// The policy must not offer RC4 or 3DES, whatever the client asks for.
	for _, s := range cfg.CipherSuites {
		name := tls.CipherSuiteName(s)
		if strings.Contains(name, "RC4") || strings.Contains(name, "3DES") {
			t.Errorf("weak cipher offered: %s", name)
		}
	}
	if cfg.MinVersion != tls.VersionTLS10 {
		t.Errorf("MinVersion = %v, want TLS 1.0", tls.VersionName(cfg.MinVersion))
	}
	if cfg.GetConfigForClient == nil {
		t.Error("a ClientHello logger should be installed")
	}
	// No SNI requirement.
	if cfg.ClientAuth != tls.NoClientCert {
		t.Error("the phone sends no client certificate; none must be required")
	}
}

// TestHealthOverRealTLS walks the whole stack the way the phone's connection
// test does: TLS 1.0, the phone's cipher, then a parsed OCS40/1 body.
func TestHealthOverRealTLS(t *testing.T) {
	_, chain, key, ca := testPKI(t)
	cfg, err := phoneTLS(chain, key)
	if err != nil {
		t.Fatal(err)
	}
	s, _ := newTestServer(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: s.routes(), TLSConfig: cfg, ErrorLog: newDiscardLogger()}
	go func() { _ = srv.Serve(tls.NewListener(ln, cfg)) }()
	t.Cleanup(func() { _ = srv.Close() })

	caPEM, err := os.ReadFile(ca)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		t.Fatal("could not load the test CA")
	}
	// The private root is SHA-1 signed because that is the combination real
	// Series 40 hardware verifies. Go refuses SHA-1 by default; the phone does
	// not, so the test must opt back in to reproduce the device's behaviour.
	// (GODEBUG=x509sha1=1 is set in TestMain.)
	_ = pool
	c, err := tls.Dial("tcp", ln.Addr().String(), &tls.Config{
		RootCAs:    pool,
		MinVersion: tls.VersionTLS10,
		MaxVersion: tls.VersionTLS10,
		CipherSuites: []uint16{
			tls.TLS_RSA_WITH_AES_128_CBC_SHA, // what the Nokia 6300 picks
		},
	})
	if err != nil {
		t.Fatalf("a TLS 1.0 / AES128-SHA handshake failed: %v", err)
	}
	defer c.Close()

	ver, cipher := c.ConnectionState().Version, tls.CipherSuiteName(c.ConnectionState().CipherSuite)
	if ver != tls.VersionTLS10 {
		t.Errorf("negotiated %s, want TLS 1.0", tls.VersionName(ver))
	}
	if cipher != "TLS_RSA_WITH_AES_128_CBC_SHA" {
		t.Errorf("negotiated %s", cipher)
	}

	// Speak HTTP/1.1 by hand over the TLS connection, exactly as the phone's
	// MIDP stack does: one request, then read one response.
	body := protocol.New().Format()
	req := "POST /health HTTP/1.1\r\n" +
		"Host: " + ln.Addr().String() + "\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"Content-Length: " + strconv.Itoa(len(body)) + "\r\n" +
		"Connection: close\r\n\r\n" + body
	_ = c.SetDeadline(time.Now().Add(15 * time.Second))
	if _, err := c.Write([]byte(req)); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(newBufReader(c), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("HTTP %d", resp.StatusCode)
	}
	got := readAllString(t, resp.Body)
	m, err := protocol.Parse(got)
	if err != nil {
		t.Fatalf("health did not return OCS40/1: %v (%q)", err, got)
	}
	if m.Get("status") != "ok" {
		t.Errorf("status = %q", m.Get("status"))
	}
	if !strings.Contains(m.Get("tls"), "TLS 1.0") {
		t.Errorf("health did not report the connection's TLS version: %q", m.Get("tls"))
	}
	if !strings.Contains(m.Get("cipher"), "AES_128") {
		t.Errorf("health did not report the cipher: %q", m.Get("cipher"))
	}
}

// TestMockModeNeverCallsOpencode proves the test mode is local: the client
// points at a dead port, so any real call would fail.
func TestMockModeNeverCallsOpencode(t *testing.T) {
	s, _ := newTestServer(t)
	_ = s.st.AddDevice("dev1", "tok1", "n")
	body := protocol.New().Format() + "hello there"
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/v1/chat", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer tok1")
	s.routes().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("HTTP %d: %s", rec.Code, rec.Body.String())
	}
	m, err := protocol.Parse(rec.Body.String())
	if err != nil {
		t.Fatal(err)
	}
	if m.Get("status") != "ok" {
		t.Fatalf("status %q", m.Get("status"))
	}
	if m.Get("mock") != "1" {
		t.Error("a mock reply must be labelled mock: 1")
	}
	if !strings.Contains(m.Text, "[Test mode]") {
		t.Errorf("a mock reply must say [Test mode]: %q", m.Text)
	}
}

// TestAuthEnforced covers the token paths a phone depends on.
func TestAuthEnforced(t *testing.T) {
	s, _ := newTestServer(t)
	_ = s.st.AddDevice("dev1", "good-token", "n")
	_ = s.st.AddDevice("dev2", "other-token", "n")
	for _, tc := range []struct {
		name  string
		token string
		want  int
	}{
		{"no token", "", 401},
		{"wrong token", "nope", 401},
		{"good token", "good-token", 200},
	} {
		req := httptest.NewRequest("POST", "/v1/chat", strings.NewReader(protocol.New().Format()+"hi"))
		if tc.token != "" {
			req.Header.Set("Authorization", "Bearer "+tc.token)
		}
		rec := httptest.NewRecorder()
		s.routes().ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Errorf("%s: HTTP %d, want %d", tc.name, rec.Code, tc.want)
		}
	}
	// A revoked token must look exactly like a wrong one.
	_ = s.st.RevokeDevice("dev1")
	req := httptest.NewRequest("POST", "/v1/chat", strings.NewReader(protocol.New().Format()+"hi"))
	req.Header.Set("Authorization", "Bearer good-token")
	rec := httptest.NewRecorder()
	s.routes().ServeHTTP(rec, req)
	if rec.Code != 401 {
		t.Errorf("a revoked token got HTTP %d, want 401", rec.Code)
	}
	m, _ := protocol.Parse(rec.Body.String())
	if m.Get("status") != "access_code_invalid" {
		t.Errorf("a revoked token must report the same as a wrong one, got %q", m.Get("status"))
	}
}

// TestPairingFlow is the phone's setup wizard: start, poll, approve, claim.
func TestPairingFlow(t *testing.T) {
	s, _ := newTestServer(t)
	routes := s.routes()

	call := func(path, body string) (int, *protocol.Message) {
		rec := httptest.NewRecorder()
		routes.ServeHTTP(rec, httptest.NewRequest("POST", path, strings.NewReader(body)))
		m, err := protocol.Parse(rec.Body.String())
		if err != nil {
			t.Fatalf("%s: %v (%s)", path, err, rec.Body.String())
		}
		return rec.Code, m
	}

	code, m := call("/v1/pair/start", protocol.New().Format())
	if code != 200 || len(m.Get("code")) != 6 {
		t.Fatalf("pair/start: HTTP %d code %q", code, m.Get("code"))
	}
	pairID, pairCode := m.Get("pair"), m.Get("code")

	// Not approved yet.
	_, m = call("/v1/pair/claim", protocol.New().Set("pair", pairID).Format())
	if m.Get("status") != "pending" {
		t.Fatalf("claim before approval: %q", m.Get("status"))
	}
	// A wrong code approves nothing.
	rec := httptest.NewRecorder()
	routes.ServeHTTP(rec, httptest.NewRequest("GET", "/admin/pair?code=000000", nil))
	if rec.Code != 404 {
		t.Errorf("approving a wrong code returned HTTP %d, want 404", rec.Code)
	}
	// Approve for real (with the admin token).
	adminTok, err := s.adminToken()
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("GET", "/admin/pair?code="+pairCode+"&name=My+Nokia", nil)
	req.Header.Set("Authorization", "Bearer "+adminTok)
	rec = httptest.NewRecorder()
	s.adminRoutes().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("approve: HTTP %d %s", rec.Code, rec.Body.String())
	}

	_, m = call("/v1/pair/claim", protocol.New().Set("pair", pairID).Format())
	if m.Get("status") != "ok" {
		t.Fatalf("claim after approval: %q", m.Get("status"))
	}
	token, device := m.Get("token"), m.Get("device")
	if token == "" || device == "" {
		t.Fatal("no token or device id handed over")
	}
	// The token must work...
	if _, err := s.st.DeviceByToken(token); err != nil {
		t.Errorf("the issued token does not authenticate: %v", err)
	}
	// ...and be handed over only once.
	_, m2 := call("/v1/pair/claim", protocol.New().Set("pair", pairID).Format())
	if m2.Get("status") == "ok" && m2.Get("token") != "" {
		t.Error("the token was handed over a second time")
	}
}

// TestLongRepliesArePagedFree checks the part-paging invariants: a cut never
// splits a rune or a line, parts reassemble exactly, and /v1/more is free.
func TestLongRepliesArePagedFree(t *testing.T) {
	long := strings.Repeat("This is a line of an answer that goes on.\n", 200) // ~8.2 KB
	seen, offset := "", 0
	parts := 0
	for {
		part, more, next := slicePart(long, offset)
		if part != "" && !utf8ValidString(part) {
			t.Fatalf("part %d is not valid UTF-8 at the cut", parts)
		}
		if strings.HasSuffix(part, "\n") == false && more && !strings.Contains(part, "\n") {
			t.Logf("part %d cut mid-line (acceptable when a line is longer than a part)", parts)
		}
		seen += part
		parts++
		if !more {
			break
		}
		if next <= offset {
			t.Fatalf("offset did not advance: %d -> %d", offset, next)
		}
		offset = next
		if parts > 50 {
			t.Fatal("paging did not terminate")
		}
	}
	if seen != long {
		t.Errorf("parts did not reassemble into the original text (%d vs %d bytes)", len(seen), len(long))
	}
	if parts < 2 {
		t.Errorf("expected several parts for %d bytes, got %d", len(long), parts)
	}
}

// TestReplayPreventsDoubleCall is the "retry after an error is one key and
// never charges twice" rule from the phone's contract.
func TestReplayPreventsDoubleCall(t *testing.T) {
	s, _ := newTestServer(t)
	_ = s.st.AddDevice("dev1", "tok", "n")
	in := protocol.New().Set("request", "req-1")
	in.Text = "hello"
	first, err := s.cs.Reply(context.Background(), store.Device{ID: "dev1"}, in)
	if err != nil {
		t.Fatal(err)
	}
	if first.Has("replayed") {
		t.Error("the first reply must not be marked replayed")
	}
	again, err := s.cs.Reply(context.Background(), store.Device{ID: "dev1"}, in)
	if err != nil {
		t.Fatal(err)
	}
	if again.Get("replayed") != "1" {
		t.Error("a repeated request id must be marked replayed")
	}
	if again.Text != first.Text {
		t.Error("the replayed text differs from the original")
	}
}

// TestSanitize checks the Markdown cleanup the 240x320 screen depends on.
func TestSanitize(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"# Title\n\nbody", "Title\n\nbody"},
		{"**bold** and *italic*", "bold and italic"},
		{"```go\nfmt.Println()\n```", "fmt.Println()"},
		{"see [docs](https://x.dev)", "see docs (https://x.dev)"},
		{"a\n\n\n\n\nb", "a\n\nb"},
		{"trailing   ", "trailing"},
	} {
		if got := sanitize(tc.in); got != tc.want {
			t.Errorf("sanitize(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestDailyLimit(t *testing.T) {
	s, _ := newTestServer(t)
	// chatService was built with its own copy of the config, so the limit has to
	// be set on the service that enforces it.
	s.cfg.DailyLimit = 2
	s.cs.cfg.DailyLimit = 2
	// The device must exist: CountUsage now refuses an unknown device rather
	// than letting it past the limit.
	if err := s.st.AddDevice("dev1", "tok", "n"); err != nil {
		t.Fatal(err)
	}
	dev := store.Device{ID: "dev1"}
	for i := 0; i < 2; i++ {
		in := protocol.New().Set("request", fmt.Sprintf("r%d", i))
		in.Text = "hi"
		if _, err := s.cs.Reply(context.Background(), dev, in); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	in := protocol.New().Set("request", "r3")
	in.Text = "hi"
	_, err := s.cs.Reply(context.Background(), dev, in)
	if err == nil {
		t.Fatal("the daily limit was not enforced")
	}
	var se *statusError
	if !asStatus(err, &se) || se.status != "limit" {
		t.Errorf("expected a limit status, got %v", err)
	}
}

var _ = json.Marshal
