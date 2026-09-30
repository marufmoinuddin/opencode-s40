package oc

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// liveBase returns the base URL of a live opencode server, or skips the test.
// Set OCS40_LIVE_OPENCODE=http://127.0.0.1:4096 to run these against a real
// `opencode serve` (the default port the docs use).
func liveBase(t *testing.T) string {
	t.Helper()
	base := os.Getenv("OCS40_LIVE_OPENCODE")
	if base == "" {
		base = "http://127.0.0.1:7777"
	}
	c := New(base)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.Version(ctx); err != nil {
		t.Skipf("no live opencode server at %s (%v); set OCS40_LIVE_OPENCODE", base, err)
	}
	return base
}

func TestLiveVersion(t *testing.T) {
	c := New(liveBase(t))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	v, err := c.Version(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if v == "" {
		t.Fatal("empty version")
	}
	t.Logf("opencode version %s", v)
}

func TestLiveAgents(t *testing.T) {
	c := New(liveBase(t))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	agents, err := c.Agents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(agents) == 0 {
		t.Fatal("no agents")
	}
	names := make([]string, 0, len(agents))
	for _, a := range agents {
		names = append(names, a.Name)
	}
	t.Logf("agents: %s", strings.Join(names, ", "))
	for _, want := range []string{"build", "plan"} {
		found := false
		for _, n := range names {
			if n == want {
				found = true
			}
		}
		if !found {
			t.Errorf("expected agent %q in %v", want, names)
		}
	}
}

// TestLiveChat is the end-to-end proof: a real prompt through the real event
// stream returns the real answer, with the tool policy honoured.
func TestLiveChat(t *testing.T) {
	c := New(liveBase(t))
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	sid, err := c.CreateSession(ctx, "ocs40 test")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("session %s", sid)
	defer func() {
		_ = c.DeleteSession(context.Background(), sid)
	}()

	var deltas strings.Builder
	text, err := c.WaitIdle(ctx, sid,
		"Reply with exactly these two words and nothing else: GATEWAY_OK",
		"plan", "", nil,
		func(s string) { deltas.WriteString(s) })
	if err != nil {
		t.Fatalf("WaitIdle: %v", err)
	}
	t.Logf("reply: %q (deltas: %d bytes)", text, deltas.Len())
	if !strings.Contains(text, "GATEWAY_OK") {
		t.Errorf("reply does not contain the marker: %q", text)
	}
	if deltas.Len() == 0 {
		t.Error("no streaming deltas were delivered; the phone would show no progress")
	}
}

// TestLiveHistory checks that a finished conversation is readable back, which
// is how the phone recovers text if the event stream drops.
func TestLiveHistory(t *testing.T) {
	c := New(liveBase(t))
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	sid, err := c.CreateSession(ctx, "ocs40 history test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.DeleteSession(context.Background(), sid) }()

	if _, err := c.WaitIdle(ctx, sid, "Reply with exactly: HIST_OK", "plan", "", nil, nil); err != nil {
		t.Fatal(err)
	}
	msgs, err := c.History(ctx, sid)
	if err != nil {
		t.Fatal(err)
	}
	var user, assistant bool
	for _, m := range msgs {
		switch m.Role {
		case "user":
			user = true
		case "assistant":
			if strings.Contains(m.Text, "HIST_OK") {
				assistant = true
			}
		}
	}
	if !user {
		t.Error("history has no user message")
	}
	if !assistant {
		t.Errorf("history has no assistant message with the marker: %+v", msgs)
	}
}

// TestLiveSystemPrompt proves the gateway can pin a system prompt (used for the
// user's own notes for the assistant).
func TestLiveSystemPrompt(t *testing.T) {
	c := New(liveBase(t))
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	sid, err := c.CreateSession(ctx, "ocs40 system test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.DeleteSession(context.Background(), sid) }()

	text, err := c.WaitIdle(ctx, sid,
		"What is the capital of France? One word answer only.",
		"plan", "You are being used through a 2007 Nokia phone. Always answer in at most 20 words.", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(text), "paris") {
		t.Errorf("unexpected answer: %q", text)
	}
}
