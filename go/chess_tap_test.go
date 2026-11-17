package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestChessTap(t *testing.T) {
	c := newTestChess(t)
	c.Handle("5", "chess level 1")
	tap := func(sq string, plies int) (string, string, *ChessReply) {
		return c.Tap("5", fmt.Sprintf("cm:%s:%d", sq, plies))
	}
	if pop, _, _ := tap("e4", 0); !strings.Contains(pop, "own pieces") && !strings.Contains(pop, "Tap one") {
		t.Fatal(pop)
	}
	pop, mk, rep := tap("e2", 0)
	if !strings.Contains(pop, "Selected e2") || rep != nil || !strings.Contains(mk, "●") || !strings.Contains(mk, "[♙]") {
		t.Fatalf("select: %s %s", pop, mk)
	}
	if pop, _, _ := tap("e2", 7); !strings.Contains(pop, "out of date") {
		t.Fatal("stale", pop)
	}
	pop, mk, rep = tap("e4", 0)
	if rep == nil || mk != "-" || !strings.Contains(rep.Text, "You: e4.") || rep.Markup == "" || len(rep.PNG) == 0 {
		t.Fatalf("move: %v %v", pop, rep)
	}
	// other chat can't tap this game
	if pop, _, _ := c.Tap("6", "cm:e2:0"); !strings.Contains(pop, "No game") {
		t.Fatal(pop)
	}
	// deselect on illegal target
	c.Tap("5", "cm:g1:2")
	pop, _, _ = c.Tap("5", "cm:g5:2")
	if !strings.Contains(pop, "Not a legal") {
		t.Fatal(pop)
	}
}

func TestChessPromotionTap(t *testing.T) {
	c := newTestChess(t)
	c.Handle("7", "chess level 1")
	f, _ := c.load()
	g := f.Games["7"]
	g.Moves = nil
	// custom position isn't expressible as moves; use a short known pawn-run game instead
	for _, m := range strings.Fields("a2a4 b8c6 a4a5 c6b4 a5a6 b4c6 a6b7 c6e5") {
		g.Moves = append(g.Moves, m)
	}
	saveJSON(c.Path, f)
	pop, _, _ := c.Tap("7", "cm:b7:8")
	if !strings.Contains(pop, "Selected b7") {
		t.Fatal(pop)
	}
	pop, mk, rep := c.Tap("7", "cm:a8:8")
	if rep != nil || !strings.Contains(pop, "Promote to") || !strings.Contains(mk, "cm:=n:8") {
		t.Fatalf("promo prompt: %q %s", pop, mk)
	}
	_, _, rep = c.Tap("7", "cm:=n:8")
	if rep == nil || !strings.Contains(rep.Text, "You: bxa8=N") {
		t.Fatalf("knight: %+v", rep)
	}
}
