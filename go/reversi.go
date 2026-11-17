package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Reversi (Othello) against a classical alpha-beta search with a corner/edge square table plus mobility.
// Black (1) moves first. The saved game is the list of squares played (0..63, row*8+col, row 0 = rank 1 side
// at the bottom of the picture); a side with no legal move passes automatically.

type RvSave struct {
	Human  int    `json:"human"`
	Level  int    `json:"level"`
	Moves  []int  `json:"moves"`
	Over   bool   `json:"over"`
	Result string `json:"result,omitempty"`
}

type rvFile struct {
	Games map[string]*RvSave `json:"games"`
}

type Reversi struct {
	Path string
	mu   sync.Mutex
}

func (r *Reversi) Prefix() string { return "rv:" }

type rvBoard [64]int8

var rvDirs = [8][2]int{{-1, -1}, {-1, 0}, {-1, 1}, {0, -1}, {0, 1}, {1, -1}, {1, 0}, {1, 1}}

func rvStart() rvBoard {
	var b rvBoard
	b[3*8+3], b[4*8+4] = 2, 2
	b[3*8+4], b[4*8+3] = 1, 1
	return b
}

// flips returns the squares that playing sq as who would flip (empty when illegal).
func (b *rvBoard) flips(sq int, who int8) []int {
	if b[sq] != 0 {
		return nil
	}
	var out []int
	r0, c0 := sq/8, sq%8
	for _, d := range rvDirs {
		var run []int
		r, c := r0+d[0], c0+d[1]
		for r >= 0 && r < 8 && c >= 0 && c < 8 && b[r*8+c] == 3-who {
			run = append(run, r*8+c)
			r, c = r+d[0], c+d[1]
		}
		if len(run) > 0 && r >= 0 && r < 8 && c >= 0 && c < 8 && b[r*8+c] == who {
			out = append(out, run...)
		}
	}
	return out
}

func (b *rvBoard) moves(who int8) []int {
	var m []int
	for s := 0; s < 64; s++ {
		if b[s] == 0 && len(b.flips(s, who)) > 0 {
			m = append(m, s)
		}
	}
	return m
}

func (b *rvBoard) play(sq int, who int8) {
	for _, f := range b.flips(sq, who) {
		b[f] = who
	}
	b[sq] = who
}

func (b *rvBoard) count() (int, int) {
	x, o := 0, 0
	for _, v := range b {
		if v == 1 {
			x++
		} else if v == 2 {
			o++
		}
	}
	return x, o
}

var rvW = [64]int{
	100, -20, 10, 5, 5, 10, -20, 100,
	-20, -50, -2, -2, -2, -2, -50, -20,
	10, -2, 1, 1, 1, 1, -2, 10,
	5, -2, 1, 0, 0, 1, -2, 5,
	5, -2, 1, 0, 0, 1, -2, 5,
	10, -2, 1, 1, 1, 1, -2, 10,
	-20, -50, -2, -2, -2, -2, -50, -20,
	100, -20, 10, 5, 5, 10, -20, 100,
}

func (b *rvBoard) eval(who int8) int {
	s := 0
	empties := 0
	for i, v := range b {
		switch v {
		case who:
			s += rvW[i]
		case 0:
			empties++
		default:
			s -= rvW[i]
		}
	}
	s += 6 * (len(b.moves(who)) - len(b.moves(3-who)))
	if empties == 0 {
		x, o := b.count()
		d := x - o
		if who == 2 {
			d = -d
		}
		return d * 1000
	}
	return s
}

type rvSearch struct {
	deadline time.Time
	stop     bool
	nodes    int
}

func (s *rvSearch) nega(b rvBoard, who int8, depth, alpha, beta int) int {
	s.nodes++
	if s.nodes&511 == 0 && time.Now().After(s.deadline) {
		s.stop = true
	}
	if s.stop {
		return 0
	}
	mv := b.moves(who)
	if len(mv) == 0 {
		if len(b.moves(3-who)) == 0 {
			x, o := b.count()
			d := x - o
			if who == 2 {
				d = -d
			}
			return d * 1000
		}
		return -s.nega(b, 3-who, depth, -beta, -alpha)
	}
	if depth == 0 {
		return b.eval(who)
	}
	best := -1 << 20
	for _, m := range mv {
		nb := b
		nb.play(m, who)
		v := -s.nega(nb, 3-who, depth-1, -beta, -alpha)
		if s.stop {
			return 0
		}
		if v > best {
			best = v
		}
		if best > alpha {
			alpha = best
		}
		if alpha >= beta {
			break
		}
	}
	return best
}

var rvLevels = map[int]struct {
	depth int
	limit time.Duration
}{1: {1, 100 * time.Millisecond}, 2: {2, 300 * time.Millisecond}, 3: {4, time.Second}, 4: {6, 2 * time.Second}, 5: {8, 4 * time.Second}}

func rvBest(b rvBoard, who int8, level int) int {
	mv := b.moves(who)
	if len(mv) == 0 {
		return -1
	}
	lv, ok := rvLevels[level]
	if !ok {
		lv = rvLevels[3]
	}
	s := &rvSearch{deadline: time.Now().Add(lv.limit)}
	best := mv[0]
	for d := 1; d <= lv.depth; d++ {
		cur, curS, alpha := -1, -1<<20, -1<<20
		for _, m := range mv {
			nb := b
			nb.play(m, who)
			v := -s.nega(nb, 3-who, d-1, -(1 << 20), -alpha)
			if s.stop {
				break
			}
			if v > curS {
				cur, curS = m, v
			}
			if v > alpha {
				alpha = v
			}
		}
		if s.stop {
			break
		}
		best = cur
	}
	return best
}

// replay returns the board, the side to move (0 = game finished), the last move and any error.
func (g *RvSave) replay() (rvBoard, int8, int, error) {
	b := rvStart()
	who := int8(1)
	last := -1
	for _, m := range g.Moves {
		if m < 0 || m > 63 || len(b.flips(m, who)) == 0 {
			return b, 0, -1, fmt.Errorf("bad saved game")
		}
		b.play(m, who)
		last = m
		if len(b.moves(3-who)) > 0 {
			who = 3 - who
		} else if len(b.moves(who)) == 0 {
			who = 0
			break
		}
	}
	if who != 0 && len(b.moves(who)) == 0 {
		who = 0
	}
	return b, who, last, nil
}

func rvName(s int) string { return string(rune('a'+s%8)) + strconv.Itoa(s/8+1) }

var (
	reRvNew  = regexp.MustCompile(`(?i)^(?:play\s+|start\s+|new\s+)?(?:reversi|othello)(?:\s+game)?(?:\s+as\s+(black|white))?(?:\s+(?:at\s+)?level\s+([1-5]))?\s*[.!]?$`)
	reRvMove = regexp.MustCompile(`(?i)^(?:reversi|othello|move|play)\s+([a-h][1-8])$`)
)

func (r *Reversi) load() (*rvFile, error) {
	f := &rvFile{Games: map[string]*RvSave{}}
	if _, err := loadJSON(r.Path, f); err != nil {
		return nil, fmt.Errorf("corrupt")
	}
	if f.Games == nil {
		f.Games = map[string]*RvSave{}
	}
	return f, nil
}

func (g *RvSave) colour(side int) string {
	if side == 1 {
		return "Black"
	}
	return "White"
}

func (r *Reversi) reply(g *RvSave, msg string) *ChessReply {
	b, who, last, err := g.replay()
	if err != nil {
		return &ChessReply{Text: "I can't read this saved game. Say \"reversi\" to start again."}
	}
	x, o := b.count()
	if who == 0 && !g.Over {
		g.Over = true
		switch {
		case x == o:
			g.Result = fmt.Sprintf("Game over. Draw, %d-%d.", x, o)
		case (x > o) == (g.Human == 1):
			g.Result = fmt.Sprintf("Game over. You win, %d-%d (Black %d, White %d).", max(x, o), min(x, o), x, o)
		default:
			g.Result = fmt.Sprintf("Game over. I win, %d-%d (Black %d, White %d).", max(x, o), min(x, o), x, o)
		}
		msg += " " + g.Result
	}
	msg += fmt.Sprintf("\nBlack %d - White %d. You are %s.", x, o, g.colour(g.Human))
	rep := &ChessReply{Text: msg, PNG: renderRv(b, last, who == int8(g.Human) && !g.Over)}
	if !g.Over && who == int8(g.Human) {
		rep.Markup = rvMarkup(&b, who, len(g.Moves))
	}
	return rep
}

func rvMarkup(b *rvBoard, who int8, plies int) string {
	type btn struct {
		T string `json:"text"`
		D string `json:"callback_data"`
	}
	mv := b.moves(who)
	sort.Ints(mv)
	var rows [][]btn
	var row []btn
	for _, m := range mv {
		row = append(row, btn{rvName(m), fmt.Sprintf("rv:%d:%d", m, plies)})
		if len(row) == 5 {
			rows = append(rows, row)
			row = nil
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	j, _ := json.Marshal(map[string]any{"inline_keyboard": rows})
	return string(j)
}

// turn plays the human move then the engine's moves (it may play several if the human must pass).
func (r *Reversi) turn(g *RvSave, sq int) string {
	b, _, _, _ := g.replay()
	me := int8(g.Human)
	g.Moves = append(g.Moves, sq)
	msg := "You: " + rvName(sq) + "."
	for {
		_, who, _, _ := g.replay()
		if who == 0 || who == me {
			if who == me && len(b.moves(me)) >= 0 {
				// recompute after engine moves
			}
			break
		}
		b, _, _, _ = g.replay()
		em := rvBest(b, who, g.Level)
		g.Moves = append(g.Moves, em)
		msg += " Me: " + rvName(em) + "."
	}
	if _, who, _, _ := g.replay(); who == me {
		b, _, _, _ = g.replay()
		_ = b
	}
	return msg
}

func (r *Reversi) Handle(chat, text string) (*ChessReply, bool) {
	t := strings.TrimSpace(text)
	low := strings.ToLower(t)
	m := reRvNew.FindStringSubmatch(t)
	mm := reRvMove.FindStringSubmatch(t)
	cmd := low == "reversi board" || low == "othello board" || low == "reversi resign" || low == "othello resign" || low == "reversi help"
	if m == nil && mm == nil && !cmd {
		return nil, false
	}
	if r == nil || r.Path == "" {
		return &ChessReply{Text: "Reversi is switched off: the owner has not set a save file for it."}, true
	}
	if low == "reversi help" {
		return &ChessReply{Text: "Reversi: say \"reversi\" (or \"reversi as white\", \"reversi level 4\"; levels 1-5). Tap a numbered square under the board, or type \"move d3\". Black moves first. A move must trap enemy discs between your new disc and another of yours; they flip. No legal move means a pass. Also: reversi board, reversi resign. I'm a classical search program, not a language model."}, true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	f, err := r.load()
	if err != nil {
		return &ChessReply{Text: "I can't read my Reversi save file, so I won't touch it. Please tell the owner."}, true
	}
	g := f.Games[chat]
	save := func() bool { return saveJSON(r.Path, f) == nil }
	if m != nil {
		if g != nil && !g.Over && len(g.Moves) > 0 {
			return r.reply(g, "You already have a game going. Make a move, or say \"reversi resign\" first."), true
		}
		if g == nil && len(f.Games) >= 100 {
			return &ChessReply{Text: "I can't start a new Reversi game right now (too many saved games)."}, true
		}
		ng := &RvSave{Human: 1, Level: 3}
		if strings.EqualFold(m[1], "white") {
			ng.Human = 2
		}
		if m[2] != "" {
			ng.Level, _ = strconv.Atoi(m[2])
		} else if g != nil && g.Level > 0 {
			ng.Level = g.Level
		}
		f.Games[chat] = ng
		intro := fmt.Sprintf("New game, level %d. ", ng.Level)
		if ng.Human == 2 {
			b, _, _, _ := ng.replay()
			em := rvBest(b, 1, ng.Level)
			ng.Moves = append(ng.Moves, em)
			intro += "I open: " + rvName(em) + ". "
		}
		if !save() {
			return &ChessReply{Text: "I couldn't save the game, so it is not started."}, true
		}
		return r.reply(ng, intro+"Tap a square."), true
	}
	if g == nil || g.Over {
		return nil, false
	}
	switch low {
	case "reversi board", "othello board":
		return r.reply(g, "Your move."), true
	case "reversi resign", "othello resign":
		g.Over, g.Result = true, "You resigned. I win."
		save()
		return r.reply(g, g.Result), true
	}
	sq := int(mm[1][0]-'a') + int(mm[1][1]-'1')*8
	return r.move(g, sq, save), true
}

func (r *Reversi) move(g *RvSave, sq int, save func() bool) *ChessReply {
	b, who, _, err := g.replay()
	if err != nil {
		return &ChessReply{Text: "I can't read this saved game. Say \"reversi\" to start again."}
	}
	if who != int8(g.Human) {
		return &ChessReply{Text: "It isn't your turn."}
	}
	if len(b.flips(sq, who)) == 0 {
		var names []string
		for _, m := range b.moves(who) {
			names = append(names, rvName(m))
		}
		sort.Strings(names)
		return &ChessReply{Text: "That isn't a legal move. Legal squares: " + strings.Join(names, ", ") + "."}
	}
	msg := r.turn(g, sq)
	if !save() {
		return &ChessReply{Text: "I couldn't save the game, so that move was NOT recorded."}
	}
	_, w2, _, _ := g.replay()
	if w2 == int8(g.Human) {
		msg += " Your move."
	}
	return r.reply(g, msg)
}

func (r *Reversi) Tap(chat, data string) (string, string, *ChessReply) {
	p := strings.Split(data, ":")
	if len(p) != 3 || r == nil || r.Path == "" {
		return "", "", nil
	}
	sq, e := strconv.Atoi(p[1])
	if e != nil || sq < 0 || sq > 63 {
		return "", "", nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	f, err := r.load()
	if err != nil {
		return "I can't read my save file.", "", nil
	}
	g := f.Games[chat]
	if g == nil || g.Over {
		return "No game in progress. Say \"reversi\" to start.", "-", nil
	}
	if p[2] != fmt.Sprint(len(g.Moves)) {
		return "That board is out of date. Use the newest one.", "-", nil
	}
	rep := r.move(g, sq, func() bool { return saveJSON(r.Path, f) == nil })
	if rep.PNG == nil {
		return rep.Text, "", nil
	}
	return "Move played.", "-", rep
}

func renderRv(b rvBoard, last int, showHints bool) []byte {
	const cell, pad = 56, 24
	size := cell*8 + 2*pad
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	draw.Draw(img, img.Bounds(), image.NewUniform(rgb(244, 239, 236)), image.Point{}, draw.Src)
	small := face(fonts.bold, 12)
	for s := 0; s < 64; s++ {
		x, y := pad+(s%8)*cell, pad+(7-s/8)*cell
		c := rgb(30, 128, 72)
		if (s%8+s/8)%2 == 1 {
			c = rgb(26, 116, 64)
		}
		if s == last {
			c = rgb(120, 160, 60)
		}
		draw.Draw(img, image.Rect(x, y, x+cell, y+cell), image.NewUniform(c), image.Point{}, draw.Src)
		cx, cy := x+cell/2, y+cell/2
		switch b[s] {
		case 1:
			chessDisc(img, cx, cy, 22, rgb(235, 235, 235))
			chessDisc(img, cx, cy, 20, rgb(25, 25, 28))
		case 2:
			chessDisc(img, cx, cy, 22, rgb(60, 60, 60))
			chessDisc(img, cx, cy, 20, rgb(248, 248, 246))
		}
	}
	for i := 0; i < 8; i++ {
		fl, rk := string(rune('a'+i)), strconv.Itoa(i+1)
		drawText(img, small, rgb(88, 82, 84), pad+i*cell+cell/2-textW(small, fl)/2, size-8, fl)
		drawText(img, small, rgb(88, 82, 84), 8, pad+(7-i)*cell+cell/2+4, rk)
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}
