// Package protocol implements "OCS40/1", the text format shared by the phone
// app and this gateway.
//
// The shape is deliberately the same as the reference project's S40/1 (a magic
// line, `key: value` headers, a blank line, then a UTF-8 text body) because
// that format is proven to survive a 2007 phone's MIDP HTTP stack:
//
//	OCS40/1
//	status: ok
//	conversation: c_9f2a1b
//
//	free text (UTF-8)
//
// A 2007 phone cannot be trusted with JSON: MIDP has no JSON parser, the
// tokenizer would be dead weight in a ~100 KB JAR, and byte counts have to be
// exact for a screen-sized buffer. Header values are ASCII; the body is UTF-8.
package protocol

import (
	"fmt"
	"sort"
	"strings"
)

// Magic is the first line of every request and response body.
const Magic = "OCS40/1"

// Message is a parsed OCS40/1 body. Field order is not preserved on parse;
// responses are written with sorted keys so bodies are deterministic.
type Message struct {
	Fields map[string]string
	Text   string
}

// New returns an empty message.
func New() *Message {
	return &Message{Fields: make(map[string]string, 8)}
}

// Parse reads an OCS40/1 body. It returns an error if the magic line is
// missing, if a header line has no colon, or if a header key is not
// lower-case ASCII (the phone only ever sends those).
func Parse(body string) (*Message, error) {
	m := New()
	if body == "" {
		return nil, fmt.Errorf("empty body")
	}
	rest := body
	first := true
	for {
		idx := strings.IndexByte(rest, '\n')
		var line string
		if idx < 0 {
			line = rest
			rest = ""
		} else {
			line = rest[:idx]
			rest = rest[idx+1:]
		}
		line = strings.TrimSuffix(line, "\r")
		if first {
			if strings.TrimSpace(line) != Magic {
				return nil, fmt.Errorf("not an %s message", Magic)
			}
			first = false
			continue
		}
		if line == "" {
			// Blank line: everything after it is the text body.
			m.Text = rest
			return m, nil
		}
		c := strings.IndexByte(line, ':')
		if c <= 0 {
			return nil, fmt.Errorf("bad header line %q", line)
		}
		key := strings.TrimSpace(line[:c])
		if !validKey(key) {
			return nil, fmt.Errorf("bad header key %q", key)
		}
		m.Fields[key] = strings.TrimSpace(line[c+1:])
		if idx < 0 {
			// No blank line and no text: headers only.
			return m, nil
		}
	}
}

func validKey(k string) bool {
	if k == "" {
		return false
	}
	for i := 0; i < len(k); i++ {
		c := k[i]
		if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' {
			continue
		}
		return false
	}
	return true
}

// Get returns a field value, or "" when absent.
func (m *Message) Get(key string) string {
	if m == nil {
		return ""
	}
	return m.Fields[key]
}

// Flag reports whether a field is exactly "1".
func (m *Message) Flag(key string) bool {
	return m.Get(key) == "1"
}

// Has reports whether a field is present.
func (m *Message) Has(key string) bool {
	if m == nil {
		return false
	}
	_, ok := m.Fields[key]
	return ok
}

// Set stores a field value. Newlines in v are collapsed to spaces so a header
// can never break the framing.
func (m *Message) Set(key, v string) *Message {
	m.Fields[key] = oneLine(v)
	return m
}

// Format renders the message. Keys are sorted, so the same message always
// produces the same bytes (the phone caches nothing, but deterministic bodies
// make the server testable and logs diffable).
func (m *Message) Format() string {
	var b strings.Builder
	b.WriteString(Magic)
	b.WriteByte('\n')
	keys := make([]string, 0, len(m.Fields))
	for k := range m.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		b.WriteString(k)
		b.WriteString(": ")
		b.WriteString(oneLine(m.Fields[k]))
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	b.WriteString(m.Text)
	return b.String()
}

// Clone returns a deep copy, so a cached reply can be handed out repeatedly
// without the caller mutating the cached copy.
func (m *Message) Clone() *Message {
	if m == nil {
		return nil
	}
	c := New()
	for k, v := range m.Fields {
		c.Fields[k] = v
	}
	c.Text = m.Text
	return c
}

func oneLine(v string) string {
	if !strings.ContainsAny(v, "\r\n") {
		return v
	}
	return strings.NewReplacer("\r\n", " ", "\n", " ", "\r", " ").Replace(v)
}
