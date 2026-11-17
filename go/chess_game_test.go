package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestChess(t *testing.T) *Chess {
	return &Chess{Path: filepath.Join(t.TempDir(), "chess.json")}
}

func TestChessSANAndMoves(t *testing.T) {
	c := newTestChess(t)
	r, ok := c.Handle("1", "chess level 1")
	if !ok || len(r.PNG) < 1000 {
		t.Fatalf("start: %v %v", ok, r)
	}
	r, ok = c.Handle("1", "e2e4")
	if !ok || !strings.Contains(r.Text, "You: e4.") || !strings.Contains(r.Text, "Me:") {
		t.Fatalf("move: %+v", r)
	}
	r, _ = c.Handle("1", "Ke5")
	if !strings.Contains(r.Text, "isn't a legal move") {
		t.Fatalf("illegal: %+v", r)
	}
	if _, ok := c.Handle("1", "what is the weather"); ok {
		t.Fatal("must not capture ordinary chat")
	}
	if _, ok := c.Handle("2", "e2e4"); ok {
		t.Fatal("chat isolation: other chat has no game")
	}
	r, _ = c.Handle("1", "undo")
	if !strings.Contains(r.Text, "Undid") {
		t.Fatalf("undo %+v", r)
	}
	r, _ = c.Handle("1", "pgn")
	if strings.Contains(r.Text, "1. ") {
		t.Fatalf("pgn after undo should be empty of moves: %s", r.Text)
	}
}

func TestChessSpecialMoves(t *testing.T) {
	g := &ChessSave{White: true, Level: 1}
	for _, m := range strings.Fields("e2e4 e7e5 g1f3 b8c6 f1c4 g8f6 e1g1") {
		g.Moves = append(g.Moves, m)
	}
	p, _, _, sans, err := replay(g)
	if err != nil || sans[len(sans)-1] != "O-O" {
		t.Fatalf("castle %v %v", err, sans)
	}
	// en passant
	p = mustFEN(t, "rnbqkbnr/ppp1pppp/8/3pP3/8/8/PPPP1PPP/RNBQKBNR w KQkq d6 0 3")
	mv, why := ParseMove(p, "exd6")
	if why != "" || p.Make(mv).b[35] != 0 {
		t.Fatalf("ep %v", why)
	}
	// promotion
	p = mustFEN(t, "8/P6k/8/8/8/8/8/K7 w - - 0 1")
	mv, why = ParseMove(p, "a8=Q+")
	if why != "" || mv.promo != wQ {
		t.Fatalf("promo %v %v", mv, why)
	}
	// ambiguity
	p = mustFEN(t, "4k3/8/8/8/8/8/4K3/R6R w - - 0 1")
	if _, why := ParseMove(p, "Rd1"); why != "ambiguous" {
		t.Fatalf("amb %v", why)
	}
	if _, why := ParseMove(p, "Rad1"); why != "" {
		t.Fatalf("disamb %v", why)
	}
}

func mustFEN(t *testing.T, s string) Pos {
	p, err := ParseFEN(s)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestChessEndings(t *testing.T) {
	p := mustFEN(t, "7k/5Q2/6K1/8/8/8/8/8 b - - 0 1")
	if s := chessStatus(p, map[string]int{p.Key(): 1}); !strings.Contains(s, "Stalemate") {
		t.Fatal(s)
	}
	p = mustFEN(t, "6k1/5ppp/8/8/8/8/8/R5K1 w - - 0 1")
	mv, why := ParseMove(p, "Ra8")
	if why != "" {
		t.Fatal(why)
	}
	q := p.Make(mv)
	if s := chessStatus(q, map[string]int{q.Key(): 1}); !strings.Contains(s, "Checkmate. White wins") {
		t.Fatal(s)
	}
	p = mustFEN(t, "8/8/4k3/8/8/4K3/8/8 w - - 0 1")
	if s := chessStatus(p, map[string]int{p.Key(): 1}); !strings.Contains(s, "material") {
		t.Fatal(s)
	}
	sp := mustFEN(t, startFEN)
	if s := chessStatus(sp, map[string]int{sp.Key(): 3}); !strings.Contains(s, "repetition") {
		t.Fatal(s)
	}
}

func TestChessEngineMate(t *testing.T) {
	p := mustFEN(t, "6k1/5ppp/8/8/8/8/8/R5K1 w - - 0 1")
	mv, sc, _ := Best(p, 3, 2*time.Second, nil)
	if mv.String() != "a1a8" || sc < 9000 {
		t.Fatalf("mate in 1: %v %d", mv, sc)
	}
	p = mustFEN(t, "r5k1/5ppp/8/8/8/8/1R6/1R4K1 w - - 0 1") // Rb8+ Rxb8 Rxb8#
	_ = p
	p = mustFEN(t, "6k1/8/6K1/8/8/8/8/R7 w - - 0 1") // Ra8#
	mv, _, _ = Best(p, 3, 2*time.Second, nil)
	if mv.String() != "a1a8" {
		t.Fatalf("mate: %v", mv)
	}
}

func TestChessPersistAndBlack(t *testing.T) {
	c := newTestChess(t)
	r, _ := c.Handle("9", "chess as black level 2")
	if !strings.Contains(r.Text, "You are Black") || !strings.Contains(r.Text, "Last moves: 1. ") {
		t.Fatalf("%+v", r)
	}
	c2 := &Chess{Path: c.Path}
	r, ok := c2.Handle("9", "board")
	if !ok || len(r.PNG) == 0 {
		t.Fatal("persist")
	}
	r, _ = c2.Handle("9", "resign")
	if !strings.Contains(r.Text, "resigned") {
		t.Fatal(r.Text)
	}
	if _, ok := c2.Handle("9", "e7e5"); ok {
		t.Fatal("game over must not take moves")
	}
}

func TestChessEngineSpeed(t *testing.T) {
	c := newTestChess(t)
	for lv := 1; lv <= 5; lv++ {
		st := time.Now()
		p := mustFEN(t, "r1bq1rk1/pp2bppp/2n1pn2/3p4/3P4/2NBPN2/PP3PPP/R1BQ1RK1 w - - 0 9")
		c.engineMove(p, nil, lv)
		t.Logf("level %d: %v", lv, time.Since(st))
	}
}
