// Command ocs40-server is the gateway between a Nokia Series 40 phone and the
// official opencode server running on the developer's own PC.
//
//	Nokia (Java ME app) --HTTPS: TLS 1.0, RSA, no SNI, cert from a private CA-->
//	ocs40-server --HTTP on loopback--> opencode serve
//
// It exists because a 2007 phone cannot complete a modern TLS handshake, and
// because opencode's own API is far too large for a MIDP HTTP client. The
// phone speaks the small OCS40/1 text protocol; this process translates.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/maruf/ocs40/server/internal/oc"
	"github.com/maruf/ocs40/server/internal/protocol"
	"github.com/maruf/ocs40/server/internal/store"
)

func main() {
	log.SetFlags(0) // the logs are JSON lines
	log.SetPrefix("")
	logJSON(map[string]any{"evt": "start", "version": Version})

	cfg, err := loadConfig()
	if err != nil {
		logJSON(map[string]any{"evt": "fatal", "detail": err.Error()})
		os.Exit(1)
	}

	st, err := store.Open(cfg.DBPath, cfg.RetentionDays)
	if err != nil {
		logJSON(map[string]any{"evt": "fatal", "detail": "database: " + err.Error()})
		os.Exit(1)
	}
	defer st.Close()

	client := oc.New(cfg.OpenURL)
	chat := newChatService(cfg, client, st)

	tlsCfg, err := phoneTLS(cfg.Cert, cfg.Key)
	if err != nil {
		logJSON(map[string]any{"evt": "fatal", "detail": "certificate: " + err.Error()})
		os.Exit(1)
	}

	// Refuse to start in live mode when opencode is not there: a phone must not
	// be able to pair with a gateway that cannot answer.
	if !cfg.Mock {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		ver, err := client.Version(ctx)
		cancel()
		if err != nil {
			logJSON(map[string]any{"evt": "fatal", "detail": "opencode unreachable: " + err.Error()})
			os.Exit(1)
		}
		logJSON(map[string]any{"evt": "opencode", "version": ver, "url": cfg.OpenURL})
	} else {
		logJSON(map[string]any{"evt": "opencode", "version": "mock"})
	}

	srv := &server{cfg: cfg, st: st, cs: chat, client: client}

	// Create the admin token up front so the operator can read it without
	// having to make a request first.
	if tok, err := srv.adminToken(); err != nil {
		logJSON(map[string]any{"evt": "fatal", "detail": "admin token: " + err.Error()})
		os.Exit(1)
	} else {
		logJSON(map[string]any{"evt": "admin_token", "path": filepath.Join(filepath.Dir(cfg.DBPath), "admin_token")})
		_ = tok
	}

	phoneLn, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		logJSON(map[string]any{"evt": "fatal", "detail": "listen: " + err.Error()})
		os.Exit(1)
	}
	tlsLn := tlsNewListener(phoneLn, tlsCfg)

	phoneSrv := &http.Server{
		Handler:   srv.routes(),
		TLSConfig: tlsCfg,
		ErrorLog:  log.New(handshakeLog{}, "", 0),
		ReadHeaderTimeout: 20 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      cfg.Timeout + 30*time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// Housekeeping: retention sweep and replay-cache sweep.
	go func() {
		t := time.NewTicker(1 * time.Hour)
		defer t.Stop()
		for range t.C {
			if n, err := st.Cleanup(); err == nil && n > 0 {
				logJSON(map[string]any{"evt": "cleanup", "conversations": n})
			}
			chat.replay.sweep(2 * time.Hour)
		}
	}()

	errc := make(chan error, 1)
	go func() {
		logJSON(map[string]any{"evt": "listen", "addr": cfg.Listen, "tls": "1.0-1.3"})
		// Serve on the already-wrapped TLS listener with Serve, not ServeTLS:
		// ServeTLS with empty certificate paths on a listener that is already
		// wrapped serves PLAIN HTTP, which would let the phone talk to the
		// gateway unencrypted. There must be no plain-HTTP path at all.
		if err := phoneSrv.Serve(tlsLn); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()

	// The admin API is always on loopback: it can mint device tokens, so it must
	// never be reachable from the network the phone uses.
	adminLn, err := net.Listen("tcp", cfg.Admin)
	if err != nil {
		logJSON(map[string]any{"evt": "fatal", "detail": "admin listen: " + err.Error()})
		os.Exit(1)
	}
	adminSrv := &http.Server{
		Handler:           srv.adminRoutes(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		logJSON(map[string]any{"evt": "admin", "addr": cfg.Admin})
		if err := adminSrv.Serve(adminLn); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	select {
	case s := <-sig:
		logJSON(map[string]any{"evt": "stop", "signal": s.String()})
	case err := <-errc:
		logJSON(map[string]any{"evt": "fatal", "detail": err.Error()})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	_ = phoneSrv.Shutdown(ctx)
	_ = adminSrv.Shutdown(ctx)
	cancel()
}

// server holds the handlers.
type server struct {
	cfg    config
	st     *store.Store
	cs     *chatService
	client *oc.Client
}

// pairing is a pending device pairing: the phone shows a 6-digit code, the
// operator approves it with the admin API, and the phone then fetches its token.
type pairing struct {
	id        string
	code      string
	created   time.Time
	expires   time.Time
	claimed   bool
	deviceID  string
	token     string
	name      string
}

// Pairings live in memory for a few minutes only. A plain sync.Mutex is used
// (not a semaphore channel): the earlier channel-based version could fill its
// buffer twice in one code path and deadlock the whole gateway.
var (
	pairMu   sync.Mutex
	pairings = map[string]*pairing{}
)

func newPairing() *pairing {
	p := &pairing{
		id:      store.NewID("p_"),
		code:    sixDigits(),
		created: time.Now(),
		expires: time.Now().Add(10 * time.Minute),
	}
	pairMu.Lock()
	defer pairMu.Unlock()
	pairings[p.id] = p
	// Drop anything that has already expired, so the map cannot grow.
	for id, other := range pairings {
		if !other.valid() {
			delete(pairings, id)
		}
	}
	return p
}

// approvePairing hands the token to a pending pairing whose code matches.
// It reports whether a pairing was found (already-approved pairings are found
// again, so the admin script is safe to retry).
func approvePairing(st *store.Store, code, name string) (string, bool) {
	pairMu.Lock()
	defer pairMu.Unlock()
	for _, p := range pairings {
		if p.code != code || !p.valid() {
			continue
		}
		if p.token != "" {
			return p.deviceID, true // already approved
		}
		tok, err := randomToken()
		if err != nil {
			return "", false
		}
		devID := store.NewID("dev_")
		if err := st.AddDevice(devID, tok, name); err != nil {
			return "", false
		}
		p.token, p.deviceID, p.name = tok, devID, name
		return devID, true
	}
	return "", false
}

// claimPairing returns the state of a pairing, and whether it was found.
func claimPairing(id string) (*pairing, bool) {
	pairMu.Lock()
	defer pairMu.Unlock()
	p, ok := pairings[id]
	return p, ok
}

// takePairingToken hands the token over exactly once and then forgets the
// pairing, so a second claim can never recover it.
func takePairingToken(id string) (deviceID, token, name string, ok bool) {
	pairMu.Lock()
	defer pairMu.Unlock()
	p, found := pairings[id]
	if !found {
		return "", "", "", false
	}
	if p.token == "" {
		return "", "", "", true // still pending
	}
	deviceID, token, name = p.deviceID, p.token, p.name
	delete(pairings, id)
	return deviceID, token, name, true
}

func (p *pairing) valid() bool { return time.Now().Before(p.expires) }

func (p *pairing) device() (string, string, string, bool) {
	return p.deviceID, p.token, p.name, p.claimed && p.valid()
}

func sixDigits() string {
	n, err := cryptoRandomInt(1000000)
	if err != nil {
		return "000000"
	}
	return fmt.Sprintf("%06d", n)
}

func (s *server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.wrap(s.health, false))
	mux.HandleFunc("/echo", s.wrap(s.echo, false))
	mux.HandleFunc("/v1/status", s.wrap(s.status, false))
	mux.HandleFunc("/v1/pair/start", s.wrap(s.pairStart, false))
	mux.HandleFunc("/v1/pair/claim", s.wrap(s.pairClaim, false))
	mux.HandleFunc("/v1/chat", s.wrap(s.chat, true))
	mux.HandleFunc("/v1/more", s.wrap(s.more, true))
	mux.HandleFunc("/v1/conversations", s.wrap(s.conversations, true))
	mux.HandleFunc("/v1/history", s.wrap(s.history, true))
	mux.HandleFunc("/v1/search", s.wrap(s.search, true))
	mux.HandleFunc("/v1/pin", s.wrap(s.pin, true))
	mux.HandleFunc("/v1/delete", s.wrap(s.delete, true))
	return mux
}

// handler is one route. auth means the request needs a device token.
type handler func(w http.ResponseWriter, r *httpRequest, in *protocol.Message, dev store.Device) (*protocol.Message, error)

// wrap adapts a handler to net/http: reads the body (capped), parses OCS40/1,
// authenticates when needed, and writes either the reply or a status error.
func (s *server) wrap(h handler, auth bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rw := &httpRequest{r: r, tls: r.TLS}
		start := time.Now()

		body, err := readBody(r)
		if err != nil {
			writeMsg(w, 413, protocol.New().Set("status", "body_too_large"))
			return
		}
		in := protocol.New()
		if strings.TrimSpace(string(body)) != "" {
			parsed, err := protocol.Parse(string(body))
			if err != nil {
				// Not our protocol: an operator proxy or a wrong address.
				writeMsg(w, 400, protocol.New().Set("status", "not_an_ocs40_request"))
				return
			}
			in = parsed
		}

		var dev store.Device
		if auth {
			tok := bearer(r)
			if tok == "" {
				writeMsg(w, 401, protocol.New().Set("status", "access_code_required"))
				return
			}
			d, err := s.st.DeviceByToken(tok)
			if err != nil {
				// A wrong and a revoked token are indistinguishable on purpose.
				writeMsg(w, 401, protocol.New().Set("status", "access_code_invalid"))
				return
			}
			dev = d
			_ = s.st.Touch(dev.ID)
		}

		out, herr := h(w, rw, in, dev)
		code := 200
		if herr != nil {
			var se *statusError
			if errors.As(herr, &se) {
				code = se.code
				out = protocol.New().Set("status", se.status)
				for k, v := range se.fields {
					out.Set(k, v)
				}
			} else {
				code = 500
				out = protocol.New().Set("status", "server_error")
			}
		}
		if out == nil {
			out = protocol.New().Set("status", "ok")
		}
		if !out.Has("status") {
			out.Set("status", "ok")
		}
		writeMsg(w, code, out)
		// The log carries method, path, status and the TLS details only: never
		// message text, replies, tokens or the client's address.
		logJSON(map[string]any{
			"evt": "req", "method": r.Method, "path": r.URL.Path,
			"code": code, "status": out.Get("status"),
			"tls": rw.tlsVersion(), "cipher": tlsCipherName(rw), "ms": time.Since(start).Milliseconds(),
		})
	}
}

// maxBody is the largest request body accepted. A phone message is capped at
// maxPhoneMessage characters, so this leaves generous headroom.
const maxBody = 16 << 10

func readBody(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	defer r.Body.Close()
	return io.ReadAll(io.LimitReader(r.Body, maxBody))
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

func writeMsg(w http.ResponseWriter, code int, m *protocol.Message) {
	b := []byte(m.Format())
	h := w.Header()
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Length", strconv.Itoa(len(b)))
	w.WriteHeader(code)
	_, _ = w.Write(b)
}

// ---------------------------------------------------------------- handlers

func (s *server) health(w http.ResponseWriter, r *httpRequest, in *protocol.Message, dev store.Device) (*protocol.Message, error) {
	m := protocol.New().Set("status", "ok").
		Set("server", "ocs40").
		Set("version", Version).
		Set("mock", boolFlag(s.cfg.Mock))
	if v, c := r.tlsVersion(), tlsCipherName(r); v != "" {
		m.Set("tls", v)
		m.Set("cipher", oneLine(c))
	}
	m.Text = "OpenCode S40 gateway " + Version
	if s.cfg.Mock {
		m.Text += " (test mode: opencode is not called)"
	}
	return m, nil
}

func (s *server) echo(w http.ResponseWriter, r *httpRequest, in *protocol.Message, dev store.Device) (*protocol.Message, error) {
	// A strict UTF-8 round trip: if this fails, everything else will too.
	if len(in.Text) > 512 {
		return nil, errStatus(400, "echo_too_long")
	}
	if !utf8Valid(in.Text) {
		return nil, errStatus(400, "not_utf8")
	}
	m := protocol.New().Set("status", "ok").Set("bytes", strconv.Itoa(len(in.Text)))
	// The Turkish probe: proves the phone's charset survives the round trip.
	if strings.Contains(in.Text, "Şişe") {
		m.Set("probe", "match")
	}
	m.Text = in.Text
	return m, nil
}

func (s *server) status(w http.ResponseWriter, r *httpRequest, in *protocol.Message, dev store.Device) (*protocol.Message, error) {
	return s.cs.Status(r.ctx())
}

func (s *server) pairStart(w http.ResponseWriter, r *httpRequest, in *protocol.Message, dev store.Device) (*protocol.Message, error) {
	p := newPairing()
	return protocol.New().Set("status", "ok").
		Set("pair", p.id).
		Set("code", p.code).
		Set("expires", strconv.FormatInt(p.expires.Unix(), 10)), nil
}

func (s *server) pairClaim(w http.ResponseWriter, r *httpRequest, in *protocol.Message, dev store.Device) (*protocol.Message, error) {
	id := in.Get("pair")
	if id == "" {
		return nil, errStatus(400, "missing_pair")
	}
	p, found := claimPairing(id)
	if !found {
		return nil, errStatus(404, "pair_not_found")
	}
	if !p.valid() {
		return nil, errStatus(410, "expired")
	}
	if p.token == "" {
		// Not approved yet: the phone polls until the operator says yes.
		return protocol.New().Set("status", "pending"), nil
	}
	// The token is handed over exactly once, then the pairing is forgotten.
	did, tok, name, _ := takePairingToken(id)
	return protocol.New().Set("status", "ok").
		Set("device", did).Set("name", name).Set("token", tok), nil
}

func (s *server) chat(w http.ResponseWriter, r *httpRequest, in *protocol.Message, dev store.Device) (*protocol.Message, error) {
	return s.cs.Reply(r.ctx(), dev, in)
}
func (s *server) more(w http.ResponseWriter, r *httpRequest, in *protocol.Message, dev store.Device) (*protocol.Message, error) {
	return s.cs.More(r.ctx(), dev, in)
}
func (s *server) conversations(w http.ResponseWriter, r *httpRequest, in *protocol.Message, dev store.Device) (*protocol.Message, error) {
	return s.cs.Conversations(r.ctx(), dev, in)
}
func (s *server) history(w http.ResponseWriter, r *httpRequest, in *protocol.Message, dev store.Device) (*protocol.Message, error) {
	return s.cs.History(r.ctx(), dev, in)
}
func (s *server) search(w http.ResponseWriter, r *httpRequest, in *protocol.Message, dev store.Device) (*protocol.Message, error) {
	return s.cs.Search(r.ctx(), dev, in)
}
func (s *server) pin(w http.ResponseWriter, r *httpRequest, in *protocol.Message, dev store.Device) (*protocol.Message, error) {
	return s.cs.Pin(r.ctx(), dev, in)
}
func (s *server) delete(w http.ResponseWriter, r *httpRequest, in *protocol.Message, dev store.Device) (*protocol.Message, error) {
	return s.cs.Delete(r.ctx(), dev, in)
}
