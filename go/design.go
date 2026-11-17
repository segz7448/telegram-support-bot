package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goitalic"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

// Design draws fixed templates (quote card, poster, bar/line/pie chart) from the user's own words and numbers with
// the built-in Go fonts. It is a small template designer, not Canva, and it has no AI: layout is arithmetic.
// ok=false: not a design request. errMsg != "" with ok=true: a design request that could not be drawn.

type theme struct{ top, bottom, text, accent, muted color.RGBA }

var themes = map[string]theme{
	"dark":   {rgb(0x14, 0x18, 0x2b), rgb(0x2b, 0x1f, 0x4a), rgb(0xf5, 0xf5, 0xfa), rgb(0xff, 0xb4, 0x00), rgb(0xb8, 0xbc, 0xd4)},
	"blue":   {rgb(0x0a, 0x3d, 0x91), rgb(0x1e, 0x88, 0xe5), rgb(0xff, 0xff, 0xff), rgb(0xff, 0xd5, 0x4f), rgb(0xd0, 0xe4, 0xff)},
	"green":  {rgb(0x0b, 0x4f, 0x3a), rgb(0x2e, 0x9e, 0x6b), rgb(0xff, 0xff, 0xff), rgb(0xf9, 0xf0, 0x7a), rgb(0xd6, 0xf2, 0xe3)},
	"sunset": {rgb(0xb2, 0x2a, 0x4e), rgb(0xf5, 0x8a, 0x3c), rgb(0xff, 0xff, 0xff), rgb(0xff, 0xf1, 0xb3), rgb(0xff, 0xe0, 0xd0)},
	"light":  {rgb(0xfa, 0xf7, 0xf2), rgb(0xe6, 0xec, 0xf5), rgb(0x1b, 0x1f, 0x3a), rgb(0xd8, 0x3b, 0x5a), rgb(0x5b, 0x62, 0x80)},
	"mono":   {rgb(0x11, 0x11, 0x11), rgb(0x3a, 0x3a, 0x3a), rgb(0xff, 0xff, 0xff), rgb(0xe0, 0xe0, 0xe0), rgb(0xa0, 0xa0, 0xa0)},
}

var palette = []color.RGBA{rgb(0x2e, 0x7d, 0xd2), rgb(0xe5, 0x5b, 0x3c), rgb(0x2e, 0x9e, 0x6b), rgb(0xf2, 0xb1, 0x34), rgb(0x8a, 0x5c, 0xd6), rgb(0x1b, 0xa6, 0xb8), rgb(0xd8, 0x3b, 0x8a), rgb(0x6c, 0x7a, 0x89)}

func rgb(r, g, b uint8) color.RGBA { return color.RGBA{r, g, b, 255} }

var reDesign = regexp.MustCompile(`(?is)^\s*(?:make|create|design|draw)?\s*(?:me\s+)?(?:a\s+|an\s+)?(quote card|quote|poster|banner|bar chart|line chart|pie chart|chart)\s*[:\-]\s*(.+)$`)
var reTheme = regexp.MustCompile(`(?i)\b(?:theme|style|colou?rs?)\s*[:=]\s*([a-z]+)`)
var reTitleOpt = regexp.MustCompile(`(?i)\btitle\s*[:=]\s*([^;\n]+)`)

type fontSet struct{ reg, bold, ital *opentype.Font }

var fonts = func() *fontSet {
	p := func(b []byte) *opentype.Font {
		f, err := opentype.Parse(b)
		if err != nil {
			panic(err)
		}
		return f
	}
	return &fontSet{p(goregular.TTF), p(gobold.TTF), p(goitalic.TTF)}
}()

func face(f *opentype.Font, size float64) font.Face {
	fc, err := opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		panic(err)
	}
	return fc
}

func textW(fc font.Face, s string) int { return font.MeasureString(fc, s).Ceil() }

func drawText(img *image.RGBA, fc font.Face, c color.Color, x, y int, s string) {
	d := &font.Drawer{Dst: img, Src: image.NewUniform(c), Face: fc, Dot: fixed.P(x, y)}
	d.DrawString(s)
}

func wrap(fc font.Face, s string, maxW int) []string {
	var lines []string
	cur := ""
	for _, w := range strings.Fields(s) {
		try := w
		if cur != "" {
			try = cur + " " + w
		}
		if textW(fc, try) <= maxW || cur == "" {
			cur = try
		} else {
			lines = append(lines, cur)
			cur = w
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

// fit picks the largest size from hi down to lo whose wrapped text fits boxW x boxH.
func fit(f *opentype.Font, s string, boxW, boxH int, hi, lo float64) (font.Face, []string, int) {
	for sz := hi; sz >= lo; sz -= 2 {
		fc := face(f, sz)
		ls := wrap(fc, s, boxW)
		lh := int(sz * 1.3)
		if len(ls)*lh <= boxH {
			ok := true
			for _, l := range ls {
				if textW(fc, l) > boxW {
					ok = false
				}
			}
			if ok {
				return fc, ls, lh
			}
		}
	}
	fc := face(f, lo)
	ls := wrap(fc, s, boxW)
	lh := int(lo * 1.3)
	if max := boxH / lh; len(ls) > max && max > 0 {
		ls = ls[:max]
		ls[max-1] = strings.TrimRight(ls[max-1], " .,") + "..."
	}
	return fc, ls, lh
}

func gradient(w, h int, th theme) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		t := float64(y) / float64(h-1)
		c := color.RGBA{lerp(th.top.R, th.bottom.R, t), lerp(th.top.G, th.bottom.G, t), lerp(th.top.B, th.bottom.B, t), 255}
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

func lerp(a, b uint8, t float64) uint8 { return uint8(float64(a) + (float64(b)-float64(a))*t) }

func rect(img *image.RGBA, x0, y0, x1, y1 int, c color.Color) {
	draw.Draw(img, image.Rect(x0, y0, x1, y1), image.NewUniform(c), image.Point{}, draw.Src)
}

func encode(img image.Image) ([]byte, error) {
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func centerText(img *image.RGBA, fc font.Face, c color.Color, cx, y int, s string) {
	drawText(img, fc, c, cx-textW(fc, s)/2, y, s)
}

func cleanDesignText(s string) string {
	return strings.Join(strings.Fields(strings.NewReplacer("\u201c", "\"", "\u201d", "\"", "\u2018", "'", "\u2019", "'", "\u2014", "-", "\u2013", "-").Replace(s)), " ")
}

func Design(text string) (png []byte, caption string, errMsg string, ok bool) {
	m := reDesign.FindStringSubmatch(text)
	if m == nil {
		return nil, "", "", false
	}
	kind := strings.ToLower(m[1])
	body := m[2]
	th := themes["dark"]
	name := "dark"
	if tm := reTheme.FindStringSubmatch(body); tm != nil {
		n := strings.ToLower(tm[1])
		t, found := themes[n]
		if !found {
			return nil, "", "Unknown theme \"" + n + "\". Themes: dark, blue, green, sunset, light, mono.", true
		}
		th, name = t, n
		body = strings.TrimSpace(reTheme.ReplaceAllString(body, ""))
		body = strings.Trim(body, " ,;")
	}
	if len([]rune(body)) > 600 {
		return nil, "", "That is too long for a design (limit 600 characters).", true
	}
	body = cleanDesignText(body)
	var img *image.RGBA
	var err string
	switch kind {
	case "quote card", "quote":
		img, err = drawQuote(body, th)
	case "poster", "banner":
		img, err = drawPoster(body, th, kind == "banner")
	default:
		img, err = drawChart(kind, body, th)
	}
	if err != "" {
		return nil, "", err, true
	}
	b, e := encode(img)
	if e != nil {
		return nil, "", "I couldn't render that image.", true
	}
	return b, kind + " (" + name + " theme), drawn from your text with built-in templates", "", true
}

func drawQuote(body string, th theme) (*image.RGBA, string) {
	quote, author := body, ""
	if i := strings.LastIndex(body, " - "); i > 0 && len(body)-i < 60 {
		quote, author = strings.TrimSpace(body[:i]), strings.TrimSpace(body[i+3:])
	} else if i := strings.LastIndex(body, " ~ "); i > 0 {
		quote, author = strings.TrimSpace(body[:i]), strings.TrimSpace(body[i+3:])
	}
	quote = strings.Trim(quote, "\"' ")
	if quote == "" {
		return nil, "Give me the quote: quote card: Your words here - Author"
	}
	const W, H = 1080, 1080
	img := gradient(W, H, th)
	rect(img, 96, 150, 176, 160, th.accent)
	big := face(fonts.bold, 220)
	drawText(img, big, th.accent, 90, 330, "\u201c")
	fc, ls, lh := fit(fonts.ital, quote, W-2*110, 520, 66, 30)
	y := 400
	for _, l := range ls {
		drawText(img, fc, th.text, 110, y, l)
		y += lh
	}
	if author != "" {
		rect(img, 110, H-210, 190, H-204, th.accent)
		drawText(img, face(fonts.bold, 38), th.text, 110, H-150, author)
	}
	return img, ""
}

func drawPoster(body string, th theme, banner bool) (*image.RGBA, string) {
	parts := strings.Split(body, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	if parts[0] == "" {
		return nil, "Give me a title: poster: Title | Subtitle | Footer"
	}
	W, H := 1080, 1350
	if banner {
		W, H = 1500, 500
	}
	img := gradient(W, H, th)
	pad := 90
	rect(img, 0, 0, 24, H, th.accent)
	tb := H/2 - 40
	hi := 120.0
	if banner {
		pad = 70
		tb = 280
		hi = 84
	}
	fc, ls, lh := fit(fonts.bold, parts[0], W-2*pad, tb-pad, hi, 36)
	y := pad + lh
	for _, l := range ls {
		drawText(img, fc, th.text, pad, y, l)
		y += lh
	}
	rect(img, pad, y, pad+140, y+10, th.accent)
	y += 70
	if banner {
		y -= 15
	}
	if len(parts) > 1 && parts[1] != "" {
		reserve := 200
		sz := 54.0
		if banner {
			reserve, sz = 70, 40
		}
		sfc, sls, slh := fit(fonts.reg, parts[1], W-2*pad, H-y-reserve, sz, 24)
		for _, l := range sls {
			drawText(img, sfc, th.muted, pad, y, l)
			y += slh
		}
	}
	if len(parts) > 2 && parts[2] != "" {
		ffc := face(fonts.bold, 36)
		if banner {
			drawText(img, ffc, th.accent, W-pad-textW(ffc, parts[2]), H-45, parts[2])
		} else {
			drawText(img, ffc, th.accent, pad, H-70, parts[2])
		}
	}
	return img, ""
}

var reKV = regexp.MustCompile(`([^=,:;]+?)\s*[=:]\s*(-?\d+(?:\.\d+)?)`)

func parseSeries(body string) (title string, labels []string, vals []float64, err string) {
	if tm := reTitleOpt.FindStringSubmatch(body); tm != nil {
		title = strings.TrimSpace(tm[1])
		body = strings.TrimSpace(strings.Replace(body, tm[0], "", 1))
	}
	if m := reKV.FindAllStringSubmatch(body, -1); len(m) >= 2 {
		for _, x := range m {
			v, _ := strconv.ParseFloat(x[2], 64)
			labels = append(labels, strings.Trim(strings.TrimSpace(x[1]), ",;"))
			vals = append(vals, v)
		}
	} else {
		for i, s := range reNumC.FindAllString(body, -1) {
			v, _ := strconv.ParseFloat(s, 64)
			labels = append(labels, strconv.Itoa(i+1))
			vals = append(vals, v)
		}
	}
	for _, v := range vals {
		if math.IsInf(v, 0) || math.IsNaN(v) || math.Abs(v) > 1e12 {
			return "", nil, nil, "A value is too large to chart safely (limit 1e12)."
		}
	}
	if len(vals) < 2 {
		return "", nil, nil, "I need at least 2 values. Example: bar chart: Mon=5, Tue=8, Wed=3 title: Sales"
	}
	if len(vals) > 20 {
		return "", nil, nil, "That is too many values (limit 20)."
	}
	return
}

func niceMax(v float64) float64 {
	if v <= 0 {
		return 1
	}
	e := math.Pow(10, math.Floor(math.Log10(v)))
	for _, m := range []float64{1, 2, 2.5, 5, 10} {
		if v <= m*e {
			return m * e
		}
	}
	return 10 * e
}

func drawLine(img *image.RGBA, x0, y0, x1, y1, w int, c color.Color) {
	dx, dy := float64(x1-x0), float64(y1-y0)
	n := int(math.Max(math.Abs(dx), math.Abs(dy)))
	for i := 0; i <= n; i++ {
		t := 0.0
		if n > 0 {
			t = float64(i) / float64(n)
		}
		disc(img, int(float64(x0)+dx*t), int(float64(y0)+dy*t), w/2, c)
	}
}

func disc(img *image.RGBA, cx, cy, r int, c color.Color) {
	for y := -r; y <= r; y++ {
		for x := -r; x <= r; x++ {
			if x*x+y*y <= r*r {
				img.Set(cx+x, cy+y, c)
			}
		}
	}
}

func drawChart(kind, body string, th theme) (*image.RGBA, string) {
	title, labels, vals, err := parseSeries(body)
	if err != "" {
		return nil, err
	}
	const W, H = 1200, 800
	img := gradient(W, H, th)
	if kind == "pie chart" {
		return drawPie(img, title, labels, vals, th)
	}
	lo, hi := 0.0, 0.0
	for _, v := range vals {
		hi = math.Max(hi, v)
		lo = math.Min(lo, v)
	}
	if lo < 0 {
		return nil, "Bar and line charts here need values of 0 or more."
	}
	top := niceMax(hi)
	left, right, up, down := 130, 50, 120, 120
	pw, ph := W-left-right, H-up-down
	if title != "" {
		drawText(img, face(fonts.bold, 44), th.text, left, 75, title)
	}
	gf := face(fonts.reg, 24)
	for i := 0; i <= 5; i++ {
		y := up + ph - ph*i/5
		rect(img, left, y, W-right, y+1, th.muted)
		lbl := fnum(top * float64(i) / 5)
		drawText(img, gf, th.muted, left-textW(gf, lbl)-14, y+8, lbl)
	}
	n := len(vals)
	slot := float64(pw) / float64(n)
	lf := face(fonts.reg, 22)
	var pts [][2]int
	for i, v := range vals {
		cx := left + int(slot*(float64(i)+0.5))
		h := int(float64(ph) * v / top)
		col := palette[i%len(palette)]
		if kind == "bar chart" || kind == "chart" {
			bw := int(slot * 0.62)
			rect(img, cx-bw/2, up+ph-h, cx+bw/2, up+ph, col)
			vl := fnum(v)
			centerText(img, face(fonts.bold, 22), th.text, cx, up+ph-h-10, vl)
		} else {
			pts = append(pts, [2]int{cx, up + ph - h})
		}
		lab := labels[i]
		if len(lab) > 12 {
			lab = lab[:11] + "~"
		}
		for textW(lf, lab) > int(slot)-6 && len(lab) > 3 {
			lab = lab[:len(lab)-2] + "~"
		}
		centerText(img, lf, th.text, cx, up+ph+36, lab)
	}
	if len(pts) > 0 {
		for i := 1; i < len(pts); i++ {
			drawLine(img, pts[i-1][0], pts[i-1][1], pts[i][0], pts[i][1], 6, th.accent)
		}
		for i, p := range pts {
			disc(img, p[0], p[1], 9, th.text)
			disc(img, p[0], p[1], 5, th.accent)
			centerText(img, face(fonts.bold, 22), th.text, p[0], p[1]-18, fnum(vals[i]))
		}
	}
	rect(img, left, up+ph, W-right, up+ph+3, th.text)
	return img, ""
}

func drawPie(img *image.RGBA, title string, labels []string, vals []float64, th theme) (*image.RGBA, string) {
	total := 0.0
	for _, v := range vals {
		if v < 0 {
			return nil, "Pie charts need values of 0 or more."
		}
		total += v
	}
	if total == 0 {
		return nil, "The values add up to 0, so there is nothing to draw."
	}
	if title != "" {
		drawText(img, face(fonts.bold, 44), th.text, 60, 75, title)
	}
	cx, cy, r := 340, 460, 270
	for y := -r; y <= r; y++ {
		for x := -r; x <= r; x++ {
			if x*x+y*y > r*r {
				continue
			}
			a := math.Atan2(float64(x), -float64(y)) // clockwise from 12 o'clock
			if a < 0 {
				a += 2 * math.Pi
			}
			frac := a / (2 * math.Pi) * total
			acc := 0.0
			for i, v := range vals {
				acc += v
				if frac < acc || i == len(vals)-1 {
					img.Set(cx+x, cy+y, palette[i%len(palette)])
					break
				}
			}
		}
	}
	lf := face(fonts.reg, 28)
	y := 200
	for i, v := range vals {
		rect(img, 700, y-22, 730, y+8, palette[i%len(palette)])
		lab := labels[i]
		if len(lab) > 18 {
			lab = lab[:17] + "~"
		}
		drawText(img, lf, th.text, 745, y, fmt.Sprintf("%s  %s%%", lab, fnum(math.Round(v/total*1000)/10)))
		y += 46
	}
	return img, ""
}
