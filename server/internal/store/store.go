package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
	"unicode"

	_ "modernc.org/sqlite" // pure-Go SQLite: no cgo, so the binary is portable
)

// Store is the gateway's chat history and device registry.
//
// Everything here is plain SQL against one file (SQLITE_DB in the environment).
// Retention: messages of a conversation are dropped RETENTION_DAYS after its
// last update unless it is pinned; pinned ones stay until unpinned or deleted.
type Store struct {
	db      *sql.DB
	retDays int
}

// Open opens (creating if needed) the database at path and applies the schema.
//
// The foreign_keys pragma is executed as a statement rather than only in the
// DSN: modernc's SQLite ignores `_pragma=foreign_keys(1)` in the connection
// string, which silently disables the ON DELETE CASCADE that keeps a deleted
// conversation from leaving orphaned messages. VerifyForeignKeys is called and
// checked by the tests so this can never regress unnoticed.
func Open(path string, retDays int) (*Store, error) {
	// _busy_timeout: several phone requests can overlap; wait instead of failing.
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One connection: SQLite has a single writer, and the pragmas below
	// (especially foreign_keys) are per-connection.
	db.SetMaxOpenConns(1)
	if err := exec(db, `PRAGMA foreign_keys = ON`); err != nil {
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}
	if on, err := foreignKeysOn(db); err != nil {
		return nil, err
	} else if !on {
		return nil, fmt.Errorf("foreign keys are not enabled on the SQLite connection")
	}
	s := &Store{db: db, retDays: retDays}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

func exec(db *sql.DB, q string, args ...any) error {
	_, err := db.Exec(q, args...)
	return err
}

// ForeignKeysOn reports whether SQLite cascade deletes are active. Exported for
// the tests that guard the pragma.
func ForeignKeysOn(s *Store) (bool, error) { return foreignKeysOn(s.db) }

func foreignKeysOn(db *sql.DB) (bool, error) {
	var on int
	if err := db.QueryRow(`PRAGMA foreign_keys`).Scan(&on); err != nil {
		return false, err
	}
	return on == 1, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS devices (
			id         TEXT PRIMARY KEY,
			token      TEXT NOT NULL UNIQUE,
			name       TEXT NOT NULL DEFAULT '',
			created    INTEGER NOT NULL,
			last_seen  INTEGER NOT NULL DEFAULT 0,
			revoked    INTEGER NOT NULL DEFAULT 0,
			req_day    TEXT NOT NULL DEFAULT '',
			req_count  INTEGER NOT NULL DEFAULT 0,
			tok_count  INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS conversations (
			id         TEXT PRIMARY KEY,
			device     TEXT NOT NULL,
			oc_session TEXT NOT NULL DEFAULT '',
			title      TEXT NOT NULL DEFAULT '',
			pinned     INTEGER NOT NULL DEFAULT 0,
			created    INTEGER NOT NULL,
			updated    INTEGER NOT NULL,
			messages   INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS messages (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			conv       TEXT NOT NULL,
			role       TEXT NOT NULL,
			text       TEXT NOT NULL,
			created    INTEGER NOT NULL,
			FOREIGN KEY (conv) REFERENCES conversations(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS messages_conv ON messages (conv, id)`,
		`CREATE INDEX IF NOT EXISTS conv_device ON conversations (device, updated DESC)`,
	}
	for _, q := range stmts {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("schema: %w", err)
		}
	}
	return nil
}

// ---------------------------------------------------------------- devices

// Device is one paired phone.
type Device struct {
	ID   string
	Name string
}

// DeviceByToken looks up a device by its access token; revoked devices are
// reported as not found so a revoked token looks exactly like a wrong one.
func (s *Store) DeviceByToken(token string) (Device, error) {
	var d Device
	err := s.db.QueryRow(
		`SELECT id, name FROM devices WHERE token = ? AND revoked = 0`, token).
		Scan(&d.ID, &d.Name)
	if err == sql.ErrNoRows {
		return Device{}, fmt.Errorf("unknown or revoked token")
	}
	return d, err
}

// AddDevice registers a paired device.
func (s *Store) AddDevice(id, token, name string) error {
	now := ms()
	_, err := s.db.Exec(
		`INSERT INTO devices (id, token, name, created, last_seen) VALUES (?, ?, ?, ?, ?)`,
		id, token, name, now, now)
	return err
}

// RevokeDevice marks a device revoked; its token stops working immediately.
// Revoking an already-revoked device is a no-op, not an error: the admin script
// may retry, and "already revoked" is the state the caller asked for.
func (s *Store) RevokeDevice(id string) error {
	if _, err := s.db.Exec(`UPDATE devices SET revoked = 1 WHERE id = ?`, id); err != nil {
		return err
	}
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM devices WHERE id = ?`, id).Scan(&n)
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("no such device")
	}
	return nil
}

// Devices lists paired devices for the admin API.
func (s *Store) Devices() ([]Device, error) {
	rows, err := s.db.Query(`SELECT id, name FROM devices WHERE revoked = 0 ORDER BY created`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Device
	for rows.Next() {
		var d Device
		if err := rows.Scan(&d.ID, &d.Name); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Touch records that the device was seen today.
func (s *Store) Touch(id string) error {
	_, err := s.db.Exec(`UPDATE devices SET last_seen = ? WHERE id = ?`, ms(), id)
	return err
}

// CountUsage adds one request to today's counter and returns the new count.
// day is the caller's UTC day string, so the caller controls the timezone rule.
//
// The day is rolled over in the same statement, so a device that has not been
// seen today starts at exactly 1 (never 2, never 0).
//
// A device that is not in the table is an error, not a zero: silently
// returning 0 would let an unknown device past the daily limit.
func (s *Store) CountUsage(id, day string) (int, error) {
	res, err := s.db.Exec(
		`UPDATE devices
		 SET req_count = CASE WHEN req_day = ? THEN req_count + 1 ELSE 1 END,
		     req_day = ?
		 WHERE id = ?`, day, day, id)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, fmt.Errorf("no such device")
	}
	var count int
	if err := s.db.QueryRow(`SELECT req_count FROM devices WHERE id = ?`, id).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

// ---------------------------------------------------------------- conversations

// Conversation is a chat between one phone and opencode.
type Conversation struct {
	ID        string
	OCSession string
	Title     string
	Pinned    bool
	Updated   int64
	Messages  int
}

// NewConversation creates a conversation and returns its id.
func (s *Store) NewConversation(device, ocSession, title string) (string, error) {
	id := newID("c_")
	now := ms()
	_, err := s.db.Exec(
		`INSERT INTO conversations (id, device, oc_session, title, created, updated)
		 VALUES (?, ?, ?, ?, ?, ?)`, id, device, ocSession, title, now, now)
	return id, err
}

// SetOCSession stores the opencode session id for a conversation.
func (s *Store) SetOCSession(conv, ocSession string) error {
	_, err := s.db.Exec(`UPDATE conversations SET oc_session = ? WHERE id = ?`, ocSession, conv)
	return err
}

// OCSession returns the opencode session id of a conversation ("" if none).
func (s *Store) OCSession(conv string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT oc_session FROM conversations WHERE id = ?`, conv).Scan(&v)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("conversation not found")
	}
	return v, err
}

// OCSessionOrEmpty returns the opencode session id of a conversation, or "" when
// the conversation is gone. Used on the delete path, where the row may already
// have been removed.
func (s *Store) OCSessionOrEmpty(conv string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT oc_session FROM conversations WHERE id = ?`, conv).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

// OwnsConversation reports whether conv belongs to device.
func (s *Store) OwnsConversation(device, conv string) bool {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM conversations WHERE id = ? AND device = ?`, conv, device).Scan(&n)
	return err == nil && n > 0
}

// AddMessage appends a message and refreshes the conversation's counters and
// title (the first user message becomes the title, truncated for a phone list).
func (s *Store) AddMessage(conv, role, text string) error {
	now := ms()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	// Commit or roll back before returning: with MaxOpenConns(1) the single
	// connection stays held until then, so a deferred Rollback that runs after
	// the commit would deadlock on the pool.
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()

	_, err = tx.Exec(`INSERT INTO messages (conv, role, text, created) VALUES (?, ?, ?, ?)`,
		conv, role, text, now)
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if role == "user" {
		// Set the title only while it is still the placeholder, so a chat keeps
		// the name of its first question.
		_, err = tx.Exec(`UPDATE conversations SET updated = ?, messages = messages + 1,
			title = CASE WHEN title = '' THEN ? ELSE title END WHERE id = ?`,
			now, deriveTitle(text), conv)
	} else {
		_, err = tx.Exec(`UPDATE conversations SET updated = ?, messages = messages + 1 WHERE id = ?`,
			now, conv)
	}
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// ListConversations returns the device's conversations, pinned first then
// newest, capped at limit.
func (s *Store) ListConversations(device string, limit int) ([]Conversation, error) {
	rows, err := s.db.Query(
		`SELECT id, oc_session, title, pinned, updated, messages
		 FROM conversations WHERE device = ?
		 ORDER BY pinned DESC, updated DESC LIMIT ?`, device, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Conversation
	for rows.Next() {
		var c Conversation
		if err := rows.Scan(&c.ID, &c.OCSession, &c.Title, &c.Pinned, &c.Updated, &c.Messages); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Message is one stored chat message.
type Message struct {
	Role string
	Text string
}

// History returns the newest messages of a conversation, oldest first, stopping
// once maxBytes would be exceeded (so the phone always gets a readable window).
// It returns hasMore when older messages were left out.
func (s *Store) History(conv string, maxBytes int) (msgs []Message, hasMore bool, err error) {
	rows, err := s.db.Query(`SELECT role, text FROM messages WHERE conv = ? ORDER BY id DESC`, conv)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	total := 0
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.Role, &m.Text); err != nil {
			return nil, false, err
		}
		n := len(m.Text) + len(m.Role) + 12
		if total+n > maxBytes && len(msgs) > 0 {
			hasMore = true
			break
		}
		msgs = append(msgs, m)
		total += n
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	// reverse to oldest first
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	return msgs, hasMore, nil
}

// Pin sets or clears the pinned flag.
func (s *Store) Pin(conv string, pinned bool) error {
	v := 0
	if pinned {
		v = 1
	}
	res, err := s.db.Exec(`UPDATE conversations SET pinned = ? WHERE id = ?`, v, conv)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("conversation not found")
	}
	return nil
}

// DeleteConversation removes a conversation and its messages.
func (s *Store) DeleteConversation(conv string) error {
	_, err := s.db.Exec(`DELETE FROM messages WHERE conv = ?`, conv)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`DELETE FROM conversations WHERE id = ?`, conv)
	return err
}

// SearchResult is one conversation matching a search.
type SearchResult struct {
	Conversation
	Snippet string
}

// Search finds conversations containing every word of query, case- and
// Turkish-insensitive ("sise" finds "Şişe"), newest first.
func (s *Store) Search(device, query string, limit int) ([]SearchResult, error) {
	words := foldWords(query)
	if len(words) == 0 {
		return nil, nil
	}
	rows, err := s.db.Query(
		`SELECT c.id, c.oc_session, c.title, c.pinned, c.updated, c.messages, m.text
		 FROM conversations c JOIN messages m ON m.conv = c.id
		 WHERE c.device = ? ORDER BY c.updated DESC`, device)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byConv := map[string]*SearchResult{}
	order := []string{}
	for rows.Next() {
		var r SearchResult
		var text string
		if err := rows.Scan(&r.ID, &r.OCSession, &r.Title, &r.Pinned, &r.Updated, &r.Messages, &text); err != nil {
			return nil, err
		}
		folded := fold(text)
		all := true
		for _, w := range words {
			if !strings.Contains(folded, w) {
				all = false
				break
			}
		}
		if !all {
			continue
		}
		cur, ok := byConv[r.ID]
		if !ok {
			cp := r
			cp.Snippet = snippet(text, 90)
			byConv[r.ID] = &cp
			order = append(order, r.ID)
			continue
		}
		if cur.Messages == 0 {
			cur.Snippet = snippet(text, 90)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]SearchResult, 0, len(order))
	for _, id := range order {
		out = append(out, *byConv[id])
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// Cleanup deletes unpinned conversations older than the retention window and
// returns how many went. The messages are removed by the cascade on the
// conversation delete, so there is no separate (racy) first DELETE here.
func (s *Store) Cleanup() (int, error) {
	cutoff := time.Now().Add(-time.Duration(s.retDays) * 24 * time.Hour).UnixMilli()
	res, err := s.db.Exec(`DELETE FROM conversations WHERE pinned = 0 AND updated < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// ---------------------------------------------------------------- helpers

func ms() int64 { return time.Now().UnixMilli() }

// deriveTitle makes a short, single-line title from the first user message.
func deriveTitle(text string) string {
	s := strings.TrimSpace(text)
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	if len(s) > 60 {
		// Cut on a rune boundary and avoid a split word.
		cut := s[:60]
		if i := strings.LastIndexByte(cut, ' '); i > 20 {
			cut = cut[:i]
		}
		s = cut + "..."
	}
	return s
}

// foldWords lowercases and splits a query into search terms.
func foldWords(q string) []string {
	f := strings.FieldsFunc(fold(q), func(r rune) bool {
		return unicode.IsSpace(r) || r == ',' || r == '.'
	})
	out := make([]string, 0, len(f))
	for _, w := range f {
		if len(w) >= 2 {
			out = append(out, w)
		}
	}
	return out
}

// fold lowercases text and maps the Turkish dotted/dotless letters to their
// ASCII-ish base, so "sise" matches "Şişe" and "icap" matches "İçap".
func fold(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range strings.ToLower(s) {
		switch r {
		case 'ı':
			b.WriteRune('i')
		case 'İ', 'î', 'ï':
			b.WriteRune('i')
		case 'ş':
			b.WriteRune('s')
		case 'ğ':
			b.WriteRune('g')
		case 'ü':
			b.WriteRune('u')
		case 'ö':
			b.WriteRune('o')
		case 'ç':
			b.WriteRune('c')
		case 'â', 'û':
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func snippet(text string, n int) string {
	s := strings.ReplaceAll(strings.TrimSpace(text), "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
