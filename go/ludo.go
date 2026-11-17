package main

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// Ludo for two (you against the bot), our own simplified 2-player variant, described honestly in /ludo help:
// 52 track squares, each side has 4 tokens, a 6 brings a token out and gives another roll (three sixes in a row
// forfeit the turn), landing on an opponent token on a non-safe square sends it home, exact roll to finish.
// Dice come from crypto/rand for BOTH sides; the bot never sees a roll before it is made.
//
// Progress p of a token: -1 in base, 0..50 on the track (absolute square (start+p) mod 52), 51..55 home column,
// 56 home. Standard four-colour board; two players sit opposite: you are red (bottom), the bot is yellow (top). Starts: you at square 40, the bot at 14.
// Safe squares (no capture) are the four start squares and the four star squares: 1, 9, 14, 22, 27, 35, 40, 48.

type LudoSave struct {
	Level   int       `json:"level"`
	Tok     [2][4]int `json:"tok"`
	Roll    int       `json:"roll"` // pending human roll (0 = none rolled yet)
	Sixes   int       `json:"sixes"`
	Over    bool      `json:"over"`
	Result  string    `json:"result,omitempty"`
	Turns   int       `json:"turns"`
	LastMsg string    `json:"-"`
}

type ludoFile struct {
	Games map[string]*LudoSave `json:"games"`
}

type Ludo struct {
	Path string
	mu   sync.Mutex
	Dice func() int // test hook; nil = crypto/rand
}

func (l *Ludo) Prefix() string { return "ld:" }

var ludoStart = [2]int{40, 14}
var ludoSafe = map[int]bool{1: true, 9: true, 14: true, 22: true, 27: true, 35: true, 40: true, 48: true}

func (l *Ludo) die() int {
	if l.Dice != nil {
		return l.Dice()
	}
	n, err := rand.Int(rand.Reader, big.NewInt(6))
	if err != nil {
		return 1
	}
	return int(n.Int64()) + 1
}

func ludoAbs(side, p int) int { return (ludoStart[side] + p) % 52 }

// legal returns the token indexes the side can move with this roll.
func (g *LudoSave) legal(side, roll int) []int {
	var out []int
	for i, p := range g.Tok[side] {
		switch {
		case p == -1:
			if roll == 6 {
				out = append(out, i)
			}
		case p+roll <= 56:
			out = append(out, i)
		}
	}
	return out
}

// move applies the move; returns whether an opponent token was captured.
func (g *LudoSave) move(side, i, roll int) bool {
	p := g.Tok[side][i]
	if p == -1 {
		g.Tok[side][i] = 0
		p = 0
	} else {
		p += roll
		g.Tok[side][i] = p
	}
	captured := false
	if p <= 50 && !ludoSafe[ludoAbs(side, p)] {
		for j, q := range g.Tok[1-side] {
			if q >= 0 && q <= 50 && ludoAbs(1-side, q) == ludoAbs(side, p) {
				g.Tok[1-side][j] = -1
				captured = true
			}
		}
	}
	return captured
}

func (g *LudoSave) won(side int) bool {
	for _, p := range g.Tok[side] {
		if p != 56 {
			return false
		}
	}
	return true
}

// exposure: how many opponent tokens could land on this absolute square with a single die roll (not safe squares).
func (g *LudoSave) exposed(side, p int) float64 {
	if p < 0 || p > 50 || ludoSafe[ludoAbs(side, p)] {
		return 0
	}
	a := ludoAbs(side, p)
	n := 0
	for _, q := range g.Tok[1-side] {
		if q < 0 {
			continue
		}
		for r := 1; r <= 6; r++ {
			if q+r <= 50 && ludoAbs(1-side, q+r) == a {
				n++
				break
			}
		}
	}
	return float64(n) / 6
}

func (g *LudoSave) eval(side int) float64 {
	s := 0.0
	for _, side2 := range []int{side, 1 - side} {
		sign := 1.0
		if side2 != side {
			sign = -1
		}
		for _, p := range g.Tok[side2] {
			switch {
			case p == -1:
			case p == 56:
				s += sign * 70
			default:
				s += sign * (float64(p) + 1.5)
				if p > 50 || ludoSafe[ludoAbs(side2, p)] {
					s += sign * 3
				}
				s -= sign * 18 * g.exposed(side2, p) * float64(p+1) / 20
			}
		}
	}
	return s
}

func (g *LudoSave) clone() *LudoSave { c := *g; return &c }

// choose picks a token for the bot. Level 1: random. Level 2: one-ply heuristic. Level 3: also averages the human
// reply over the six dice (their best reply by the same heuristic).
func (l *Ludo) choose(g *LudoSave, side, roll, level int, legal []int) int {
	if level <= 1 {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(legal))))
		return legal[int(n.Int64())]
	}
	best, bestV := legal[0], -1e18
	for _, i := range legal {
		h := g.clone()
		cap := h.move(side, i, roll)
		v := h.eval(side)
		if cap {
			v += 12
		}
		if roll == 6 {
			v += 2
		}
		if level >= 3 {
			e := 0.0
			for r := 1; r <= 6; r++ {
				lg := h.legal(1-side, r)
				if len(lg) == 0 {
					e += h.eval(side)
					continue
				}
				worst := 1e18
				for _, j := range lg {
					h2 := h.clone()
					c2 := h2.move(1-side, j, r)
					w := h2.eval(side)
					if c2 {
						w -= 12
					}
					if w < worst {
						worst = w
					}
				}
				e += worst
			}
			v = 0.5*v + 0.5*(e/6)
			if cap {
				v += 6
			}
		}
		if v > bestV {
			best, bestV = i, v
		}
	}
	return best
}

func tokName(i int) string { return strconv.Itoa(i + 1) }

// botTurn plays the bot's whole turn (with extra rolls on a six) and returns the story.
func (l *Ludo) botTurn(g *LudoSave) string {
	var story []string
	sixes := 0
	for {
		r := l.die()
		lg := g.legal(1, r)
		if r == 6 {
			sixes++
			if sixes == 3 {
				story = append(story, "I rolled a third 6 in a row, so my turn is lost.")
				break
			}
		}
		if len(lg) == 0 {
			story = append(story, fmt.Sprintf("I rolled %d: no legal move.", r))
		} else {
			i := l.choose(g, 1, r, g.Level, lg)
			cap := g.move(1, i, r)
			t := fmt.Sprintf("I rolled %d and moved token %s", r, tokName(i))
			if cap {
				t += ", capturing one of yours"
			}
			story = append(story, t+".")
			if g.won(1) {
				g.Over, g.Result = true, "I win: all four of my tokens are home."
				break
			}
		}
		if r != 6 {
			break
		}
	}
	return strings.Join(story, " ")
}

var (
	reLudoNew = regexp.MustCompile(`(?i)^(?:play\s+|start\s+|new\s+)?ludo(?:\s+game)?(?:\s+(?:at\s+)?level\s+([1-3]))?\s*[.!]?$`)
)

func (l *Ludo) load() (*ludoFile, error) {
	f := &ludoFile{Games: map[string]*LudoSave{}}
	if _, err := loadJSON(l.Path, f); err != nil {
		return nil, fmt.Errorf("corrupt")
	}
	if f.Games == nil {
		f.Games = map[string]*LudoSave{}
	}
	return f, nil
}

const ludoHelp = "Ludo on the standard cross-shaped board, two players sitting opposite (you red at the bottom, me yellow at the top). Rule choices, since Ludo has many house variants: 4 tokens each; you need a 6 to bring a token out onto your start square; any 6 gives another roll, and three 6s in a row lose the turn; a roll must be used if any token can move (you pick which); landing on my lone token sends it back to base, except on a safe square (the four start squares and the four star squares); a capture or reaching home gives no extra roll; tokens of one colour may share a square; there are no blockades; you need the exact roll to enter home. First to get all four tokens home wins. Dice come from the system's secure random generator for both of us, and I never see your roll before it is made. Say \"ludo\" or \"ludo level 2\" (levels 1-3), then tap Roll or say \"roll\". My level 1 moves randomly, level 2 uses a one-step scoring rule, level 3 also averages over your possible replies; these are baseline heuristics. Also: ludo board, ludo resign."

func (l *Ludo) buttons(g *LudoSave) string {
	type btn struct {
		T string `json:"text"`
		D string `json:"callback_data"`
	}
	var row []btn
	if g.Roll == 0 {
		row = []btn{{"🎲 Roll", fmt.Sprintf("ld:roll:%d", g.Turns)}}
	} else {
		for _, i := range g.legal(0, g.Roll) {
			row = append(row, btn{"Token " + tokName(i), fmt.Sprintf("ld:t%d:%d", i, g.Turns)})
		}
	}
	j, _ := json.Marshal(map[string]any{"inline_keyboard": [][]btn{row}})
	return string(j)
}

func (l *Ludo) reply(g *LudoSave, msg string) *ChessReply {
	rep := &ChessReply{Text: msg, PNG: renderLudo(g)}
	if !g.Over {
		rep.Markup = l.buttons(g)
	}
	return rep
}

func (g *LudoSave) tokenSummary() string {
	var h, b int
	for _, p := range g.Tok[0] {
		if p == 56 {
			h++
		}
	}
	for _, p := range g.Tok[1] {
		if p == 56 {
			b++
		}
	}
	return fmt.Sprintf("Home: you %d/4, me %d/4.", h, b)
}

// doRoll rolls for the human and plays the consequences until the human must choose or roll again.
func (l *Ludo) doRoll(g *LudoSave) string {
	r := l.die()
	g.Sixes++
	if r != 6 {
		g.Sixes = 0
	}
	msg := fmt.Sprintf("You rolled %d.", r)
	if g.Sixes == 3 {
		g.Sixes = 0
		msg += " Third 6 in a row: your turn is lost. " + l.botTurn(g)
		g.Turns++
		return msg + " " + g.tokenSummary() + " Roll when ready."
	}
	lg := g.legal(0, r)
	if len(lg) == 0 {
		msg += " No legal move. " + l.botTurn(g)
		g.Turns++
		return msg + " " + g.tokenSummary() + " Roll when ready."
	}
	g.Roll = r
	if len(lg) == 1 {
		return l.afterPick(g, lg[0], msg+" Only one move: token "+tokName(lg[0])+".")
	}
	g.Turns++
	return msg + " Pick a token."
}

func (l *Ludo) afterPick(g *LudoSave, i int, msg string) string {
	r := g.Roll
	g.Roll = 0
	cap := g.move(0, i, r)
	if !strings.Contains(msg, "Only one") {
		msg += fmt.Sprintf(" You moved token %s.", tokName(i))
	}
	if cap {
		msg += " You captured one of my tokens!"
	}
	if g.won(0) {
		g.Over, g.Result = true, "You win: all four of your tokens are home."
		return msg + " " + g.Result
	}
	if r == 6 {
		g.Turns++
		return msg + " You rolled a 6, so roll again. " + g.tokenSummary()
	}
	g.Sixes = 0
	msg += " " + l.botTurn(g)
	g.Turns++
	if g.Over {
		return msg + " " + g.Result
	}
	return msg + " " + g.tokenSummary() + " Roll when ready."
}

func (l *Ludo) Handle(chat, text string) (*ChessReply, bool) {
	t := strings.TrimSpace(text)
	low := strings.ToLower(t)
	m := reLudoNew.FindStringSubmatch(t)
	cmd := low == "ludo help" || low == "ludo board" || low == "ludo resign" || low == "roll" || low == "roll dice" || low == "roll the dice"
	if m == nil && !cmd {
		return nil, false
	}
	if l == nil || l.Path == "" {
		return &ChessReply{Text: "Ludo is switched off: the owner has not set a save file for it."}, true
	}
	if low == "ludo help" {
		return &ChessReply{Text: ludoHelp}, true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	f, err := l.load()
	if err != nil {
		return &ChessReply{Text: "I can't read my Ludo save file, so I won't touch it. Please tell the owner."}, true
	}
	g := f.Games[chat]
	save := func() bool { return saveJSON(l.Path, f) == nil }
	if m != nil {
		if g != nil && !g.Over && g.Turns > 0 {
			return l.reply(g, "You already have a game going. Roll, or say \"ludo resign\" first."), true
		}
		if g == nil && len(f.Games) >= 100 {
			return &ChessReply{Text: "I can't start a new Ludo game right now (too many saved games)."}, true
		}
		ng := &LudoSave{Level: 2}
		if m[1] != "" {
			ng.Level, _ = strconv.Atoi(m[1])
		} else if g != nil && g.Level > 0 {
			ng.Level = g.Level
		}
		for s := 0; s < 2; s++ {
			for i := 0; i < 4; i++ {
				ng.Tok[s][i] = -1
			}
		}
		f.Games[chat] = ng
		if !save() {
			return &ChessReply{Text: "I couldn't save the game, so it is not started."}, true
		}
		return l.reply(ng, fmt.Sprintf("New Ludo game, level %d. You are red (bottom-left start), I'm blue. Roll the dice.", ng.Level)), true
	}
	if g == nil || g.Over {
		return nil, false
	}
	switch low {
	case "ludo board":
		return l.reply(g, "Your move."), true
	case "ludo resign":
		g.Over, g.Result = true, "You resigned. I win."
		save()
		return l.reply(g, g.Result), true
	}
	if g.Roll != 0 {
		return l.reply(g, fmt.Sprintf("You rolled %d. Pick a token with the buttons.", g.Roll)), true
	}
	msg := l.doRoll(g)
	if !save() {
		return &ChessReply{Text: "I couldn't save the game, so that roll was NOT recorded."}, true
	}
	return l.reply(g, msg), true
}

func (l *Ludo) Tap(chat, data string) (string, string, *ChessReply) {
	p := strings.Split(data, ":")
	if len(p) != 3 || l == nil || l.Path == "" {
		return "", "", nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	f, err := l.load()
	if err != nil {
		return "I can't read my save file.", "", nil
	}
	g := f.Games[chat]
	if g == nil || g.Over {
		return "No game in progress. Say \"ludo\" to start.", "-", nil
	}
	if p[2] != fmt.Sprint(g.Turns) {
		return "That button is out of date. Use the newest board.", "-", nil
	}
	var msg string
	switch {
	case p[1] == "roll" && g.Roll == 0:
		msg = l.doRoll(g)
	case strings.HasPrefix(p[1], "t") && g.Roll != 0:
		i, e := strconv.Atoi(p[1][1:])
		ok := false
		for _, x := range g.legal(0, g.Roll) {
			if x == i {
				ok = true
			}
		}
		if e != nil || !ok {
			return "That token can't move with this roll.", "", nil
		}
		msg = l.afterPick(g, i, "")
		g.Turns++
	default:
		return "Nothing to do with that button now.", "", nil
	}
	if saveJSON(l.Path, f) != nil {
		return "I couldn't save, so that was NOT recorded.", "", nil
	}
	return "Done.", "-", l.reply(g, msg)
}

// ---- board picture: the standard 15x15 cross board ----

func ludoRing() [52][2]int {
	var r [52][2]int
	i := 0
	add := func(x, y int) { r[i] = [2]int{x, y}; i++ }
	for x := 0; x <= 5; x++ {
		add(x, 6)
	}
	for y := 5; y >= 0; y-- {
		add(6, y)
	}
	add(7, 0)
	add(8, 0)
	for y := 1; y <= 5; y++ {
		add(8, y)
	}
	for x := 9; x <= 14; x++ {
		add(x, 6)
	}
	add(14, 7)
	add(14, 8)
	for x := 13; x >= 9; x-- {
		add(x, 8)
	}
	for y := 9; y <= 14; y++ {
		add(8, y)
	}
	add(7, 14)
	add(6, 14)
	for y := 13; y >= 9; y-- {
		add(6, y)
	}
	for x := 5; x >= 0; x-- {
		add(x, 8)
	}
	add(0, 7)
	return r
}

// ludoPos returns the centre of a token in cell units (may be fractional for base slots).
func ludoPos(side, p, slot int, ring [52][2]int) (float64, float64) {
	switch {
	case p == -1:
		if side == 0 {
			return 1.5 + 2*float64(slot%2), 10.5 + 2*float64(slot/2)
		}
		return 10.5 + 2*float64(slot%2), 1.5 + 2*float64(slot/2)
	case p <= 50:
		c := ring[ludoAbs(side, p)]
		return float64(c[0]) + 0.5, float64(c[1]) + 0.5
	case p <= 55:
		k := float64(p - 51)
		if side == 0 {
			return 7.5, 13.5 - k
		}
		return 7.5, 1.5 + k
	}
	if side == 0 {
		return 7.5, 8.4
	}
	return 7.5, 6.6
}

func renderLudo(g *LudoSave) []byte {
	const cell, pad = 34, 10
	size := cell*15 + 2*pad
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	draw.Draw(img, img.Bounds(), image.NewUniform(rgb(250, 247, 242)), image.Point{}, draw.Src)
	fillRect := func(x0, y0, x1, y1 float64, c color.RGBA) {
		draw.Draw(img, image.Rect(pad+int(x0*cell), pad+int(y0*cell), pad+int(x1*cell), pad+int(y1*cell)), image.NewUniform(c), image.Point{}, draw.Src)
	}
	red, yel, grn, blu := rgb(214, 56, 52), rgb(244, 190, 40), rgb(70, 160, 80), rgb(50, 100, 190)
	pale := func(c color.RGBA) color.RGBA {
		return rgb(uint8(255-(255-int(c.R))/3), uint8(255-(255-int(c.G))/3), uint8(255-(255-int(c.B))/3))
	}
	// base quadrants
	fillRect(0, 0, 6, 6, grn)
	fillRect(9, 0, 15, 6, yel)
	fillRect(0, 9, 6, 15, red)
	fillRect(9, 9, 15, 15, blu)
	for _, q := range [][4]float64{{0.8, 0.8, 5.2, 5.2}, {9.8, 0.8, 14.2, 5.2}, {0.8, 9.8, 5.2, 14.2}, {9.8, 9.8, 14.2, 14.2}} {
		fillRect(q[0], q[1], q[2], q[3], rgb(255, 255, 255))
	}
	ring := ludoRing()
	for i, c := range ring {
		col := rgb(255, 255, 255)
		switch i {
		case 1:
			col = pale(grn)
		case 14:
			col = yel
		case 27:
			col = pale(blu)
		case 40:
			col = red
		}
		fillRect(float64(c[0]), float64(c[1]), float64(c[0]+1), float64(c[1]+1), rgb(60, 60, 60))
		fillRect(float64(c[0])+0.04, float64(c[1])+0.04, float64(c[0]+1)-0.04, float64(c[1]+1)-0.04, col)
		if ludoSafe[i] && i != 1 && i != 14 && i != 27 && i != 40 {
			cx, cy := pad+c[0]*cell+cell/2, pad+c[1]*cell+cell/2
			chessDisc(img, cx, cy, 8, rgb(120, 120, 120))
			chessDisc(img, cx, cy, 6, rgb(250, 220, 90))
		}
	}
	for k := 0; k < 5; k++ { // home columns
		fillRect(7, float64(13-k), 8, float64(14-k), rgb(60, 60, 60))
		fillRect(7.04, float64(13-k)+0.04, 7.96, float64(14-k)-0.04, pale(red))
		fillRect(7, float64(1+k), 8, float64(2+k), rgb(60, 60, 60))
		fillRect(7.04, float64(1+k)+0.04, 7.96, float64(2+k)-0.04, pale(yel))
		fillRect(float64(1+k), 7, float64(2+k), 8, rgb(60, 60, 60))
		fillRect(float64(1+k)+0.04, 7.04, float64(2+k)-0.04, 7.96, pale(grn))
		fillRect(float64(13-k), 7, float64(14-k), 8, rgb(60, 60, 60))
		fillRect(float64(13-k)+0.04, 7.04, float64(14-k)-0.04, 7.96, pale(blu))
	}
	// centre: four coloured wedges drawn as triangles by scanline
	cx0, cy0 := pad+6*cell, pad+6*cell
	for y := 0; y < 3*cell; y++ {
		for x := 0; x < 3*cell; x++ {
			u, v := x-3*cell/2, y-3*cell/2
			var c color.RGBA
			switch {
			case v < 0 && abs2(u) <= -v:
				c = yel
			case v > 0 && abs2(u) <= v:
				c = red
			case u < 0:
				c = grn
			default:
				c = blu
			}
			img.SetRGBA(cx0+x, cy0+y, c)
		}
	}
	// base slots
	for s := 0; s < 2; s++ {
		for slot := 0; slot < 4; slot++ {
			x, y := ludoPos(s, -1, slot, ring)
			cx, cy := pad+int(x*cell), pad+int(y*cell)
			col := red
			if s == 1 {
				col = yel
			}
			chessDisc(img, cx, cy, 15, pale(col))
		}
	}
	small := face(fonts.bold, 14)
	cnt := map[[2]int]int{}
	for s := 0; s < 2; s++ {
		for i, p := range g.Tok[s] {
			x, y := ludoPos(s, p, i, ring)
			key := [2]int{int(x * 10), int(y * 10)}
			off := cnt[key]
			cnt[key]++
			if p == 56 {
				x += float64(off-1) * 0.3
			}
			cx, cy := pad+int(x*cell)+off*5, pad+int(y*cell)-off*5
			if p == 56 {
				cx, cy = pad+int(x*cell), pad+int(y*cell)
			}
			col, ring2 := red, rgb(255, 255, 255)
			if s == 1 {
				col = rgb(224, 160, 20)
			}
			chessDisc(img, cx, cy, 14, rgb(30, 30, 30))
			chessDisc(img, cx, cy, 12, ring2)
			chessDisc(img, cx, cy, 10, col)
			n := strconv.Itoa(i + 1)
			drawText(img, small, rgb(255, 255, 255), cx-textW(small, n)/2, cy+5, n)
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func abs2(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
