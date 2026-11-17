package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestQuiz(t *testing.T) {
	p := filepath.Join(t.TempDir(), "k.json")
	b, _ := json.Marshal([]fact{{Key: "capital france", Text: "The capital of France is Paris", Added: "2026-11-17"}})
	os.WriteFile(p, b, 0600)
	q := &Quiz{K: &Knowledge{Path: p, Owner: "42"}}
	if r, ok := q.Handle("7", "quiz me"); !ok || !strings.Contains(r, "owner") {
		t.Fatalf("non-owner: %q", r)
	}
	if _, ok := q.Handle("42", "a: hello"); ok {
		t.Fatal("answer with no quiz must pass through")
	}
	r, ok := q.Handle("42", "quiz me")
	if !ok || !strings.Contains(r, "The capital of France") {
		t.Fatalf("start: %q", r)
	}
	r, _ = q.Handle("42", "answer: the paris!")
	if !strings.Contains(r, "Exact match") || !strings.Contains(r, "1 of 1") {
		t.Fatalf("exact: %q", r)
	}
	q.Handle("42", "next")
	r, _ = q.Handle("42", "answer: Lyon")
	if !strings.Contains(r, "Not an exact match") || !strings.Contains(r, "1 of 2") {
		t.Fatalf("wrong: %q", r)
	}
	if r, _ = q.Handle("42", "stop quiz"); !strings.Contains(r, "1 of 2") {
		t.Fatalf("stop: %q", r)
	}
	if _, ok := q.Handle("42", "next"); ok {
		t.Fatal("next with no quiz must pass through")
	}
}
