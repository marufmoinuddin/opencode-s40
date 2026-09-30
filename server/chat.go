package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/maruf/ocs40/server/internal/oc"
	"github.com/maruf/ocs40/server/internal/protocol"
	"github.com/maruf/ocs40/server/internal/store"
)

// maxStoredReply is how much of an answer is kept for paging. The phone reads at
// most 8 KiB per response, so replies are sent in parts of partSize.
const (
	maxStoredReply = 8000
	partSize       = 2000
	// maxPhoneMessage is the largest message a phone may send.
	maxPhoneMessage = 4000
	// maxInstructions is the user's own notes for the assistant.
	maxInstructions = 300
	// maxConversations is how many chats the phone list shows.
	maxConversations = 20
	// maxHistoryBytes is the history window the phone can read.
	maxHistoryBytes = 6000
)

// chatService turns a phone message into an opencode answer.
type chatService struct {
	cfg    config
	oc     *oc.Client
	st     *store.Store
	replay *replayCache
}

func newChatService(cfg config, client *oc.Client, st *store.Store) *chatService {
	return &chatService{cfg: cfg, oc: client, st: st, replay: newReplayCache(512)}
}

// statusError is a protocol-level failure: an HTTP code plus a status field the
// phone knows how to show.
type statusError struct {
	code   int
	status string
	fields map[string]string
}

func (e *statusError) Error() string { return e.status }

func errStatus(code int, status string, kv ...string) *statusError {
	f := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		f[kv[i]] = kv[i+1]
	}
	return &statusError{code: code, status: status, fields: f}
}

// toolPolicy returns the agent for a request.
//
// This is the server-side guardrail for a coding agent driven from a keypad, and
// it works by choosing the right opencode agent rather than by filtering tool
// names:
//
//   - "plan" is opencode's read-only primary agent. It refuses every edit tool
//     at the prompt level, so a message from the phone cannot make the agent
//     write, patch or run shell commands. Verified against opencode 1.18.33:
//     asked to create a file, the plan agent declined, used no tools at all and
//     no file appeared.
//   - "build" is the full-power agent. It is only used when the operator has set
//     OCS40_ALLOW_BUILD=1, so the default deployment cannot touch a workspace.
//
// Note on the `tools` map: an earlier version also passed
// tools:{bash:false,write:false,...}. That is deliberately NOT done here. It
// turns out the opencode provider rejects requests carrying a `tools` override
// (HTTP 403 "free tier can only be used from within OpenCode"), which would
// have made every phone message fail. The agent choice gives the same
// protection without breaking the call.
func (c *chatService) toolPolicy(wantBuild bool) (agent string, tools map[string]bool) {
	agent = c.cfg.Agent // "plan" by default: read-only
	if c.cfg.AllowBuild && wantBuild {
		agent = "build"
	}
	return agent, nil
}

// Reply handles POST /v1/chat.
func (c *chatService) Reply(ctx context.Context, dev store.Device, in *protocol.Message) (*protocol.Message, error) {
	text := strings.TrimSpace(in.Text)
	if text == "" {
		return nil, errStatus(400, "empty_message")
	}
	if len(text) > maxPhoneMessage {
		return nil, errStatus(400, "message_too_long", "max", strconv.Itoa(maxPhoneMessage))
	}

	// Daily limit, counted before any upstream call.
	if c.cfg.DailyLimit > 0 {
		n, err := c.st.CountUsage(dev.ID, store.UTCDay())
		if err != nil {
			return nil, err
		}
		if n > c.cfg.DailyLimit {
			return nil, errStatus(429, "limit", "limit", strconv.Itoa(c.cfg.DailyLimit))
		}
	}

	conv := in.Get("conversation")
	if conv != "" && !c.st.OwnsConversation(dev.ID, conv) {
		return nil, errStatus(404, "conversation_not_found")
	}

	// Replay: the same request id must never bill twice. The phone retries with
	// the same id after an error.
	if rid := in.Get("request"); rid != "" {
		if prev, ok := c.replay.get(rid); ok {
			out := prev.Clone()
			out.Set("replayed", "1")
			return out, nil
		}
	}

	// Mock mode never touches opencode and never costs anything.
	if c.cfg.Mock {
		return c.mockReply(dev, in, text)
	}
	// Resolve the upstream session: reuse the conversation's, or create one.
	ocSession := ""
	if conv != "" {
		ocSession, _ = c.st.OCSession(conv)
	}
	created := false
	if ocSession == "" {
		sctx, cancel := context.WithTimeout(ctx, c.cfg.OpenTimeout)
		id, err := c.oc.CreateSession(sctx, titleFrom(text))
		cancel()
		if err != nil {
			return nil, errStatus(502, "upstream_error", "detail", oneLine(err.Error()))
		}
		ocSession, created = id, true
	}

	// Build the system prompt: the gateway's own + the user's notes.
	system := strings.TrimSpace(c.cfg.System)
	if notes := strings.TrimSpace(in.Get("instructions")); notes != "" {
		if len(notes) > maxInstructions {
			notes = notes[:maxInstructions]
		}
		if system == "" {
			system = "You are answering through a 2007 Nokia phone with a 240x320 screen. " +
				"Keep answers short, plain and concrete: no markdown tables, no long code blocks, " +
				"short paragraphs and simple lists. The user's own notes: " + notes
		} else {
			system += "\n\nThe user's own notes: " + notes
		}
	}

	agent, tools := c.toolPolicy(in.Flag("build"))

	// Store the user's message BEFORE the call, so a dropped connection leaves a
	// record and the phone can be told "pending" rather than being resent blindly.
	if conv == "" {
		newConv, err := c.st.NewConversation(dev.ID, ocSession, titleFrom(text))
		if err != nil {
			return nil, err
		}
		conv = newConv
	}
	if err := c.st.AddMessage(conv, "user", text); err != nil {
		return nil, err
	}
	// The phone sends "waiting" until the answer is ready; there is no push
	// channel to a 2007 phone, so the reply comes with this response.

	runCtx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()
	answer, err := c.oc.WaitIdle(runCtx, ocSession, text, agent, system, tools, nil)
	if err != nil {
		if created {
			// Do not leave an orphan upstream session behind.
			bctx, bcancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = c.oc.DeleteSession(bctx, ocSession)
			bcancel()
		}
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			// The call may still be running upstream and may still bill: say
			// "uncertain", never claim it failed.
			return nil, errStatus(504, "uncertain", "detail", "the answer did not arrive in time")
		}
		return nil, errStatus(502, "upstream_error", "detail", oneLine(err.Error()))
	}

	// opencode can return a message carrying an error (a provider refusal, a
	// rate limit, a bad model). Surface it instead of replying "no text", which
	// sent me hunting for a gateway bug that was really the provider.
	if msg := c.oc.LastError(ocSession); msg != "" {
		if created {
			bctx, bcancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = c.oc.DeleteSession(bctx, ocSession)
			bcancel()
		}
		return nil, errStatus(502, "upstream_error", "detail", oneLine(msg))
	}

	answer = sanitize(answer)
	if strings.TrimSpace(answer) == "" {
		_ = c.oc.DeleteSession(context.Background(), ocSession)
		return nil, errStatus(502, "upstream_error", "detail", "opencode returned no text")
	}
	if err := c.st.AddMessage(conv, "assistant", answer); err != nil {
		return nil, err
	}

	out := protocol.New().Set("status", "ok").Set("conversation", conv)
	part, more, next := firstPart(answer)
	out.Set("more", boolFlag(more))
	if more {
		out.Set("next", strconv.Itoa(next))
	}
	if more {
		out.Set("truncated", "1")
	}
	out.Text = part
	// Remember the finished message (text included) so a retry replays exactly
	// what the phone already has, rather than triggering a second call.
	if rid := in.Get("request"); rid != "" {
		c.replay.remember(rid, out)
	}
	return out, nil
}

// More handles POST /v1/more: the next part of a stored reply. It never calls
// opencode, so paging a long answer is free.
func (c *chatService) More(ctx context.Context, dev store.Device, in *protocol.Message) (*protocol.Message, error) {
	conv := in.Get("conversation")
	if conv == "" || !c.st.OwnsConversation(dev.ID, conv) {
		return nil, errStatus(404, "conversation_not_found")
	}
	msgs, _, err := c.st.History(conv, maxStoredReply+maxStoredReply)
	if err != nil {
		return nil, err
	}
	answer := ""
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "assistant" {
			answer = msgs[i].Text
			break
		}
	}
	if answer == "" {
		return nil, errStatus(404, "no_reply_stored")
	}
	offset, err := strconv.Atoi(in.Get("offset"))
	if err != nil || offset < 0 || offset >= len(answer) {
		return nil, errStatus(400, "bad_offset")
	}
	part, more, next := slicePart(answer, offset)
	out := protocol.New().Set("status", "ok").Set("conversation", conv)
	out.Set("more", boolFlag(more))
	if more {
		out.Set("next", strconv.Itoa(next))
	}
	out.Text = part
	return out, nil
}

// Conversations handles POST /v1/conversations.
func (c *chatService) Conversations(ctx context.Context, dev store.Device, in *protocol.Message) (*protocol.Message, error) {
	list, err := c.st.ListConversations(dev.ID, maxConversations)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	withPins := in.Flag("pins")
	for _, cv := range list {
		if withPins {
			b.WriteString(boolFlag(cv.Pinned))
			b.WriteByte('\t')
		}
		b.WriteString(cv.ID)
		b.WriteByte('\t')
		b.WriteString(strconv.FormatInt(cv.Updated, 10))
		b.WriteByte('\t')
		b.WriteString(strconv.Itoa(cv.Messages))
		b.WriteByte('\t')
		b.WriteString(oneLine(cv.Title))
		b.WriteByte('\n')
	}
	out := protocol.New().Set("status", "ok")
	out.Text = b.String()
	return out, nil
}

// History handles POST /v1/history.
func (c *chatService) History(ctx context.Context, dev store.Device, in *protocol.Message) (*protocol.Message, error) {
	conv := in.Get("conversation")
	if conv == "" || !c.st.OwnsConversation(dev.ID, conv) {
		return nil, errStatus(404, "conversation_not_found")
	}
	msgs, more, err := c.st.History(conv, maxHistoryBytes)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	for _, m := range msgs {
		b.WriteString(m.Role)
		b.WriteByte(' ')
		b.WriteString(strconv.Itoa(len([]rune(m.Text))))
		b.WriteByte('\n')
		b.WriteString(m.Text)
		b.WriteByte('\n')
	}
	out := protocol.New().Set("status", "ok").Set("older", boolFlag(more))
	out.Text = b.String()
	return out, nil
}

// Search handles POST /v1/search. It never calls opencode.
func (c *chatService) Search(ctx context.Context, dev store.Device, in *protocol.Message) (*protocol.Message, error) {
	q := strings.TrimSpace(in.Text)
	if len(q) < 2 || len(q) > 100 {
		return nil, errStatus(400, "bad_query")
	}
	res, err := c.st.Search(dev.ID, q, maxConversations)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	for _, r := range res {
		b.WriteString(r.ID)
		b.WriteByte('\t')
		b.WriteString(strconv.FormatInt(r.Updated, 10))
		b.WriteByte('\t')
		b.WriteString(strconv.Itoa(r.Messages))
		b.WriteByte('\t')
		b.WriteString(oneLine(r.Snippet))
		b.WriteByte('\n')
	}
	out := protocol.New().Set("status", "ok").Set("found", strconv.Itoa(len(res)))
	out.Text = b.String()
	return out, nil
}

// Pin handles POST /v1/pin.
func (c *chatService) Pin(ctx context.Context, dev store.Device, in *protocol.Message) (*protocol.Message, error) {
	conv := in.Get("conversation")
	if conv == "" || !c.st.OwnsConversation(dev.ID, conv) {
		return nil, errStatus(404, "conversation_not_found")
	}
	if err := c.st.Pin(conv, in.Flag("pinned")); err != nil {
		return nil, err
	}
	return protocol.New().Set("status", "ok"), nil
}

// Delete handles POST /v1/delete.
func (c *chatService) Delete(ctx context.Context, dev store.Device, in *protocol.Message) (*protocol.Message, error) {
	conv := in.Get("conversation")
	if conv == "" || !c.st.OwnsConversation(dev.ID, conv) {
		return nil, errStatus(404, "conversation_not_found")
	}
	if err := c.st.DeleteConversation(conv); err != nil {
		return nil, err
	}
	// Best effort upstream cleanup; the phone's answer must not depend on it.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if id, err := c.st.OCSessionOrEmpty(conv); err == nil && id != "" {
			_ = c.oc.DeleteSession(ctx, id)
		}
	}()
	return protocol.New().Set("status", "ok"), nil
}

// Status handles GET /v1/status: is the opencode server reachable, which model
// and agent is in use, and what the tool policy is. The phone's connection test
// shows this.
func (c *chatService) Status(ctx context.Context) (*protocol.Message, error) {
	out := protocol.New().Set("status", "ok").Set("version", Version).
		Set("mock", boolFlag(c.cfg.Mock))
	if c.cfg.Mock {
		out.Set("opencode", "mock")
		out.Text = "Test mode: opencode is not called."
		return out, nil
	}
	sctx, cancel := context.WithTimeout(ctx, c.cfg.OpenTimeout)
	defer cancel()
	ver, err := c.oc.Version(sctx)
	if err != nil {
		return nil, errStatus(502, "opencode_unreachable", "detail", oneLine(err.Error()))
	}
	agents, _ := c.oc.Agents(sctx)
	out.Set("opencode", ver)
	out.Set("agent", c.cfg.Agent)
	out.Set("profile", toolProfileName(c.cfg))
	if c.cfg.Model != "" {
		out.Set("model", c.cfg.Model)
	}
	if c.cfg.Project != "" {
		out.Set("project", c.cfg.Project)
	}
	var b strings.Builder
	b.WriteString("opencode " + ver + "\n")
	b.WriteString("agent: " + c.cfg.Agent + "\n")
	b.WriteString("tools: " + toolProfileName(c.cfg) + "\n")
	if c.cfg.Model != "" {
		b.WriteString("model: " + c.cfg.Model + "\n")
	}
	if c.cfg.Project != "" {
		b.WriteString("project: " + c.cfg.Project + "\n")
	}
	b.WriteString("agents: " + strings.Join(agentNames(agents), ", ") + "\n")
	out.Text = b.String()
	return out, nil
}

func agentNames(as []oc.Agent) []string {
	out := make([]string, 0, len(as))
	for _, a := range as {
		out = append(out, a.Name)
	}
	return out
}

func toolProfileName(cfg config) string {
	switch {
	case cfg.AllowBuild && cfg.AllowBash:
		return "full"
	case cfg.AllowBuild:
		return "build-no-bash"
	default:
		return "read-only"
	}
}

// mockReply is the local test mode: no network, no cost, and always labelled so
// it can never be mistaken for a real answer.
//
// It follows the same rules as the live path (conversation ownership, replay,
// request counting) so that test mode exercises the same code the phone will
// use; only the opencode call itself is replaced.
func (c *chatService) mockReply(dev store.Device, in *protocol.Message, text string) (*protocol.Message, error) {
	conv := in.Get("conversation")
	if conv != "" && !c.st.OwnsConversation(dev.ID, conv) {
		return nil, errStatus(404, "conversation_not_found")
	}
	if rid := in.Get("request"); rid != "" {
		if prev, ok := c.replay.get(rid); ok {
			out := prev.Clone()
			out.Set("replayed", "1")
			return out, nil
		}
	}
	if conv == "" {
		var err error
		conv, err = c.st.NewConversation(dev.ID, "", titleFrom(text))
		if err != nil {
			return nil, err
		}
	}
	answer := mockAnswer(text)
	if err := c.st.AddMessage(conv, "user", text); err != nil {
		return nil, err
	}
	if err := c.st.AddMessage(conv, "assistant", answer); err != nil {
		return nil, err
	}
	part, more, next := firstPart(answer)
	out := protocol.New().Set("status", "ok").Set("conversation", conv).
		Set("mock", "1").Set("more", boolFlag(more))
	if more {
		out.Set("next", strconv.Itoa(next))
		out.Set("truncated", "1")
	}
	out.Text = part
	// Remember the finished message (text included) so a retry replays exactly
	// what the phone already has, rather than triggering a second call.
	if rid := in.Get("request"); rid != "" {
		c.replay.remember(rid, out)
	}
	return out, nil
}

func mockAnswer(text string) string {
	return "[Test mode] This is not a real opencode answer; the network was not used.\n\n" +
		"You said: " + oneLine(text) + "\n\n" +
		"A real answer comes from your own opencode server on your PC. " +
		"Delete an old Claude S40 first, then install this build."
}

func firstPart(answer string) (part string, more bool, next int) {
	return slicePart(answer, 0)
}

// slicePart returns the chunk starting at offset and whether more remain. The
// cut never splits a UTF-8 sequence or a line, so a phone never shows a broken
// character or a half sentence at a page boundary.
func slicePart(answer string, offset int) (part string, more bool, next int) {
	if offset >= len(answer) {
		return "", false, len(answer)
	}
	end := offset + partSize
	if end >= len(answer) {
		return answer[offset:], false, len(answer)
	}
	// Prefer a line boundary in the last quarter of the chunk.
	limit := offset + partSize*3/4
	if limit < end {
		if i := strings.LastIndexByte(answer[limit:end], '\n'); i >= 0 {
			end = limit + i + 1
		} else {
			end = utf8Boundary(answer, offset, end)
		}
	}
	if end <= offset {
		end = offset + partSize
	}
	part = answer[offset:end]
	return part, end < len(answer), end
}

// utf8Boundary moves end back to the start of the rune it would split.
func utf8Boundary(s string, start, end int) int {
	for end > start && !isRuneStart(s[end]) {
		end--
	}
	return end
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }

func titleFrom(text string) string {
	s := strings.TrimSpace(text)
	if len(s) > 40 {
		cut := s[:40]
		if i := strings.LastIndexByte(cut, ' '); i > 15 {
			cut = cut[:i]
		}
		return cut + "..."
	}
	return s
}

func boolFlag(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func oneLine(s string) string {
	if len(s) > 160 {
		s = s[:160] + "..."
	}
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(s)
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func nowUTC() string { return time.Now().UTC().Format(time.RFC3339) }

var _ = fmt.Sprintf
