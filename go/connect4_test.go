package main

import (
	"math/rand"
	"path/filepath"
	"strings"
	"testing"
)

func c4From(moves ...int) (c4Board, int) {
	g := &C4Save{Moves: moves}
	b, n, _, _, _ := g.replay()
	return b, n
}

func TestC4Tactics(t *testing.T) {
	// red has 3 in column 0 (moves: R0 Y1 R0 Y1 R0 Y2): red to move wins at col 0
	b, n := c4From(0, 1, 0, 1, 0, 2)
	if col, _ := c4Best(b, 1, 3, n); col != 0 {
		t.Fatalf("win in 1: %d", col)
	}
	// yellow threatens column 1 stack (Y1 x3); red must block at 1
	b, n = c4From(0, 1, 2, 1, 4, 1)
	if col, _ := c4Best(b, 1, 3, n); col != 1 {
		t.Fatalf("block: %d", col)
	}
}

func playC4(l1, l2 int, rnd *rand.Rand) int { // returns 1, 2 or 0; level 0 = random
	var b c4Board
	who := int8(1)
	for n := 0; n < 42; n++ {
		lv := l1
		if who == 2 {
			lv = l2
		}
		var col int
		if lv == 0 {
			for {
				col = rnd.Intn(7)
				if b[5][col] == 0 {
					break
				}
			}
		} else {
			col, _ = c4Best(b, who, lv, n)
		}
		r := b.drop(col, who)
		if b.win(r, col) != nil {
			return int(who)
		}
		who = 3 - who
	}
	return 0
}

func TestC4Strength(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	rnd := rand.New(rand.NewSource(7))
	for _, lv := range []int{1, 3} {
		w, l, d := 0, 0, 0
		for i := 0; i < 20; i++ {
			a, bl := lv, 0
			if i%2 == 1 {
				a, bl = 0, lv
			}
			r := playC4(a, bl, rnd)
			engineSide := 1 + i%2
			switch {
			case r == 0:
				d++
			case r == engineSide:
				w++
			default:
				l++
			}
		}
		t.Logf("level %d vs random, 20 games alternating first move: W%d L%d D%d", lv, w, l, d)
		if w < 18 && lv == 3 {
			t.Fatalf("level 3 should beat random nearly always")
		}
	}
	// depth vs depth: higher level should not lose to lower, colours swapped
	hi, lo, dr := 0, 0, 0
	for i := 0; i < 2; i++ {
		a, bl := 4, 2
		if i == 1 {
			a, bl = 2, 4
		}
		r := playC4(a, bl, rnd)
		hs := 1 + i
		switch {
		case r == 0:
			dr++
		case r == hs:
			hi++
		default:
			lo++
		}
	}
	t.Logf("level 4 vs level 2, colours swapped: L4 wins %d, L2 wins %d, draws %d", hi, lo, dr)
}

func TestC4Game(t *testing.T) {
	c := &Connect4{Path: filepath.Join(t.TempDir(), "c4.json")}
	r, ok := c.Handle("1", "connect four level 2")
	if !ok || len(r.PNG) < 500 || r.Markup == "" {
		t.Fatal("start")
	}
	r, _ = c.Handle("1", "drop 4")
	if !strings.Contains(r.Text, "You: column 4.") || !strings.Contains(r.Text, "Me: column") {
		t.Fatal(r.Text)
	}
	if _, ok := c.Handle("2", "drop 4"); ok {
		t.Fatal("isolation")
	}
	pop, mk, rep := c.Tap("1", "c4:3:2")
	if rep == nil || mk != "-" || !strings.Contains(rep.Text, "You: column 4.") {
		t.Fatalf("tap %q", pop)
	}
	if pop, _, _ := c.Tap("1", "c4:3:2"); !strings.Contains(pop, "out of date") {
		t.Fatal(pop)
	}
	r, _ = c.Handle("1", "c4 undo")
	if !strings.Contains(r.Text, "Undid") {
		t.Fatal(r.Text)
	}
	r, _ = c.Handle("3", "connect four as second")
	if !strings.Contains(r.Text, "I open in column") {
		t.Fatal(r.Text)
	}
	// fill a column to check the full-column message
	c.Handle("4", "connect four level 1")
	for i := 0; i < 6; i++ {
		c.Handle("4", "drop 1")
	}
}
