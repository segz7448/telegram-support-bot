package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type gmEnv struct {
	c *Chat
	s *fakeS
}

func newGmEnv(t *testing.T) *gmEnv {
	d := t.TempDir()
	s := &fakeS{}
	c := NewChat(s, nil, nil, nil, func(string) {})
	c.Chess = &Chess{Path: filepath.Join(d, "chess.json")}
	c4, rv, dr, ld := &Connect4{Path: filepath.Join(d, "c4.json")}, &Reversi{Path: filepath.Join(d, "rv.json")}, &Draughts{Path: filepath.Join(d, "dr.json")}, &Ludo{Path: filepath.Join(d, "ld.json")}
	menu := &GameMenu{Entries: []gameEntry{
		{"chess", "Chess", "chess", c.Chess, true}, {"c4", "Connect Four", "connect four", c4, true},
		{"reversi", "Reversi", "reversi", rv, true}, {"draughts", "Draughts", "draughts", dr, true}, {"ludo", "Ludo", "ludo", ld, true}}}
	c.Games = []GameHandler{menu, c.Chess, c4, rv, dr, ld, &QR{}}
	return &gmEnv{c, s}
}

// send mimics the Runner: games first, then the chat reply.
func (e *gmEnv) send(chat, text string) (string, *ChessReply) {
	for _, g := range e.c.Games {
		if cr, ok := g.Handle(chat, text); ok {
			return cr.Text, cr
		}
	}
	return e.c.Reply(chat, text), nil
}

func TestScreenshotExchangeNoSearch(t *testing.T) {
	e := newGmEnv(t)
	r1, cr := e.send("42", "Let\u2019s play a game")
	if cr == nil || !strings.Contains(r1, "Which game") || cr.Markup == "" || !strings.Contains(cr.Markup, "gm:chess") {
		t.Fatalf("no menu with buttons: %q", r1)
	}
	r2, cr2 := e.send("42", "Which game")
	if cr2 == nil || !strings.Contains(r2, "Chess") || !strings.Contains(r2, "Ludo") {
		t.Fatalf("which game did not list games: %q", r2)
	}
	if len(e.s.qs) != 0 {
		t.Fatalf("web search was called: %v", e.s.qs)
	}
}

func TestGameIntentVariantsNoSearch(t *testing.T) {
	for _, in := range []string{"let's play a game", "Lets play a game!", "lets play", "play a game", "Play a game?", "I want to play a game", "wanna play something", "can we play a game", "Can we play a game?", "hey, let\u2019s play a game please", "games", "what games do you have", "Which games can you play?", "do you have any games", "I'm bored", "show me the games", "let's play a game with me", "ayo let's play"} {
		e := newGmEnv(t)
		r, cr := e.send("7", in)
		if cr == nil || !strings.Contains(r, "Which game") {
			t.Errorf("%q -> %q", in, r)
		}
		if len(e.s.qs) != 0 {
			t.Errorf("%q searched", in)
		}
	}
}

func TestChoicesStartRealGames(t *testing.T) {
	for _, tc := range []struct{ say, want string }{{"chess", "you are white"}, {"Connect Four", "drop 4"}, {"ludo", "ludo"}, {"reversi", "black 2 - white 2"}, {"draughts", "red 12"}, {"2", "drop 4"}, {"the first one", "you are white"}, {"play ludo", "ludo"}, {"checkers", "red 12"}} {
		e := newGmEnv(t)
		e.send("9", "let's play a game")
		r, _ := e.send("9", tc.say)
		if !strings.Contains(strings.ToLower(r), tc.want) || len(e.s.qs) != 0 {
			t.Errorf("%q -> %q (searches %d)", tc.say, r, len(e.s.qs))
		}
	}
	e := newGmEnv(t)
	r, cr := e.send("9", "let's play chess")
	if cr == nil || !strings.Contains(strings.ToLower(r), "you are white") || len(cr.PNG) == 0 {
		t.Fatalf("play chess did not start with a board: %q", r)
	}
}

func TestMenuTapStartsGame(t *testing.T) {
	e := newGmEnv(t)
	menu := e.c.Games[0].(*GameMenu)
	for _, k := range []string{"chess", "c4", "reversi", "draughts", "ludo"} {
		_, mk, rep := menu.Tap("11", "gm:"+k)
		if mk != "-" || rep == nil || rep.Text == "" {
			t.Errorf("tap %s: markup %q rep %v", k, mk, rep)
		}
	}
	if p, _, rep := menu.Tap("11", "gm:nope"); rep != nil || p == "" {
		t.Error("unknown tap")
	}
}

func TestMenuDoesNotHijackFactualOrMoves(t *testing.T) {
	for _, in := range []string{"history of chess", "who invented ludo", "when did Let's Play videos start", "what is the best game console", "how do I play connect four", "game of thrones cast", "what is a board game", "let's play videos history", "the game awards 2024"} {
		e := newGmEnv(t)
		_, cr := e.send("5", in)
		if cr != nil {
			t.Errorf("%q hijacked", in)
		}
		if len(e.s.qs) == 0 {
			t.Errorf("%q did not reach search", in)
		}
	}
	// a menu is shown, the user ignores it and plays a move number later: not a menu choice
	e := newGmEnv(t)
	e.send("6", "connect four")
	e.send("6", "what is the capital of france")
	e.send("6", "let's play a game")
	_, cr := e.send("6", "tell me about whales")
	if cr != nil {
		t.Error("whales hijacked")
	}
	if r, cr := e.send("6", "3"); cr != nil && strings.Contains(r, "Which game") {
		t.Error("stale menu took a move number")
	}
}

func TestDialogueNoSearch(t *testing.T) {
	for _, in := range []string{"I'm sad", "i am bored", "tell me a joke", "are you human?", "do you understand me", "you're stupid", "that's not what I asked", "what can I ask you", "can you help me", "ok", "lol", "hmm", "do you like pizza", "I like pizza", "why", "which one", "play some music", "sing me a song", "can you play chess"} {
		e := newGmEnv(t)
		r, _ := e.send("3", in)
		if len(e.s.qs) != 0 || r == "" {
			t.Errorf("%q searched or empty (%q)", in, r)
		}
	}
}

func TestDialogueLeavesQuestionsAlone(t *testing.T) {
	for _, in := range []string{"why is the sky blue", "how do I bake bread", "do you know who won the world cup in 2018", "what is photosynthesis", "I think the moon landing happened in 1969, when exactly?", "who is the president of france"} {
		e := newGmEnv(t)
		e.send("3", in)
		if len(e.s.qs) == 0 {
			t.Errorf("%q was swallowed", in)
		}
	}
}

func TestResumeAndNaturalFeatures(t *testing.T) {
	e := newGmEnv(t)
	r, cr := e.send("4", "continue my game")
	if cr == nil || !strings.Contains(r, "no game in progress") {
		t.Fatalf("resume without game: %q", r)
	}
	e.send("4", "chess")
	r, cr = e.send("4", "show my game")
	if cr == nil || len(cr.PNG) == 0 {
		t.Fatalf("resume did not show the chess board: %q", r)
	}
	if len(e.s.qs) != 0 {
		t.Fatal("searched")
	}
}

func TestNaturalPhrasings(t *testing.T) {
	for in, want := range map[string]string{
		"add a note buy milk": "note: buy milk", "make a note that the gate code is 12": "note: the gate code is 12",
		"add a task call Sam": "task: call Sam", "add call Sam to my todo list": "task: call Sam",
		"show my reminders": "reminders", "what are my reminders?": "reminders", "show me my notes": "notes", "what are my tasks": "tasks",
		"remind me to call mum tomorrow at 5pm": "remind me tomorrow at 5pm to call mum", "remind me to stretch in 20 minutes": "remind me in 20 minutes to stretch",
		"remind me about the rent at 9am": "remind me at 9am to the rent",
	} {
		if got := nlRewrite(in); got != want {
			t.Errorf("%q -> %q want %q", in, got, want)
		}
	}
	for _, in := range []string{"what is a note in music", "note the date of the moon landing"} {
		if got := nlRewrite(in); got != in {
			t.Errorf("%q was rewritten to %q", in, got)
		}
	}
	if _, _, _, ok := Design("draw me a pie chart A=1,B=2"); !ok {
		t.Error("natural chart request not understood")
	}
}

func TestLocalPicks(t *testing.T) {
	e := newGmEnv(t)
	for _, in := range []string{"flip a coin", "roll a dice", "roll 2d6", "pick a number between 1 and 10", "choose between tea or coffee", "what did I just say"} {
		r, _ := e.send("2", in)
		if len(e.s.qs) != 0 || r == "" || strings.Contains(r, "couldn't find") {
			t.Errorf("%q -> %q", in, r)
		}
	}
	r, _ := e.send("2", "what did I just say")
	if !strings.Contains(r, "what did I just say") && !strings.Contains(r, "choose between") {
		t.Errorf("repeat: %q", r)
	}
}

func textUpdate(id int, text string) map[string]any {
	return map[string]any{"update_id": float64(id), "message": map[string]any{"message_id": float64(id), "text": text, "chat": map[string]any{"id": 5.0, "type": "private"}, "from": map[string]any{"id": 5.0}}}
}

func TestRunnerGameMenuEndToEnd(t *testing.T) {
	var searcher *fakeS
	tap := map[string]any{"update_id": 4.0, "callback_query": map[string]any{"id": "cb1", "data": "gm:ludo", "message": map[string]any{"message_id": 9.0, "chat": map[string]any{"id": 5.0, "type": "private"}}}}
	tr := runAlbumCfg(t, [][]map[string]any{{textUpdate(1, "Let\u2019s play a game")}, {textUpdate(2, "Which game")}, {textUpdate(3, "connect four")}, {tap}, {}}, []time.Duration{time.Second, time.Second, time.Second, time.Second, time.Second}, func(r *Runner, a *albumT) {
		e := newGmEnv(t)
		searcher = e.s
		r.Chat = e.c
	})
	if len(searcher.qs) != 0 {
		t.Fatalf("web search called: %v", searcher.qs)
	}
	all := strings.Join(tr.sent, "|")
	if strings.Count(all, "Which game?") < 2 || len(tr.markups) < 2 || !strings.Contains(tr.markups[0], "gm:chess") {
		t.Fatalf("menu not sent with buttons: sent=%v markups=%d", tr.sent, len(tr.markups))
	}
	if !strings.Contains(all, "drop 4") || !strings.Contains(strings.ToLower(all), "ludo") {
		t.Fatalf("games not started: %v", tr.sent)
	}
}
