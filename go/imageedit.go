package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	_ "image/png"
)

// Local, non-AI sharpening (unsharp mask). It makes edges crisper; it cannot recover detail that blur destroyed.
// Decoding is bounded (file size, dimensions, pixel count) before any pixel buffer is allocated. Output is a fresh
// JPEG, so camera metadata (location, device, time) is NOT carried over.

const (
	maxImageBytes  = 8 << 20
	maxImagePixels = 4_000_000
	maxImageSide   = 4096
)

func SharpenImage(data []byte, radius int, amount float64) ([]byte, string, error) {
	if len(data) == 0 || len(data) > maxImageBytes {
		return nil, "", fmt.Errorf("that image is too big (limit 8 MB)")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "jpeg" && format != "png") {
		return nil, "", fmt.Errorf("I can only sharpen JPEG or PNG pictures")
	}
	if cfg.Width < 16 || cfg.Height < 16 || cfg.Width > maxImageSide || cfg.Height > maxImageSide || cfg.Width*cfg.Height > maxImagePixels {
		return nil, "", fmt.Errorf("that picture is %dx%d; I handle up to 4 million pixels (about 2000x2000)", cfg.Width, cfg.Height)
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", fmt.Errorf("I couldn't read that picture")
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	rgba := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(rgba, rgba.Bounds(), src, b.Min, draw.Src)
	out := unsharp(rgba, radius, amount)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, out, &jpeg.Options{Quality: 92}); err != nil {
		return nil, "", fmt.Errorf("I couldn't write the result")
	}
	return buf.Bytes(), fmt.Sprintf("%dx%d", w, h), nil
}

// unsharp sharpens the luma only (so colours do not get halos): out = orig + amount*(orig - blur), with a small
// threshold so flat noise is not boosted.
func unsharp(src *image.RGBA, radius int, amount float64) *image.RGBA {
	w, h := src.Bounds().Dx(), src.Bounds().Dy()
	luma := make([]float32, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := src.RGBAAt(x, y)
			luma[y*w+x] = 0.299*float32(c.R) + 0.587*float32(c.G) + 0.114*float32(c.B)
		}
	}
	blur := append([]float32(nil), luma...)
	tmp := make([]float32, w*h)
	for i := 0; i < 3; i++ { // three box blurs approximate a Gaussian
		boxBlurH(blur, tmp, w, h, radius)
		boxBlurV(tmp, blur, w, h, radius)
	}
	out := image.NewRGBA(src.Bounds())
	const thr = 2.0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := y*w + x
			d := luma[i] - blur[i]
			if d > -thr && d < thr {
				d = 0
			}
			delta := float32(amount) * d
			c := src.RGBAAt(x, y)
			out.SetRGBA(x, y, color.RGBA{clamp8(float32(c.R) + delta), clamp8(float32(c.G) + delta), clamp8(float32(c.B) + delta), 255})
		}
	}
	return out
}

func clamp8(v float32) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v + 0.5)
}

func boxBlurH(src, dst []float32, w, h, r int) {
	for y := 0; y < h; y++ {
		row := src[y*w : (y+1)*w]
		out := dst[y*w : (y+1)*w]
		var sum float32
		n := float32(2*r + 1)
		for k := -r; k <= r; k++ {
			sum += row[clampI(k, w)]
		}
		for x := 0; x < w; x++ {
			out[x] = sum / n
			sum += row[clampI(x+r+1, w)] - row[clampI(x-r, w)]
		}
	}
}

func boxBlurV(src, dst []float32, w, h, r int) {
	n := float32(2*r + 1)
	for x := 0; x < w; x++ {
		var sum float32
		for k := -r; k <= r; k++ {
			sum += src[clampI(k, h)*w+x]
		}
		for y := 0; y < h; y++ {
			dst[y*w+x] = sum / n
			sum += src[clampI(y+r+1, h)*w+x] - src[clampI(y-r, h)*w+x]
		}
	}
}

func clampI(i, n int) int {
	if i < 0 {
		return 0
	}
	if i >= n {
		return n - 1
	}
	return i
}
