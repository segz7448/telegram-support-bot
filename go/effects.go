package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"regexp"
	"strconv"
	"strings"

	xdraw "golang.org/x/image/draw"
)

// Local photo effects (no AI, no network). Each edit is a fixed, bounded pixel operation. The original picture is
// never changed or stored: the result is a NEW picture. EXIF orientation is applied first so the result is upright,
// and the aspect ratio only changes when you ask for a rotation or a resize.

type imgOp struct {
	name string
	n    int // rotation degrees (clockwise), resize percent, or none
}

var (
	reBW       = regexp.MustCompile(`(?i)\b(black and white|black & white|b&w|b/w|grayscale|greyscale|monochrome)\b`)
	reSepia    = regexp.MustCompile(`(?i)\bsepia\b`)
	reBrighter = regexp.MustCompile(`(?i)\b(brighter|brighten|lighter|lighten|increase brightness)\b`)
	reDarker   = regexp.MustCompile(`(?i)\b(darker|darken|dim|decrease brightness)\b`)
	reMoreCon  = regexp.MustCompile(`(?i)\b(more contrast|increase contrast|higher contrast|contrast up)\b`)
	reLessCon  = regexp.MustCompile(`(?i)\b(less contrast|decrease contrast|lower contrast|reduce contrast|contrast down)\b`)
	reRotate   = regexp.MustCompile(`(?i)\brotate\s*(left|right|180|90|270)?\s*(?:by\s*)?(90|180|270)?\b|\bturn\s+(left|right|around)\b`)
	reResize   = regexp.MustCompile(`(?i)\b(?:resize|scale|shrink|make (?:it )?smaller)\b[^0-9%a-z]*(?:to |by )?(\d{1,3})\s*%|\b(half size|half the size)\b`)
	reFlip     = regexp.MustCompile(`(?i)\b(mirror|flip)\b`)
)

// parseOps reads the caption. It returns the ops in a fixed safe order and a short list of what it understood.
func parseOps(caption string) []imgOp {
	var ops []imgOp
	if m := reRotate.FindStringSubmatch(caption); m != nil {
		deg := 90
		w := strings.ToLower(m[1] + m[2] + m[3])
		switch {
		case strings.Contains(w, "180") || strings.Contains(w, "around"):
			deg = 180
		case strings.Contains(w, "270") || strings.Contains(w, "left"):
			deg = 270
		}
		ops = append(ops, imgOp{"rotate", deg})
	}
	if m := reResize.FindStringSubmatch(caption); m != nil {
		pct := 50
		if m[1] != "" {
			pct, _ = strconv.Atoi(m[1])
		}
		if pct >= 10 && pct <= 200 {
			ops = append(ops, imgOp{"resize", pct})
		}
	}
	if reFlip.MatchString(caption) {
		ops = append(ops, imgOp{"mirror", 0})
	}
	switch {
	case reBW.MatchString(caption):
		ops = append(ops, imgOp{"bw", 0})
	case reSepia.MatchString(caption):
		ops = append(ops, imgOp{"sepia", 0})
	}
	if reBrighter.MatchString(caption) {
		ops = append(ops, imgOp{"bright", 30})
	} else if reDarker.MatchString(caption) {
		ops = append(ops, imgOp{"bright", -30})
	}
	if reMoreCon.MatchString(caption) {
		ops = append(ops, imgOp{"contrast", 130})
	} else if reLessCon.MatchString(caption) {
		ops = append(ops, imgOp{"contrast", 75})
	}
	if reSharpen.MatchString(caption) {
		ops = append(ops, imgOp{"sharpen", 0})
	}
	return ops
}

func describeOps(ops []imgOp) string {
	var p []string
	for _, o := range ops {
		switch o.name {
		case "rotate":
			p = append(p, fmt.Sprintf("rotated %d° clockwise", o.n))
		case "resize":
			p = append(p, fmt.Sprintf("resized to %d%%", o.n))
		case "mirror":
			p = append(p, "mirrored left-right")
		case "bw":
			p = append(p, "black and white")
		case "sepia":
			p = append(p, "sepia")
		case "bright":
			if o.n > 0 {
				p = append(p, "brighter (+30)")
			} else {
				p = append(p, "darker (-30)")
			}
		case "contrast":
			if o.n > 100 {
				p = append(p, "more contrast (x1.3)")
			} else {
				p = append(p, "less contrast (x0.75)")
			}
		case "sharpen":
			p = append(p, "sharpened (unsharp mask; cannot restore lost detail)")
		}
	}
	return strings.Join(p, ", ")
}

func EditImage(data []byte, ops []imgOp) ([]byte, string, error) {
	if len(data) == 0 || len(data) > maxImageBytes {
		return nil, "", fmt.Errorf("that image is too big (limit 8 MB)")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || (format != "jpeg" && format != "png") {
		return nil, "", fmt.Errorf("I can only edit JPEG or PNG pictures")
	}
	if cfg.Width < 16 || cfg.Height < 16 || cfg.Width > maxImageSide || cfg.Height > maxImageSide || cfg.Width*cfg.Height > maxImagePixels {
		return nil, "", fmt.Errorf("that picture is %dx%d; I handle up to 4 million pixels (about 2000x2000)", cfg.Width, cfg.Height)
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, "", fmt.Errorf("I couldn't read that picture")
	}
	b := src.Bounds()
	cur := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(cur, cur.Bounds(), src, b.Min, draw.Src)
	if format == "jpeg" {
		cur = orient(cur, exifOrientation(data)) // upright, as a viewer would show it
	}
	for _, o := range ops {
		switch o.name {
		case "rotate":
			cur = rotateRGBA(cur, o.n)
		case "mirror":
			cur = mirrorRGBA(cur)
		case "resize":
			w, h := cur.Bounds().Dx()*o.n/100, cur.Bounds().Dy()*o.n/100
			if w < 16 || h < 16 || w > maxImageSide || h > maxImageSide || w*h > maxImagePixels {
				return nil, "", fmt.Errorf("that resize would give %dx%d, outside what I handle", w, h)
			}
			dst := image.NewRGBA(image.Rect(0, 0, w, h))
			xdraw.CatmullRom.Scale(dst, dst.Bounds(), cur, cur.Bounds(), xdraw.Src, nil)
			cur = dst
		case "bw", "sepia", "bright", "contrast":
			pixelOp(cur, o)
		case "sharpen":
			cur = unsharp(cur, 2, 1.2)
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, cur, &jpeg.Options{Quality: 92}); err != nil {
		return nil, "", fmt.Errorf("I couldn't write the result")
	}
	return buf.Bytes(), fmt.Sprintf("%dx%d", cur.Bounds().Dx(), cur.Bounds().Dy()), nil
}

func pixelOp(m *image.RGBA, o imgOp) {
	cl := func(v float64) uint8 {
		if v < 0 {
			return 0
		}
		if v > 255 {
			return 255
		}
		return uint8(v + 0.5)
	}
	for i := 0; i+3 < len(m.Pix); i += 4 {
		r, g, b := float64(m.Pix[i]), float64(m.Pix[i+1]), float64(m.Pix[i+2])
		switch o.name {
		case "bw":
			y := 0.299*r + 0.587*g + 0.114*b
			r, g, b = y, y, y
		case "sepia":
			y := 0.299*r + 0.587*g + 0.114*b
			r, g, b = y*1.07+8, y*0.92+3, y*0.72
		case "bright":
			r, g, b = r+float64(o.n), g+float64(o.n), b+float64(o.n)
		case "contrast":
			f := float64(o.n) / 100
			r, g, b = (r-128)*f+128, (g-128)*f+128, (b-128)*f+128
		}
		m.Pix[i], m.Pix[i+1], m.Pix[i+2] = cl(r), cl(g), cl(b)
	}
}

func rotateRGBA(src *image.RGBA, deg int) *image.RGBA {
	w, h := src.Bounds().Dx(), src.Bounds().Dy()
	switch deg {
	case 180:
		d := image.NewRGBA(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				d.SetRGBA(w-1-x, h-1-y, src.RGBAAt(x, y))
			}
		}
		return d
	case 90: // clockwise
		d := image.NewRGBA(image.Rect(0, 0, h, w))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				d.SetRGBA(h-1-y, x, src.RGBAAt(x, y))
			}
		}
		return d
	case 270:
		d := image.NewRGBA(image.Rect(0, 0, h, w))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				d.SetRGBA(y, w-1-x, src.RGBAAt(x, y))
			}
		}
		return d
	}
	return src
}

func mirrorRGBA(src *image.RGBA) *image.RGBA {
	w, h := src.Bounds().Dx(), src.Bounds().Dy()
	d := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			d.SetRGBA(w-1-x, y, src.RGBAAt(x, y))
		}
	}
	return d
}

// exifOrientation reads the JPEG EXIF orientation tag (1-8); 1 when absent or unreadable.
func exifOrientation(d []byte) int {
	if len(d) < 4 || d[0] != 0xFF || d[1] != 0xD8 {
		return 1
	}
	i := 2
	for i+4 < len(d) {
		if d[i] != 0xFF {
			return 1
		}
		mk := d[i+1]
		l := int(d[i+2])<<8 | int(d[i+3])
		if mk == 0xE1 && i+4+l-2 <= len(d) && l > 14 && string(d[i+4:i+10]) == "Exif\x00\x00" {
			t := d[i+10 : i+2+l]
			if len(t) < 12 {
				return 1
			}
			var u16 func([]byte) int
			var u32 func([]byte) int
			if t[0] == 'I' {
				u16 = func(b []byte) int { return int(b[0]) | int(b[1])<<8 }
				u32 = func(b []byte) int { return int(b[0]) | int(b[1])<<8 | int(b[2])<<16 | int(b[3])<<24 }
			} else {
				u16 = func(b []byte) int { return int(b[0])<<8 | int(b[1]) }
				u32 = func(b []byte) int { return int(b[0])<<24 | int(b[1])<<16 | int(b[2])<<8 | int(b[3]) }
			}
			off := u32(t[4:8])
			if off < 0 || off+2 > len(t) {
				return 1
			}
			n := u16(t[off : off+2])
			for k := 0; k < n && off+2+12*(k+1) <= len(t); k++ {
				e := t[off+2+12*k : off+2+12*(k+1)]
				if u16(e[0:2]) == 0x0112 {
					if v := u16(e[8:10]); v >= 1 && v <= 8 {
						return v
					}
				}
			}
			return 1
		}
		if mk == 0xDA || mk == 0xD9 {
			return 1
		}
		i += 2 + l
	}
	return 1
}

func orient(m *image.RGBA, o int) *image.RGBA {
	switch o {
	case 2:
		return mirrorRGBA(m)
	case 3:
		return rotateRGBA(m, 180)
	case 4:
		return rotateRGBA(mirrorRGBA(m), 180)
	case 5:
		return mirrorRGBA(rotateRGBA(m, 90))
	case 6:
		return rotateRGBA(m, 90)
	case 7:
		return mirrorRGBA(rotateRGBA(m, 270))
	case 8:
		return rotateRGBA(m, 270)
	}
	return m
}

var _ = color.RGBA{}
