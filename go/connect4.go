package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Connect Four against a classical alpha-beta search (no model). Games are saved as the list of columns played,
// 0-6, and replayed. Red moves first.

const c4W, c4H = 7, 6

type C4Save struct {
	Human  int    `json:"human"` // 1 = first (red), 2 = second (yellow)
	Level  int    `json:"level"`
	Moves  []int  `json:"moves"`
	Over   bool   `json:"over"`
	Result string `json:"result,omitempty"`
}

type c4File struct {
	Games map[string]*C4Save `json:"games"`
}

type Connect4 struct {
	Path string
	mu   sync.Mutex
}

func (c *Connect4) Prefix() string { return "c4:" }

type c4Board [c4H][c4W]int8 // [row 0 = bottom][col]

var c4Levels = map[int]struct {
	depth int
	limit time.Duration
}{1: {2, 200 * time.Millisecond}, 2: {4, 400 * time.Millisecond}, 3: {6, time.Second}, 4: {8, 2 * time.Second}, 5: {12, 4 * time.Second}}

func (b *c4Board) drop(col int, who int8) int {
	for r := 0; r < c4H; r++ {
		if b[r][col] == 0 {
			b[r][col] = who
			return r
		}
	}
	return -1
}

// win returns the four cells of a line through (r,c) owned by who, or nil.
func (b *c4Board) win(r, c int) [][2]int {
	who := b[r][c]
	for _, d := range [][2]int{{0, 1}, {1, 0}, {1, 1}, {1, -1}} {
		cells := [][2]int{{r, c}}
		for _, s := range []int{1, -1} {
			rr, cc := r+s*d[0], c+s*d[1]
			for rr >= 0 && rr < c4H && cc >= 0 && cc < c4W && b[rr][cc] == who {
				cells = append(cells, [2]int{rr, cc})
				rr, cc = rr+s*d[0], cc+s*d[1]
			}
		}
		if len(cells) >= 4 {
			return cells
		}
	}
	return nil
}

func (b *c4Board) eval(who int8) int {
	score := 0
	line := func(r, c, dr, dc int) {
		var mine, theirs int
		for i := 0; i < 4; i++ {
			switch b[r+i*dr][c+i*dc] {
			case who:
				mine++
			case 0:
			default:
				theirs++
			}
		}
		if mine > 0 && theirs > 0 {
			return
		}
		switch {
		case mine == 3:
			score += 5
		case mine == 2:
			score += 2
		case theirs == 3:
			score -= 5
		case theirs == 2:
			score -= 2
		}
	}
	for r := 0; r < c4H; r++ {
		for c := 0; c < c4W; c++ {
			if c+3 < c4W {
				line(r, c, 0, 1)
			}
			if r+3 < c4H {
				line(r, c, 1, 0)
				if c+3 < c4W {
					line(r, c, 1, 1)
				}
				if c-3 >= 0 {
					line(r, c, 1, -1)
				}
			}
		}
	}
	for r := 0; r < c4H; r++ {
		if b[r][3] == who {
			score += 3
		} else if b[r][3] != 0 {
			score -= 3
		}
	}
	return score
}

var c4Order = []int{3, 2, 4, 1, 5, 0, 6}

type c4Search struct {
	deadline time.Time
	stop     bool
	nodes    int
}

func (s *c4Search) nega(b *c4Board, who int8, depth, ply int, alpha, beta int, filled int) int {
	s.nodes++
	if s.nodes&1023 == 0 && time.Now().After(s.deadline) {
		s.stop = true
	}
	if s.stop {
		return 0
	}
	if filled == c4W*c4H {
		return 0
	}
	if depth == 0 {
		return b.eval(who)
	}
	best := -1 << 20
	for _, col := range c4Order {
		if b[c4H-1][col] != 0 {
			continue
		}
		r := b.drop(col, who)
		var v int
		if b.win(r, col) != nil {
			v = 100000 - ply
		} else {
			v = -s.nega(b, 3-who, depth-1, ply+1, -beta, -alpha, filled+1)
		}
		b[r][col] = 0
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

// c4Best returns the best column by iterative deepening within the level's depth and time caps.
func c4Best(b c4Board, who int8, level, filled int) (int, int) {
	lv, ok := c4Levels[level]
	if !ok {
		lv = c4Levels[3]
	}
	s := &c4Search{deadline: time.Now().Add(lv.limit)}
	bestCol, bestScore := -1, 0
	for d := 1; d <= lv.depth; d++ {
		curCol, curScore := -1, -1<<20
		alpha := -1 << 20
		for _, col := range c4Order {
			if b[c4H-1][col] != 0 {
				continue
			}
			bb := b
			r := bb.drop(col, who)
			var v int
			if bb.win(r, col) != nil {
				v = 100000
			} else {
				v = -s.nega(&bb, 3-who, d-1, 1, -(1 << 20), -alpha, filled+1)
			}
			if s.stop {
				break
			}
			if v > curScore {
				curCol, curScore = col, v
			}
			if v > alpha {
				alpha = v
			}
		}
		if s.stop {
			break
		}
		bestCol, bestScore = curCol, curScore
		if bestScore >= 99000 {
			break
		}
	}
	if bestCol < 0 { // out of time before depth 1 finished: first open column
		for _, col := range c4Order {
			if b[c4H-1][col] == 0 {
				return col, 0
			}
		}
	}
	return bestCol, bestScore
}

func (g *C4Save) replay() (c4Board, int, *[2]int, [][2]int, error) {
	var b c4Board
	who := int8(1)
	var last *[2]int
	var line [][2]int
	for _, col := range g.Moves {
		if col < 0 || col >= c4W {
			return b, 0, nil, nil, fmt.Errorf("bad saved game")
		}
		r := b.drop(col, who)
		if r < 0 {
			return b, 0, nil, nil, fmt.Errorf("bad saved game")
		}
		last = &[2]int{r, col}
		if w := b.win(r, col); w != nil {
			line = w
		}
		who = -who + 3 // 1 <-> 2
	}
	return b, len(g.Moves), last, line, nil
}

var (
	reC4New  = regexp.MustCompile(`(?i)^(?:play\s+|start\s+|new\s+)?(?:connect\s*(?:4|four)|c4)(?:\s+game)?(?:\s+as\s+(first|second))?(?:\s+(?:at\s+)?level\s+([1-5]))?\s*[.!]?$`)
	reC4Drop = regexp.MustCompile(`(?i)^(?:drop|c4|column|col)\s+([1-7])$`)
)

const c4Help = "Connect Four: say \"connect four\" (or \"connect four as second\", \"connect four level 4\", levels 1-5). Tap a column button under the board, or type \"drop 4\". Also: c4 board, c4 undo, c4 resign. I'm a classical search program, not a language model; level 5 thinks up to a few seconds."

func (c *Connect4) load() (*c4File, error) {
	f := &c4File{Games: map[string]*C4Save{}}
	if _, err := loadJSON(c.Path, f); err != nil {
		return nil, fmt.Errorf("corrupt")
	}
	if f.Games == nil {
		f.Games = map[string]*C4Save{}
	}
	return f, nil
}

func (c *Connect4) markup(g *C4Save) string {
	type btn struct {
		T string `json:"text"`
		D string `json:"callback_data"`
	}
	var row []btn
	for i := 0; i < c4W; i++ {
		row = append(row, btn{strconv.Itoa(i + 1), fmt.Sprintf("c4:%d:%d", i, len(g.Moves))})
	}
	b, _ := json.Marshal(map[string]any{"inline_keyboard": [][]btn{row}})
	return string(b)
}

func (c *Connect4) reply(g *C4Save, msg string) *ChessReply {
	b, _, last, line, err := g.replay()
	if err != nil {
		return &ChessReply{Text: "I can't read this saved game. Say \"connect four\" to start again."}
	}
	rep := &ChessReply{Text: msg, PNG: renderC4(b, last, line)}
	if !g.Over {
		rep.Markup = c.markup(g)
	}
	return rep
}

func (g *C4Save) colourName(side int) string {
	if side == 1 {
		return "Red"
	}
	return "Yellow"
}

// play applies the human move and the engine reply. Caller holds the lock and saves.
func (c *Connect4) play(g *C4Save, col int) string {
	b, n, _, _, _ := g.replay()
	me := int8(g.Human)
	r := b.drop(col, me)
	g.Moves = append(g.Moves, col)
	msg := fmt.Sprintf("You: column %d.", col+1)
	if b.win(r, col) != nil {
		g.Over, g.Result = true, fmt.Sprintf("You win with four in a row (%s).", g.colourName(g.Human))
		return msg + " " + g.Result
	}
	if n+1 == c4W*c4H {
		g.Over, g.Result = true, "Draw: the board is full."
		return msg + " " + g.Result
	}
	ec, _ := c4Best(b, 3-me, g.Level, n+1)
	r = b.drop(ec, 3-me)
	g.Moves = append(g.Moves, ec)
	msg += fmt.Sprintf(" Me: column %d.", ec+1)
	if b.win(r, ec) != nil {
		g.Over, g.Result = true, fmt.Sprintf("I win with four in a row (%s).", g.colourName(3-g.Human))
		return msg + " " + g.Result
	}
	if n+2 == c4W*c4H {
		g.Over, g.Result = true, "Draw: the board is full."
		return msg + " " + g.Result
	}
	return msg + " Your move."
}

func (c *Connect4) Handle(chat, text string) (*ChessReply, bool) {
	t := strings.TrimSpace(text)
	low := strings.ToLower(t)
	m := reC4New.FindStringSubmatch(t)
	dm := reC4Drop.FindStringSubmatch(t)
	cmd := low == "c4 board" || low == "connect four board" || low == "c4 undo" || low == "c4 resign" || low == "c4 help" || low == "connect four help"
	if m == nil && dm == nil && !cmd {
		return nil, false
	}
	if c == nil || c.Path == "" {
		return &ChessReply{Text: "Connect Four is switched off: the owner has not set a save file for it."}, true
	}
	if low == "c4 help" || low == "connect four help" {
		return &ChessReply{Text: c4Help}, true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	f, err := c.load()
	if err != nil {
		return &ChessReply{Text: "I can't read my Connect Four save file, so I won't touch it. Please tell the owner."}, true
	}
	g := f.Games[chat]
	save := func() bool { return saveJSON(c.Path, f) == nil }
	if m != nil {
		if g != nil && !g.Over && len(g.Moves) > 0 {
			return c.reply(g, "You already have a game going. Make a move, or say \"c4 resign\" first."), true
		}
		if g == nil && len(f.Games) >= 100 {
			return &ChessReply{Text: "I can't start a new Connect Four game right now (too many saved games)."}, true
		}
		ng := &C4Save{Human: 1, Level: 3}
		if strings.EqualFold(m[1], "second") {
			ng.Human = 2
		}
		if m[2] != "" {
			ng.Level, _ = strconv.Atoi(m[2])
		} else if g != nil && g.Level > 0 {
			ng.Level = g.Level
		}
		f.Games[chat] = ng
		intro := fmt.Sprintf("New game, level %d. You are %s. ", ng.Level, ng.colourName(ng.Human))
		if ng.Human == 2 {
			col, _ := c4Best(c4Board{}, 1, ng.Level, 0)
			ng.Moves = append(ng.Moves, col)
			intro += fmt.Sprintf("I open in column %d. ", col+1)
		}
		if !save() {
			return &ChessReply{Text: "I couldn't save the game, so it is not started."}, true
		}
		return c.reply(ng, intro+"Tap a column or type \"drop 4\"."), true
	}
	if g == nil {
		return nil, false
	}
	switch low {
	case "c4 board", "connect four board":
		return c.reply(g, map[bool]string{true: g.Result, false: "Your move."}[g.Over]), true
	case "c4 resign":
		if g.Over {
			return nil, false
		}
		g.Over, g.Result = true, "You resigned. I win."
		save()
		return c.reply(g, g.Result), true
	case "c4 undo":
		if g.Over || len(g.Moves) < 2+(g.Human-1) {
			return &ChessReply{Text: "There's nothing to undo."}, true
		}
		g.Moves = g.Moves[:len(g.Moves)-2]
		save()
		return c.reply(g, "Undid your last move and my reply."), true
	}
	if g.Over {
		return nil, false
	}
	col, _ := strconv.Atoi(dm[1])
	return c.move(g, f, col-1, save), true
}

func (c *Connect4) move(g *C4Save, f *c4File, col int, save func() bool) *ChessReply {
	b, n, _, _, err := g.replay()
	if err != nil {
		return &ChessReply{Text: "I can't read this saved game. Say \"connect four\" to start again."}
	}
	if (n%2 == 0) != (g.Human == 1) {
		return &ChessReply{Text: "It isn't your turn."}
	}
	if b[c4H-1][col] != 0 {
		return &ChessReply{Text: fmt.Sprintf("Column %d is full. Pick another.", col+1)}
	}
	msg := c.play(g, col)
	if !save() {
		return &ChessReply{Text: "I couldn't save the game, so that move was NOT recorded."}
	}
	return c.reply(g, msg)
}

func (c *Connect4) Tap(chat, data string) (string, string, *ChessReply) {
	p := strings.Split(data, ":")
	if len(p) != 3 || c == nil || c.Path == "" {
		return "", "", nil
	}
	col, e1 := strconv.Atoi(p[1])
	if e1 != nil || col < 0 || col >= c4W {
		return "", "", nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	f, err := c.load()
	if err != nil {
		return "I can't read my save file.", "", nil
	}
	g := f.Games[chat]
	if g == nil || g.Over {
		return "No game in progress. Say \"connect four\" to start.", "-", nil
	}
	if p[2] != fmt.Sprint(len(g.Moves)) {
		return "That board is out of date. Use the newest one.", "-", nil
	}
	rep := c.move(g, f, col, func() bool { return saveJSON(c.Path, f) == nil })
	if rep.PNG == nil {
		return rep.Text, "", nil
	}
	return "Move played.", "-", rep
}

func renderC4(b c4Board, last *[2]int, line [][2]int) []byte {
	const cell, pad = 64, 16
	w, h := c4W*cell+2*pad, c4H*cell+2*pad
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), image.NewUniform(rgb(244, 239, 236)), image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(pad/2, pad/2, w-pad/2, h-pad/2), image.NewUniform(rgb(36, 82, 168)), image.Point{}, draw.Src)
	win := map[[2]int]bool{}
	for _, c := range line {
		win[c] = true
	}
	for r := 0; r < c4H; r++ {
		for c := 0; c < c4W; c++ {
			cx, cy := pad+c*cell+cell/2, pad+(c4H-1-r)*cell+cell/2
			col := rgb(235, 240, 250)
			switch b[r][c] {
			case 1:
				col = rgb(214, 48, 49)
			case 2:
				col = rgb(250, 200, 40)
			}
			if win[[2]int{r, c}] {
				chessDisc(img, cx, cy, 29, rgb(255, 255, 255))
			} else if last != nil && last[0] == r && last[1] == c {
				chessDisc(img, cx, cy, 29, rgb(30, 30, 30))
			}
			chessDisc(img, cx, cy, 26, col)
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}
