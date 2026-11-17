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
// 56 home. Starts: side 0 (human, red) at square 0, side 1 (bot, blue) at square 26.
// Safe squares (no capture): 0, 8, 13, 21, 26, 34, 39, 47.

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

var ludoStart = [2]int{0, 26}
var ludoSafe = map[int]bool{0: true, 8: true, 13: true, 21: true, 26: true, 34: true, 39: true, 47: true}

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

const ludoHelp = "Ludo (two players, my own simplified rules): 4 tokens each, 52-square track. Say \"ludo\" (or \"ludo level 2\"; levels 1-3), then tap Roll or say \"roll\". You need a 6 to bring a token out and a 6 gives another roll (three 6s in a row lose the turn). Land on my token (not on a safe star/start square) to send it home. You need the exact roll to finish. Dice are random for both of us and I never see your roll in advance. My level 1 plays randomly, level 2 uses a one-step scoring rule, level 3 also averages over your possible replies. That is a baseline heuristic, not deep search. Also: ludo board, ludo resign."

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

// ---- board picture: a 14x14 ring (52 squares), home columns inside, bases in the middle ----

func ludoRing() [52][2]int {
	var r [52][2]int
	i := 0
	for y := 13; y >= 0; y-- {
		r[i] = [2]int{0, y}
		i++
	}
	for x := 1; x <= 13; x++ {
		r[i] = [2]int{x, 0}
		i++
	}
	for y := 1; y <= 13; y++ {
		r[i] = [2]int{13, y}
		i++
	}
	for x := 12; x >= 1; x-- {
		r[i] = [2]int{x, 13}
		i++
	}
	return r
}

func ludoCell(side, p, slot int, ring [52][2]int) (int, int) {
	switch {
	case p == -1:
		if side == 0 {
			return 4 + slot%2, 10 + slot/2
		}
		return 8 + slot%2, 2 + slot/2
	case p <= 50:
		c := ring[ludoAbs(side, p)]
		return c[0], c[1]
	default:
		k := p - 51 // 0..5
		if side == 0 {
			return 2, 12 - k
		}
		return 11, 1 + k
	}
}

func renderLudo(g *LudoSave) []byte {
	const cell, pad = 34, 16
	size := cell*14 + 2*pad
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	draw.Draw(img, img.Bounds(), image.NewUniform(rgb(244, 239, 236)), image.Point{}, draw.Src)
	ring := ludoRing()
	box := func(x, y int, c color.RGBA) {
		draw.Draw(img, image.Rect(pad+x*cell+1, pad+y*cell+1, pad+x*cell+cell-1, pad+y*cell+cell-1), image.NewUniform(c), image.Point{}, draw.Src)
	}
	for i, c := range ring {
		col := rgb(255, 255, 255)
		if ludoSafe[i] {
			col = rgb(255, 232, 140)
		}
		if i == 0 {
			col = rgb(240, 150, 150)
		}
		if i == 26 {
			col = rgb(150, 180, 240)
		}
		box(c[0], c[1], col)
	}
	for k := 0; k < 6; k++ {
		box(2, 12-k, rgb(250, 200, 200))
		box(11, 1+k, rgb(200, 215, 250))
	}
	for s := 0; s < 2; s++ {
		for slot := 0; slot < 4; slot++ {
			x, y := ludoCell(s, -1, slot, ring)
			box(x, y, rgb(228, 224, 220))
		}
	}
	small := face(fonts.bold, 14)
	cnt := map[[2]int]int{}
	for s := 0; s < 2; s++ {
		for i, p := range g.Tok[s] {
			x, y := ludoCell(s, p, i, ring)
			if p == 56 {
				x, y = 6+s*2, 6
			}
			off := cnt[[2]int{x, y}]
			cnt[[2]int{x, y}]++
			cx, cy := pad+x*cell+cell/2+off*4, pad+y*cell+cell/2-off*4
			col := rgb(204, 44, 44)
			if s == 1 {
				col = rgb(36, 82, 168)
			}
			chessDisc(img, cx, cy, 14, rgb(255, 255, 255))
			chessDisc(img, cx, cy, 12, col)
			n := strconv.Itoa(i + 1)
			drawText(img, small, rgb(255, 255, 255), cx-textW(small, n)/2, cy+5, n)
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}
