package main

import (
	"math/rand"
	"path/filepath"
	"strings"
	"testing"
)

func TestDrRules(t *testing.T) {
	b := drStart()
	if n := len(b.moves(1)); n != 7 {
		t.Fatalf("opening moves %d", n)
	}
	// forced capture: red man c3 (18), black man d4 (27), e5 (36) empty
	var e drBoard
	e[18], e[27] = 1, -1
	mv := e.moves(1)
	if len(mv) != 1 || mv[0].String() != "c3-e5" {
		t.Fatalf("forced capture: %v", mv)
	}
	// multi-jump: red c3, black d4 and f6; red must go c3-e5-g7
	e = drBoard{}
	e[18], e[27], e[45] = 1, -1, -1
	mv = e.moves(1)
	if len(mv) != 1 || mv[0].String() != "c3-e5-g7" || len(mv[0].caps) != 2 {
		t.Fatalf("multi-jump: %v", mv)
	}
	// promotion ends a jump: red f6 jumps g7 to h8 and becomes king
	e = drBoard{}
	e[45], e[54] = 1, -1
	mv = e.moves(1)
	if len(mv) != 1 || mv[0].String() != "f6-h8" {
		t.Fatalf("promo jump: %v", mv)
	}
	e.apply(mv[0])
	if e[63] != 2 {
		t.Fatal("should be king")
	}
	// men can't capture backward, kings can
	e = drBoard{}
	e[27], e[18] = 1, -1
	if m := e.moves(1); len(m) == 0 || m[0].caps != nil {
		t.Fatalf("man must not jump backward: %v", m)
	}
	e = drBoard{}
	e[27], e[18] = 2, -1
	if m := e.moves(1); len(m) != 1 || m[0].String() != "d4-b2" {
		t.Fatalf("king backward: %v", m)
	}
	// no moves = loss
	e = drBoard{}
	e[0] = 1
	e[9], e[18] = -1, -1
	if len(e.moves(1)) != 0 {
		t.Fatal("blocked piece")
	}
}

func drPlay(l1, l2 int, rnd *rand.Rand) int { // 1, -1 or 0 for the winner
	b := drStart()
	side := int8(1)
	quiet := 0
	for ply := 0; ply < 300; ply++ {
		mv := b.moves(side)
		if len(mv) == 0 {
			return int(-side)
		}
		lv := l1
		if side == -1 {
			lv = l2
		}
		var m drMove
		if lv == 0 {
			m = mv[rnd.Intn(len(mv))]
		} else {
			m = drBest(b, side, lv)
		}
		v := b[m.path[0]]
		if m.caps != nil || v == 1 || v == -1 {
			quiet = 0
		} else {
			quiet++
		}
		b.apply(m)
		side = -side
		if quiet >= 80 {
			return 0
		}
	}
	return 0
}

func TestDrStrength(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	rnd := rand.New(rand.NewSource(5))
	for _, lv := range []int{1, 3} {
		w, l, d := 0, 0, 0
		for i := 0; i < 10; i++ {
			a, bl := lv, 0
			eng := 1
			if i%2 == 1 {
				a, bl, eng = 0, lv, -1
			}
			switch r := drPlay(a, bl, rnd); {
			case r == 0:
				d++
			case r == eng:
				w++
			default:
				l++
			}
		}
		t.Logf("level %d vs random, 10 games alternating colour: W%d L%d D%d", lv, w, l, d)
		if lv == 3 && w < 8 {
			t.Fatal("level 3 should beat random")
		}
	}
	r1 := drPlay(4, 2, rnd)
	r2 := drPlay(2, 4, rnd)
	t.Logf("L4 (red) vs L2: winner %d; L2 (red) vs L4: winner %d (1 = red)", r1, r2)
}

func TestDrGame(t *testing.T) {
	d := &Draughts{Path: filepath.Join(t.TempDir(), "dr.json")}
	rep, ok := d.Handle("1", "draughts level 2")
	if !ok || len(rep.PNG) < 500 || !strings.Contains(rep.Markup, "dr:c3:0") {
		t.Fatalf("start %+v", rep)
	}
	pop, mk, _ := d.Tap("1", "dr:c3:0")
	if !strings.Contains(pop, "Selected c3") || !strings.Contains(mk, "dr:b4:0") {
		t.Fatalf("select %q %s", pop, mk)
	}
	_, mk2, rep := d.Tap("1", "dr:b4:0")
	if rep == nil || mk2 != "-" || !strings.Contains(rep.Text, "You: c3-b4.") || !strings.Contains(rep.Text, "Me:") {
		t.Fatalf("move %+v", rep)
	}
	rep, _ = d.Handle("1", "move a1-b2")
	if !strings.Contains(rep.Text, "isn't a legal move") {
		t.Fatal(rep.Text)
	}
	if _, ok := d.Handle("2", "move c3-d4"); ok {
		t.Fatal("isolation")
	}
	rep, _ = d.Handle("3", "checkers as black")
	if !strings.Contains(rep.Text, "Me: ") {
		t.Fatal(rep.Text)
	}
	// whole game, human always takes the first legal move
	d.Handle("9", "draughts level 1")
	for i := 0; i < 300; i++ {
		f, _ := d.load()
		g := f.Games["9"]
		if g.Over {
			break
		}
		b, side, _, quiet, err := g.replay()
		if err != nil {
			t.Fatal(err)
		}
		if g.status(b, side, quiet) != "" {
			break
		}
		if side != g.humanSide() {
			break
		}
		d.play(g, b.moves(side)[0], func() bool { return saveJSON(d.Path, f) == nil })
	}
}
