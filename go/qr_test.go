package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestQR(t *testing.T) {
	q := &QR{}
	r, ok := q.Handle("1", "qr code for https://example.com/hello")
	if !ok || len(r.PNG) < 200 || !bytes.HasPrefix(r.PNG, []byte("\x89PNG")) {
		t.Fatalf("no png: %v %v", ok, r)
	}
	for _, bad := range []string{"qr code for my password is hunter2", "qr: " + strings.Repeat("a", 301)} {
		r, ok := q.Handle("1", bad)
		if !ok || len(r.PNG) != 0 {
			t.Fatalf("should refuse %q", bad)
		}
	}
	if _, ok := q.Handle("1", "what is the weather"); ok {
		t.Fatal("must not claim other text")
	}
	if _, ok := q.Handle("1", "quote of the day"); ok {
		t.Fatal("quote must not match")
	}
}
