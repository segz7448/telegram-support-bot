package main

import (
	"math/rand"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func newLudoGame() *LudoSave {
	g := &LudoSave{Level: 2}
	for s := 0; s < 2; s++ {
		for i := 0; i < 4; i++ {
			g.Tok[s][i] = -1
		}
	}
	return g
}

func TestLudoRules(t *testing.T) {
	g := newLudoGame()
	if len(g.legal(0, 5)) != 0 || len(g.legal(0, 6)) != 4 {
		t.Fatal("need a six to leave base")
	}
	g.Tok[0][0] = 54
	if len(g.legal(0, 3)) != 0 && len(g.legal(0, 2)) == 0 {
		t.Fatal("exact roll")
	}
	for _, r := range g.legal(0, 3) {
		if r == 0 {
			t.Fatal("54+3 overshoots home")
		}
	}
	// capture on a non-safe square: red at p=3 (abs 3), blue at p such that abs(1,p)=6 -> p=32; red moves 3 -> 6
	g = newLudoGame()
	g.Tok[0][0] = 3
	g.Tok[1][0] = 32
	if !g.move(0, 0, 3) || g.Tok[1][0] != -1 {
		t.Fatal("should capture")
	}
	// no capture on a safe square (abs 8): red p=5 moves 3 to p=8; blue at abs 8 -> p=34
	g = newLudoGame()
	g.Tok[0][0] = 5
	g.Tok[1][0] = 34
	if g.move(0, 0, 3) || g.Tok[1][0] != 34 {
		t.Fatal("safe square must not capture")
	}
	// home column tokens are never capturable
	g = newLudoGame()
	g.Tok[0][0] = 52
	g.Tok[1][0] = 10
	g.move(1, 0, 3)
	if g.Tok[0][0] != 52 {
		t.Fatal("home column safe")
	}
}

func ludoSim(l *Ludo, lv [2]int, rnd *rand.Rand) int { // returns winner 0/1, -1 if cut off
	g := newLudoGame()
	side := 0
	for turn := 0; turn < 4000; turn++ {
		sixes := 0
		for {
			r := rnd.Intn(6) + 1
			if r == 6 {
				sixes++
				if sixes == 3 {
					break
				}
			}
			lg := g.legal(side, r)
			if len(lg) > 0 {
				i := l.choose(g, side, r, lv[side], lg)
				g.move(side, i, r)
				if g.won(side) {
					return side
				}
			}
			if r != 6 {
				break
			}
		}
		side = 1 - side
	}
	return -1
}

func TestLudoStrength(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	rnd := rand.New(rand.NewSource(11))
	l := &Ludo{}
	for _, lv := range []int{2, 3} {
		wins, n := 0, 400
		for i := 0; i < n; i++ {
			a := i % 2 // which side the tested level plays; alternates who starts
			var lvs [2]int
			lvs[a], lvs[1-a] = lv, 1
			if ludoSim(l, lvs, rnd) == a {
				wins++
			}
		}
		t.Logf("level %d vs level 1 (random legal moves), %d games alternating who moves first: %d wins (%.1f%%)", lv, n, wins, 100*float64(wins)/float64(n))
		if wins*100/n < 60 {
			t.Fatalf("level %d should clearly beat random", lv)
		}
	}
	w, n := 0, 400
	for i := 0; i < n; i++ {
		a := i % 2
		var lvs [2]int
		lvs[a], lvs[1-a] = 3, 2
		if ludoSim(l, lvs, rnd) == a {
			w++
		}
	}
	t.Logf("level 3 vs level 2, %d games alternating first: %d wins (%.1f%%)", n, w, 100*float64(w)/float64(n))
	// fairness: identical level on both sides should be near 50% for the side moving first
	f0, nn := 0, 600
	for i := 0; i < nn; i++ {
		if ludoSim(l, [2]int{2, 2}, rnd) == 0 {
			f0++
		}
	}
	t.Logf("fairness check, level 2 vs level 2, first mover wins %d/%d", f0, nn)
}

func TestLudoGame(t *testing.T) {
	seq := []int{6, 3, 2, 6, 1, 5, 4, 6, 6, 2}
	i := 0
	l := &Ludo{Path: filepath.Join(t.TempDir(), "l.json"), Dice: func() int { v := seq[i%len(seq)]; i++; return v }}
	rep, ok := l.Handle("1", "ludo level 3")
	if !ok || !strings.Contains(rep.Markup, "ld:roll:0") || len(rep.PNG) < 500 {
		t.Fatal("start")
	}
	rep, _ = l.Handle("1", "roll")
	if !strings.Contains(rep.Text, "You rolled 6.") {
		t.Fatal(rep.Text)
	}
	for k := 0; k < 40; k++ {
		f, _ := l.load()
		g := f.Games["1"]
		if g.Over {
			break
		}
		if g.Roll == 0 {
			rep, _ = l.Handle("1", "roll")
		} else {
			lg := g.legal(0, g.Roll)
			_, _, rep = l.Tap("1", "ld:t"+string(rune('0'+lg[0]))+":"+itoa(g.Turns))
		}
	}
	fin, _ := l.load()
	moved := 0
	for _, p := range fin.Games["1"].Tok[0] {
		if p >= 0 {
			moved++
		}
	}
	if moved == 0 && !fin.Games["1"].Over {
		t.Fatal("the scripted game never moved a token")
	}
	if pop, _, _ := l.Tap("1", "ld:roll:0"); !strings.Contains(pop, "out of date") {
		t.Fatal(pop)
	}
	if _, ok := l.Handle("2", "roll"); ok {
		t.Fatal("isolation: no game in chat 2")
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestLudoBoundaries(t *testing.T) {
	g := &LudoSave{}
	g.Tok[0] = [4]int{50, 51, 55, 56}
	g.Tok[1] = [4]int{-1, -1, -1, -1}
	// p=50 is the last track square (one step before the home column); 6 steps lands exactly on home (56).
	if lg := g.legal(0, 6); len(lg) != 1 || lg[0] != 0 {
		t.Fatalf("from 50 a 6 must finish and nothing else may move 6: %v", lg)
	}
	if lg := g.legal(0, 5); len(lg) != 2 { // 50->55 and 51->56
		t.Fatalf("roll 5: %v", lg)
	}
	if lg := g.legal(0, 1); len(lg) != 3 { // 50,51,55 may move; 56 may not
		t.Fatalf("roll 1: %v", lg)
	}
	if lg := g.legal(0, 2); len(lg) != 2 { // 50->52, 51->53 ; 55 overshoots
		t.Fatalf("roll 2: %v", lg)
	}
	// the route length: entering at p=0 then 56 steps ends at 56
	h := &LudoSave{}
	h.Tok[0] = [4]int{-1, 56, 56, 56}
	h.Tok[1] = [4]int{-1, -1, -1, -1}
	if lg := h.legal(0, 6); len(lg) != 1 || lg[0] != 0 {
		t.Fatalf("entry on 6: %v", lg)
	}
	h.move(0, 0, 6)
	if h.Tok[0][0] != 0 {
		t.Fatalf("entry should place token on its start square, got %d", h.Tok[0][0])
	}
}
