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
