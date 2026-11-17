package main

import (
	"os"
	"strings"
	"time"
	"testing"
)

func withGates(t *testing.T, v string) {
	os.Setenv("TG_DISABLE_SOURCES", v)
	loadSourceGates()
	t.Cleanup(func() { os.Unsetenv("TG_DISABLE_SOURCES"); loadSourceGates() })
}

func TestSourceGatesNeverContactDisabledHosts(t *testing.T) {
	withGates(t, "weather, FX ,wiki,define,news,feeds,github")
	tl, f, _ := newFake(t)
	for _, q := range []string{"weather in Lagos", "forecast for Lagos", "sunrise in Lagos", "100 usd to eur", "define serendipity", "wiki Alan Turing"} {
		r, ok := tl.Pre(q)
		if !ok || !strings.Contains(r, "switched off") {
			t.Fatalf("%q: %q %v", q, r, ok)
		}
	}
	if r, ok := tl.Post("what is photosynthesis in plants"); ok && !strings.Contains(r, "switched off") {
		t.Fatalf("post: %q", r)
	}
	if len(f.hits) != 0 {
		t.Fatalf("disabled sources were contacted: %v", f.hits)
	}
	ff := &fakeFeed{body: bbcXML, code: 200}
	n, _ := newsFor(t, ff, "42")
	r, _ := n.Handle("7", "news")
	if !strings.Contains(r, "switched off") || len(ff.calls) != 0 {
		t.Fatalf("news: %q %v", r, ff.calls)
	}
	gh := &fakeGH{body: ghOK, code: 200}
	if r := GitHubReply(gh, "weather apps", func() time.Time { return time.Now() }); !strings.Contains(r, "switched off") || gh.url != "" {
		t.Fatalf("github: %q %q", r, gh.url)
	}
	fd, pf, _ := feedsFor(t, map[string]PageResult{"https://example.org/rss.xml": {Status: 200, Body: []byte(rssSample), ContentType: "application/rss+xml"}})
	fd.Handle("42", "add feed https://example.org/rss.xml as news")
	before := len(pf.calls)
	if _, err := fd.Items("news"); err == nil || !strings.Contains(err.Error(), "switched off") || len(pf.calls) != before {
		t.Fatalf("feeds: %v calls %d->%d", err, before, len(pf.calls))
	}
}

func TestSourceGatesOffByDefaultAndPerFeature(t *testing.T) {
	loadSourceGates()
	tl, f, _ := newFake(t)
	if r, _ := tl.Pre("weather in Lagos"); strings.Contains(r, "switched off") || len(f.hits) == 0 {
		t.Fatalf("default should be on: %q", r)
	}
	withGates(t, "weather")
	tl, f, _ = newFake(t)
	if r, _ := tl.Pre("weather in Lagos"); !strings.Contains(r, "switched off") {
		t.Fatal(r)
	}
	if r, _ := tl.Pre("define serendipity"); strings.Contains(r, "switched off") {
		t.Fatalf("only weather should be off: %q", r)
	}
	if m, off := gateOff("holidays"); off || m != "" {
		t.Fatal("static holidays must not be gated")
	}
}
