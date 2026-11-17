package main

import (
	"math/rand"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestRvRules(t *testing.T) {
	b := rvStart()
	m := b.moves(1)
	sort.Ints(m)
	if len(m) != 4 || m[0] != 19 || m[1] != 26 || m[2] != 37 || m[3] != 44 {
		t.Fatalf("opening moves %v", m)
	}
	b.play(19, 1) // d3 flips d4
	x, o := b.count()
	if x != 4 || o != 1 {
		t.Fatalf("after d3: %d %d", x, o)
	}
	if len(b.flips(0, 1)) != 0 {
		t.Fatal("a1 illegal")
	}
}

func rvPlay(l1, l2 int, rnd *rand.Rand) (int, int) { // levels (0 = random); returns black, white counts
	b := rvStart()
	who := int8(1)
	for guard := 0; guard < 200; guard++ {
		mv := b.moves(who)
		if len(mv) == 0 {
			if len(b.moves(3-who)) == 0 {
				break
			}
			who = 3 - who
			continue
		}
		lv := l1
		if who == 2 {
			lv = l2
		}
		var s int
		if lv == 0 {
			s = mv[rnd.Intn(len(mv))]
		} else {
			s = rvBest(b, who, lv)
		}
		b.play(s, who)
		who = 3 - who
	}
	return b.count()
}

func TestRvStrength(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	rnd := rand.New(rand.NewSource(3))
	for _, lv := range []int{1, 3} {
		w, l, d := 0, 0, 0
		for i := 0; i < 20; i++ {
			a, bl := lv, 0
			if i%2 == 1 {
				a, bl = 0, lv
			}
			x, o := rvPlay(a, bl, rnd)
			if i%2 == 1 {
				x, o = o, x
			}
			switch {
			case x > o:
				w++
			case x < o:
				l++
			default:
				d++
			}
		}
		t.Logf("level %d vs random, 20 games alternating colour: W%d L%d D%d", lv, w, l, d)
		if lv == 3 && w < 18 {
			t.Fatal("level 3 should beat random almost always")
		}
	}
	x, o := rvPlay(4, 2, rnd)
	x2, o2 := rvPlay(2, 4, rnd)
	t.Logf("L4 as black vs L2: %d-%d; L2 as black vs L4: %d-%d (L4 discs %d and %d)", x, o, x2, o2, x, o2)
}

func TestRvGame(t *testing.T) {
	r := &Reversi{Path: filepath.Join(t.TempDir(), "rv.json")}
	rep, ok := r.Handle("1", "reversi level 2")
	if !ok || len(rep.PNG) < 500 || !strings.Contains(rep.Markup, "rv:19:0") {
		t.Fatalf("start %v", rep)
	}
	rep, _ = r.Handle("1", "move a1")
	if !strings.Contains(rep.Text, "isn't a legal move") {
		t.Fatal(rep.Text)
	}
	pop, mk, rep := r.Tap("1", "rv:19:0")
	if rep == nil || mk != "-" || !strings.Contains(rep.Text, "You: d3.") || !strings.Contains(rep.Text, "Me:") {
		t.Fatalf("tap %q %v", pop, rep)
	}
	if pop, _, _ := r.Tap("1", "rv:19:0"); !strings.Contains(pop, "out of date") {
		t.Fatal(pop)
	}
	if _, ok := r.Handle("2", "move d3"); ok {
		t.Fatal("isolation")
	}
	rep, _ = r.Handle("5", "reversi as white")
	if !strings.Contains(rep.Text, "I open:") {
		t.Fatal(rep.Text)
	}
	// play a whole game with level 1 vs a human who always takes the first legal move
	r.Handle("9", "reversi level 1")
	for i := 0; i < 70; i++ {
		f, _ := r.load()
		g := f.Games["9"]
		if g.Over {
			break
		}
		b, who, _, _ := g.replay()
		if who != int8(g.Human) {
			break
		}
		mv := b.moves(who)
		rp := r.move(g, mv[0], func() bool { return saveJSON(r.Path, f) == nil })
		_ = rp
	}
	f, _ := r.load()
	if g := f.Games["9"]; g != nil {
		if _, _, _, err := g.replay(); err != nil {
			t.Fatal(err)
		}
	}
}
