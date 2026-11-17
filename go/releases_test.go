package main

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type relFake struct {
	body  string
	code  int
	h     map[string]string
	calls []string
}

func (f *relFake) GetWithHeaders(u string, hd map[string]string, t int) ([]byte, map[string]string, int, error) {
	f.calls = append(f.calls, u)
	return []byte(f.body), f.h, f.code, nil
}

func rel(tag string, extra string) string {
	return relU(tag, "https://github.com/golang/go/releases/tag/"+tag, extra)
}

func relU(tag, u, extra string) string {
	return fmt.Sprintf(`{"tag_name":"%s","name":"Release %s","draft":false,"prerelease":false,"html_url":"%s","published_at":"2026-11-17T00:00:00Z"%s}`, tag, tag, u, extra)
}

func TestReleasesWatchFlow(t *testing.T) {
	f := &relFake{body: rel("v1.0.0", ""), code: 200}
	r := &Releases{G: f, Path: filepath.Join(t.TempDir(), "rel.json"), Owner: "42"}
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	if x, ok := r.Handle("7", "watch releases of golang/go"); !ok || !strings.Contains(x, "Only the bot owner") {
		t.Fatalf("non-owner: %q", x)
	}
	if x, _ := r.Handle("42", "watch releases of golang/go"); !strings.Contains(x, "Watching releases of golang/go") {
		t.Fatalf("watch: %q", x)
	}
	if x, _ := r.Handle("42", "watch releases of https://github.com/golang/go"); !strings.Contains(x, "Already") {
		t.Fatalf("dup: %q", x)
	}
	if out := r.Tick(now); len(out) != 0 { // first check seeds silently
		t.Fatalf("seed announced: %v", out)
	}
	if len(f.calls) != 1 || f.calls[0] != "https://api.github.com/repos/golang/go/releases/latest" {
		t.Fatalf("calls %v", f.calls)
	}
	if out := r.Tick(now.Add(10 * time.Minute)); len(out) != 0 || len(f.calls) != 1 {
		t.Fatalf("must not recheck inside an hour: %v %v", out, f.calls)
	}
	f.body = rel("v1.1.0", "")
	out := r.Tick(now.Add(61 * time.Minute))
	if len(out) != 1 || out[0].Chat != "42" || !strings.Contains(out[0].Text, "v1.1.0") || !strings.Contains(out[0].Text, "https://github.com/golang/go/releases/tag/v1.1.0") {
		t.Fatalf("announce: %v", out)
	}
	if out := r.Tick(now.Add(130 * time.Minute)); len(out) != 0 { // same tag again: no repeat
		t.Fatalf("repeat: %v", out)
	}
	f.body = strings.Replace(rel("v2.0.0-rc1", ""), `"prerelease":false`, `"prerelease":true`, 1)
	if out := r.Tick(now.Add(200 * time.Minute)); len(out) != 0 {
		t.Fatalf("prerelease announced: %v", out)
	}
	f.body = relU("release/2026+rc 1", "https://github.com/golang/go/releases/tag/release%2F2026+rc%201", "")
	if out := r.Tick(now.Add(270 * time.Minute)); len(out) != 1 || !strings.Contains(out[0].Text, "https://github.com/golang/go/releases/tag/release%2F2026+rc%201") {
		t.Fatalf("odd tag with encoded url: %v", out)
	}
	for i, bad := range []string{"https://evil.test/golang/go/releases/tag/v9", "http://github.com/golang/go/releases/tag/v9", "https://github.com/other/repo/releases/tag/v9", "https://github.com/golang/go/releases/tag/v8", "https://github.com/golang/go/releases/tag/V9", "https://github.com.evil.test/golang/go/releases/tag/v9", "https://user@github.com/golang/go/releases/tag/v9", "https://github.com/golang/go/releases/tag/v9?x=1"} {
		f.body = relU("v9", bad, "")
		out := r.Tick(now.Add(time.Duration(340+140*i) * time.Minute))
		if len(out) != 1 || strings.Contains(out[0].Text, "evil.test") || strings.Contains(out[0].Text, "other/repo") || strings.Contains(out[0].Text, "releases/tag/v8") || !strings.Contains(out[0].Text, "not showing it") {
			t.Fatalf("bad url %q: %v", bad, out)
		}
		f.body = relU("v10", "https://github.com/golang/go/releases/tag/v10", "")
		r.Tick(now.Add(time.Duration(410+140*i) * time.Minute))
	}
	if x, _ := r.Handle("42", "release watches"); !strings.Contains(x, "golang/go (last seen: v10)") {
		t.Fatalf("list: %q", x)
	}
	if x, _ := r.Handle("42", "unwatch releases of golang/go"); !strings.Contains(x, "Stopped") {
		t.Fatalf("unwatch: %q", x)
	}
}

func TestReleasesRateLimitPausesAndCap(t *testing.T) {
	f := &relFake{code: 403, h: map[string]string{"x-ratelimit-remaining": "0", "x-ratelimit-reset": "1794880000"}}
	r := &Releases{G: f, Path: filepath.Join(t.TempDir(), "rel.json"), Owner: "42"}
	now := time.Unix(1794876400, 0)
	r.Handle("42", "watch releases of a/b")
	r.Handle("42", "watch releases of c/d")
	r.Tick(now)
	n := len(f.calls)
	r.Tick(now.Add(2 * time.Minute))
	if len(f.calls) != n {
		t.Fatalf("must pause after a rate limit, calls %v", f.calls)
	}
	for i := 0; i < 12; i++ {
		r.Handle("42", fmt.Sprintf("watch releases of x/r%d", i))
	}
	if x, _ := r.Handle("42", "watch releases of x/overflow"); !strings.Contains(x, "at most 10") {
		t.Fatalf("cap: %q", x)
	}
	off := &Releases{}
	if x, ok := off.Handle("42", "watch releases of a/b"); !ok || !strings.Contains(x, "switched off") {
		t.Fatalf("off: %q", x)
	}
}
