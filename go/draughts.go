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

// English draughts (checkers) against a classical alpha-beta search. Red (side 1) starts at the bottom and moves
// first. Captures are compulsory; multi-jumps are taken in one move; a man that reaches the far row becomes a king
// and its move ends there. Kings move and jump one step in any diagonal direction (not flying). A side with no legal
// move loses. 80 plies without a capture or man move is a draw.

type DrSave struct {
	Human  int      `json:"human"`
	Level  int      `json:"level"`
	Moves  []string `json:"moves"` // paths like "c3-e5-g7"
	Over   bool     `json:"over"`
	Result string   `json:"result,omitempty"`
	Sel    string   `json:"sel,omitempty"`
}

type drFile struct {
	Games map[string]*DrSave `json:"games"`
}

type Draughts struct {
	Path string
	mu   sync.Mutex
}

func (d *Draughts) Prefix() string { return "dr:" }

type drBoard [64]int8

type drMove struct {
	path []int
	caps []int
}

func (m drMove) String() string {
	var s []string
	for _, p := range m.path {
		s = append(s, rvName(p))
	}
	return strings.Join(s, "-")
}

func drStart() drBoard {
	var b drBoard
	for r := 0; r < 3; r++ {
		for c := 0; c < 8; c++ {
			if (r+c)%2 == 0 {
				b[r*8+c] = 1
			}
		}
	}
	for r := 5; r < 8; r++ {
		for c := 0; c < 8; c++ {
			if (r+c)%2 == 0 {
				b[r*8+c] = -1
			}
		}
	}
	return b
}

func drSide(v int8) int8 {
	if v > 0 {
		return 1
	} else if v < 0 {
		return -1
	}
	return 0
}

// moves lists every legal move for side (1 or -1); captures only if any capture exists.
func (b *drBoard) moves(side int8) []drMove {
	var caps, steps []drMove
	for s := 0; s < 64; s++ {
		v := b[s]
		if drSide(v) != side {
			continue
		}
		king := v == 2 || v == -2
		dirs := [][2]int{{int(side), -1}, {int(side), 1}}
		if king {
			dirs = [][2]int{{1, -1}, {1, 1}, {-1, -1}, {-1, 1}}
		}
		var jump func(cur int, kingNow bool, path, cp []int, bb drBoard)
		jump = func(cur int, kingNow bool, path, cp []int, bb drBoard) {
			ds := dirs
			if kingNow {
				ds = [][2]int{{1, -1}, {1, 1}, {-1, -1}, {-1, 1}}
			}
			found := false
			for _, d := range ds {
				r, c := cur/8+d[0], cur%8+d[1]
				r2, c2 := r+d[0], c+d[1]
				if r2 < 0 || r2 > 7 || c2 < 0 || c2 > 7 {
					continue
				}
				mid := bb[r*8+c]
				if drSide(mid) != -side || bb[r2*8+c2] != 0 {
					continue
				}
				found = true
				nb := bb
				nb[r*8+c] = 0
				nb[cur] = 0
				nk := kingNow
				if !king && ((side == 1 && r2 == 7) || (side == -1 && r2 == 0)) {
					nk = true
				}
				nb[r2*8+c2] = v
				np := append(append([]int{}, path...), r2*8+c2)
				ncp := append(append([]int{}, cp...), r*8+c)
				if nk && !king {
					caps = append(caps, drMove{np, ncp}) // promotion ends the jump
				} else {
					jump(r2*8+c2, nk, np, ncp, nb)
				}
			}
			if !found && len(cp) > 0 {
				caps = append(caps, drMove{path, cp})
			}
		}
		bb := *b
		jump(s, king, []int{s}, nil, bb)
		for _, d := range dirs {
			r, c := s/8+d[0], s%8+d[1]
			if r < 0 || r > 7 || c < 0 || c > 7 || b[r*8+c] != 0 {
				continue
			}
			steps = append(steps, drMove{[]int{s, r*8 + c}, nil})
		}
	}
	if len(caps) > 0 {
		return caps
	}
	return steps
}

func (b *drBoard) apply(m drMove) {
	v := b[m.path[0]]
	b[m.path[0]] = 0
	for _, c := range m.caps {
		b[c] = 0
	}
	end := m.path[len(m.path)-1]
	if v == 1 && end/8 == 7 {
		v = 2
	}
	if v == -1 && end/8 == 0 {
		v = -2
	}
	b[end] = v
}

func (b *drBoard) eval(side int8) int {
	s := 0
	for i, v := range b {
		if v == 0 {
			continue
		}
		val := 0
		switch v {
		case 1, -1:
			adv := i / 8
			if v == -1 {
				adv = 7 - i/8
			}
			val = 100 + adv*4
			if adv == 0 {
				val += 6
			}
			if c := i % 8; c == 0 || c == 7 {
				val -= 2
			}
		default:
			val = 170
			if c, r := i%8, i/8; c > 1 && c < 6 && r > 1 && r < 6 {
				val += 4
			}
		}
		if drSide(v) == side {
			s += val
		} else {
			s -= val
		}
	}
	return s
}

type drSearch struct {
	deadline time.Time
	stop     bool
	nodes    int
}

func (s *drSearch) nega(b drBoard, side int8, depth, ply, alpha, beta int) int {
	s.nodes++
	if s.nodes&511 == 0 && time.Now().After(s.deadline) {
		s.stop = true
	}
	if s.stop {
		return 0
	}
	mv := b.moves(side)
	if len(mv) == 0 {
		return -90000 + ply
	}
	if depth <= 0 {
		if len(mv) > 0 && mv[0].caps != nil && depth > -6 { // settle forced captures
			depth = 0
		} else {
			return b.eval(side)
		}
	}
	best := -1 << 20
	for _, m := range mv {
		nb := b
		nb.apply(m)
		v := -s.nega(nb, -side, depth-1, ply+1, -beta, -alpha)
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

var drLevels = map[int]struct {
	depth int
	limit time.Duration
}{1: {1, 100 * time.Millisecond}, 2: {3, 300 * time.Millisecond}, 3: {5, time.Second}, 4: {8, 2 * time.Second}, 5: {12, 4 * time.Second}}

func drBest(b drBoard, side int8, level int) drMove {
	mv := b.moves(side)
	lv, ok := drLevels[level]
	if !ok {
		lv = drLevels[3]
	}
	s := &drSearch{deadline: time.Now().Add(lv.limit)}
	best := mv[0]
	if len(mv) == 1 {
		return best
	}
	for d := 1; d <= lv.depth; d++ {
		cur, curS, alpha := mv[0], -1<<20, -1<<20
		for _, m := range mv {
			nb := b
			nb.apply(m)
			v := -s.nega(nb, -side, d-1, 1, -(1 << 20), -alpha)
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

// replay gives the board, side to move (1 or -1), last move, plies since a capture or man move, and an error.
func (g *DrSave) replay() (drBoard, int8, *drMove, int, error) {
	b := drStart()
	side := int8(1)
	var last *drMove
	quiet := 0
	for _, s := range g.Moves {
		var found *drMove
		for _, m := range b.moves(side) {
			if m.String() == s {
				mm := m
				found = &mm
			}
		}
		if found == nil {
			return b, side, nil, 0, fmt.Errorf("bad saved game")
		}
		v := b[found.path[0]]
		if found.caps != nil || v == 1 || v == -1 {
			quiet = 0
		} else {
			quiet++
		}
		b.apply(*found)
		last = found
		side = -side
	}
	return b, side, last, quiet, nil
}

func (g *DrSave) humanSide() int8 {
	if g.Human == 1 {
		return 1
	}
	return -1
}

func (g *DrSave) status(b drBoard, side int8, quiet int) string {
	if len(b.moves(side)) == 0 {
		if side == g.humanSide() {
			return "Game over. I win: you have no legal move."
		}
		return "Game over. You win: I have no legal move."
	}
	if quiet >= 80 {
		return "Game over. Draw: 80 moves without a capture or man move."
	}
	return ""
}

var (
	reDrNew  = regexp.MustCompile(`(?i)^(?:play\s+|start\s+|new\s+)?(?:draughts|checkers)(?:\s+game)?(?:\s+as\s+(red|black))?(?:\s+(?:at\s+)?level\s+([1-5]))?\s*[.!]?$`)
	reDrMove = regexp.MustCompile(`(?i)^(?:draughts|checkers|move|play)\s+([a-h][1-8](?:\s*[-x ]\s*[a-h][1-8])+)$`)
)

func (d *Draughts) load() (*drFile, error) {
	f := &drFile{Games: map[string]*DrSave{}}
	if _, err := loadJSON(d.Path, f); err != nil {
		return nil, fmt.Errorf("corrupt")
	}
	if f.Games == nil {
		f.Games = map[string]*DrSave{}
	}
	return f, nil
}

func drCounts(b drBoard) (int, int) {
	r, k := 0, 0
	for _, v := range b {
		if v > 0 {
			r++
		} else if v < 0 {
			k++
		}
	}
	return r, k
}

func (d *Draughts) reply(g *DrSave, msg string) *ChessReply {
	b, side, last, quiet, err := g.replay()
	if err != nil {
		return &ChessReply{Text: "I can't read this saved game. Say \"draughts\" to start again."}
	}
	if !g.Over {
		if st := g.status(b, side, quiet); st != "" {
			g.Over, g.Result = true, st
			msg += " " + st
		}
	}
	r, k := drCounts(b)
	msg += fmt.Sprintf("\nRed %d - Black %d pieces. You are %s.", r, k, map[int]string{1: "Red", 2: "Black"}[g.Human])
	rep := &ChessReply{Text: msg, PNG: renderDr(b, last, g.humanSide() == 1)}
	if !g.Over && side == g.humanSide() {
		rep.Markup = drMarkup(&b, g.humanSide(), len(g.Moves), g.Sel)
	}
	return rep
}

func drMarkup(b *drBoard, side int8, plies int, sel string) string {
	type btn struct {
		T string `json:"text"`
		D string `json:"callback_data"`
	}
	set := map[int]bool{}
	mv := b.moves(side)
	for _, m := range mv {
		if sel == "" {
			set[m.path[0]] = true
		} else if rvName(m.path[0]) == sel {
			set[m.path[len(m.path)-1]] = true
		}
	}
	var sq []int
	for s := range set {
		sq = append(sq, s)
	}
	sort.Ints(sq)
	var rows [][]btn
	var row []btn
	for _, s := range sq {
		row = append(row, btn{rvName(s), fmt.Sprintf("dr:%s:%d", rvName(s), plies)})
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

func (d *Draughts) engineTurn(g *DrSave) string {
	b, side, _, quiet, _ := g.replay()
	if g.status(b, side, quiet) != "" || side == g.humanSide() {
		return ""
	}
	m := drBest(b, side, g.Level)
	g.Moves = append(g.Moves, m.String())
	return " Me: " + m.String() + "."
}

func (d *Draughts) Handle(chat, text string) (*ChessReply, bool) {
	t := strings.TrimSpace(text)
	low := strings.ToLower(t)
	m := reDrNew.FindStringSubmatch(t)
	mm := reDrMove.FindStringSubmatch(t)
	cmd := low == "draughts board" || low == "checkers board" || low == "draughts resign" || low == "checkers resign" || low == "draughts help"
	if m == nil && mm == nil && !cmd {
		return nil, false
	}
	if d == nil || d.Path == "" {
		return &ChessReply{Text: "Draughts is switched off: the owner has not set a save file for it."}, true
	}
	if low == "draughts help" {
		return &ChessReply{Text: "Draughts (English rules, like checkers): say \"draughts\" (or \"draughts as black\", \"draughts level 4\"; levels 1-5). Tap the piece to move, then where it goes, or type \"move c3-d4\" (jumps: \"move c3-e5-g7\"). Red moves first, up the board. Captures are compulsory. Also: draughts board, draughts resign. I'm a classical search program, not a language model."}, true
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	f, err := d.load()
	if err != nil {
		return &ChessReply{Text: "I can't read my draughts save file, so I won't touch it. Please tell the owner."}, true
	}
	g := f.Games[chat]
	save := func() bool { return saveJSON(d.Path, f) == nil }
	if m != nil {
		if g != nil && !g.Over && len(g.Moves) > 0 {
			return d.reply(g, "You already have a game going. Make a move, or say \"draughts resign\" first."), true
		}
		if g == nil && len(f.Games) >= 100 {
			return &ChessReply{Text: "I can't start a new draughts game right now (too many saved games)."}, true
		}
		ng := &DrSave{Human: 1, Level: 3}
		if strings.EqualFold(m[1], "black") {
			ng.Human = 2
		}
		if m[2] != "" {
			ng.Level, _ = strconv.Atoi(m[2])
		} else if g != nil && g.Level > 0 {
			ng.Level = g.Level
		}
		f.Games[chat] = ng
		intro := fmt.Sprintf("New game, level %d.", ng.Level)
		if ng.Human == 2 {
			intro += d.engineTurn(ng)
		}
		if !save() {
			return &ChessReply{Text: "I couldn't save the game, so it is not started."}, true
		}
		return d.reply(ng, intro+" Tap the piece you want to move."), true
	}
	if g == nil || g.Over {
		return nil, false
	}
	switch low {
	case "draughts board", "checkers board":
		return d.reply(g, "Your move."), true
	case "draughts resign", "checkers resign":
		g.Over, g.Result = true, "You resigned. I win."
		save()
		return d.reply(g, g.Result), true
	}
	return d.typed(g, mm[1], save), true
}

func (d *Draughts) typed(g *DrSave, text string, save func() bool) *ChessReply {
	b, side, _, _, err := g.replay()
	if err != nil {
		return &ChessReply{Text: "I can't read this saved game."}
	}
	if side != g.humanSide() {
		return &ChessReply{Text: "It isn't your turn."}
	}
	var sq []int
	for _, p := range regexp.MustCompile(`[a-h][1-8]`).FindAllString(strings.ToLower(text), -1) {
		sq = append(sq, int(p[0]-'a')+int(p[1]-'1')*8)
	}
	var hits []drMove
	for _, m := range b.moves(side) {
		if len(sq) == len(m.path) {
			eq := true
			for i := range sq {
				if sq[i] != m.path[i] {
					eq = false
				}
			}
			if eq {
				hits = append(hits, m)
			}
		} else if len(sq) == 2 && m.path[0] == sq[0] && m.path[len(m.path)-1] == sq[1] {
			hits = append(hits, m)
		}
	}
	if len(hits) == 0 {
		return &ChessReply{Text: d.legalHint(b, side)}
	}
	if len(hits) > 1 {
		return &ChessReply{Text: "Two different jump routes end there. Type the whole route, like c3-e5-g7."}
	}
	return d.play(g, hits[0], save)
}

func (d *Draughts) legalHint(b drBoard, side int8) string {
	mv := b.moves(side)
	var s []string
	for _, m := range mv {
		s = append(s, m.String())
	}
	sort.Strings(s)
	if len(s) > 10 {
		s = s[:10]
	}
	pre := "That isn't a legal move."
	if len(mv) > 0 && mv[0].caps != nil {
		pre += " You must capture."
	}
	return pre + " Legal moves: " + strings.Join(s, ", ") + "."
}

func (d *Draughts) play(g *DrSave, m drMove, save func() bool) *ChessReply {
	g.Sel = ""
	g.Moves = append(g.Moves, m.String())
	msg := "You: " + m.String() + "."
	msg += d.engineTurn(g)
	if !save() {
		return &ChessReply{Text: "I couldn't save the game, so that move was NOT recorded."}
	}
	if _, side, _, _, _ := g.replay(); side == g.humanSide() && !g.Over {
		msg += " Your move."
	}
	return d.reply(g, msg)
}

func (d *Draughts) Tap(chat, data string) (string, string, *ChessReply) {
	p := strings.Split(data, ":")
	if len(p) != 3 || d == nil || d.Path == "" || len(p[1]) != 2 {
		return "", "", nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	f, err := d.load()
	if err != nil {
		return "I can't read my save file.", "", nil
	}
	g := f.Games[chat]
	if g == nil || g.Over {
		return "No game in progress. Say \"draughts\" to start.", "-", nil
	}
	if p[2] != fmt.Sprint(len(g.Moves)) {
		return "That board is out of date. Use the newest one.", "-", nil
	}
	b, side, _, _, err := g.replay()
	if err != nil || side != g.humanSide() {
		return "It isn't your turn.", "", nil
	}
	save := func() bool { return saveJSON(d.Path, f) == nil }
	sq := int(p[1][0]-'a') + int(p[1][1]-'1')*8
	if sq < 0 || sq > 63 {
		return "", "", nil
	}
	if g.Sel != "" {
		var hits []drMove
		for _, m := range b.moves(side) {
			if rvName(m.path[0]) == g.Sel && m.path[len(m.path)-1] == sq {
				hits = append(hits, m)
			}
		}
		if len(hits) > 0 {
			rep := d.play(g, hits[0], save)
			if rep.PNG == nil {
				return rep.Text, "", nil
			}
			return "Move played.", "-", rep
		}
	}
	for _, m := range b.moves(side) {
		if m.path[0] == sq {
			g.Sel = rvName(sq)
			save()
			return "Selected " + g.Sel + ". Tap where it goes.", drMarkup(&b, side, len(g.Moves), g.Sel), nil
		}
	}
	g.Sel = ""
	save()
	return "Pick a piece that can move (captures are compulsory).", drMarkup(&b, side, len(g.Moves), ""), nil
}

func renderDr(b drBoard, last *drMove, redBottom bool) []byte {
	const cell, pad = 56, 24
	size := cell*8 + 2*pad
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	draw.Draw(img, img.Bounds(), image.NewUniform(rgb(244, 239, 236)), image.Point{}, draw.Src)
	small := face(fonts.bold, 12)
	big := face(fonts.bold, 22)
	lastSq := map[int]bool{}
	if last != nil {
		lastSq[last.path[0]] = true
		lastSq[last.path[len(last.path)-1]] = true
	}
	pos := func(s int) (int, int) {
		c, r := s%8, s/8
		if !redBottom {
			c, r = 7-c, 7-r
		}
		return pad + c*cell, pad + (7-r)*cell
	}
	for s := 0; s < 64; s++ {
		x, y := pos(s)
		col := rgb(238, 218, 184)
		if (s/8+s%8)%2 == 0 {
			col = rgb(150, 104, 70)
			if lastSq[s] {
				col = rgb(176, 150, 60)
			}
		}
		draw.Draw(img, image.Rect(x, y, x+cell, y+cell), image.NewUniform(col), image.Point{}, draw.Src)
		v := b[s]
		if v == 0 {
			continue
		}
		cx, cy := x+cell/2, y+cell/2
		fill := rgb(204, 44, 44)
		if v < 0 {
			fill = rgb(40, 40, 44)
		}
		chessDisc(img, cx, cy, 23, rgb(250, 250, 250))
		chessDisc(img, cx, cy, 20, fill)
		chessDisc(img, cx, cy, 14, rgb(250, 250, 250))
		chessDisc(img, cx, cy, 13, fill)
		if v == 2 || v == -2 {
			drawText(img, big, rgb(255, 214, 10), cx-textW(big, "K")/2, cy+8, "K")
		}
	}
	for i := 0; i < 8; i++ {
		fl, rk := string(rune('a'+i)), strconv.Itoa(i+1)
		if !redBottom {
			fl, rk = string(rune('a'+7-i)), strconv.Itoa(8-i)
		}
		drawText(img, small, rgb(88, 82, 84), pad+i*cell+cell/2-textW(small, fl)/2, size-8, fl)
		drawText(img, small, rgb(88, 82, 84), 8, pad+(7-i)*cell+cell/2+4, rk)
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}
