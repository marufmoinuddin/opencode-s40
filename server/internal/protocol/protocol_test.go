package protocol

import (
	"strings"
	"testing"
)

func TestParseSimple(t *testing.T) {
	m, err := Parse("OCS40/1\nstatus: ok\nconversation: c_1\n\nhello")
	if err != nil {
		t.Fatal(err)
	}
	if m.Get("status") != "ok" || m.Get("conversation") != "c_1" {
		t.Errorf("bad fields: %+v", m.Fields)
	}
	if m.Text != "hello" {
		t.Errorf("text = %q", m.Text)
	}
}

func TestParseNoFields(t *testing.T) {
	m, err := Parse("OCS40/1\n\nbody only")
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Fields) != 0 || m.Text != "body only" {
		t.Errorf("got %+v / %q", m.Fields, m.Text)
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	// The critical case: an HTML error page from an operator proxy must not be
	// mistaken for a gateway reply.
	for _, bad := range []string{
		"",
		"hello",
		"<html><body>Error 502</body></html>",
		"OCS40/1\nno colon here\n\nx",
		"OCS40/1\nBadKey: v\n\nx", // keys are lower-case ASCII
	} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("Parse(%q) should have failed", bad)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	m := New().Set("status", "ok").Set("more", "1")
	m.Text = "line one\nline two\n\nblank line above"
	back, err := Parse(m.Format())
	if err != nil {
		t.Fatal(err)
	}
	if back.Text != m.Text {
		t.Errorf("text lost in round trip: %q vs %q", back.Text, m.Text)
	}
	if back.Get("more") != "1" {
		t.Error("field lost")
	}
}

func TestHeaderNewlinesCannotBreakFraming(t *testing.T) {
	m := New().Set("detail", "line1\nline2\r\nline3")
	body := m.Format()
	back, err := Parse(body)
	if err != nil {
		t.Fatalf("a newline in a value broke the format: %v", err)
	}
	if strings.Contains(back.Get("detail"), "\n") {
		t.Error("header value still contains a newline")
	}
}

func TestFormatIsDeterministic(t *testing.T) {
	a := New().Set("z", "1").Set("a", "2").Set("m", "3")
	b := New().Set("m", "3").Set("a", "2").Set("z", "1")
	if a.Format() != b.Format() {
		t.Error("field order changed the bytes; responses must be deterministic")
	}
	if !strings.HasPrefix(a.Format(), "OCS40/1\n") {
		t.Error("missing magic line")
	}
}

func TestFlagsAndNilSafety(t *testing.T) {
	var nilMsg *Message
	if nilMsg.Get("x") != "" || nilMsg.Flag("x") || nilMsg.Has("x") {
		t.Error("a nil message must read as empty")
	}
	m := New().Set("flag", "1").Set("other", "0")
	if !m.Flag("flag") {
		t.Error("flag should be true")
	}
	if m.Flag("other") || m.Flag("missing") {
		t.Error("only the exact value 1 is a flag")
	}
	if !m.Has("other") {
		t.Error("Has should see a present field")
	}
}

func TestCRLFLineEndings(t *testing.T) {
	m, err := Parse("OCS40/1\r\nstatus: ok\r\n\r\nbody")
	if err != nil {
		t.Fatal(err)
	}
	if m.Get("status") != "ok" || m.Text != "body" {
		t.Errorf("CRLF body mis-parsed: %+v / %q", m.Fields, m.Text)
	}
}

// TestUTF8Survives is the Turkish test string from the reference project: if
// this round trip breaks, the connection test on the phone will fail.
func TestUTF8Survives(t *testing.T) {
	const probe = "Şişe ğüöç ığİ iü"
	m := New().Set("probe", "match")
	m.Text = probe
	back, err := Parse(m.Format())
	if err != nil {
		t.Fatal(err)
	}
	if back.Text != probe {
		t.Errorf("UTF-8 mangled: %q", back.Text)
	}
	if back.Get("probe") != "match" {
		t.Error("probe flag lost")
	}
}
