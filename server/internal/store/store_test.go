package store

import (
	"path/filepath"
	"strings"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "t.db"), 30)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestDeviceLifecycle(t *testing.T) {
	s := newTestStore(t)
	if err := s.AddDevice("dev1", "tok1", "My Nokia"); err != nil {
		t.Fatal(err)
	}
	d, err := s.DeviceByToken("tok1")
	if err != nil || d.ID != "dev1" || d.Name != "My Nokia" {
		t.Fatalf("got %+v, %v", d, err)
	}
	if _, err := s.DeviceByToken("nope"); err == nil {
		t.Error("a wrong token must not authenticate")
	}
	if err := s.RevokeDevice("dev1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeviceByToken("tok1"); err == nil {
		t.Error("a revoked token must stop working")
	}
	// Revoking again is a no-op, not a failure: the admin script may retry.
	if err := s.RevokeDevice("dev1"); err != nil {
		t.Errorf("revoking an already-revoked device should succeed, got %v", err)
	}
	if err := s.RevokeDevice("nosuch"); err == nil {
		t.Error("revoking a device that never existed should fail")
	}
}

func TestDailyUsageResetsPerDay(t *testing.T) {
	s := newTestStore(t)
	_ = s.AddDevice("dev1", "tok1", "n")
	for i := 1; i <= 3; i++ {
		n, err := s.CountUsage("dev1", "2026-01-01")
		if err != nil || n != i {
			t.Fatalf("day1 count %d, %v", n, err)
		}
	}
	n, err := s.CountUsage("dev1", "2026-01-02")
	if err != nil || n != 1 {
		t.Fatalf("a new day must reset the counter, got %d (%v)", n, err)
	}
}

func TestConversationAndHistory(t *testing.T) {
	s := newTestStore(t)
	conv, err := s.NewConversation("dev1", "ses_abc", "")
	if err != nil {
		t.Fatal(err)
	}
	if !s.OwnsConversation("dev1", conv) {
		t.Error("owner check failed")
	}
	if s.OwnsConversation("dev2", conv) {
		t.Error("another device must not own this conversation")
	}
	_ = s.AddMessage(conv, "user", "Merhaba, nasilsin?")
	_ = s.AddMessage(conv, "assistant", "Iyi, teşekkürler.")

	list, err := s.ListConversations("dev1", 10)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %v %v", list, err)
	}
	if !strings.HasPrefix(list[0].Title, "Merhaba") {
		t.Errorf("title should come from the first user message, got %q", list[0].Title)
	}
	if list[0].Messages != 2 {
		t.Errorf("message count = %d, want 2", list[0].Messages)
	}
	msgs, more, err := s.History(conv, 6000)
	if err != nil {
		t.Fatal(err)
	}
	if more {
		t.Error("nothing should be left out at 6000 bytes")
	}
	if len(msgs) != 2 || msgs[0].Role != "user" || msgs[1].Role != "assistant" {
		t.Errorf("history must be oldest first: %+v", msgs)
	}

	// A small byte budget must trim and flag it.
	msgs, more, err = s.History(conv, 20)
	if err != nil {
		t.Fatal(err)
	}
	if !more {
		t.Error("hasMore should be set when messages are left out")
	}
	if len(msgs) == 0 {
		t.Error("at least the newest message must fit")
	}
}

func TestHistoryKeepsTheNewestWindow(t *testing.T) {
	s := newTestStore(t)
	conv, _ := s.NewConversation("dev1", "ses", "")
	for i := 0; i < 10; i++ {
		_ = s.AddMessage(conv, "user", strings.Repeat("x", 100))
	}
	msgs, more, err := s.History(conv, 250)
	if err != nil {
		t.Fatal(err)
	}
	if !more {
		t.Error("expected older messages to be left out")
	}
	if len(msgs) > 3 {
		t.Errorf("budget should keep only a couple of messages, got %d", len(msgs))
	}
}

func TestPinAndDelete(t *testing.T) {
	s := newTestStore(t)
	c1, _ := s.NewConversation("dev1", "s1", "one")
	c2, _ := s.NewConversation("dev1", "s2", "two")
	_ = s.AddMessage(c2, "user", "newer")

	if err := s.Pin(c1, true); err != nil {
		t.Fatal(err)
	}
	list, _ := s.ListConversations("dev1", 10)
	if len(list) != 2 || list[0].ID != c1 {
		t.Errorf("pinned conversation must come first, got %+v", list)
	}
	if err := s.Pin("nope", true); err == nil {
		t.Error("pinning a missing conversation should fail")
	}
	if err := s.DeleteConversation(c1); err != nil {
		t.Fatal(err)
	}
	list, _ = s.ListConversations("dev1", 10)
	if len(list) != 1 {
		t.Errorf("conversation was not deleted: %+v", list)
	}
}

// TestSearchTurkishInsensitive is the behaviour the reference project calls out
// by name: "sise" must find "şişe".
func TestSearchTurkishInsensitive(t *testing.T) {
	s := newTestStore(t)
	conv, _ := s.NewConversation("dev1", "s1", "sise")
	_ = s.AddMessage(conv, "user", "Şişe nerede?")
	_ = s.AddMessage(conv, "assistant", "Dolabın üstünde.")

	// typed without Turkish letters
	res, err := s.Search("dev1", "sise", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].ID != conv {
		t.Errorf(`"sise" must find "Şişe", got %+v`, res)
	}
	res, err = s.Search("dev1", "dolab", 10)
	if err != nil || len(res) != 1 {
		t.Errorf(`"dolab" must find "Dolabın", got %+v (%v)`, res, err)
	}
	// every word must match
	if res, _ := s.Search("dev1", "sise bilmem", 10); len(res) != 0 {
		t.Errorf("a missing word must exclude the conversation, got %+v", res)
	}
	// other devices see nothing
	if res, _ := s.Search("dev2", "sise", 10); len(res) != 0 {
		t.Error("search must be scoped to the device")
	}
}

func TestCleanupKeepsPinned(t *testing.T) {
	s := newTestStore(t)
	old, _ := s.NewConversation("dev1", "s1", "old")
	kept, _ := s.NewConversation("dev1", "s2", "pinned")
	_ = s.AddMessage(old, "user", "x")
	_ = s.AddMessage(kept, "user", "x")
	_ = s.Pin(kept, true)

	// Push both far into the past.
	old1 := ms() - 60*24*3600*1000
	_, _ = s.db.Exec(`UPDATE conversations SET updated = ?`, old1)

	n, err := s.Cleanup()
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("cleanup removed %d, want 1", n)
	}
	if !s.OwnsConversation("dev1", kept) {
		t.Error("a pinned conversation must survive cleanup")
	}
	if s.OwnsConversation("dev1", old) {
		t.Error("an old unpinned conversation must be gone")
	}
}

// TestForeignKeysEnabled guards a silent failure mode: modernc's SQLite ignores
// the foreign_keys pragma in the DSN, and with it off ON DELETE CASCADE never
// runs, leaving orphaned messages behind when a conversation is deleted.
func TestForeignKeysEnabled(t *testing.T) {
	s := newTestStore(t)
	on, err := ForeignKeysOn(s)
	if err != nil {
		t.Fatal(err)
	}
	if !on {
		t.Fatal("foreign keys are off: cascade deletes would not fire")
	}
}

// TestDeleteRemovesMessages proves the cascade is really working, which the
// pragma test above only asserts indirectly.
func TestDeleteRemovesMessages(t *testing.T) {
	s := newTestStore(t)
	conv, _ := s.NewConversation("dev1", "s1", "t")
	_ = s.AddMessage(conv, "user", "hello")
	_ = s.AddMessage(conv, "assistant", "hi")
	if err := s.DeleteConversation(conv); err != nil {
		t.Fatal(err)
	}
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM messages WHERE conv = ?`, conv).Scan(&n)
	if n != 0 {
		t.Errorf("%d orphaned messages left after deleting the conversation", n)
	}
}
