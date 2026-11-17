package main

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"strings"
	"testing"
)

func testJPEG(w, h int) []byte {
	m := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			m.SetRGBA(x, y, color.RGBA{uint8(x * 255 / w), uint8(y * 255 / h), 120, 255})
		}
	}
	var b bytes.Buffer
	jpeg.Encode(&b, m, &jpeg.Options{Quality: 95})
	return b.Bytes()
}

func TestParseOps(t *testing.T) {
	cases := map[string][]string{
		"black and white, more contrast, sharpen": {"bw", "contrast", "sharpen"},
		"make it sepia and brighter":              {"sepia", "bright"},
		"rotate right":                            {"rotate"},
		"resize 50%":                              {"resize"},
		"hello there":                             {},
	}
	for c, want := range cases {
		got := parseOps(c)
		if len(got) != len(want) {
			t.Fatalf("%q: got %+v want %v", c, got, want)
		}
		for i := range want {
			if got[i].name != want[i] {
				t.Fatalf("%q: got %+v want %v", c, got, want)
			}
		}
	}
}

func decode(t *testing.T, b []byte) image.Image {
	im, err := jpeg.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	return im
}

func TestEditImageOps(t *testing.T) {
	src := testJPEG(200, 100)
	out, dims, err := EditImage(src, parseOps("black and white"))
	if err != nil || dims != "200x100" {
		t.Fatal(err, dims)
	}
	r, g, b, _ := decode(t, out).At(150, 50).RGBA()
	if abs(int(r>>8)-int(g>>8)) > 3 || abs(int(g>>8)-int(b>>8)) > 3 {
		t.Fatalf("not gray: %d %d %d", r>>8, g>>8, b>>8)
	}
	_, dims, _ = EditImage(src, parseOps("rotate right"))
	if dims != "100x200" {
		t.Fatalf("rotate dims %s", dims)
	}
	_, dims, _ = EditImage(src, parseOps("resize 50%"))
	if dims != "100x50" {
		t.Fatalf("resize dims %s", dims)
	}
	// brighter really is brighter; darker really is darker; aspect unchanged
	avg := func(b []byte) float64 {
		im := decode(t, b)
		s := 0.0
		for x := 0; x < 200; x += 10 {
			r, _, _, _ := im.At(x, 50).RGBA()
			s += float64(r >> 8)
		}
		return s
	}
	base, _, _ := EditImage(src, parseOps("sharpen less contrast"))
	br, _, _ := EditImage(src, parseOps("brighter"))
	dk, _, _ := EditImage(src, parseOps("darker"))
	if !(avg(br) > avg(testJPEG(200, 100)) && avg(dk) < avg(testJPEG(200, 100))) || base == nil {
		t.Fatal("brightness direction wrong")
	}
	if _, _, err := EditImage([]byte("not an image"), parseOps("sepia")); err == nil {
		t.Fatal("expected error for non-image")
	}
}

func abs(a int) int {
	if a < 0 {
		return -a
	}
	return a
}

// exif orientation 6 (rotate 90 clockwise to display) must make a 200x100 file come out 100x200.
func TestExifOrientationApplied(t *testing.T) {
	j := testJPEG(200, 100)
	exif := []byte{0xFF, 0xE1, 0x00, 0x22, 'E', 'x', 'i', 'f', 0, 0, 'M', 'M', 0, 0x2A, 0, 0, 0, 8, 0, 1, 0x01, 0x12, 0, 3, 0, 0, 0, 1, 0, 6, 0, 0, 0, 0, 0, 0}
	withExif := append(append([]byte{0xFF, 0xD8}, exif...), j[2:]...)
	if exifOrientation(withExif) != 6 {
		t.Fatalf("orientation %d", exifOrientation(withExif))
	}
	_, dims, err := EditImage(withExif, parseOps("sepia"))
	if err != nil || dims != "100x200" {
		t.Fatal(err, dims)
	}
}

// TGBOT_PHOTO=/path.jpg writes /tmp/fx_*.jpg for each effect so the results can be looked at.
func TestEffectsOnRealPhoto(t *testing.T) {
	p := os.Getenv("TGBOT_PHOTO")
	if p == "" {
		t.Skip("set TGBOT_PHOTO")
	}
	data, _ := os.ReadFile(p)
	for name, cap := range map[string]string{"bw": "black and white", "sepia": "sepia", "bright": "brighter", "dark": "darker", "contrast": "more contrast", "rotate": "rotate right", "resize": "resize 50%", "combo": "black and white, more contrast, sharpen"} {
		out, dims, err := EditImage(data, parseOps(cap))
		if err != nil {
			t.Fatal(name, err)
		}
		os.WriteFile("/tmp/fx_"+name+".jpg", out, 0o644)
		t.Log(name, dims, len(out))
	}
}

func TestPhotoToolkitExtras(t *testing.T) {
	// 40x24 test picture: left half red, right half blue, top row marked green.
	m := image.NewRGBA(image.Rect(0, 0, 40, 24))
	for y := 0; y < 24; y++ {
		for x := 0; x < 40; x++ {
			c := color.RGBA{200, 0, 0, 255}
			if x >= 20 {
				c = color.RGBA{0, 0, 200, 255}
			}
			if y < 6 {
				c = color.RGBA{0, 200, 0, 255}
			}
			m.SetRGBA(x, y, c)
		}
	}
	var in bytes.Buffer
	png.Encode(&in, m)
	run := func(caption string) image.Image {
		out, _, err := EditImage(in.Bytes(), parseOps(caption))
		if err != nil {
			t.Fatalf("%s: %v", caption, err)
		}
		im, err := jpeg.Decode(bytes.NewReader(out))
		if err != nil {
			t.Fatal(err)
		}
		return im
	}
	near := func(c color.Color, r, g, b int) bool {
		cr, cg, cb, _ := c.RGBA()
		d := func(a uint32, w int) bool { x := int(a>>8) - w; return x > -40 && x < 40 }
		return d(cr, r) && d(cg, g) && d(cb, b)
	}
	if sq := run("crop square"); sq.Bounds().Dx() != 24 || sq.Bounds().Dy() != 24 {
		t.Errorf("square: %v", sq.Bounds())
	}
	if f := run("flip vertically"); !near(f.At(5, 21), 0, 200, 0) || !near(f.At(5, 2), 200, 0, 0) {
		t.Errorf("vflip: top row should now be at the bottom")
	}
	if m := run("flip"); !near(m.At(5, 12), 0, 0, 200) {
		t.Errorf("plain flip must stay a left-right mirror")
	}
	if iv := run("invert colors"); !near(iv.At(5, 12), 55, 255, 255) {
		t.Errorf("invert: %v", iv.At(5, 12))
	}
	if ops := parseOps("blur it"); len(ops) != 1 || ops[0].name != "blur" {
		t.Errorf("blur parse: %v", ops)
	}
	if _, _, err := EditImage(in.Bytes(), parseOps("blur")); err != nil {
		t.Error(err)
	}
	if d := describeOps(parseOps("crop square, invert, blur, flip upside down")); !strings.Contains(d, "centred square") || !strings.Contains(d, "inverted") || !strings.Contains(d, "blur") || !strings.Contains(d, "upside down") {
		t.Errorf("describe: %q", d)
	}
}

func TestPhotoOverlayCropConvertCollage(t *testing.T) {
	m := image.NewRGBA(image.Rect(0, 0, 400, 300))
	for y := 0; y < 300; y++ {
		for x := 0; x < 400; x++ {
			m.SetRGBA(x, y, color.RGBA{uint8(x / 2), uint8(y), 90, 255})
		}
	}
	var b bytes.Buffer
	jpeg.Encode(&b, m, nil)
	src := b.Bytes()
	dim := func(caption string) (string, []byte) {
		ops := parseOps(caption)
		if len(ops) == 0 {
			t.Fatalf("no ops from %q", caption)
		}
		out, d, err := EditImage(src, ops)
		if err != nil {
			t.Fatal(caption, err)
		}
		return d, out
	}
	if d, _ := dim("crop to 16:9"); d != "400x225" {
		t.Fatalf("16:9 got %s", d)
	}
	if d, _ := dim("crop to 1:1"); d != "300x300" {
		t.Fatalf("1:1 got %s", d)
	}
	if d, _ := dim("crop centre 50%"); d != "200x150" {
		t.Fatalf("pct got %s", d)
	}
	if d, _ := dim(`caption: Hello`); d != "400x324" {
		t.Fatalf("caption band got %s", d)
	}
	if d, _ := dim(`watermark: (c) me`); d != "400x300" {
		t.Fatalf("watermark got %s", d)
	}
	_, png1 := dim("convert to PNG")
	if len(png1) < 8 || string(png1[1:4]) != "PNG" {
		t.Fatal("not a PNG")
	}
	// words inside the overlay text must not trigger other edits
	ops := parseOps(`sepia caption: invert blur rotate`)
	if len(ops) != 2 || ops[0].name == "invert" {
		t.Fatalf("overlay text leaked into ops: %+v", ops)
	}
	if got := cleanOverlayText("héllo\x00 wörld"); got != "hllo wrld" {
		t.Fatalf("clean %q", got)
	}
	out, d, err := Collage([][]byte{src, src, src})
	if err != nil || d != "984x984" || out[0] != 0xFF {
		t.Fatalf("collage %v %v", d, err)
	}
	if _, _, err := Collage([][]byte{src}); err == nil {
		t.Fatal("one picture collage accepted")
	}
}

// TGBOT_PHOTO=/path.jpg writes the new photo outputs to /tmp/pe_*.(jpg|png) for visual inspection.
func TestPhotoToolkitRealOutputs(t *testing.T) {
	p := os.Getenv("TGBOT_PHOTO")
	if p == "" {
		t.Skip("set TGBOT_PHOTO")
	}
	data, _ := os.ReadFile(p)
	for name, cap := range map[string]string{"crop169": "crop to 16:9", "crop60": "crop centre 60%", "caption": "caption: Grace Hopper, 1984", "watermark": "watermark: sample (c) test", "png": "convert to PNG, sepia"} {
		out, _, err := EditImage(data, parseOps(cap))
		if err != nil {
			t.Fatal(name, err)
		}
		ext := ".jpg"
		if out[0] == 0x89 {
			ext = ".png"
		}
		os.WriteFile("/tmp/pe_"+name+ext, out, 0o644)
	}
	c, _, err := Collage([][]byte{data, data, data, data})
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile("/tmp/pe_collage.jpg", c, 0o644)
}
