package main

import (
	"strings"
	"testing"
)

// Held-out paraphrases (never in the training file) and out-of-domain factual questions.
var heldOut = map[string][]string{
	"games":       {"i am bored let us play something", "do you have anything to play", "could we have a game please", "fancy a game", "i would love to play a game with you", "show me something to play", "let's do a game", "any games we can try"},
	"reminders":   {"remind me later please", "i must not forget my meeting", "ping me about the call tonight", "can you alert me at eight", "make sure i do not forget my keys", "i want you to remind me"},
	"notes":       {"please save this idea", "i want to write something down", "can you store this for me", "keep a note of this", "jot something for me", "put this on my to do list"},
	"weather":     {"is it going to be sunny today", "do i need to take an umbrella", "how cold is it outside", "will it rain tonight", "is it hot outside right now", "should i wear a coat today"},
	"news":        {"what is happening in the world today", "any news today", "tell me what is going on", "catch me up on what happened", "what are the top stories", "update me on current events"},
	"calc":        {"how much is eight times nine", "what is a quarter of 80", "add 20 and 35", "what is fifteen percent of 200", "how many feet in a mile", "divide 90 by 6"},
	"design":      {"can you make a poster for me", "i want a picture with words on it", "create a banner for my event", "make me a flyer", "i need a logo for my shop", "design a card"},
	"chitchat":    {"you are really funny", "i like talking to you", "that was good", "nice one", "you are so cool", "how is your day going"},
	"unsupported": {"call my dad", "send an email to my teacher", "order me some food", "play some music for me", "book me a flight", "text my sister for me"},
}

var oodFactual = []string{
	"how hot is mercury", "why do volcanoes erupt", "what is the capital of australia", "who discovered penicillin", "how far is mars from earth",
	"what is the history of football", "who invented the telephone", "how does a refrigerator work", "what is the population of nigeria", "when did the berlin wall fall",
	"what is the weather like in the amazon", "how is rain formed", "why is the ocean salty", "what are the rules of chess", "who is the ceo of apple",
	"how do i make pancakes", "what is quantum computing", "who wrote the odyssey", "how many bones in the human body", "what is the gdp of japan",
	"how does gps work", "what is the boiling point of water", "who was cleopatra", "what is the longest river", "how do bees make honey",
	"what is a good name for a dog", "how to learn spanish quickly", "what is the best phone to buy", "how much is a plane ticket to london", "when is the next solar eclipse",
	"who won the nobel prize in physics last year", "what is the difference between weather and climate", "how do i write a good essay", "why is chess popular", "what is the plot of hamlet",
	"how does a calculator work", "what is a note taking method", "how does the news cycle work", "what is a reminder app", "tell me about napoleon",
}

func TestClassifierHeldOut(t *testing.T) {
	right, n := 0, 0
	for want, xs := range heldOut {
		for _, x := range xs {
			n++
			c, p, k := intentNB.classify(x)
			if c == want && p >= clsMinP && k >= clsMinKnown {
				right++
			} else {
				t.Logf("held-out miss: %q -> %s p=%.2f known=%.2f (want %s)", x, c, p, k, want)
			}
		}
	}
	t.Logf("held-out acted correctly: %d/%d", right, n)
	if float64(right)/float64(n) < 0.7 {
		t.Errorf("held-out accuracy too low: %d/%d", right, n)
	}
}

func TestClassifierDoesNotHijackFactual(t *testing.T) {
	bad := 0
	for _, x := range oodFactual {
		if h, ok := classifyHint(x); ok {
			bad++
			t.Errorf("factual question hijacked: %q -> %.30s", x, h)
		}
	}
	t.Logf("out-of-domain factual questions hijacked: %d/%d", bad, len(oodFactual))
}

func TestClassifierNoHintForGibberish(t *testing.T) {
	for _, x := range []string{"zxcv qwer tyui", "blorp flib wug", "asdf ghjk"} {
		if _, ok := classifyHint(x); ok {
			t.Errorf("gibberish acted on: %q", x)
		}
	}
}

// A second, fresh held-out set written after the training data was extended (the first set above was used to find gaps).
var heldOut2 = map[string][]string{
	"games":       {"let us try a game together", "do you know any games we could play", "i want us to play a game", "give me something to play"},
	"reminders":   {"remind me in the evening", "i need you to remind me about the bill", "do not let me forget my lunch", "ping me before the meeting"},
	"notes":       {"save my thought please", "i want to keep a note", "write down a thing for me", "add this to my task list"},
	"weather":     {"will it rain this afternoon", "is it freezing outside", "do i need a hat today", "how warm is it outside"},
	"news":        {"what is in the headlines", "any news right now", "what is happening around the world", "give me the top news"},
	"calc":        {"what is a tenth of 70", "add 11 and 22", "multiply 9 by 4", "how many seconds in an hour"},
	"design":      {"i want a chart of my sales", "make a poster for my show", "can you create a banner", "design a flyer for me"},
	"chitchat":    {"you are amazing", "that was funny", "how is your evening going", "good job bot"},
	"unsupported": {"call my boss", "play a song for me", "order me a taxi", "send an email to my friend"},
}

func TestClassifierFreshHeldOut(t *testing.T) {
	right, n := 0, 0
	for want, xs := range heldOut2 {
		for _, x := range xs {
			n++
			c, p, k := intentNB.classify(x)
			if c == want && p >= clsMinP && k >= clsMinKnown {
				right++
			} else {
				t.Logf("fresh held-out miss: %q -> %s p=%.2f known=%.2f (want %s)", x, c, p, k, want)
			}
		}
	}
	t.Logf("fresh held-out acted correctly: %d/%d", right, n)
	wrong := 0
	for want, xs := range heldOut2 {
		for _, x := range xs {
			if c, p, k := intentNB.classify(x); c != want && c != "search" && p >= clsMinP && k >= clsMinKnown {
				wrong++
				t.Errorf("acted on the WRONG intent: %q -> %s", x, c)
			}
		}
	}
}

func TestClassifierThroughChat(t *testing.T) {
	for in, want := range map[string]string{"do i need an umbrella today": "weather", "can you design a flyer for me": "picture", "let us have a match": "Which game", "what is happening in the world today": "news", "call my mom": "can't"} {
		e := newGmEnv(t)
		r, _ := e.send("3", in)
		if len(e.s.qs) != 0 || !strings.Contains(r, want) {
			t.Errorf("%q -> %q (searches %d)", in, r, len(e.s.qs))
		}
	}
	for _, in := range []string{"how hot is the sun", "why does it rain", "history of the roman empire"} {
		e := newGmEnv(t)
		e.send("3", in)
		if len(e.s.qs) == 0 {
			t.Errorf("%q should search", in)
		}
	}
}
