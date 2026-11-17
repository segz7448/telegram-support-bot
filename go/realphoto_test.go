package main

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"os"
	"testing"
)

func gradEnergy(img image.Image) float64 {
	b := img.Bounds()
	var s float64
	n := 0
	for y := b.Min.Y; y < b.Max.Y-1; y++ {
		for x := b.Min.X; x < b.Max.X-1; x++ {
			r, _, _, _ := img.At(x, y).RGBA()
			r2, _, _, _ := img.At(x+1, y).RGBA()
			r3, _, _, _ := img.At(x, y+1).RGBA()
			d1, d2 := float64(r)-float64(r2), float64(r)-float64(r3)
			s += (d1*d1 + d2*d2) / (65535 * 65535)
			n++
		}
	}
	return s / float64(n)
}

// TGBOT_PHOTO=/path/real.jpg writes /tmp/real_blur.jpg and /tmp/real_sharp.jpg and prints gradient energy.
func TestRealPhoto(t *testing.T) {
	p := os.Getenv("TGBOT_PHOTO")
	if p == "" {
		t.Skip("set TGBOT_PHOTO")
	}
	data, _ := os.ReadFile(p)
	img, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	rgba := image.NewRGBA(img.Bounds())
	draw.Draw(rgba, rgba.Bounds(), img, image.Point{}, draw.Src)
	blur := boxBlurRGBA(rgba, 2)
	var bb bytes.Buffer
	jpeg.Encode(&bb, blur, &jpeg.Options{Quality: 92})
	os.WriteFile("/tmp/real_blur.jpg", bb.Bytes(), 0o644)
	out, note, err := SharpenImage(bb.Bytes(), 2, 1.2)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile("/tmp/real_sharp.jpg", out, 0o644)
	so, _ := jpeg.Decode(bytes.NewReader(out))
	fmt.Printf("original %.6f blurred %.6f sharpened %.6f (%s)\n", gradEnergy(img), gradEnergy(blur), gradEnergy(so), note)
}

func boxBlurRGBA(src *image.RGBA, r int) *image.RGBA {
	b := src.Bounds()
	dst := image.NewRGBA(b)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			var sr, sg, sb, n int
			for dy := -r; dy <= r; dy++ {
				for dx := -r; dx <= r; dx++ {
					xx, yy := x+dx, y+dy
					if xx < b.Min.X || yy < b.Min.Y || xx >= b.Max.X || yy >= b.Max.Y {
						continue
					}
					c := src.RGBAAt(xx, yy)
					sr += int(c.R)
					sg += int(c.G)
					sb += int(c.B)
					n++
				}
			}
			i := dst.PixOffset(x, y)
			dst.Pix[i], dst.Pix[i+1], dst.Pix[i+2], dst.Pix[i+3] = uint8(sr/n), uint8(sg/n), uint8(sb/n), 255
		}
	}
	return dst
}
