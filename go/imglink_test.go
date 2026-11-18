package main

import (
	"strings"
	"testing"
)

func TestImageLinkTaskParser(t *testing.T) {
	for _, s := range []string{
		"describe this https://picsum.photos/seed/fx2652091/640/480",
		"what is in https://upload.wikimedia.org/a/b/c.PNG ?",
		"look at https://i.imgur.com/abc123.jpg please",
		"describe https://fastly.picsum.photos/id/110/640/480.jpg",
	} {
		if _, p, why, ok, trig := imageLinkTask(s); !ok || !trig || p == "" || why != "" {
			t.Fatalf("%q should pass: %q", s, why)
		}
	}
	// triggered (a link and an ask) but refused
	for _, s := range []string{
		"describe https://api.telegram.org/file/bot123456:AAAAAAAAAAAAAAAAAAAAAAAAAA/photos/f.jpg",
		"describe https://example.com/a.png",
		"describe https://picsum.photos.evil.com/seed/x/640/480",
		"describe https://picsum.photos@evil.com/seed/x/640/480",
		"describe https://picsum.photos/seed/x/640/480?token=1",
		"describe https://picsum.photos/seed/x/640/480#frag",
		"describe https://picsum.photos:8443/seed/x/640/480",
		"describe https://i.imgur.com/a.png?x=1",
		"describe http://i.imgur.com/a.png",
		"describe ftp://i.imgur.com/a.png",
		"describe https://i.imgur.com/page.html",
		"describe https://127.0.0.1/a.png",
		"describe https://i.imgur.com/a.png and https://i.imgur.com/b.png",
		"describe https://i.imgur.com/a.png and ftp://evil.example/x",
		"describe https://i.imgur.com/a.png also www.evil.example/x",
		"describe https://i.imgur.com/a.png sk-abcdefghijklmnopqrstuvwxyz0123456789ABCD",
	} {
		_, _, why, ok, trig := imageLinkTask(s)
		if ok || !trig || why == "" {
			t.Fatalf("%q must be refused, ok=%v trig=%v why=%q", s, ok, trig, why)
		}
	}
	// not a picture-link request at all
	for _, s := range []string{"https://i.imgur.com/a.png", "describe my day", "what is the capital of France"} {
		if _, _, _, _, trig := imageLinkTask(s); trig {
			t.Fatalf("%q must not trigger", s)
		}
	}
}

func TestImageLinkGateNeverLeaks(t *testing.T) {
	bad := []string{
		"describe this https://api.telegram.org/file/bot123456:AAAAAAAAAAAAAAAAAAAAAAAAAA/photos/f.jpg please tell me everything",
		"please describe https://example.com/a.png and compare it with my business plan notes in detail",
		"describe https://i.imgur.com/a.png and https://evil.example/steal?k=1 now with all the detail you can",
		"explain in detail what is shown at http://i.imgur.com/a.png and write a long answer about it",
		"describe https://i.imgur.com/a.png?sig=SECRETVALUE1234567890 and then compare with other pictures",
		"describe https://i.imgur.com/a.png sk-abcdefghijklmnopqrstuvwxyz0123456789ABCD and explain it fully",
		"describe https://picsum.photos/seed/x/640/480#tokenabc and give me a full, long analysis please",
	}
	for _, in := range bad {
		f := &fakeExt{img: true}
		e := newGmEnv(t)
		e.c.Ext = &extGate{A: f, Owner: "5", PerDay: 5}
		r, _ := e.send("5", in)
		if len(f.asked) != 0 || len(e.s.qs) != 0 {
			t.Fatalf("leaked %q -> asked=%v raws=%v searches=%v", in, f.asked, f.raws, e.s.qs)
		}
		// either this gate's refusal or an earlier local handler's reply; in both cases nothing was forwarded
		if r == "" {
			t.Fatalf("%q got no reply", in)
		}
	}
}

func TestImageLinkGateSendsOnlyIntendedPrompt(t *testing.T) {
	f := &fakeExt{}
	e := newGmEnv(t)
	e.c.Ext = &extGate{A: f, Owner: "5", PerDay: 5}
	r, _ := e.send("5", "what do you see in https://picsum.photos/seed/fx2652091/640/480 ? are there animals")
	if len(f.raws) != 1 || f.raws[0] == "" || f.asked[0] != "" {
		t.Fatalf("expected exactly one Raw prompt and empty Text: asked=%v raws=%v reply=%q", f.asked, f.raws, r)
	}
	p := f.raws[0]
	if !strings.Contains(p, "https://picsum.photos/seed/fx2652091/640/480") || !strings.Contains(p, "are there animals") {
		t.Fatalf("prompt lacks the link or question: %q", p)
	}
	if strings.Count(p, "http") != 1 {
		t.Fatalf("exactly one link may be in the prompt: %q", p)
	}
	if !strings.Contains(r, "Unchecked, may be wrong") {
		t.Fatalf("reply must be labelled: %q", r)
	}
	// and the live bug: the wire message must be the Raw prompt, not the empty-text preamble
	w := &WajoFo{}
	if got := wireText(ExtRequest{Raw: "RAWPROMPT"}, w); got != "RAWPROMPT" {
		t.Fatalf("raw prompt not used on the text path: %q", got)
	}
}
