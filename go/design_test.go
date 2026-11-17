package main

import (
	"bytes"
	"image/png"
	"os"
	"strings"
	"testing"
)

func TestDesignRendersValidPNGs(t *testing.T) {
	cases := []struct {
		in   string
		w, h int
	}{
		{"quote card: The only way to do great work is to love what you do - Steve Jobs", 1080, 1080},
		{"poster: Community Clean-up Day | Saturday 9am at the town park. Bring gloves and water. | Everyone welcome theme: green", 1080, 1350},
		{"banner: Grand Opening | This Friday | www.example.com theme: sunset", 1500, 500},
		{"bar chart: Mon=5, Tue=8, Wed=3, Thu=11, Fri=7 title: Sales this week", 1200, 800},
		{"line chart: Jan=10, Feb=25, Mar=18, Apr=40, May=32 title: Growth theme: light", 1200, 800},
		{"pie chart: Rent=900, Food=400, Transport=150, Savings=250 title: Budget", 1200, 800},
	}
	for i, c := range cases {
		b, cap, errMsg, ok := Design(c.in)
		if !ok || errMsg != "" || len(b) < 2000 || cap == "" {
			t.Fatalf("%q: ok=%v err=%q len=%d", c.in, ok, errMsg, len(b))
		}
		im, err := png.Decode(bytes.NewReader(b))
		if err != nil || im.Bounds().Dx() != c.w || im.Bounds().Dy() != c.h {
			t.Fatalf("%q: %v %v", c.in, err, im.Bounds())
		}
		if d := os.Getenv("TGBOT_DESIGN_OUT"); d != "" {
			_ = os.WriteFile(d+"/design"+string(rune('0'+i))+".png", b, 0o644)
		}
	}
	for _, bad := range []string{"bar chart: 5", "quote card: ", "poster: | x", "pie chart: A=0, B=0", "line chart: A=-1, B=2", "quote card: hi theme: neon"} {
		if _, _, e, ok := Design(bad); !ok || e == "" {
			t.Errorf("%q should fail with a message (ok=%v e=%q)", bad, ok, e)
		}
	}
	if _, _, _, ok := Design("what is a poster"); ok {
		t.Error("plain question hijacked")
	}
	if _, _, e, _ := Design("quote card: " + strings.Repeat("x ", 400)); e == "" {
		t.Error("overlong text accepted")
	}
}
