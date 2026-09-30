package main

import (
	"crypto/subtle"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// The admin API can mint and revoke device tokens, so it is deliberately small
// and bound to loopback: it is reached over SSH, never from the network the
// phone uses. It authenticates with a token generated on the server at first
// start and stored in secrets/admin_token (mode 600), never in git.

// adminRoutes builds the admin mux.
func (s *server) adminRoutes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/admin/pair", s.adminPair)
	mux.HandleFunc("/admin/devices", s.adminDevices)
	mux.HandleFunc("/admin/revoke", s.adminRevoke)
	mux.HandleFunc("/admin/status", s.adminStatus)
	mux.HandleFunc("/admin/logs", s.adminLogs)
	return mux
}

// adminToken returns the admin token, reading or creating it on first use.
func (s *server) adminToken() (string, error) {
	path := filepath.Join(filepath.Dir(s.cfg.DBPath), "admin_token")
	if b, err := os.ReadFile(path); err == nil {
		t := strings.TrimSpace(string(b))
		if t != "" {
			return t, nil
		}
	}
	tok, err := randomToken()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(tok+"\n"), 0o600); err != nil {
		return "", err
	}
	return tok, nil
}

func (s *server) adminOK(r *http.Request) bool {
	want, err := s.adminToken()
	if err != nil {
		return false
	}
	got := bearer(r)
	if got == "" {
		got = r.Header.Get("X-Admin-Token")
	}
	// Constant-time compare: the token is a bearer secret.
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func adminText(w http.ResponseWriter, code int, body string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(code)
	_, _ = w.Write([]byte(body))
}

// adminPair approves a pending pairing: `pair <code> [name]`.
func (s *server) adminPair(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(r) {
		adminText(w, 401, "admin token required\n")
		return
	}
	code := strings.TrimSpace(r.URL.Query().Get("code"))
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		name = "Nokia"
	}
	if code == "" {
		adminText(w, 400, "usage: /admin/pair?code=123456&name=My+Nokia\n")
		return
	}

	devID, ok := approvePairing(s.st, code, name)
	if !ok {
		adminText(w, 404, "no pending pairing with that code (it may have expired)\n")
		return
	}
	adminText(w, 200, "approved "+devID+" ("+name+"); the phone will fetch its token now\n")
}

// adminDevices lists paired devices.
func (s *server) adminDevices(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(r) {
		adminText(w, 401, "admin token required\n")
		return
	}
	devices, err := s.st.Devices()
	if err != nil {
		adminText(w, 500, err.Error()+"\n")
		return
	}
	if len(devices) == 0 {
		adminText(w, 200, "no paired devices\n")
		return
	}
	var b strings.Builder
	for _, d := range devices {
		b.WriteString(d.ID)
		b.WriteByte('\t')
		b.WriteString(d.Name)
		b.WriteByte('\n')
	}
	adminText(w, 200, b.String())
}

// adminRevoke revokes a device by id.
func (s *server) adminRevoke(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(r) {
		adminText(w, 401, "admin token required\n")
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		adminText(w, 400, "usage: /admin/revoke?id=dev_xxx\n")
		return
	}
	if err := s.st.RevokeDevice(id); err != nil {
		adminText(w, 404, err.Error()+"\n")
		return
	}
	adminText(w, 200, "revoked "+id+"\n")
}

// adminStatus shows the gateway's configuration state without secrets.
func (s *server) adminStatus(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(r) {
		adminText(w, 401, "admin token required\n")
		return
	}
	var b strings.Builder
	b.WriteString("ocs40 " + Version + "\n")
	b.WriteString("mock: " + boolFlag(s.cfg.Mock) + "\n")
	b.WriteString("opencode: " + s.cfg.OpenURL + "\n")
	b.WriteString("agent: " + s.cfg.Agent + "\n")
	b.WriteString("tools: " + toolProfileName(s.cfg) + "\n")
	b.WriteString("daily limit: " + itoa(s.cfg.DailyLimit) + "\n")
	b.WriteString("retention days: " + itoa(s.cfg.RetentionDays) + "\n")
	adminText(w, 200, b.String())
}

// adminLogs is a no-op placeholder: request logging already goes to stdout as
// JSON lines, and nothing sensitive is ever written there.
func (s *server) adminLogs(w http.ResponseWriter, r *http.Request) {
	if !s.adminOK(r) {
		adminText(w, 401, "admin token required\n")
		return
	}
	adminText(w, 200, "request logs are written to the server's stdout as JSON lines (no message text, no tokens, no client addresses)\n")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
