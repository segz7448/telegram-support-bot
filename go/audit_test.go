package main

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestAuditRouting(t *testing.T) {
	e := newGmEnv(t)
	d := t.TempDir()
	e.c.Rem = &Reminders{Path: filepath.Join(d, "rem.json")}
	e.c.Notes = &Notes{Path: filepath.Join(d, "notes.json")}
	e.c.Know = &Knowledge{Path: filepath.Join(d, "k.json"), Owner: "1"}
	for _, in := range []string{"remind me to call mum tomorrow at 5pm", "add a note buy milk", "what's 15% of 80", "convert 5 miles to km", "what's the weather in Lagos", "weather tomorrow", "show me the news", "latest headlines", "make me a qr code for example.com", "draw me a pie chart A=1,B=2", "what time is it in Tokyo", "set a timer for 5 minutes", "flip a coin", "roll a dice", "pick a number between 1 and 10", "give me a password", "quiz me", "trivia", "tell me something interesting", "hello there", "good morning", "how are you", "thank you", "bye", "who are you", "what's your name", "what can you do", "help", "what's the date today", "what day is it", "translate hello to french", "play chess with me", "continue my game", "my notes", "show my reminders", "what did I just say", "remember my name is Sam", "what's my name", "i need a recipe for jollof rice", "random fact", "good night", "tell me about yourself", "can you search the web", "search for cats", "what's trending"} {
		e.s.qs = nil
		r, _ := e.send("8", in)
		if len(e.s.qs) > 0 {
			if cr, ok := Compute(in, time.Now()); ok {
				r, e.s.qs = cr, nil
			} else if cr, ok := NewTools(failFetch{}, nil).Pre(in); ok {
				r, e.s.qs = cr, nil
			} else if _, _, _, ok := Design(in); ok {
				e.s.qs = nil
			}
		}
		route := "handled"
		if len(e.s.qs) > 0 {
			route = "SEARCH"
		}
		short := r
		if len(short) > 70 {
			short = short[:70]
		}
		t.Logf("%-8s %-45s | %s", route, in, short)
	}
	_ = time.Now
}

type failFetch struct{}

func (failFetch) Get(string, int) ([]byte, int, error) { return nil, 0, fmt.Errorf("offline") }
