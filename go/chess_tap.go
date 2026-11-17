package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
)

// Tap-to-move: the board photo carries an 8x8 inline keyboard. Tap the square of the piece to move, then the
// destination. Each button's data is "cm:<square>:<moves so far>", so a tap on an old board is recognised and refused.

var glyph = map[int8]string{wP: "♙", wN: "♘", wB: "♗", wR: "♖", wQ: "♕", wK: "♔", -wP: "♟", -wN: "♞", -wB: "♝", -wR: "♜", -wQ: "♛", -wK: "♚"}

func sqIndex(s string) int {
	if len(s) != 2 || s[0] < 'a' || s[0] > 'h' || s[1] < '1' || s[1] > '8' {
		return -1
	}
	return int(s[1]-'1')*8 + int(s[0]-'a')
}

// chessMarkup builds the keyboard JSON. sel is the selected square ("" for none); targets are marked.
func chessMarkup(p Pos, whiteBottom bool, plies int, sel string) string {
	targets := map[int]bool{}
	if si := sqIndex(sel); si >= 0 {
		for _, m := range p.Legal() {
			if int(m.from) == si {
				targets[int(m.to)] = true
			}
		}
	}
	type btn struct {
		T string `json:"text"`
		D string `json:"callback_data"`
	}
	var rows [][]btn
	for i := 0; i < 8; i++ {
		r := 7 - i
		if !whiteBottom {
			r = i
		}
		var row []btn
		for j := 0; j < 8; j++ {
			f := j
			if !whiteBottom {
				f = 7 - j
			}
			s := r*8 + f
			name := sqName(int8(s))
			t := "·"
			if v := p.b[s]; v != 0 {
				t = glyph[v]
			}
			switch {
			case name == sel:
				t = "[" + t + "]"
			case targets[s]:
				if p.b[s] == 0 {
					t = "●"
				} else {
					t = "×" + t
				}
			}
			row = append(row, btn{t, fmt.Sprintf("cm:%s:%d", name, plies)})
		}
		rows = append(rows, row)
	}
	b, _ := json.Marshal(map[string]any{"inline_keyboard": rows})
	return string(b)
}

// Tap handles one button press. It returns the pop-up text, the new keyboard JSON for the same message ("" = leave it,
// "-" = remove it) and a full reply when a move was played.
func (c *Chess) Tap(chat, data string) (popup, markup string, rep *ChessReply) {
	parts := strings.Split(data, ":")
	promoPick := len(parts) == 3 && parts[0] == "cm" && len(parts[1]) == 2 && parts[1][0] == '=' && strings.ContainsRune("qrbn", rune(parts[1][1]))
	if len(parts) != 3 || parts[0] != "cm" || (!promoPick && sqIndex(parts[1]) < 0) {
		return "", "", nil
	}
	sq := -1
	if !promoPick {
		sq = sqIndex(parts[1])
	}
	if !c.enabled() {
		return "Chess is off.", "", nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	f, err := c.load()
	if err != nil {
		return "I can't read my chess save file.", "", nil
	}
	g := f.Games[chat]
	if g == nil || g.Over {
		return "No game in progress. Say \"chess\" to start.", "-", nil
	}
	if parts[2] != fmt.Sprint(len(g.Moves)) {
		return "That board is out of date. Use the newest one.", "-", nil
	}
	p, rep0, _, _, err := replay(g)
	if err != nil || p.white != g.White {
		return "It isn't your turn.", "", nil
	}
	legal := p.Legal()
	own := func(s int) bool { v := p.b[s]; return v != 0 && (v > 0) == g.White }
	if promoPick && g.Promo == "" {
		return "No promotion is waiting.", "", nil
	}
	if g.Sel != "" {
		si := sqIndex(g.Sel)
		var mv *Mv
		var promos []Mv
		want := int8(0)
		if promoPick {
			want = int8(strings.Index(" pnbrqk", parts[1][1:]))
			sq = sqIndex(g.Promo)
		}
		for i := range legal {
			if int(legal[i].from) == si && int(legal[i].to) == sq {
				if legal[i].promo != 0 {
					promos = append(promos, legal[i])
				}
				if legal[i].promo == want {
					mv = &legal[i]
				}
			}
		}
		if len(promos) > 0 && !promoPick {
			g.Promo = sqName(int8(sq))
			saveJSON(c.Path, f)
			return "Promote to: Queen, Rook, Bishop or Knight?", promoMarkup(len(g.Moves)), nil
		}
		g.Promo = ""
		if mv != nil {
			g.Sel = ""
			yourSAN := SAN(p, *mv, legal)
			g.Moves = append(g.Moves, mv.String())
			p = p.Make(*mv)
			rep0[p.Key()]++
			msg := "You: " + yourSAN + "."
			if st := chessStatus(p, rep0); st != "" {
				g.Over, g.Result = true, st
				saveJSON(c.Path, f)
				return "Move played.", "-", c.reply(g, msg+" "+st)
			}
			em, _, _ := c.engineMove(p, rep0, g.Level)
			mySAN := SAN(p, em, p.Legal())
			g.Moves = append(g.Moves, em.String())
			p = p.Make(em)
			rep0[p.Key()]++
			msg += " Me: " + mySAN + "."
			if st := chessStatus(p, rep0); st != "" {
				g.Over, g.Result = true, st
				msg += " " + st
			} else {
				if p.InCheck() {
					msg += " You are in check."
				}
				msg += " Your move."
			}
			if saveJSON(c.Path, f) != nil {
				return "I couldn't save, so that move was NOT recorded.", "", nil
			}
			return "Move played.", "-", c.reply(g, msg)
		}
	}
	if own(sq) {
		has := false
		for _, m := range legal {
			if int(m.from) == sq {
				has = true
			}
		}
		if !has {
			return "That piece has no legal move.", "", nil
		}
		g.Sel = sqName(int8(sq))
		saveJSON(c.Path, f)
		return "Selected " + g.Sel + ". Tap where it goes.", chessMarkup(p, g.White, len(g.Moves), g.Sel), nil
	}
	if g.Sel != "" {
		g.Sel = ""
		saveJSON(c.Path, f)
		return "Not a legal move for that piece. Pick a piece again.", chessMarkup(p, g.White, len(g.Moves), ""), nil
	}
	return "Tap one of your pieces first.", "", nil
}

func (r *Runner) sendBoard(ps PhotoSender, chat string, cr *ChessReply) ([]byte, error) {
	if mp, ok := ps.(MarkupPhotoSender); ok && cr.Markup != "" {
		return mp.PostPhotoMarkup(r.API, r.Token, chat, cr.Text, cr.Markup, cr.PNG, filepath.Dir(r.StatePath), 60)
	}
	return ps.PostPhoto(r.API, r.Token, chat, cr.Text, cr.PNG, filepath.Dir(r.StatePath), 60)
}

func (r *Runner) handleTap(cq map[string]any) {
	id, _ := cq["id"].(string)
	data, _ := cq["data"].(string)
	msg, _ := cq["message"].(map[string]any)
	ch, _ := msg["chat"].(map[string]any)
	cidf, _ := ch["id"].(float64)
	midf, _ := msg["message_id"].(float64)
	typ, _ := ch["type"].(string)
	answer := func(t string) {
		call(r.T, r.API, r.Token, "answerCallbackQuery", url.Values{"callback_query_id": {id}, "text": {t}}, 15)
	}
	if r.Chat == nil || cidf == 0 || typ != "private" {
		answer("")
		return
	}
	chat := fmt.Sprint(int64(cidf))
	var popup, markup string
	var rep *ChessReply
	for _, gm := range r.Chat.Games {
		if strings.HasPrefix(data, gm.Prefix()) {
			popup, markup, rep = gm.Tap(chat, data)
			break
		}
	}
	answer(popup)
	mid := fmt.Sprint(int64(midf))
	switch {
	case markup == "-":
		call(r.T, r.API, r.Token, "editMessageReplyMarkup", url.Values{"chat_id": {chat}, "message_id": {mid}, "reply_markup": {`{"inline_keyboard":[]}`}}, 15)
	case markup != "":
		call(r.T, r.API, r.Token, "editMessageReplyMarkup", url.Values{"chat_id": {chat}, "message_id": {mid}, "reply_markup": {markup}}, 15)
	}
	if rep != nil {
		if ps, ok := r.T.(PhotoSender); ok && len(rep.PNG) > 0 {
			if _, err := r.sendBoard(ps, chat, rep); err == nil {
				r.Log("reply sent (chess tap move)")
				return
			}
		}
		call(r.T, r.API, r.Token, "sendMessage", url.Values{"chat_id": {chat}, "text": {rep.Text}}, 30)
	}
}

// GameHandler is one board game: text commands, button taps, and the callback-data prefix it owns.
type GameHandler interface {
	Handle(chat, text string) (*ChessReply, bool)
	Tap(chat, data string) (popup, markup string, rep *ChessReply)
	Prefix() string
}

func (c *Chess) Prefix() string { return "cm:" }

func promoMarkup(plies int) string {
	type btn struct {
		T string `json:"text"`
		D string `json:"callback_data"`
	}
	row := []btn{}
	for _, o := range []struct{ n, k string }{{"Queen ♕", "q"}, {"Rook ♖", "r"}, {"Bishop ♗", "b"}, {"Knight ♘", "n"}} {
		row = append(row, btn{o.n, fmt.Sprintf("cm:=%s:%d", o.k, plies)})
	}
	b, _ := json.Marshal(map[string]any{"inline_keyboard": [][]btn{row}})
	return string(b)
}
