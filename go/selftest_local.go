package main

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

func lumaEdge(img image.Image) float64 {
	b := img.Bounds()
	var sum float64
	n := 0
	for y := b.Min.Y; y < b.Max.Y; y += 2 {
		for x := b.Min.X; x+1 < b.Max.X; x++ {
			r1, g1, b1, _ := img.At(x, y).RGBA()
			r2, g2, b2, _ := img.At(x+1, y).RGBA()
			d := (0.299*float64(r2) + 0.587*float64(g2) + 0.114*float64(b2)) - (0.299*float64(r1) + 0.587*float64(g1) + 0.114*float64(b1))
			if d < 0 {
				d = -d
			}
			sum += d / 257
			n++
		}
	}
	return sum / float64(n)
}

// SelfTestLocal checks the offline V4 features (no network, no token, no key): maths/time, calculator script,
// designs, owner knowledge on a temp file, and sharpening, with timings and memory. Fixed inputs only.
func SelfTestLocal() bool {
	ok := true
	check := func(name string, cond bool, detail string) {
		st := "OK  "
		if !cond {
			st, ok = "FAIL", false
		}
		fmt.Printf("%s %s %s\n", st, name, detail)
	}
	t0 := time.Now()
	now := time.Date(2026, 11, 17, 18, 0, 0, 0, time.UTC)
	for _, c := range []struct{ in, want string }{
		{"mean of 3, 5, 8", "5.333333"}, {"standard deviation of 2, 4, 4, 4, 5, 5, 7, 9", "Population standard deviation: 2"},
		{"15% of 80", "= 12"}, {"percent change from 50 to 75", "50% increase"}, {"days until 2026-12-25", "38 days"},
		{"what day is 2027-03-01", "Monday 1 March 2027"}, {"time in Tokyo", "Wednesday 03:00"}, {"convert 3 pm New York to Lagos", "21:00 Lagos"},
		{"calc script: let s = 0; for i = 1 to 100 { s = s + i }; print s", "5050"},
		{"calc script: print 1/0", "Script error"},
	} {
		r, isC := Compute(c.in, now)
		check("compute "+c.in, isC && strings.Contains(r, c.want), r)
	}
	for _, in := range []string{"quote card: Stay curious - Zenas", "poster: Open Day | Saturday 10am | Everyone welcome theme: green", "bar chart: Mon=5, Tue=8, Wed=3 title: Sales", "line chart: 1, 4, 2, 6", "pie chart: A=1, B=3"} {
		s := time.Now()
		b, _, e, isD := Design(in)
		im, err := png.Decode(bytes.NewReader(b))
		dims := ""
		if err == nil {
			dims = fmt.Sprintf("%dx%d", im.Bounds().Dx(), im.Bounds().Dy())
		}
		check("design "+in, isD && e == "" && err == nil && len(b) > 2000, fmt.Sprintf("%s %d bytes %.2fs", dims, len(b), time.Since(s).Seconds()))
	}
	dir, err := os.MkdirTemp("", "k")
	if err == nil {
		defer os.RemoveAll(dir)
		k := &Knowledge{Path: filepath.Join(dir, "k.json"), Owner: "1"}
		r1, _ := k.Handle("1", "remember that my wifi name is BlueHouse")
		r2, _ := k.Lookup("1", "what is my wifi name")
		r3, _ := k.Handle("2", "remember that x is y")
		check("knowledge owner learns and recalls", strings.Contains(r1, "remember") && strings.Contains(r2, "BlueHouse"), r2)
		check("knowledge stranger refused", strings.Contains(r3, "Only the bot owner"), r3)
	} else {
		check("knowledge temp dir", false, err.Error())
	}
	// sharpening on a deliberately blurred render
	b, _, _, _ := Design("quote card: Sharpness test - the quick brown fox jumps over the lazy dog 0123456789")
	im, _ := png.Decode(bytes.NewReader(b))
	rgba := image.NewRGBA(im.Bounds())
	for y := 0; y < im.Bounds().Dy(); y++ {
		for x := 0; x < im.Bounds().Dx(); x++ {
			rgba.Set(x, y, im.At(x, y))
		}
	}
	w, h := rgba.Bounds().Dx(), rgba.Bounds().Dy()
	for c := 0; c < 3; c++ {
		ch := make([]float32, w*h)
		for i := range ch {
			ch[i] = float32(rgba.Pix[i*4+c])
		}
		tmp := make([]float32, w*h)
		for k := 0; k < 3; k++ {
			boxBlurH(ch, tmp, w, h, 2)
			boxBlurV(tmp, ch, w, h, 2)
		}
		for i := range ch {
			rgba.Pix[i*4+c] = clamp8(ch[i])
		}
	}
	var jb bytes.Buffer
	_ = jpeg.Encode(&jb, rgba, &jpeg.Options{Quality: 90})
	s := time.Now()
	out, dims, err := SharpenImage(jb.Bytes(), 2, 1.2)
	el := time.Since(s).Seconds()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	if err == nil {
		a, _ := jpeg.Decode(bytes.NewReader(jb.Bytes()))
		bb, _ := jpeg.Decode(bytes.NewReader(out))
		ea, eb := lumaEdge(a), lumaEdge(bb)
		check("sharpen blurred 1080x1080", eb > ea*1.15, fmt.Sprintf("%s edge energy %.3f -> %.3f in %.2fs, Go heap sys %d MB", dims, ea, eb, el, ms.Sys>>20))
	} else {
		check("sharpen", false, err.Error())
	}
	fmt.Printf("selftest-local %s in %.1fs\n", map[bool]string{true: "PASSED", false: "FAILED"}[ok], time.Since(t0).Seconds())
	return ok
}
