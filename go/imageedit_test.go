package main

import (
	"bytes"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"strings"
	"testing"
)

// edgeEnergy: mean absolute horizontal luma gradient, a simple sharpness measure.
func edgeEnergy(img image.Image) float64 {
	b := img.Bounds()
	var sum float64
	n := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x+1 < b.Max.X; x++ {
			r1, g1, b1, _ := img.At(x, y).RGBA()
			r2, g2, b2, _ := img.At(x+1, y).RGBA()
			l1 := 0.299*float64(r1) + 0.587*float64(g1) + 0.114*float64(b1)
			l2 := 0.299*float64(r2) + 0.587*float64(g2) + 0.114*float64(b2)
			d := l2 - l1
			if d < 0 {
				d = -d
			}
			sum += d / 257
			n++
		}
	}
	return sum / float64(n)
}

func blurredTestImage(t *testing.T) []byte {
	b, _, e, ok := Design("quote card: Sharpness test - the quick brown fox jumps over the lazy dog 0123456789")
	if !ok || e != "" {
		t.Fatal(e)
	}
	im, _ := png.Decode(bytes.NewReader(b))
	rgba := image.NewRGBA(im.Bounds())
	for y := 0; y < im.Bounds().Dy(); y++ {
		for x := 0; x < im.Bounds().Dx(); x++ {
			rgba.Set(x, y, im.At(x, y))
		}
	}
	// blur it ourselves (3 box blurs, radius 2) so we have a "slightly blurry picture"
	w, h := rgba.Bounds().Dx(), rgba.Bounds().Dy()
	for c := 0; c < 3; c++ {
		ch := make([]float32, w*h)
		for i := 0; i < w*h; i++ {
			ch[i] = float32(rgba.Pix[i*4+c])
		}
		tmp := make([]float32, w*h)
		for k := 0; k < 3; k++ {
			boxBlurH(ch, tmp, w, h, 2)
			boxBlurV(tmp, ch, w, h, 2)
		}
		for i := 0; i < w*h; i++ {
			rgba.Pix[i*4+c] = clamp8(ch[i])
		}
	}
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, rgba, &jpeg.Options{Quality: 90})
	return buf.Bytes()
}

func TestSharpenIncreasesEdgeContrastAndIsBounded(t *testing.T) {
	blurry := blurredTestImage(t)
	out, dims, err := SharpenImage(blurry, 2, 1.2)
	if err != nil || dims != "1080x1080" {
		t.Fatalf("%v %s", err, dims)
	}
	a, _ := jpeg.Decode(bytes.NewReader(blurry))
	b, err := jpeg.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	ea, eb := edgeEnergy(a), edgeEnergy(b)
	if eb < ea*1.15 {
		t.Fatalf("not sharper: before %.3f after %.3f", ea, eb)
	}
	t.Logf("edge energy before %.3f after %.3f", ea, eb)
	if d := os.Getenv("TGBOT_DESIGN_OUT"); d != "" {
		_ = os.WriteFile(d+"/before.jpg", blurry, 0o644)
		_ = os.WriteFile(d+"/after.jpg", out, 0o644)
	}
	// a flat image stays flat (no noise boost), and bad input is refused
	flat := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for i := range flat.Pix {
		flat.Pix[i] = 128
	}
	var fb bytes.Buffer
	_ = png.Encode(&fb, flat)
	o2, _, err := SharpenImage(fb.Bytes(), 2, 1.2)
	if err != nil {
		t.Fatal(err)
	}
	f2, _ := jpeg.Decode(bytes.NewReader(o2))
	if edgeEnergy(f2) > 1.5 {
		t.Fatal("flat image gained noise")
	}
	if _, _, err := SharpenImage([]byte("not an image"), 2, 1); err == nil {
		t.Fatal("garbage accepted")
	}
	big := image.NewRGBA(image.Rect(0, 0, 3000, 3000))
	var bb bytes.Buffer
	_ = png.Encode(&bb, big)
	if _, _, err := SharpenImage(bb.Bytes(), 2, 1); err == nil || !strings.Contains(err.Error(), "million pixels") {
		t.Fatalf("oversize accepted: %v", err)
	}
	if _, _, err := SharpenImage(make([]byte, maxImageBytes+1), 2, 1); err == nil {
		t.Fatal("huge file accepted")
	}
}
