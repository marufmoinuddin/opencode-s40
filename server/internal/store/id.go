package store

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// idMu guards the counter part of newID. The random part comes from
// crypto/rand; the timestamp prefix keeps ids sortable and makes them
// unguessable enough for a per-device pairing id on a phone.
var idMu sync.Mutex

func newID(prefix string) string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand does not fail in practice; fall back to time only.
		return fmt.Sprintf("%s%d", prefix, time.Now().UnixNano())
	}
	idMu.Lock()
	defer idMu.Unlock()
	return fmt.Sprintf("%s%x%s", prefix, time.Now().UnixMilli(), hex.EncodeToString(b[:]))
}

// NewID returns a fresh random id with the given prefix. Used for pairing ids
// and device ids.
func NewID(prefix string) string { return newID(prefix) }

// UTCDay returns the current UTC day as YYYY-MM-DD, the key for daily limits.
func UTCDay() string { return time.Now().UTC().Format("2006-01-02") }
