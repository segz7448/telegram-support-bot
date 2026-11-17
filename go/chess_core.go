package main

import (
	"fmt"
	"strings"
	"time"
)

// Chess rules and a classical search engine. No learning, no model: legal-move generation plus alpha-beta search
// over a hand-written evaluation (material and piece placement). Correctness of the rules is checked by perft.

const (
	wP, wN, wB, wR, wQ, wK = 1, 2, 3, 4, 5, 6
)

type Pos struct {
	b      [64]int8 // sq = rank*8+file, rank 0 = White's first rank; white pieces positive
	white  bool     // side to move
	castle uint8    // 1 white short, 2 white long, 4 black short, 8 black long
	ep     int8     // en-passant target square or -1
	half   int      // half-move clock
	full   int
}

type Mv struct{ from, to, promo int8 }

const startFEN = "rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1"

func ParseFEN(f string) (Pos, error) {
	var p Pos
	parts := strings.Fields(f)
	if len(parts) < 4 {
		return p, fmt.Errorf("bad fen")
	}
	rows := strings.Split(parts[0], "/")
	if len(rows) != 8 {
		return p, fmt.Errorf("bad fen rows")
	}
	for i, row := range rows {
		rank, file := 7-i, 0
		for _, ch := range row {
			if ch >= '1' && ch <= '8' {
				file += int(ch - '0')
				continue
			}
			if file > 7 {
				return p, fmt.Errorf("bad fen row")
			}
			v := int8(strings.IndexRune(".PNBRQK", ch))
			if v < 0 {
				v = -int8(strings.IndexRune(".pnbrqk", ch))
			}
			if v == 0 || v == -0 && ch != '.' {
				return p, fmt.Errorf("bad fen piece")
			}
			p.b[rank*8+file] = v
			file++
		}
		if file != 8 {
			return p, fmt.Errorf("bad fen row length")
		}
	}
	p.white = parts[1] == "w"
	for _, ch := range parts[2] {
		switch ch {
		case 'K':
			p.castle |= 1
		case 'Q':
			p.castle |= 2
		case 'k':
			p.castle |= 4
		case 'q':
			p.castle |= 8
		}
	}
	p.ep = -1
	if parts[3] != "-" && len(parts[3]) == 2 {
		p.ep = int8(int(parts[3][1]-'1')*8 + int(parts[3][0]-'a'))
	}
	if len(parts) > 5 {
		fmt.Sscan(parts[4], &p.half)
		fmt.Sscan(parts[5], &p.full)
	}
	if p.full < 1 {
		p.full = 1
	}
	return p, nil
}

func StartPos() Pos { p, _ := ParseFEN(startFEN); return p }

func sqName(s int8) string { return string([]byte{byte('a' + s%8), byte('1' + s/8)}) }

func (m Mv) String() string {
	s := sqName(m.from) + sqName(m.to)
	if m.promo != 0 {
		s += string("  nbrq"[m.promo])
	}
	return s
}

// Key identifies a position for repetition (board, side, castling, en passant; not the clocks).
func (p *Pos) Key() string {
	var sb strings.Builder
	for _, v := range p.b {
		sb.WriteByte(byte(v + 'a' + 6))
	}
	fmt.Fprintf(&sb, "%v%d%d", p.white, p.castle, p.ep)
	return sb.String()
}

var (
	knightD = [8][2]int{{1, 2}, {2, 1}, {2, -1}, {1, -2}, {-1, -2}, {-2, -1}, {-2, 1}, {-1, 2}}
	kingD   = [8][2]int{{1, 0}, {1, 1}, {0, 1}, {-1, 1}, {-1, 0}, {-1, -1}, {0, -1}, {1, -1}}
	bishopD = [4][2]int{{1, 1}, {1, -1}, {-1, 1}, {-1, -1}}
	rookD   = [4][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}}
)

func onBoard(f, r int) bool { return f >= 0 && f < 8 && r >= 0 && r < 8 }

// attacked reports whether square s is attacked by the given colour.
func (p *Pos) attacked(s int, byWhite bool) bool {
	f, r := s%8, s/8
	sign := int8(1)
	if !byWhite {
		sign = -1
	}
	pr := r - 1 // a white pawn attacking s stands one rank below it
	if !byWhite {
		pr = r + 1
	}
	for _, df := range []int{-1, 1} {
		if onBoard(f+df, pr) && p.b[pr*8+f+df] == sign*wP {
			return true
		}
	}
	for _, d := range knightD {
		if onBoard(f+d[0], r+d[1]) && p.b[(r+d[1])*8+f+d[0]] == sign*wN {
			return true
		}
	}
	for _, d := range kingD {
		if onBoard(f+d[0], r+d[1]) && p.b[(r+d[1])*8+f+d[0]] == sign*wK {
			return true
		}
	}
	for _, d := range bishopD {
		for x, y := f+d[0], r+d[1]; onBoard(x, y); x, y = x+d[0], y+d[1] {
			v := p.b[y*8+x]
			if v != 0 {
				if v == sign*wB || v == sign*wQ {
					return true
				}
				break
			}
		}
	}
	for _, d := range rookD {
		for x, y := f+d[0], r+d[1]; onBoard(x, y); x, y = x+d[0], y+d[1] {
			v := p.b[y*8+x]
			if v != 0 {
				if v == sign*wR || v == sign*wQ {
					return true
				}
				break
			}
		}
	}
	return false
}

func (p *Pos) kingSq(white bool) int {
	k := int8(wK)
	if !white {
		k = -wK
	}
	for i, v := range p.b {
		if v == k {
			return i
		}
	}
	return -1
}

func (p *Pos) InCheck() bool {
	k := p.kingSq(p.white)
	return k >= 0 && p.attacked(k, !p.white)
}

func (p *Pos) pseudo(caps bool) []Mv {
	ms := make([]Mv, 0, 48)
	add := func(from, to int, promo int8) { ms = append(ms, Mv{int8(from), int8(to), promo}) }
	us := int8(1)
	if !p.white {
		us = -1
	}
	for s := 0; s < 64; s++ {
		v := p.b[s]
		if v == 0 || (v > 0) != p.white {
			continue
		}
		f, r := s%8, s/8
		switch t := v * us; t {
		case wP:
			dir, start, last := 1, 1, 7
			if !p.white {
				dir, start, last = -1, 6, 0
			}
			nr := r + dir
			if nr < 0 || nr > 7 {
				continue
			}
			if !caps || nr == last {
				if p.b[nr*8+f] == 0 {
					if nr == last {
						for _, pr := range []int8{wQ, wR, wB, wN} {
							add(s, nr*8+f, pr)
						}
					} else {
						add(s, nr*8+f, 0)
						if r == start && p.b[(r+2*dir)*8+f] == 0 && !caps {
							add(s, (r+2*dir)*8+f, 0)
						}
					}
				}
			}
			for _, df := range []int{-1, 1} {
				if !onBoard(f+df, nr) {
					continue
				}
				to := nr*8 + f + df
				tv := p.b[to]
				if (tv != 0 && (tv > 0) != p.white) || int8(to) == p.ep {
					if nr == last {
						for _, pr := range []int8{wQ, wR, wB, wN} {
							add(s, to, pr)
						}
					} else {
						add(s, to, 0)
					}
				}
			}
		case wN, wK:
			dd := knightD[:]
			if t == wK {
				dd = kingD[:]
			}
			for _, d := range dd {
				x, y := f+d[0], r+d[1]
				if !onBoard(x, y) {
					continue
				}
				tv := p.b[y*8+x]
				if tv == 0 && !caps || tv != 0 && (tv > 0) != p.white {
					add(s, y*8+x, 0)
				}
			}
			if t == wK && !caps {
				p.castleMoves(s, add)
			}
		default:
			var dd [][2]int
			if t == wB || t == wQ {
				dd = append(dd, bishopD[:]...)
			}
			if t == wR || t == wQ {
				dd = append(dd, rookD[:]...)
			}
			for _, d := range dd {
				for x, y := f+d[0], r+d[1]; onBoard(x, y); x, y = x+d[0], y+d[1] {
					tv := p.b[y*8+x]
					if tv == 0 {
						if !caps {
							add(s, y*8+x, 0)
						}
						continue
					}
					if (tv > 0) != p.white {
						add(s, y*8+x, 0)
					}
					break
				}
			}
		}
	}
	return ms
}

func (p *Pos) castleMoves(s int, add func(from, to int, promo int8)) {
	if p.InCheck() {
		return
	}
	if p.white && s == 4 {
		if p.castle&1 != 0 && p.b[7] == wR && p.b[5] == 0 && p.b[6] == 0 && !p.attacked(5, false) && !p.attacked(6, false) {
			add(4, 6, 0)
		}
		if p.castle&2 != 0 && p.b[0] == wR && p.b[1] == 0 && p.b[2] == 0 && p.b[3] == 0 && !p.attacked(3, false) && !p.attacked(2, false) {
			add(4, 2, 0)
		}
	}
	if !p.white && s == 60 {
		if p.castle&4 != 0 && p.b[63] == -wR && p.b[61] == 0 && p.b[62] == 0 && !p.attacked(61, true) && !p.attacked(62, true) {
			add(60, 62, 0)
		}
		if p.castle&8 != 0 && p.b[56] == -wR && p.b[57] == 0 && p.b[58] == 0 && p.b[59] == 0 && !p.attacked(59, true) && !p.attacked(58, true) {
			add(60, 58, 0)
		}
	}
}

// Make returns the position after m (m must be pseudo-legal).
func (p Pos) Make(m Mv) Pos {
	v := p.b[m.from]
	cap := p.b[m.to]
	isPawn := v == wP || v == -wP
	p.b[m.from] = 0
	if isPawn && m.to == p.ep && cap == 0 {
		if p.white {
			p.b[m.to-8] = 0
		} else {
			p.b[m.to+8] = 0
		}
		cap = 1
	}
	if m.promo != 0 {
		if p.white {
			v = m.promo
		} else {
			v = -m.promo
		}
	}
	p.b[m.to] = v
	if (v == wK || v == -wK) && (m.to-m.from == 2 || m.from-m.to == 2) {
		switch m.to {
		case 6:
			p.b[7], p.b[5] = 0, wR
		case 2:
			p.b[0], p.b[3] = 0, wR
		case 62:
			p.b[63], p.b[61] = 0, -wR
		case 58:
			p.b[56], p.b[59] = 0, -wR
		}
	}
	// castling rights
	for _, s := range []int8{m.from, m.to} {
		switch s {
		case 4:
			p.castle &^= 3
		case 60:
			p.castle &^= 12
		case 7:
			p.castle &^= 1
		case 0:
			p.castle &^= 2
		case 63:
			p.castle &^= 4
		case 56:
			p.castle &^= 8
		}
	}
	p.ep = -1
	if isPawn && (m.to-m.from == 16 || m.from-m.to == 16) {
		p.ep = (m.from + m.to) / 2
	}
	if isPawn || cap != 0 {
		p.half = 0
	} else {
		p.half++
	}
	if !p.white {
		p.full++
	}
	p.white = !p.white
	return p
}

// Legal returns all legal moves.
func (p *Pos) Legal() []Mv {
	ps := p.pseudo(false)
	out := ps[:0]
	for _, m := range ps {
		q := p.Make(m)
		k := q.kingSq(p.white)
		if k >= 0 && !q.attacked(k, !p.white) {
			out = append(out, m)
		}
	}
	return out
}

func Perft(p Pos, depth int) int {
	if depth == 0 {
		return 1
	}
	ms := p.Legal()
	if depth == 1 {
		return len(ms)
	}
	n := 0
	for _, m := range ms {
		n += Perft(p.Make(m), depth-1)
	}
	return n
}

// InsufficientMaterial: K v K, K+minor v K, K+B v K+B on same-coloured squares.
func (p *Pos) InsufficientMaterial() bool {
	var minors, bishopsW, bishopsB int
	for s, v := range p.b {
		switch a := abs8(v); a {
		case 0, wK:
		case wP, wR, wQ:
			return false
		case wN:
			minors++
		case wB:
			minors++
			if (s%8+s/8)%2 == 0 {
				bishopsW++
			} else {
				bishopsB++
			}
		}
	}
	if minors <= 1 {
		return true
	}
	return minors == bishopsW+bishopsB && (bishopsW == 0 || bishopsB == 0)
}

func abs8(v int8) int8 {
	if v < 0 {
		return -v
	}
	return v
}

// ---- engine ----

var pieceVal = [7]int{0, 100, 320, 330, 500, 900, 0}

// Piece-square bonuses for White from White's view (rank 0 first). Black mirrors vertically.
var pstP = [64]int{0, 0, 0, 0, 0, 0, 0, 0, 5, 10, 10, -20, -20, 10, 10, 5, 5, -5, -10, 0, 0, -10, -5, 5, 0, 0, 0, 20, 20, 0, 0, 0, 5, 5, 10, 25, 25, 10, 5, 5, 10, 10, 20, 30, 30, 20, 10, 10, 50, 50, 50, 50, 50, 50, 50, 50, 0, 0, 0, 0, 0, 0, 0, 0}
var pstN = [64]int{-50, -40, -30, -30, -30, -30, -40, -50, -40, -20, 0, 5, 5, 0, -20, -40, -30, 5, 10, 15, 15, 10, 5, -30, -30, 0, 15, 20, 20, 15, 0, -30, -30, 5, 15, 20, 20, 15, 5, -30, -30, 0, 10, 15, 15, 10, 0, -30, -40, -20, 0, 0, 0, 0, -20, -40, -50, -40, -30, -30, -30, -30, -40, -50}
var pstB = [64]int{-20, -10, -10, -10, -10, -10, -10, -20, -10, 5, 0, 0, 0, 0, 5, -10, -10, 10, 10, 10, 10, 10, 10, -10, -10, 0, 10, 10, 10, 10, 0, -10, -10, 5, 5, 10, 10, 5, 5, -10, -10, 0, 5, 10, 10, 5, 0, -10, -10, 0, 0, 0, 0, 0, 0, -10, -20, -10, -10, -10, -10, -10, -10, -20}
var pstR = [64]int{0, 0, 0, 5, 5, 0, 0, 0, -5, 0, 0, 0, 0, 0, 0, -5, -5, 0, 0, 0, 0, 0, 0, -5, -5, 0, 0, 0, 0, 0, 0, -5, -5, 0, 0, 0, 0, 0, 0, -5, -5, 0, 0, 0, 0, 0, 0, -5, 5, 10, 10, 10, 10, 10, 10, 5, 0, 0, 0, 0, 0, 0, 0, 0}
var pstKmid = [64]int{20, 30, 10, 0, 0, 10, 30, 20, 20, 20, 0, 0, 0, 0, 20, 20, -10, -20, -20, -20, -20, -20, -20, -10, -20, -30, -30, -40, -40, -30, -30, -20, -30, -40, -40, -50, -50, -40, -40, -30, -30, -40, -40, -50, -50, -40, -40, -30, -30, -40, -40, -50, -50, -40, -40, -30, -30, -40, -40, -50, -50, -40, -40, -30}
var pstKend = [64]int{-50, -30, -30, -30, -30, -30, -30, -50, -30, -30, 0, 0, 0, 0, -30, -30, -30, -10, 20, 30, 30, 20, -10, -30, -30, -10, 30, 40, 40, 30, -10, -30, -30, -10, 30, 40, 40, 30, -10, -30, -30, -10, 20, 30, 30, 20, -10, -30, -30, -20, -10, 0, 0, -10, -20, -30, -50, -40, -30, -20, -20, -30, -40, -50}

// Eval returns a score in centipawns from the side to move's point of view.
func (p *Pos) Eval() int {
	score, material := 0, 0
	for _, v := range p.b {
		if v == 0 {
			continue
		}
		a := abs8(v)
		if a != wK && a != wP {
			material += pieceVal[a]
		}
	}
	for s, v := range p.b {
		if v == 0 {
			continue
		}
		a := abs8(v)
		idx := s
		if v < 0 {
			idx = (7-s/8)*8 + s%8
		}
		x := pieceVal[a]
		switch a {
		case wP:
			x += pstP[idx]
		case wN:
			x += pstN[idx]
		case wB:
			x += pstB[idx]
		case wR:
			x += pstR[idx]
		case wQ:
		case wK:
			if material <= 2600 {
				x += pstKend[idx]
			} else {
				x += pstKmid[idx]
			}
		}
		if v > 0 {
			score += x
		} else {
			score -= x
		}
	}
	if !p.white {
		return -score
	}
	return score
}

type searcher struct {
	deadline time.Time
	nodes    int
	stop     bool
	rep      map[string]int // positions seen in the game so far (for repetition inside the search)
}

const mateScore = 30000

func (s *searcher) timeUp() bool {
	s.nodes++
	if s.nodes&2047 == 0 && !s.deadline.IsZero() && time.Now().After(s.deadline) {
		s.stop = true
	}
	return s.stop
}

func orderMoves(p *Pos, ms []Mv) {
	score := func(m Mv) int {
		sc := 0
		if v := p.b[m.to]; v != 0 {
			sc = 10*pieceVal[abs8(v)] - pieceVal[abs8(p.b[m.from])]/10 + 1000
		} else if m.to == p.ep && abs8(p.b[m.from]) == wP {
			sc = 1000 + 900
		}
		if m.promo != 0 {
			sc += pieceVal[m.promo]
		}
		return sc
	}
	for i := 1; i < len(ms); i++ {
		for j := i; j > 0 && score(ms[j]) > score(ms[j-1]); j-- {
			ms[j], ms[j-1] = ms[j-1], ms[j]
		}
	}
}

func (s *searcher) quiesce(p Pos, alpha, beta, ply int) int {
	if s.timeUp() {
		return 0
	}
	stand := p.Eval()
	if stand >= beta {
		return stand
	}
	if stand > alpha {
		alpha = stand
	}
	ps := p.pseudo(true)
	orderMoves(&p, ps)
	for _, m := range ps {
		q := p.Make(m)
		k := q.kingSq(p.white)
		if k < 0 || q.attacked(k, !p.white) {
			continue
		}
		sc := -s.quiesce(q, -beta, -alpha, ply+1)
		if s.stop {
			return 0
		}
		if sc >= beta {
			return sc
		}
		if sc > alpha {
			alpha = sc
		}
	}
	return alpha
}

func (s *searcher) negamax(p Pos, depth, alpha, beta, ply int) int {
	if s.timeUp() {
		return 0
	}
	if p.half >= 100 || p.InsufficientMaterial() {
		return 0
	}
	if ply > 0 && s.rep[p.Key()] >= 1 {
		return 0
	}
	inCheck := p.InCheck()
	if inCheck {
		depth++
	}
	if depth <= 0 {
		return s.quiesce(p, alpha, beta, ply)
	}
	ms := p.Legal()
	if len(ms) == 0 {
		if inCheck {
			return -(mateScore - ply)
		}
		return 0
	}
	orderMoves(&p, ms)
	best := -mateScore - 1
	for _, m := range ms {
		sc := -s.negamax(p.Make(m), depth-1, -beta, -alpha, ply+1)
		if s.stop {
			return 0
		}
		if sc > best {
			best = sc
		}
		if sc > alpha {
			alpha = sc
		}
		if alpha >= beta {
			break
		}
	}
	return best
}

// Best searches with iterative deepening to maxDepth or until the time limit; it returns the best move of the last
// completed depth, its score (centipawns for the side to move) and the depth reached.
func Best(p Pos, maxDepth int, limit time.Duration, rep map[string]int) (Mv, int, int) {
	ms := p.Legal()
	if len(ms) == 0 {
		return Mv{}, 0, 0
	}
	s := &searcher{rep: rep}
	if limit > 0 {
		s.deadline = time.Now().Add(limit)
	}
	best, bestScore, reached := ms[0], 0, 0
	orderMoves(&p, ms)
	for d := 1; d <= maxDepth; d++ {
		curBest, curScore := Mv{}, -mateScore-1
		alpha := -mateScore - 1
		for _, m := range ms {
			sc := -s.negamax(p.Make(m), d-1, -mateScore-1, -alpha, 1)
			if s.stop {
				break
			}
			if sc > curScore {
				curBest, curScore = m, sc
			}
			if sc > alpha {
				alpha = sc
			}
		}
		if s.stop {
			break
		}
		best, bestScore, reached = curBest, curScore, d
		// search the previous best first next time
		for i, m := range ms {
			if m == best {
				copy(ms[1:i+1], ms[0:i])
				ms[0] = best
				break
			}
		}
		if bestScore > mateScore-100 {
			break
		}
	}
	return best, bestScore, reached
}
