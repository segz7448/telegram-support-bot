package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Telegram can redeliver an update after an abrupt stop (side effect done, offset not yet saved). These tests pin what
// a replay does. Button taps carry a move counter, so a replayed tap is rejected and changes nothing. Plain-text moves
// have no token: a replay is usually illegal (the piece already moved) but this is at-least-once, not exactly-once.

func fileBytes(t *testing.T, p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestReplayedTapsAreRejectedAndChangeNothing(t *testing.T) {
	d := t.TempDir()
	ch := &Chess{Path: filepath.Join(d, "ch.json")}
	c4 := &Connect4{Path: filepath.Join(d, "c4.json")}
	rv := &Reversi{Path: filepath.Join(d, "rv.json")}
	dr := &Draughts{Path: filepath.Join(d, "dr.json")}
	ld := &Ludo{Path: filepath.Join(d, "ld.json"), Dice: func() int { return 3 }}

	ch.Handle("1", "chess level 1")
	ch.Tap("1", "cm:e2:0")
	if _, _, r := ch.Tap("1", "cm:e4:0"); r == nil {
		t.Fatal("chess move not played")
	}
	snap := fileBytes(t, ch.Path)
	if pop, _, r := ch.Tap("1", "cm:e4:0"); r != nil || !strings.Contains(pop, "out of date") || fileBytes(t, ch.Path) != snap {
		t.Fatalf("chess replay: %q", pop)
	}

	c4.Handle("2", "connect four level 1")
	if _, _, r := c4.Tap("2", "c4:3:0"); r == nil {
		t.Fatal("c4 move not played")
	}
	snap = fileBytes(t, c4.Path)
	if pop, _, r := c4.Tap("2", "c4:3:0"); r != nil || !strings.Contains(pop, "out of date") || fileBytes(t, c4.Path) != snap {
		t.Fatalf("c4 replay: %q", pop)
	}

	rv.Handle("3", "reversi level 1")
	if _, _, r := rv.Tap("3", "rv:19:0"); r == nil {
		t.Fatal("reversi move not played")
	}
	snap = fileBytes(t, rv.Path)
	if pop, _, r := rv.Tap("3", "rv:19:0"); r != nil || !strings.Contains(pop, "out of date") || fileBytes(t, rv.Path) != snap {
		t.Fatalf("reversi replay: %q", pop)
	}

	dr.Handle("4", "draughts level 1")
	dr.Tap("4", "dr:c3:0")
	if _, _, r := dr.Tap("4", "dr:b4:0"); r == nil {
		t.Fatal("draughts move not played")
	}
	snap = fileBytes(t, dr.Path)
	if pop, _, r := dr.Tap("4", "dr:b4:0"); r != nil || !strings.Contains(pop, "out of date") || fileBytes(t, dr.Path) != snap {
		t.Fatalf("draughts replay: %q", pop)
	}

	ld.Handle("5", "ludo level 1")
	if _, _, r := ld.Tap("5", "lu:roll:0"); r == nil {
		t.Fatal("ludo roll not played")
	}
	snap = fileBytes(t, ld.Path)
	pop, _, r := ld.Tap("5", "lu:roll:0")
	if r != nil || fileBytes(t, ld.Path) != snap {
		t.Fatalf("ludo roll replay changed state: %q", pop)
	}
}

func TestReplayedTextMovesDoNotApplyTwiceWhenIllegal(t *testing.T) {
	d := t.TempDir()
	ch := &Chess{Path: filepath.Join(d, "ch.json")}
	ch.Handle("1", "chess level 1")
	ch.Handle("1", "e2e4")
	snap := fileBytes(t, ch.Path)
	rep, _ := ch.Handle("1", "e2e4") // same text delivered again
	if fileBytes(t, ch.Path) != snap {
		t.Fatalf("replayed e2e4 changed the game: %v", rep)
	}
	c4 := &Connect4{Path: filepath.Join(d, "c4.json")}
	c4.Handle("2", "connect four level 1")
	c4.Handle("2", "drop 4")
	f1 := fileBytes(t, c4.Path)
	c4.Handle("2", "drop 4") // text has no counter: this is a NEW legal move, so a replay is at-least-once, not rejected
	if fileBytes(t, c4.Path) == f1 {
		t.Log("c4 text drop replay was ignored")
	} else {
		t.Log("known limit: a replayed plain-text 'drop 4' is a legal new move and is applied again")
	}
}
