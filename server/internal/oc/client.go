// Package oc is a small, hand-written client for the official opencode server
// (`opencode serve`), covering only what the phone gateway needs.
//
// Why not the published Go SDK: github.com/sst/opencode-sdk-go was last tagged
// v0.19.2 (December 2025) while the CLI is at 1.18.33, and the SDK does not
// expose the SSE event stream the phone needs for live progress. The endpoints
// used here were read from the running server's own OpenAPI document
// (GET /doc) and verified by live calls:
//
//	POST /session                          create
//	POST /session/{id}/prompt_async        send (HTTP 204, no body)
//	GET  /event                            SSE: message.part.updated / session.idle
//	GET  /session/{id}/message             history
//	POST /session/{id}/abort               stop a run
//	GET  /global/health                    server health
//	GET  /agent                            available agents
package oc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Client talks to one opencode server.
type Client struct {
	BaseURL string
	HTTP    *http.Client

	mu      sync.Mutex
	streams map[string]chan Event // sessionID -> event fan-out
}

// New returns a client for baseURL (e.g. "http://127.0.0.1:4096").
func New(baseURL string) *Client {
	baseURL = strings.TrimRight(baseURL, "/")
	return &Client{
		BaseURL: baseURL,
		HTTP: &http.Client{
			Timeout: 0, // long runs; per-request contexts carry the deadlines
		},
		streams: make(map[string]chan Event),
	}
}

// Version returns the opencode server version from /global/health.
func (c *Client) Version(ctx context.Context) (string, error) {
	var out struct {
		Healthy bool   `json:"healthy"`
		Version string `json:"version"`
	}
	if err := c.do(ctx, http.MethodGet, "/global/health", nil, &out); err != nil {
		return "", err
	}
	if !out.Healthy {
		return "", fmt.Errorf("opencode server is not healthy")
	}
	return out.Version, nil
}

// Agent is one agent the server offers.
type Agent struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Mode        string `json:"mode"`
}

// Agents lists the server's agents.
func (c *Client) Agents(ctx context.Context) ([]Agent, error) {
	var out []Agent
	err := c.do(ctx, http.MethodGet, "/agent", nil, &out)
	return out, err
}

type sessionResp struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	ParentID string `json:"parentID"`
}

// CreateSession starts a new opencode session. title may be empty.
func (c *Client) CreateSession(ctx context.Context, title string) (string, error) {
	body := map[string]any{}
	if title != "" {
		body["title"] = title
	}
	var out sessionResp
	if err := c.do(ctx, http.MethodPost, "/session", body, &out); err != nil {
		return "", err
	}
	if out.ID == "" {
		return "", fmt.Errorf("opencode returned no session id")
	}
	return out.ID, nil
}

// DeleteSession removes a session upstream. Best effort.
func (c *Client) DeleteSession(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/session/"+id, nil, nil)
}

// Abort stops whatever the session is currently doing.
func (c *Client) Abort(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodPost, "/session/"+id+"/abort", map[string]any{}, nil)
}

// TextPartInput is the phone's text message.
type TextPartInput struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// Prompt sends a message. agent selects the opencode agent (e.g. "plan" or
// "build"); system is prepended as the system prompt; tools maps a tool name to
// enabled=false to switch it off. The call returns as soon as opencode accepts
// the message (HTTP 204): the answer arrives over the event stream, which is
// what lets the phone show progress instead of one long silence.
func (c *Client) Prompt(ctx context.Context, sessionID, text, agent, system string, tools map[string]bool) error {
	body := map[string]any{
		"parts": []TextPartInput{{Type: "text", Text: text}},
	}
	if agent != "" {
		body["agent"] = agent
	}
	if system != "" {
		body["system"] = system
	}
	if len(tools) > 0 {
		body["tools"] = tools
	}
	return c.do(ctx, http.MethodPost, "/session/"+sessionID+"/prompt_async", body, nil)
}

// messageResp mirrors the part of opencode's message shape we read.
type messageResp struct {
	Info struct {
		ID      string `json:"id"`
		Role    string `json:"role"`
		ModelID string `json:"modelID"`
		ProvID  string `json:"providerID"`
		Error   *struct {
			Name    string `json:"name"`
			Message string `json:"message"`
		} `json:"error"`
		Tokens struct {
			Input  int `json:"input"`
			Output int `json:"output"`
		} `json:"tokens"`
		Finish string `json:"finish"`
	} `json:"info"`
	Parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"parts"`
}

// Message is a finished message as the gateway stores it.
type Message struct {
	Role     string
	Text     string
	ErrName  string
	ErrMsg   string
	TokensIn int
	TokensOut int
}

// History returns the session's messages, oldest first, with only the text
// parts of each one joined.
func (c *Client) History(ctx context.Context, sessionID string) ([]Message, error) {
	var out []messageResp
	if err := c.do(ctx, http.MethodGet, "/session/"+sessionID+"/message", nil, &out); err != nil {
		return nil, err
	}
	msgs := make([]Message, 0, len(out))
	for _, m := range out {
		var b strings.Builder
		for _, p := range m.Parts {
			if p.Type == "text" && p.Text != "" {
				if b.Len() > 0 {
					b.WriteString("\n")
				}
				b.WriteString(p.Text)
			}
		}
		msg := Message{
			Role: m.Info.Role, Text: b.String(),
			TokensIn: m.Info.Tokens.Input, TokensOut: m.Info.Tokens.Output,
		}
		if m.Info.Error != nil {
			msg.ErrName, msg.ErrMsg = m.Info.Error.Name, m.Info.Error.Message
		}
		msgs = append(msgs, msg)
	}
	return msgs, nil
}

// Event is one opencode server event, flattened to what the phone cares about.
type Event struct {
	Type string // message.part.updated | message.part.delta | session.idle | ...
	// Text is the full text of an assistant text part (part.updated) or the
	// increment (part.delta). Empty for other event types.
	Text string
	// PartID and SessionID identify the part/session the event belongs to.
	PartID    string
	SessionID string
	// Tool and ToolStatus are set for tool events.
	Tool       string
	ToolStatus string
	// MessageID identifies the message the part belongs to. opencode puts the
	// user's own message on the stream too, and its text looks exactly like an
	// assistant part, so the caller must be able to tell them apart.
	MessageID string
	// IsDelta distinguishes an increment (message.part.delta) from a whole-part
	// snapshot (message.part.updated).
	IsDelta bool
	// Role is the owning message's role ("user" or "assistant"), taken from the
	// message.updated event that introduces it. Empty for part events, which is
	// why the pump remembers it per message.
	Role string
}

// Subscribe returns a channel of events for one session. The channel is closed
// when ctx is done or the stream fails. Exactly one stream is kept per session;
// calling Subscribe again for the same session returns the same channel.
func (c *Client) Subscribe(ctx context.Context, sessionID string) <-chan Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ch, ok := c.streams[sessionID]; ok {
		return ch
	}
	ch := make(chan Event, 256)
	c.streams[sessionID] = ch
	go c.pump(ctx, sessionID, ch)
	return ch
}

func (c *Client) pump(ctx context.Context, sessionID string, out chan<- Event) {
	defer func() {
		c.mu.Lock()
		delete(c.streams, sessionID)
		c.mu.Unlock()
		close(out)
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/event", nil)
	if err != nil {
		return
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "data:") {
			continue // comments, "event:", "id:", empty lines
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		var raw struct {
			Type       string `json:"type"`
			Properties struct {
				SessionID string `json:"sessionID"`
				MessageID string `json:"messageID"`
				PartID    string `json:"partID"`
				Field     string `json:"field"`
				Delta     string `json:"delta"`
				Info      struct {
					ID   string `json:"id"`
					Role string `json:"role"`
					Error *struct {
						Name    string `json:"name"`
						Message string `json:"message"`
						Data    struct {
							Message string `json:"message"`
							Status  int    `json:"statusCode"`
						} `json:"data"`
					} `json:"error"`
				} `json:"info"`
				Part      struct {
					ID        string `json:"id"`
					MessageID string `json:"messageID"`
					Type      string `json:"type"`
					Text      string `json:"text"`
					Tool   string `json:"tool"`
					State  struct {
						Status string `json:"status"`
					} `json:"state"`
				} `json:"part"`
			} `json:"properties"`
		}
		if err := json.Unmarshal([]byte(payload), &raw); err != nil {
			continue
		}
		p := raw.Properties
		// Only this session's events.
		if p.SessionID != "" && p.SessionID != sessionID {
			continue
		}
		ev := Event{Type: raw.Type, PartID: p.PartID, SessionID: p.SessionID, MessageID: p.MessageID}
		if p.Part.ID != "" {
			ev.MessageID = p.Part.MessageID
		}

		// Record the role the moment it is read, BEFORE anything is stamped on
		// the event, so this event and every later part event see it.
		if raw.Type == "message.updated" && p.Info.ID != "" && p.Info.Role != "" {
			c.rememberRole(p.Info.ID, p.Info.Role)
		}

		// Roles are authoritative and are never guessed: a history lookup
		// cannot tell the user's message from the assistant's when only one of
		// them exists yet, and guessing there is what made the reply come back
		// as the question.
		ev.Role = c.roleOf(ev.MessageID)
		if raw.Type == "message.updated" && p.Info.Role != "" {
			// Forward the announcement itself, with the id it belongs to: the
			// consumer learns which message is the assistant's from this and
			// from nothing else.
			ev.MessageID, ev.Role, ev.Text = p.Info.ID, p.Info.Role, ""
		}

		if raw.Type == "message.updated" && p.Info.Error != nil {
			msg := p.Info.Error.Message
			if msg == "" {
				msg = p.Info.Error.Name
			}
			if p.Info.Error.Data.Message != "" {
				msg = p.Info.Error.Data.Message
			}
			c.setLastError(p.SessionID, msg)
		}
		switch raw.Type {
		case "message.part.updated":
			if p.Part.Type == "text" {
				ev.Text = p.Part.Text
				ev.PartID = p.Part.ID
			}
			if p.Part.Type == "tool" {
				ev.Tool, ev.ToolStatus, ev.PartID = p.Part.Tool, p.Part.State.Status, p.Part.ID
				ev.Text = ""
			}
		case "message.part.delta":
			if p.Field == "text" {
				ev.Text, ev.PartID, ev.IsDelta = p.Delta, p.PartID, true
			} else {
				continue
			}
		}
		select {
		case out <- ev:
		case <-ctx.Done():
			return
		default:
			// A slow phone must never stall opencode's own stream: drop the
			// event. The final full text is recovered from History below.
		}
	}
}

// lastErrors remembers the error an assistant message carried, per session.
//
// opencode reports provider failures (a refused model, a rate limit, a quota)
// on the message itself rather than as an HTTP status, and it sends no text in
// that case. Without this, a provider error looks exactly like an empty answer.
var lastErrors sync.Map // sessionID -> string

// LastError returns the error opencode reported for a session's last message,
// or "" when the last message was fine.
func (c *Client) LastError(sessionID string) string {
	if v, ok := lastErrors.Load(sessionID); ok {
		return v.(string)
	}
	return ""
}

func (c *Client) setLastError(sessionID, msg string) {
	if sessionID != "" && msg != "" {
		lastErrors.Store(sessionID, msg)
	}
}

func (c *Client) clearLastError(sessionID string) {
	lastErrors.Delete(sessionID)
}

// roles remembers which message id belongs to which role, learned from
// message.updated. A part event carries only a message id, so this is how a
// text part is attributed to the assistant rather than to the user's question
// (which is streamed with an identical shape).
var roles sync.Map // messageID -> "user" | "assistant"

func (c *Client) rememberRole(messageID, role string) {
	if messageID != "" && role != "" {
		roles.Store(messageID, role)
	}
}

func (c *Client) roleOf(messageID string) string {
	if v, ok := roles.Load(messageID); ok {
		return v.(string)
	}
	return ""
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("accept", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 400))
		return fmt.Errorf("opencode %s %s: HTTP %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(snippet)))
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out)
}

// WaitIdle collects the assistant's text for one prompt. It subscribes to the
// event stream, sends the prompt, and returns when the session goes idle (or the
// context ends). onDelta, when non-nil, receives incremental text so the phone
// can show a live typing indicator.
func (c *Client) WaitIdle(ctx context.Context, sessionID, text0, agent, system string, tools map[string]bool, onDelta func(string)) (string, error) {
	// Subscribe first: opencode may start emitting before Prompt returns.
	subCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	events := c.Subscribe(subCtx, sessionID)

	if err := c.Prompt(ctx, sessionID, text0, agent, system, tools); err != nil {
		return "", err
	}

	// A fresh prompt: forget the previous run's error.
	c.clearLastError(sessionID)



	// opencode puts the user's own message on the same event stream as the
	// assistant's, and a user text part is indistinguishable from an assistant
	// one by shape alone. The assistant's message id is learned from the first
	// delta / non-empty text part that arrives after the prompt, and everything
	// belonging to any other message is ignored. Without this the reply came
	// back as the question plus the answer, and the answer's own full-text
	// snapshot was appended a second time.
	parts := map[string]*partText{}
	var order []string

	// onDelta gets the live text so the phone can show a typing indicator.
	deltaForUI := func(delta string) {
		if onDelta != nil {
			onDelta(delta)
		}
	}

	for {
		select {
		case ev, ok := <-events:
			if !ok {
				// Stream ended without an idle signal; fall back to history.
				if e := c.LastError(sessionID); e != "" {
					return "", fmt.Errorf("opencode: %s", e)
				}
				return c.finalText(ctx, sessionID)
			}
			switch ev.Type {
			case "message.updated":
				// The assistant's message id is already known from history, so
				// nothing is inferred here. This case exists only to note the
				// event so the loop reads as a state machine.
			case "message.part.delta", "message.part.updated":
				// The echo of the question is dropped here; see isQuestion.
				if ev.Text == "" || isQuestion(ev, text0) {
					continue
				}
				addPart(parts, &order, ev)
				if ev.IsDelta {
					deltaForUI(ev.Text)
				}
			case "session.idle":
				if e := c.LastError(sessionID); e != "" {
					return "", fmt.Errorf("opencode: %s", e)
				}
				// The idle signal does not always arrive after the text.
				// Observed on opencode 1.18.33: the event order is not
				// guaranteed, and an idle can land between deltas or even
				// before the first one. Returning here would then hand the
				// phone an empty answer.
				//
				// So: if nothing has been collected yet, wait a short grace
				// period for the text parts; if they are already in, take
				// them. Anything that never arrives is recovered from
				// history below.
				got := assemble(parts, order, "")
				if got == "" {
					// An idle can arrive before the text parts. Wait briefly
					// rather than hand the phone an empty answer.
					if got = waitForText(ctx, events, parts, &order, text0, idleGrace); got != "" {
						return got, nil
					}
					if full, err := c.finalText(ctx, sessionID); err == nil && full != "" {
						return full, nil
					}
					return "", nil
				}
				return got, nil
			}
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
}

// idleGrace is how long to keep reading after an idle that carried no text.
// The parts follow the idle within milliseconds in practice; this only has to
// outlast event ordering, not a slow model.
const idleGrace = 2 * time.Second

// isQuestion reports whether a text event is the user's own question coming
// back round the loop.
//
// opencode echoes the prompt onto the same event stream as the answer, and the
// echo is a text part with the same shape as the answer's, so the stream alone
// cannot tell them apart. Everything else about the ordering is unreliable
// either: the echo can arrive before or after the first answer delta, and
// session.idle does not always come last.
//
// The one thing that is always true is that this code sent the question, so it
// knows its exact text. A part whose text is that text is the question. The
// role is used as a second, independent signal when the server has labelled
// the message, and either signal alone is enough.
func isQuestion(ev Event, question string) bool {
	if ev.Role == "user" {
		return true
	}
	if ev.Role == "assistant" {
		return false
	}
	q := strings.TrimSpace(question)
	return q != "" && strings.TrimSpace(ev.Text) == q
}

// partText is one assistant text part, built from the two ways opencode
// streams it: incremental deltas, and a final whole-part snapshot.
type partText struct {
	full string // accumulated from deltas
	snap string // last whole-part snapshot
}

// addPart records a text event into the per-part store, keeping the first-seen
// order so multi-part answers read in the order opencode produced them.
func addPart(parts map[string]*partText, order *[]string, ev Event) {
	if ev.PartID == "" {
		return
	}
	pt := parts[ev.PartID]
	if pt == nil {
		pt = &partText{}
		parts[ev.PartID] = pt
		*order = append(*order, ev.PartID)
	}
	if ev.IsDelta {
		pt.full += ev.Text
	} else if ev.Text != "" {
		pt.snap = ev.Text
	}
}

// waitForText drains events for a short grace period after an early idle,
// returning the assembled answer as soon as any text shows up.
func waitForText(ctx context.Context, events <-chan Event, parts map[string]*partText,
	order *[]string, question string, grace time.Duration) string {
	timer := time.NewTimer(grace)
	defer timer.Stop()
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return assemble(parts, *order, "")
			}
			if ev.Type == "message.updated" {
				continue
			}
			if ev.Type == "message.part.delta" || ev.Type == "message.part.updated" {
				if ev.Text == "" || isQuestion(ev, question) {
					continue
				}
				addPart(parts, order, ev)
				if text := assemble(parts, *order, ""); text != "" {
					return text
				}
			}
			if ev.Type == "session.idle" {
				// A second idle while still nothing: stop waiting.
				if assemble(parts, *order, "") == "" {
					return ""
				}
			}
		case <-timer.C:
			return assemble(parts, *order, "")
		case <-ctx.Done():
			return ""
		}
	}
}

// assemble joins the text parts of one assistant message in the order they
// appeared. For each part a whole-part snapshot wins over the deltas (it is
// opencode's own final word for that part); deltas are the fallback when no
// snapshot arrived.
func assemble(parts map[string]*partText, order []string, _ string) string {
	var b strings.Builder
	for _, id := range order {
		pt := parts[id]
		if pt == nil {
			continue
		}
		switch {
		case pt.snap != "":
			b.WriteString(pt.snap)
		case pt.full != "":
			b.WriteString(pt.full)
		}
	}
	return b.String()
}

// finalText reads the finished assistant text from history, used when the event
// stream did not carry it.
func (c *Client) finalText(ctx context.Context, sessionID string) (string, error) {
	msgs, err := c.History(ctx, sessionID)
	if err != nil {
		return "", err
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "assistant" && strings.TrimSpace(msgs[i].Text) != "" {
			return msgs[i].Text, nil
		}
	}
	return "", nil
}
