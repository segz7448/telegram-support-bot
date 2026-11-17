package main

import (
	"math/rand"
	"os"
	"testing"
)

// chessMatch plays one game: both sides use the real engine settings of their level; the first few plies are random
// (seeded) so games differ. Returns 1 white win, -1 black win, 0 draw or unfinished at 160 plies.
func chessMatch(c *Chess, wl, bl int, rnd *rand.Rand) int {
	p := StartPos()
	rep := map[string]int{p.Key(): 1}
	for ply := 0; ply < 160; ply++ {
		legal := p.Legal()
		if len(legal) == 0 {
			if p.InCheck() {
				if p.white {
					return -1
				}
				return 1
			}
			return 0
		}
		if st := chessStatus(p, rep); st != "" && ply > 0 {
			return 0
		}
		var m Mv
		if ply < 6 {
			m = legal[rnd.Intn(len(legal))]
		} else {
			lv := bl
			if p.white {
				lv = wl
			}
			m, _, _ = c.engineMove(p, rep, lv)
		}
		p = p.Make(m)
		rep[p.Key()]++
	}
	return 0
}

func TestChessLevelsMatch(t *testing.T) {
	if os.Getenv("CHESS_MATCH") == "" {
		t.Skip("slow; set CHESS_MATCH=1")
	}
	rnd := rand.New(rand.NewSource(21))
	c := &Chess{}
	for _, pair := range [][2]int{{3, 1}, {4, 2}} {
		hi, lo, dr := 0, 0, 0
		for i := 0; i < 6; i++ {
			w, b := pair[0], pair[1]
			if i%2 == 1 {
				w, b = b, w
			}
			r := chessMatch(c, w, b, rnd)
			switch {
			case r == 0:
				dr++
			case (r == 1) == (w == pair[0]):
				hi++
			default:
				lo++
			}
		}
		t.Logf("chess level %d vs level %d, 6 games, colours alternating, random first 3 moves each: higher wins %d, lower wins %d, draws/unfinished %d", pair[0], pair[1], hi, lo, dr)
	}
}
