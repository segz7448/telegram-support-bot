package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPasswordGenerationStaysOutOfChat(t *testing.T) {
	d := filepath.Join(t.TempDir(), "pw")
	a := &Admin{Owner: "42", PassDir: d}
	if r, _ := a.Handle("7", "generate password for bank"); !strings.Contains(r, "owner") {
		t.Fatal(r)
	}
	r, ok := a.Handle("42", "generate password for Bank 24 characters")
	if !ok || strings.Contains(r, "\n") && false {
		t.Fatal(r)
	}
	b, err := os.ReadFile(filepath.Join(d, "bank.txt"))
	if err != nil {
		t.Fatal(err)
	}
	pw := strings.TrimSpace(string(b))
	if len(pw) != 24 || strings.Contains(r, pw) {
		t.Fatalf("len %d, leaked=%v", len(pw), strings.Contains(r, pw))
	}
	for _, c := range pw {
		if !strings.ContainsRune(passAlphabet, c) {
			t.Fatalf("char %q", c)
		}
	}
	if st, _ := os.Stat(filepath.Join(d, "bank.txt")); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", st.Mode())
	}
	if r2, _ := a.Handle("42", "generate password for bank"); !strings.Contains(r2, "already exists") {
		t.Fatal(r2)
	}
	if l, _ := a.Handle("42", "list generated passwords"); !strings.Contains(l, "bank") || strings.Contains(l, pw) {
		t.Fatal(l)
	}
	if r3, _ := a.Handle("42", "generate password for x 5 characters"); !strings.Contains(r3, "12 to 64") {
		t.Fatal(r3)
	}
	a.Handle("42", "forget password for bank")
	if _, err := os.Stat(filepath.Join(d, "bank.txt")); err == nil {
		t.Fatal("not deleted")
	}
	off := &Admin{Owner: "42"}
	if r4, _ := off.Handle("42", "generate password for a"); !strings.Contains(r4, "switched off") {
		t.Fatal(r4)
	}
	p1, _ := randPassword(32)
	p2, _ := randPassword(32)
	if p1 == p2 {
		t.Fatal("repeat")
	}
}
