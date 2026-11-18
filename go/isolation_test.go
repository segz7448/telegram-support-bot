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
