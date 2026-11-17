package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math/rand"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Chess game per chat: the saved record is just the list of moves from the standard start, so a restart, an undo or
// a repetition check is always a replay of that list. Engine play is classical alpha-beta search (see chess_core.go).

type ChessSave struct {
	White  bool     `json:"white"` // the human plays White
	Level  int      `json:"level"`
	Moves  []string `json:"moves"` // coordinate notation, e2e4, e7e8q
	Over   bool     `json:"over"`
	Result string   `json:"result,omitempty"`
	Date   string   `json:"date"`
	Promo  string   `json:"promo,omitempty"`
	Sel    string   `json:"sel,omitempty"` // square picked by a tap, waiting for its destination
}

type chessFile struct {
	Games map[string]*ChessSave `json:"games"`
}

type Chess struct {
	Path string
	Now  func() time.Time
	mu   sync.Mutex
}

const chessMaxGames = 100

type chessLevel struct {
	depth int
	limit time.Duration
	noise int // centipawns: level 1 may pick any move within this of the best
}

var chessLevels = map[int]chessLevel{
	1: {1, 300 * time.Millisecond, 150},
	2: {2, 500 * time.Millisecond, 0},
	3: {3, time.Second, 0},
	4: {4, 2 * time.Second, 0},
	5: {6, 4 * time.Second, 0},
}

var (
	reChessNew   = regexp.MustCompile(`(?i)^(?:(?:play|start|new|restart)\s+(?:a\s+)?(?:new\s+)?chess(?:\s+game)?|chess(?:\s+(?:new|start|game))?|new chess game)(?:\s+as\s+(white|black))?(?:\s+(?:at\s+)?level\s+([1-5]))?\s*[.!]?$`)
	reChessRest  = regexp.MustCompile(`(?i)^(?:restart|new)\s+chess$|^chess\s+(?:new|restart)$`)
	reChessLevel = regexp.MustCompile(`(?i)^(?:chess\s+)?level\s+([1-5])$`)
	reMoveish    = regexp.MustCompile(`(?i)^(?:[a-h][1-8]\s?[a-h][1-8]\s?[qrbn]?|[kqrbn]?[a-h]?[1-8]?x?[a-h][1-8](?:=?[qrbn])?[+#]?|o-o(?:-o)?|0-0(?:-0)?)[!?]*$`)
)

func (c *Chess) enabled() bool { return c != nil && c.Path != "" }

func (c *Chess) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

func (c *Chess) load() (*chessFile, error) {
	f := &chessFile{Games: map[string]*ChessSave{}}
	if _, err := loadJSON(c.Path, f); err != nil {
		return nil, fmt.Errorf("corrupt chess file")
	}
	if f.Games == nil {
		f.Games = map[string]*ChessSave{}
	}
	return f, nil
}

// replay rebuilds the position and the repetition counts from the move list. Corrupt lists return an error.
func replay(g *ChessSave) (Pos, map[string]int, []Mv, []string, error) {
	p := StartPos()
	rep := map[string]int{p.Key(): 1}
	var mvs []Mv
	var sans []string
	for _, s := range g.Moves {
		var found *Mv
		legal := p.Legal()
		for i := range legal {
			if legal[i].String() == s {
				found = &legal[i]
				break
			}
		}
		if found == nil {
			return p, nil, nil, nil, fmt.Errorf("illegal move in saved game")
		}
		sans = append(sans, SAN(p, *found, legal))
		p = p.Make(*found)
		mvs = append(mvs, *found)
		rep[p.Key()]++
	}
	return p, rep, mvs, sans, nil
}

// SAN returns standard algebraic notation for a legal move.
func SAN(p Pos, m Mv, legal []Mv) string {
	v := abs8(p.b[m.from])
	var s string
	switch {
	case v == wK && (m.to-m.from == 2):
		s = "O-O"
	case v == wK && (m.from-m.to == 2):
		s = "O-O-O"
	default:
		capture := p.b[m.to] != 0 || (v == wP && m.to == p.ep)
		if v == wP {
			if capture {
				s = string(rune('a'+m.from%8)) + "x"
			}
		} else {
			s = string(" PNBRQK"[v])
			sameFile, sameRank, other := false, false, false
			for _, o := range legal {
				if o != m && o.to == m.to && abs8(p.b[o.from]) == v {
					other = true
					if o.from%8 == m.from%8 {
						sameFile = true
					}
					if o.from/8 == m.from/8 {
						sameRank = true
					}
				}
			}
			if other {
				if !sameFile {
					s += string(rune('a' + m.from%8))
				} else if !sameRank {
					s += string(rune('1' + m.from/8))
				} else {
					s += sqName(m.from)
				}
			}
			if capture {
				s += "x"
			}
		}
		s += sqName(m.to)
		if m.promo != 0 {
			s += "=" + string(" PNBRQK"[m.promo])
		}
	}
	q := p.Make(m)
	if q.InCheck() {
		if len(q.Legal()) == 0 {
			s += "#"
		} else {
			s += "+"
		}
	}
	return s
}

func normSAN(s string) string {
	s = strings.ToLower(s)
	for _, ch := range "x+#=!? -" {
		s = strings.ReplaceAll(s, string(ch), "")
	}
	return strings.ReplaceAll(s, "0", "o")
}

// ParseMove finds the legal move a text means: coordinates (e2e4, e7e8q) or standard notation (Nf3, exd5, O-O, e8=Q).
func ParseMove(p Pos, text string) (Mv, string) {
	t := strings.TrimSpace(text)
	legal := p.Legal()
	if len(strings.Fields(t)) == 2 && len(strings.ReplaceAll(t, " ", "")) >= 4 && len(t) <= 6 {
		t = strings.ReplaceAll(t, " ", "")
	}
	low := strings.ToLower(t)
	if len(low) >= 4 && low[0] >= 'a' && low[0] <= 'h' && low[1] >= '1' && low[1] <= '8' && low[2] >= 'a' && low[2] <= 'h' && low[3] >= '1' && low[3] <= '8' {
		var hits []Mv
		for _, m := range legal {
			ms := m.String()
			if ms == low || (len(low) == 4 && m.promo == wQ && ms == low+"q") {
				hits = append(hits, m)
			}
		}
		if len(hits) == 1 {
			return hits[0], ""
		}
		return Mv{}, "illegal"
	}
	var exact, loose []Mv
	for _, m := range legal {
		san := SAN(p, m, legal)
		if strings.TrimRight(san, "+#") == strings.TrimRight(t, "+#!?") {
			exact = append(exact, m)
		}
		if normSAN(san) == normSAN(t) {
			loose = append(loose, m)
		}
	}
	if len(exact) == 1 {
		return exact[0], ""
	}
	if len(loose) == 1 {
		return loose[0], ""
	}
	if len(exact) == 0 && len(loose) == 0 {
		// "Rd1" when two rooks can go there: the saved notation has a file letter, the typed one doesn't
		nt := normSAN(t)
		var amb []Mv
		for _, m := range legal {
			ns := normSAN(SAN(p, m, legal))
			if len(ns) >= 4 && len(nt) == 3 && strings.ContainsRune("nbrqk", rune(ns[0])) && ns[:1]+ns[len(ns)-2:] == nt {
				amb = append(amb, m)
			}
		}
		if len(amb) > 1 {
			return Mv{}, "ambiguous"
		}
	}
	if len(loose) > 1 || len(exact) > 1 {
		return Mv{}, "ambiguous"
	}
	return Mv{}, "illegal"
}

// Status describes a finished game, or "" when play continues.
func chessStatus(p Pos, rep map[string]int) string {
	legal := p.Legal()
	switch {
	case len(legal) == 0 && p.InCheck():
		if p.white {
			return "Checkmate. Black wins (0-1)."
		}
		return "Checkmate. White wins (1-0)."
	case len(legal) == 0:
		return "Stalemate. The game is a draw (1/2-1/2)."
	case p.InsufficientMaterial():
		return "Draw: not enough material to checkmate (1/2-1/2)."
	case rep[p.Key()] >= 3:
		return "Draw by threefold repetition (1/2-1/2)."
	case p.half >= 100:
		return "Draw by the fifty-move rule (1/2-1/2)."
	}
	return ""
}

func (c *Chess) engineMove(p Pos, rep map[string]int, level int) (Mv, int, int) {
	lv := chessLevels[level]
	if lv.depth == 0 {
		lv = chessLevels[3]
	}
	best, score, d := Best(p, lv.depth, lv.limit, rep)
	if lv.noise > 0 {
		var near []Mv
		for _, m := range p.Legal() {
			q := p.Make(m)
			s := &searcher{rep: rep}
			sc := -s.negamax(q, 0, -mateScore-1, mateScore+1, 1)
			if sc >= score-lv.noise {
				near = append(near, m)
			}
		}
		if len(near) > 0 {
			best = near[rand.Intn(len(near))]
		}
	}
	return best, score, d
}

// ChessReply is what the bot sends: text, and optionally a board picture (PNG) with the text as its caption.
type ChessReply struct {
	Text   string
	PNG    []byte
	Markup string // inline keyboard JSON for tap-to-move
}

const chessHelp = "Chess: say \"chess\" to start (\"chess as black\", \"chess level 3\"; levels 1-5). Moves: e2e4 or Nf3 or O-O. Also: board, hint, undo, draw, resign, pgn, level N, new chess. I'm a classical search program, not a language model: I can lose, and higher levels think longer (up to a few seconds)."

func (c *Chess) Handle(chat, text string) (*ChessReply, bool) {
	t := strings.TrimSpace(text)
	low := strings.ToLower(t)
	m := reChessNew.FindStringSubmatch(t)
	isNew := m != nil
	if !isNew && !c.looksChess(low) {
		// only active games take bare moves; checked below
	}
	if !c.enabled() {
		if isNew {
			return &ChessReply{Text: "Chess is switched off: the owner has not set a chess save file for this bot."}, true
		}
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	f, err := c.load()
	if err != nil {
		if isNew {
			return &ChessReply{Text: "I can't read my chess save file, so I won't touch it. Please tell the owner."}, true
		}
		return nil, false
	}
	g := f.Games[chat]
	active := g != nil && !g.Over
	save := func() bool { return saveJSON(c.Path, f) == nil }
	if low == "chess help" || low == "help chess" {
		return &ChessReply{Text: chessHelp}, true
	}
	if reChessRest.MatchString(t) {
		isNew = true
		m = reChessNew.FindStringSubmatch("chess")
	}
	if isNew {
		if active && !reChessRest.MatchString(t) && len(g.Moves) > 0 {
			return c.reply(g, "You already have a game going. Say \"board\" to see it, \"resign\" to end it, or \"new chess\" to start over."), true
		}
		if _, ok := f.Games[chat]; !ok && len(f.Games) >= chessMaxGames {
			return &ChessReply{Text: "I can't start a new chess game right now (too many saved games)."}, true
		}
		ng := &ChessSave{White: !strings.EqualFold(m[1], "black"), Level: 3, Date: c.now().UTC().Format("2006-01-02")}
		if m[2] != "" {
			ng.Level, _ = strconv.Atoi(m[2])
		} else if g != nil && g.Level > 0 {
			ng.Level = g.Level
		}
		f.Games[chat] = ng
		g = ng
		intro := fmt.Sprintf("New game, level %d. You are %s. ", ng.Level, map[bool]string{true: "White", false: "Black"}[ng.White])
		if !ng.White {
			p, rep, _, _, _ := replay(g)
			mv, _, _ := c.engineMove(p, rep, g.Level)
			g.Moves = append(g.Moves, mv.String())
		}
		if !save() {
			return &ChessReply{Text: "I couldn't save the game, so it is not started."}, true
		}
		return c.reply(g, intro+"Send a move like e2e4 or Nf3."), true
	}
	if !active {
		if g != nil && g.Over && (low == "board" || low == "pgn") {
			return c.reply(g, g.Result), true
		}
		return nil, false
	}
	// commands for an active game
	switch {
	case low == "board" || low == "show board" || low == "chess board":
		return c.reply(g, ""), true
	case low == "pgn" || low == "chess pgn":
		_, _, _, sans, err := replay(g)
		if err != nil {
			return &ChessReply{Text: "I can't read this saved game."}, true
		}
		return &ChessReply{Text: pgn(g, sans)}, true
	case low == "resign":
		g.Over, g.Result = true, "You resigned. "+map[bool]string{true: "Black wins (0-1).", false: "White wins (1-0)."}[g.White]
		save()
		return c.reply(g, ""), true
	case low == "undo":
		if len(g.Moves) < 2 || (!g.White && len(g.Moves) < 3) {
			return &ChessReply{Text: "There's nothing to undo yet."}, true
		}
		g.Moves = g.Moves[:len(g.Moves)-2]
		if !save() {
			return &ChessReply{Text: "I couldn't save that."}, true
		}
		return c.reply(g, "Undid your last move and my reply."), true
	case low == "hint":
		p, rep, _, _, err := replay(g)
		if err != nil {
			return &ChessReply{Text: "I can't read this saved game."}, true
		}
		mv, sc, d := Best(p, 4, 2*time.Second, rep)
		return &ChessReply{Text: fmt.Sprintf("A move to consider: %s (my search, depth %d, evaluation %+.2f pawns for you; it can be wrong).", SAN(p, mv, p.Legal()), d, float64(sc)/100)}, true
	case low == "draw" || low == "offer draw":
		p, rep, _, _, err := replay(g)
		if err != nil {
			return &ChessReply{Text: "I can't read this saved game."}, true
		}
		_, sc, _ := Best(p.passTurn(), 3, time.Second, rep) // my evaluation of my own side
		if sc <= 30 && len(g.Moves) >= 20 {
			g.Over, g.Result = true, "Draw agreed (1/2-1/2)."
			save()
			return c.reply(g, ""), true
		}
		return &ChessReply{Text: "I decline the draw: I think I'm doing at least as well as you here (or it's too early). Keep playing."}, true
	}
	if lm := reChessLevel.FindStringSubmatch(t); lm != nil {
		g.Level, _ = strconv.Atoi(lm[1])
		save()
		return &ChessReply{Text: fmt.Sprintf("Level %d from my next move.", g.Level)}, true
	}
	if !reMoveish.MatchString(t) {
		return nil, false
	}
	p, rep, _, _, err := replay(g)
	if err != nil {
		return &ChessReply{Text: "I can't read this saved game. Say \"new chess\" to start again."}, true
	}
	if p.white != g.White {
		return &ChessReply{Text: "It isn't your turn."}, true
	}
	mv, why := ParseMove(p, t)
	if why != "" {
		if why == "ambiguous" {
			return &ChessReply{Text: "That move is ambiguous (two pieces can do it). Use coordinates, like e2e4."}, true
		}
		return &ChessReply{Text: "That isn't a legal move here. " + legalSample(p)}, true
	}
	yourSAN := SAN(p, mv, p.Legal())
	g.Moves = append(g.Moves, mv.String())
	p = p.Make(mv)
	rep[p.Key()]++
	msg := "You: " + yourSAN + "."
	if st := chessStatus(p, rep); st != "" {
		g.Over, g.Result = true, st
		save()
		return c.reply(g, msg+" "+st), true
	}
	em, sc, d := c.engineMove(p, rep, g.Level)
	mySAN := SAN(p, em, p.Legal())
	g.Moves = append(g.Moves, em.String())
	p = p.Make(em)
	rep[p.Key()]++
	msg += " Me: " + mySAN + "."
	if st := chessStatus(p, rep); st != "" {
		g.Over, g.Result = true, st
		msg += " " + st
	} else {
		if p.InCheck() {
			msg += " You are in check."
		}
		msg += " Your move."
	}
	_ = sc
	_ = d
	if !save() {
		return &ChessReply{Text: "I couldn't save the game, so that move was NOT recorded."}, true
	}
	return c.reply(g, msg), true
}

func (c *Chess) looksChess(low string) bool { return strings.Contains(low, "chess") }

// passTurn returns the position with the other side to move (used to ask the engine for its own evaluation).
func (p Pos) passTurn() Pos { p.white = !p.white; p.ep = -1; return p }

func legalSample(p Pos) string {
	legal := p.Legal()
	var sans []string
	for _, m := range legal {
		sans = append(sans, SAN(p, m, legal))
	}
	sort.Strings(sans)
	if len(sans) > 12 {
		sans = sans[:12]
	}
	return "A few legal moves: " + strings.Join(sans, ", ") + "."
}

func pgn(g *ChessSave, sans []string) string {
	res := "*"
	if g.Over {
		switch {
		case strings.Contains(g.Result, "1-0"):
			res = "1-0"
		case strings.Contains(g.Result, "0-1"):
			res = "0-1"
		case strings.Contains(g.Result, "1/2"):
			res = "1/2-1/2"
		}
	}
	white, black := "You", "tgbot (level "+strconv.Itoa(g.Level)+")"
	if !g.White {
		white, black = black, white
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[Event \"Telegram game\"]\n[Date \"%s\"]\n[White \"%s\"]\n[Black \"%s\"]\n[Result \"%s\"]\n\n", strings.ReplaceAll(g.Date, "-", "."), white, black, res)
	for i, s := range sans {
		if i%2 == 0 {
			fmt.Fprintf(&b, "%d. ", i/2+1)
		}
		b.WriteString(s + " ")
	}
	b.WriteString(res)
	return b.String()
}

// reply builds the answer with a board picture. The caption stays under Telegram's 1024-character limit.
func (c *Chess) reply(g *ChessSave, msg string) *ChessReply {
	p, rep, mvs, sans, err := replay(g)
	if err != nil {
		return &ChessReply{Text: "I can't read this saved game. Say \"new chess\" to start again."}
	}
	var last *Mv
	if len(mvs) > 0 {
		last = &mvs[len(mvs)-1]
	}
	if g.Over && msg == "" {
		msg = g.Result
	}
	if msg == "" {
		msg = "Your move."
		if p.white != g.White {
			msg = "My move."
		}
		if p.InCheck() {
			msg += " Check!"
		}
	}
	_ = rep
	var recent []string
	start := len(sans) - 6
	if start < 0 {
		start = 0
	}
	for i := start; i < len(sans); i++ {
		if i%2 == 0 {
			recent = append(recent, fmt.Sprintf("%d. %s", i/2+1, sans[i]))
		} else if i == start {
			recent = append(recent, fmt.Sprintf("%d... %s", i/2+1, sans[i]))
		} else {
			recent[len(recent)-1] += " " + sans[i]
		}
	}
	cap := msg
	if len(recent) > 0 {
		cap += "\nLast moves: " + strings.Join(recent, "  ")
	}
	if len([]rune(cap)) > 1000 {
		cap = string([]rune(cap)[:1000])
	}
	rep2 := &ChessReply{Text: cap, PNG: RenderBoard(p, g.White, last)}
	if !g.Over && p.white == g.White {
		rep2.Markup = chessMarkup(p, g.White, len(g.Moves), "")
	}
	return rep2
}

// ASCIIBoard is the text fallback when the picture can't be sent.
func ASCIIBoard(p Pos, whiteBottom bool) string {
	var b strings.Builder
	for i := 0; i < 8; i++ {
		r := 7 - i
		if !whiteBottom {
			r = i
		}
		b.WriteString(strconv.Itoa(r+1) + " ")
		for j := 0; j < 8; j++ {
			f := j
			if !whiteBottom {
				f = 7 - j
			}
			v := p.b[r*8+f]
			ch := "."
			if v != 0 {
				ch = string(" PNBRQK"[abs8(v)])
				if v < 0 {
					ch = strings.ToLower(ch)
				}
			}
			b.WriteString(ch + " ")
		}
		b.WriteString("\n")
	}
	if whiteBottom {
		b.WriteString("  a b c d e f g h")
	} else {
		b.WriteString("  h g f e d c b a")
	}
	return b.String()
}

// RenderBoard draws the position as a PNG with the player's colour at the bottom. Pieces are drawn as discs with the
// piece letter (K Q R B N P): white pieces light, black pieces dark. The last move and a king in check are tinted.
func RenderBoard(p Pos, whiteBottom bool, last *Mv) []byte {
	const sq, margin = 56, 24
	size := sq*8 + 2*margin
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	draw.Draw(img, img.Bounds(), image.NewUniform(rgb(244, 239, 236)), image.Point{}, draw.Src)
	light, dark := rgb(240, 217, 181), rgb(181, 136, 99)
	hl := rgb(205, 210, 106)
	checkC := rgb(235, 97, 80)
	kingSq := -1
	if p.InCheck() {
		kingSq = p.kingSq(p.white)
	}
	cell := func(s int) (int, int) {
		f, r := s%8, s/8
		if !whiteBottom {
			f, r = 7-f, 7-r
		}
		return margin + f*sq, margin + (7-r)*sq
	}
	small := face(fonts.bold, 12)
	for s := 0; s < 64; s++ {
		x, y := cell(s)
		c := dark
		if (s%8+s/8)%2 == 1 {
			c = light
		}
		if last != nil && (int8(s) == last.from || int8(s) == last.to) {
			c = hl
		}
		if s == kingSq {
			c = checkC
		}
		draw.Draw(img, image.Rect(x, y, x+sq, y+sq), image.NewUniform(c), image.Point{}, draw.Src)
		v := p.b[s]
		if v == 0 {
			continue
		}
		sp := sprite(int(v), sq-2)
		draw.Draw(img, image.Rect(x+1, y+1, x+sq-1, y+sq-1), sp, image.Point{}, draw.Over)
	}
	for i := 0; i < 8; i++ {
		fl, rk := string(rune('a'+i)), strconv.Itoa(i+1)
		if !whiteBottom {
			fl, rk = string(rune('a'+7-i)), strconv.Itoa(8-i)
		}
		drawText(img, small, rgb(88, 82, 84), margin+i*sq+sq/2-textW(small, fl)/2, size-8, fl)
		drawText(img, small, rgb(88, 82, 84), 8, margin+(7-i)*sq+sq/2+4, rk)
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func chessDisc(img *image.RGBA, cx, cy, r int, c color.RGBA) {
	for y := -r; y <= r; y++ {
		for x := -r; x <= r; x++ {
			if x*x+y*y <= r*r {
				img.SetRGBA(cx+x, cy+y, c)
			}
		}
	}
}
