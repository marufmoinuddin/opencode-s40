package oc

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// TestLiveReplyIsNotDuplicated is the regression test for a real bug.
//
// opencode puts the user's own question on the same SSE stream as the answer,
// and the answer arrives twice over: once as incremental
// `message.part.delta` events and once as a whole-part
// `message.part.updated` snapshot. The first implementation appended both and
// ignored the user's part, so the phone received:
//
//	"Reply with exactly: MARKER" + "MARKER"
//
// i.e. the question glued to a doubled answer. This asserts the reply is
// exactly the answer, appearing once, with no trace of the question.
func TestLiveReplyIsNotDuplicated(t *testing.T) {
	c := New(liveBase(t))
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	const question = "Reply with exactly: UNIQ_MARKER_42 and nothing else."
	const marker = "UNIQ_MARKER_42"

	sid, err := c.CreateSession(ctx, "ocs40 duplicate check")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.DeleteSession(context.Background(), sid) }()

	var streamed strings.Builder
	got, err := c.WaitIdle(ctx, sid, question, "plan", "", nil,
		func(s string) { streamed.WriteString(s) })
	if err != nil {
		t.Fatalf("WaitIdle: %v", err)
	}

	if n := strings.Count(got, marker); n != 1 {
		t.Errorf("the answer appears %d times, want exactly 1: %q", n, got)
	}
	if strings.Contains(got, question) {
		t.Errorf("the reply contains the user's own question: %q", got)
	}
	if !strings.Contains(got, marker) {
		t.Errorf("the reply does not contain the marker at all: %q", got)
	}
	if streamed.Len() == 0 {
		t.Error("no deltas were delivered, so the phone would show no progress")
	}
	t.Logf("reply %q (streamed %d bytes)", got, streamed.Len())
}

// TestAssemblePrefersSnapshot pins the assembly rule on its own, with no
// network: a whole-part snapshot is opencode's final word for that part and
// must win over the deltas that built it up.
func TestAssemblePrefersSnapshot(t *testing.T) {
	parts := map[string]*partText{
		"p1": {full: "Hello", snap: "Hello, world"},
		"p2": {full: "Second part."},
		"p3": {full: "unseen", snap: ""},
	}
	order := []string{"p1", "p2", "p3"}
	if got := assemble(parts, order, ""); got != "Hello, worldSecond part.unseen" {
		t.Errorf("assemble = %q", got)
	}
}

// TestAssembleIgnoresEmptySnapshot: the opening empty snapshot for a part must
// not wipe the deltas that follow it.
func TestAssembleIgnoresEmptySnapshot(t *testing.T) {
	parts := map[string]*partText{"p1": {full: "text", snap: ""}}
	if got := assemble(parts, []string{"p1"}, ""); got != "text" {
		t.Errorf("assemble = %q, want the deltas", got)
	}
}

// TestLastErrorIsPerSession guards the error cache: a fresh prompt must clear
// the previous run's error, and one session's error must not leak into another.
func TestLastErrorIsPerSession(t *testing.T) {
	c := New("http://127.0.0.1:1") // never connected
	c.setLastError("ses_a", "provider said no")
	if got := c.LastError("ses_a"); got != "provider said no" {
		t.Errorf("LastError(ses_a) = %q", got)
	}
	if got := c.LastError("ses_b"); got != "" {
		t.Errorf("an error leaked into another session: %q", got)
	}
	c.clearLastError("ses_a")
	if got := c.LastError("ses_a"); got != "" {
		t.Errorf("clearLastError did not clear: %q", got)
	}
}

// TestSubscribeIsSharedPerSession: two callers watching one session get the
// same channel, so opencode's stream is not opened twice.
func TestSubscribeIsSharedPerSession(t *testing.T) {
	c := New("http://127.0.0.1:1")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := c.Subscribe(ctx, "ses_x")
	b := c.Subscribe(ctx, "ses_x")
	if a != b {
		t.Error("Subscribe returned different channels for the same session")
	}
}

// TestHistoryIgnoresNonTextParts: tool and step parts must not leak into the
// stored text of a message.
func TestHistoryIgnoresNonTextParts(t *testing.T) {
	c := New(liveBase(t))
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	sid, err := c.CreateSession(ctx, "ocs40 history shape")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.DeleteSession(context.Background(), sid) }()

	if _, err := c.WaitIdle(ctx, sid, "Say exactly: HIST_SHAPE_OK", "plan", "", nil, nil); err != nil {
		t.Fatal(err)
	}
	msgs, err := c.History(ctx, sid)
	if err != nil {
		t.Fatal(err)
	}
	var user, assistant string
	for _, m := range msgs {
		switch m.Role {
		case "user":
			user = m.Text
		case "assistant":
			assistant = m.Text
		}
	}
	if !strings.Contains(assistant, "HIST_SHAPE_OK") {
		t.Errorf("assistant text %q lost the marker", assistant)
	}
	if n := strings.Count(assistant, "HIST_SHAPE_OK"); n != 1 {
		t.Errorf("history stored the answer %d times: %q", n, assistant)
	}
	if strings.Contains(assistant, "Say exactly:") {
		t.Errorf("the user's question leaked into the assistant text: %q", assistant)
	}
	_ = user
	_ = os.Getenv
}
