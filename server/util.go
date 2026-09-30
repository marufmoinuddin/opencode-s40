package main

import (
	"encoding/json"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/maruf/ocs40/server/internal/protocol"
)

// ipRE matches IPv4 and bracketed IPv6 addresses, with an optional port. Used
// to keep client addresses out of the logs.
var ipRE = regexp.MustCompile(`\b\d{1,3}(\.\d{1,3}){3}(:\d+)?\b|\[[0-9A-Fa-f:.%a-z]+\](:\d+)?`)

func jsonMarshal(v any) ([]byte, error) { return json.Marshal(v) }

// sanitize makes an assistant answer readable on a 240x320 screen.
//
// opencode answers in Markdown; a 2007 phone has no Markdown renderer and no
// room for a table. This keeps the text, strips the decoration that would
// confuse the layout code (fences, heading hashes, bold/italic markers, list
// bullets that the phone renders itself) and guarantees a UTF-8-safe result.
func sanitize(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")

	// Code fences: keep the code, drop the ``` lines. A 2007 screen cannot show
	// a syntax-highlighted block, but a short snippet is still useful.
	lines := strings.Split(s, "\n")
	var out []string
	inFence := false
	for _, ln := range lines {
		trimmed := strings.TrimSpace(ln)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}
		out = append(out, ln)
	}
	_ = inFence
	s = strings.Join(out, "\n")

	// Headings: keep the words, drop the #'s.
	s = headingRE.ReplaceAllString(s, "$1")

	// Inline emphasis. Order matters: bold first, then single-asterisk italic.
	// Doing single-asterisk italics first would eat the second "*" of a "**bold**"
	// and swallow the rest of the line.
	s = strings.ReplaceAll(s, "**", "")
	s = italicRE.ReplaceAllString(s, "$1$2")
	s = strings.ReplaceAll(s, "__", "")

	// Links: show the text, not the URL, unless the URL is the point.
	s = mdLinkRE.ReplaceAllString(s, "$1 ($2)")

	// Collapse runs of blank lines to one.
	s = blankRunRE.ReplaceAllString(s, "\n\n")

	// Tidy trailing spaces so the phone's line-wrap is predictable.
	lines = strings.Split(s, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}
	s = strings.Join(lines, "\n")

	return strings.TrimSpace(s)
}

var (
	headingRE  = regexp.MustCompile(`(?m)^#{1,6}\s*`)
	italicRE   = regexp.MustCompile(`(^|[\s(])\*([^*\n]+)\*`)
	mdLinkRE   = regexp.MustCompile(`\[([^\]]*)\]\(([^)\s]+)\)`)
	blankRunRE = regexp.MustCompile(`\n{3,}`)
)

// ---------------------------------------------------------------- replay

// replayCache remembers the reply already produced for a request id, so a phone
// retrying after a dropped connection never triggers a second billed call.
//
// It is deliberately in-memory and small: a restart simply means the phone's
// retry is treated as new, which is why the protocol makes no exactly-once
// claim.
type replayCache struct {
	mu sync.Mutex
	m  map[string]replayEntry
}

type replayEntry struct {
	msg *protocol.Message
	at  time.Time
}

func newReplayCache(n int) *replayCache {
	return &replayCache{m: make(map[string]replayEntry, n)}
}

func (c *replayCache) remember(id string, m *protocol.Message) {
	if id == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[id] = replayEntry{msg: m.Clone(), at: time.Now()}
}

func (c *replayCache) get(id string) (*protocol.Message, bool) {
	if id == "" {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[id]
	if !ok {
		return nil, false
	}
	return e.msg, true
}

// sweep drops entries older than maxAge so the map cannot grow without bound.
func (c *replayCache) sweep(maxAge time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, v := range c.m {
		if time.Since(v.at) > maxAge {
			delete(c.m, k)
		}
	}
}
