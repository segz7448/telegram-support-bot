package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Public-use isolation: one chat can't read, change or delete another chat's notes or reminders,
// and owner-only commands refuse everyone else (also when no owner is configured).
func TestCrossChatIsolation(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	n := &Notes{Path: filepath.Join(dir, "n.json"), Now: func() time.Time { return now }}
	r := &Reminders{Path: filepath.Join(dir, "r.json"), Now: func() time.Time { return now }}
	if rep, _ := n.Handle("A", "note: zebra-alpha plan"); !strings.Contains(strings.ToLower(rep), "saved") && !strings.Contains(rep, "1") {
		t.Fatalf("add: %q", rep)
	}
	if rep, _ := r.Handle("A", "remind me in 30 minutes to call zebra-alpha"); rep == "" {
		t.Fatal("rem add")
	}
	for _, q := range []string{"notes", "my tasks", "tags", "find notes zebra", "#zebra"} {
		if rep, _ := n.Handle("B", q); strings.Contains(rep, "zebra-alpha") {
			t.Fatalf("B read A's note via %q: %q", q, rep)
		}
	}
	if rep, _ := r.Handle("B", "my reminders"); strings.Contains(rep, "zebra-alpha") {
		t.Fatalf("B read A's reminder: %q", rep)
	}
	n.Handle("B", "delete note 1")
	r.Handle("B", "cancel reminder 1")
	if rep, _ := n.Handle("A", "notes"); !strings.Contains(rep, "zebra-alpha") {
		t.Fatalf("B deleted A's note: %q", rep)
	}
	if rep, _ := r.Handle("A", "my reminders"); !strings.Contains(rep, "zebra-alpha") {
		t.Fatalf("B cancelled A's reminder: %q", rep)
	}
	// the owner-only admin commands
	know := &Knowledge{Path: filepath.Join(dir, "k.json"), Owner: "42"}
	a := &Admin{Owner: "42", Know: know, Notes: n, Rem: r, Start: now, Now: func() time.Time { return now }}
	for _, c := range []string{"A", "B", ""} {
		for _, cmd := range []string{"/status", "export facts", "/export"} {
			if rep, ok := a.Handle(c, cmd); !ok || !strings.Contains(rep, "owner") {
				t.Fatalf("%q %q not refused: %q", c, cmd, rep)
			}
		}
	}
	none := &Admin{Know: know, Start: now, Now: func() time.Time { return now }}
	if rep, _ := none.Handle("", "/status"); !strings.Contains(rep, "owner") {
		t.Fatalf("no-owner admin open: %q", rep)
	}
	// teaching facts is owner-only
	if rep, _ := know.Handle("B", "remember that zebra is a secret"); strings.Contains(strings.ToLower(rep), "remembered") {
		t.Fatalf("non-owner taught a fact: %q", rep)
	}
}

func TestPrivateFactsStayPrivateAndPublishIsExplicit(t *testing.T) {
	dir := t.TempDir()
	k := &Knowledge{Path: filepath.Join(dir, "k.json"), Owner: "42"}
	if r, _ := k.Handle("42", "remember that my wifi name is Blue"); !strings.Contains(r, "Private") {
		t.Fatal(r)
	}
	if r, _ := k.Handle("42", "remember publicly that shop hours are 9am to 5pm"); !strings.Contains(r, "Public") {
		t.Fatal(r)
	}
	// a fact saved before the scope field existed has no Public flag, so it is private
	fs, _ := k.load()
	fs = append(fs, fact{Key: keyOf("old password hint"), Text: "old password hint is red"})
	k.save(fs)
	if r, ok := k.Lookup("7", "what is my wifi name"); ok {
		t.Fatalf("private fact leaked: %q", r)
	}
	if r, ok := k.Lookup("7", "what is old password hint"); ok {
		t.Fatalf("legacy fact leaked: %q", r)
	}
	if r, ok := k.Lookup("7", "what are shop hours"); !ok || !strings.Contains(r, "9am") {
		t.Fatalf("public fact not served: %q %v", r, ok)
	}
	if r, ok := k.Lookup("42", "what is my wifi name"); !ok || !strings.Contains(r, "Blue") {
		t.Fatalf("owner can't read private: %q", r)
	}
	if r, _ := k.Handle("7", "remember publicly that x is y"); strings.Contains(r, "Public:") {
		t.Fatalf("non-owner published: %q", r)
	}
}

func TestGroupChatsGetNoNotesOrReminders(t *testing.T) {
	dir := t.TempDir()
	c := &Chat{Notes: &Notes{Path: filepath.Join(dir, "n.json")}, Rem: &Reminders{Path: filepath.Join(dir, "r.json")}}
	for _, q := range []string{"note: buy milk", "notes", "remind me in 5 minutes to stretch", "my reminders", "my tasks"} {
		if r := c.Reply("-100123", q); r != groupPrivacyMsg {
			t.Fatalf("%q in group: %q", q, r)
		}
	}
	if r := c.Reply("55", "note: buy milk"); r == groupPrivacyMsg {
		t.Fatal("private chat blocked")
	}
}
