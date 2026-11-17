package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SelfTestGames drives the real game handlers (text commands and button taps) with files in a temp directory. It
// needs no token and no network, and it reports time per engine move so the phone's speed can be measured.
func SelfTestGames(dir string, log func(string)) bool {
	tmp, err := os.MkdirTemp(dir, "gamestest-*")
	if err != nil {
		log("cannot make temp dir: " + err.Error())
		return false
	}
	defer os.RemoveAll(tmp)
	ch := &Chess{Path: filepath.Join(tmp, "chess.json")}
	c4 := &Connect4{Path: filepath.Join(tmp, "c4.json")}
	rv := &Reversi{Path: filepath.Join(tmp, "rv.json")}
	ok := true
	check := func(name string, cond bool, extra string) {
		st := "PASS"
		if !cond {
			st, ok = "FAIL", false
		}
		log(fmt.Sprintf("%s %s %s", st, name, extra))
	}
	timed := func(f func()) string {
		t := time.Now()
		f()
		return fmt.Sprintf("(%d ms)", time.Since(t).Milliseconds())
	}
	var rep *ChessReply
	var hit bool
	for lv := 1; lv <= 5; lv++ {
		ch2 := &Chess{Path: filepath.Join(tmp, fmt.Sprintf("chess-l%d.json", lv))}
		ch2.Handle("1", fmt.Sprintf("chess level %d", lv))
		d := timed(func() { rep, hit = ch2.Handle("1", "e2e4") })
		check(fmt.Sprintf("chess level %d replies to e2e4 with a board image", lv), hit && rep != nil && len(rep.PNG) > 1000 && strings.Contains(rep.Text, "Me:"), d)
	}
	ch.Handle("2", "chess level 2")
	pop, mk, r2 := ch.Tap("2", "cm:e2:0")
	check("chess tap selects a pawn and marks destinations", strings.Contains(pop, "Selected e2") && strings.Contains(mk, "●"), "")
	_, _, r2 = ch.Tap("2", "cm:e4:0")
	check("chess tap plays the move", r2 != nil && strings.Contains(r2.Text, "You: e4."), "")
	c4.Handle("3", "connect four level 3")
	var d string
	d = timed(func() { _, _, r2 = c4.Tap("3", "c4:3:0") })
	check("connect four tap plays and answers", r2 != nil && len(r2.PNG) > 500, d)
	c4.Handle("4", "connect four level 5")
	d = timed(func() { rep, hit = c4.Handle("4", "drop 4") })
	check("connect four level 5 reply time", hit && strings.Contains(rep.Text, "Me: column"), d)
	rv.Handle("5", "reversi level 3")
	d = timed(func() { _, _, r2 = rv.Tap("5", "rv:19:0") })
	check("reversi tap plays and answers", r2 != nil && strings.Contains(r2.Text, "Me:"), d)
	rv.Handle("6", "reversi level 5")
	d = timed(func() { rep, hit = rv.Handle("6", "move d3") })
	check("reversi level 5 reply time", hit && strings.Contains(rep.Text, "Me:"), d)
	dr := &Draughts{Path: filepath.Join(tmp, "dr.json")}
	dr.Handle("8", "draughts level 5")
	d = timed(func() { _, _, r2 = dr.Tap("8", "dr:c3:0"); _, _, r2 = dr.Tap("8", "dr:b4:0") })
	check("draughts level 5 tap move plus reply time", r2 != nil && strings.Contains(r2.Text, "Me:"), d)
	ld := &Ludo{Path: filepath.Join(tmp, "ludo.json")}
	ld.Handle("10", "ludo level 3")
	d = timed(func() { rep, hit = ld.Handle("10", "roll") })
	check("ludo roll and bot reply (real random dice)", hit && strings.Contains(rep.Text, "You rolled") && len(rep.PNG) > 500, d)
	_, hit = ch.Handle("7", "hello there")
	check("ordinary chat is not captured by games", !hit, "")
	log("note: this drives the real game code with temp files; it does not touch Telegram, the network or live state")
	return ok
}
