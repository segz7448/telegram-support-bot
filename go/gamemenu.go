package main

import (
	"encoding/json"
	"regexp"
	"strings"
	"sync"
	"time"
)

// GameMenu understands everyday requests to play ("let's play a game", "which game", "wanna play something") and shows
// the installed games with tap buttons, before any web search is tried. It is a normal GameHandler placed first in
// the list, so these messages never reach general search. Only whole short messages match, so questions about games
// ("history of chess", "how do I play ludo", "game of thrones") still go to normal answers.

type gameEntry struct {
	Key, Label, Word string // Word is the text the game's own handler accepts to start a game
	H                GameHandler
	On               bool
}

type GameMenu struct {
	Entries []gameEntry
	Now     func() time.Time
	mu      sync.Mutex
	shown   map[string]time.Time
}

const gmPrefix = "gm:"

var reGmFactual = regexp.MustCompile(`\b(?:history|videos?|store|download|cast|score|scores|rules|invented|awards?|streaming|stream|review|reviews|wiki|meaning|definition|theory|console|release|price)\b`)

var (
	reGmLead   = `(?:(?:hey|hi|hello|ok|okay|so|well|yo|please|pls|ayo)[ ,]+)*`
	reGmAsk    = regexp.MustCompile(`^` + reGmLead + `(?:(?:let ?s|let us|can we|could we|shall we|should we|how about we|do you want to|you wanna|you want to|i want to|i wanna|i would like to|i d like to|i feel like|wanna|want to|lets|im bored lets|i m bored let s)\s+)?play(?: with me| together)?(?: (?:a|an|some|another|one|any|the))?(?: (?:game|games|fun game|quick game|little game|board game|something|anything|something fun|a game))?(?: with me| together| now| please| pls| with you)*$|^` + reGmLead + `(?:games?|game time|play time|play a game|games please|show (?:me )?(?:the )?games|list (?:the |your )?games|(?:what|which) games(?: (?:do you (?:have|know|play)|can you play|can we play|are there|you got|do you got))?|do you (?:have|know|play) (?:any )?games|any games|im bored|i m bored|i am bored|entertain me|something fun)$`)
	reGmNamed  = regexp.MustCompile(`^` + reGmLead + `(?:(?:let ?s|let us|can we|could we|shall we|i want to|i wanna|wanna|want to|lets|how about we)\s+)?play(?: a game of| a game| some| the game)?\s+(chess|connect ?(?:4|four)|c4|reversi|othello|draughts|checkers|ludo)(?: with me| now| please| together)?$`)
	reGmOther  = regexp.MustCompile(`^` + reGmLead + `(?:let ?s|let us|can we|lets|i want to|i wanna|wanna|want to|how about we)\s+play (?:a game of |a game |some |the game )?([a-z0-9' ]{2,30})$`)
	reGmWhich  = regexp.MustCompile(`^(?:which|what)(?: game| one| games| ones)?(?: (?:do you have|can you play|should we|should i|are there|else|you got))?$|^which game$|^(?:the )?(?:first|second|third|fourth|fifth) one$|^(?:options|choices|menu|list|games menu)$|^(?:any|random|surprise me|you choose|you pick|your choice|pick one|choose one|anything)$`)
	reGmResume = regexp.MustCompile(`^(?:continue|resume|show|see|where were we in|go back to|back to|what s|whats|what is)?(?: the| my| our)?(?: current| last| previous| unfinished)? ?(?:game|board|match)(?: in progress| again| please)?$|^(?:continue|resume) (?:the |my |our )?(?:game|match)$`)
	reGmNum    = regexp.MustCompile(`^(?:number )?([1-9])$`)
	reGmFirst  = regexp.MustCompile(`^(?:the )?(first|second|third|fourth|fifth) one$`)
)

func gmNorm(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.NewReplacer("\u2019", "'", "\u2018", "'", "`", "'").Replace(s)
	s = strings.NewReplacer("'", " ", "?", "", "!", "", ".", "", ",", "", "\"", "").Replace(s)
	// "let s" came from "let's"; "lets" and "let s" are both accepted above
	return strings.Join(strings.Fields(s), " ")
}

func (m *GameMenu) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *GameMenu) on() []gameEntry {
	var out []gameEntry
	for _, e := range m.Entries {
		if e.On {
			out = append(out, e)
		}
	}
	return out
}

func (m *GameMenu) Prefix() string { return gmPrefix }

func (m *GameMenu) menu(chat, lead string) *ChessReply {
	es := m.on()
	if len(es) == 0 {
		return &ChessReply{Text: "No games are switched on for this bot right now."}
	}
	var b strings.Builder
	if lead != "" {
		b.WriteString(lead + "\n")
	}
	b.WriteString("Which game? I can play:\n")
	for i, e := range es {
		b.WriteString(gmDigit(i+1) + ". " + e.Label + "\n")
	}
	group := strings.HasPrefix(chat, "-")
	if group {
		b.WriteString("Type the game's name to start.")
	} else {
		b.WriteString("Tap one, or type its name (or its number).")
	}
	m.mu.Lock()
	if m.shown == nil {
		m.shown = map[string]time.Time{}
	}
	m.shown[chat] = m.now()
	m.mu.Unlock()
	r := &ChessReply{Text: b.String()}
	if !group {
		type btn struct {
			T string `json:"text"`
			D string `json:"callback_data"`
		}
		var rows [][]btn
		var row []btn
		for _, e := range es {
			row = append(row, btn{e.Label, gmPrefix + e.Key})
			if len(row) == 2 {
				rows = append(rows, row)
				row = nil
			}
		}
		if len(row) > 0 {
			rows = append(rows, row)
		}
		mk, _ := json.Marshal(map[string]any{"inline_keyboard": rows})
		r.Markup = string(mk)
	}
	return r
}

func gmDigit(n int) string { return string(rune(0x30 + n)) }

func (m *GameMenu) recent(chat string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.shown[chat]
	return ok && m.now().Sub(t) < 15*time.Minute
}

func (m *GameMenu) start(chat string, e gameEntry) (*ChessReply, bool) {
	m.mu.Lock()
	delete(m.shown, chat)
	m.mu.Unlock()
	return e.H.Handle(chat, e.Word)
}

func (m *GameMenu) find(name string) (gameEntry, bool) {
	name = strings.ReplaceAll(name, " ", "")
	key := map[string]string{"chess": "chess", "connect4": "c4", "connectfour": "c4", "c4": "c4", "reversi": "reversi", "othello": "reversi", "draughts": "draughts", "checkers": "draughts", "ludo": "ludo"}[name]
	for _, e := range m.Entries {
		if e.Key == key {
			return e, e.On
		}
	}
	return gameEntry{}, false
}

func (m *GameMenu) Handle(chat, text string) (*ChessReply, bool) {
	if len(text) > 120 {
		return nil, false
	}
	t := gmNorm(text)
	if t == "" || len(m.on()) == 0 && !m.recent(chat) {
		return nil, false
	}
	if mm := reGmNamed.FindStringSubmatch(t); mm != nil {
		e, on := m.find(mm[1])
		if !on {
			return m.menu(chat, "That game isn't switched on."), true
		}
		return m.start(chat, e)
	}
	if reGmAsk.MatchString(t) {
		return m.menu(chat, ""), true
	}
	if reGmResume.MatchString(t) {
		for _, e := range m.on() {
			if r, ok := e.H.Handle(chat, e.Word+" board"); ok && r != nil && len(r.PNG) > 0 {
				return r, true
			}
		}
		return m.menu(chat, "You have no game in progress."), true
	}
	if c, p, k := intentNB.classify(text); c == "games" && clsGuard(text) && !reGmFactual.MatchString(t) && p >= clsMinP && k >= clsMinKnown && len(strings.Fields(t)) >= 2 && len(strings.Fields(t)) <= 12 {
		return m.menu(chat, ""), true
	}
	if m.recent(chat) {
		es := m.on()
		if mm := reGmNum.FindStringSubmatch(t); mm != nil {
			if i := int(mm[1][0] - '1'); i < len(es) {
				return m.start(chat, es[i])
			}
		}
		if mm := reGmFirst.FindStringSubmatch(t); mm != nil {
			if i := map[string]int{"first": 0, "second": 1, "third": 2, "fourth": 3, "fifth": 4}[mm[1]]; i < len(es) {
				return m.start(chat, es[i])
			}
		}
		switch t {
		case "any", "random", "surprise me", "you choose", "you pick", "your choice", "pick one", "choose one", "anything":
			return m.start(chat, es[int(m.now().UnixNano()/1000)%len(es)])
		}
		if reGmWhich.MatchString(t) {
			return m.menu(chat, ""), true
		}
		if mm := reGmOther.FindStringSubmatch(t); mm != nil {
			return m.menu(chat, "I don't have \""+strings.TrimSpace(mm[1])+"\"."), true
		}
		if strings.HasPrefix(t, "play ") && len(strings.Fields(t)) <= 4 {
			if e, on := m.find(strings.TrimPrefix(t, "play ")); on {
				return m.start(chat, e)
			}
			return m.menu(chat, "I don't have that one."), true
		}
		m.mu.Lock()
		delete(m.shown, chat) // any other message ends the menu, so game moves like "3" are never taken as a choice later
		m.mu.Unlock()
		return nil, false
	}
	// no recent menu: "which game" style questions with no context still list the games when they name games
	if reGmWhich.MatchString(t) && (strings.Contains(t, "games") || t == "which game") {
		return m.menu(chat, ""), true
	}
	if mm := reGmOther.FindStringSubmatch(t); mm != nil {
		w := strings.TrimSpace(mm[1])
		if w != "game" && w != "games" && !strings.Contains(w, " ") {
			return m.menu(chat, "I don't have \""+w+"\"."), true
		}
	}
	return nil, false
}

func (m *GameMenu) Tap(chat, data string) (popup, markup string, rep *ChessReply) {
	key := strings.TrimPrefix(data, gmPrefix)
	for _, e := range m.Entries {
		if e.Key == key && e.On {
			r, ok := m.start(chat, e)
			if !ok {
				return "Couldn't start that game.", "", nil
			}
			return "", "-", r
		}
	}
	return "That game isn't available.", "", nil
}
